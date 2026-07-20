package check

// bed_run.go — the R10 acceptance-sequence engine for disposable test beds (P12:
// relocated from charly/check_bed_run.go), driving the "check-bed" host-session seam
// + HostBuild("cli").
//
// A check bed is a `disposable: true` deploy. runCheckBed drives the canonical
// sequence against it:
//
//	build → check box → deploy add → config → start → check live →
//	fresh update (R10 acceptance gate) → tear down
//
// The lock / lease / repo-override-env / deploy-config-isolation / GPU-prereq
// lifecycle is CORE STATE a separate module cannot hold — the "check-bed" session
// seam (setup/members-up/members-down/wait-ready/teardown) owns it and returns the
// node-derived BedDescriptor the kind-blind plugin drives the sequence from. Every
// `charly` subcommand rides HostBuild("cli"); the plugin owns the sequence LOGIC,
// the per-step .log + summary.yml writes and the exit-code
// classification.
//
// #33: the current post-rebase sequence passes `--domain <bedDomain>` on `charly vm
// create/destroy/start` while `charly vm build` stays ENTITY-scoped (VMTemplate).
// Preserved EXACTLY — d.BedDomain for --domain, d.VMTemplate for the build/entity arg.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// repoOverrideEnvName is the env var the check-bed session sets so the cli-forked
// `charly` children build the LOCAL candy tree. Named here only for the debug
// retention notice's inspect-command hint (the core RepoOverrideEnv const is
// package-main-only); the SESSION owns the actual set/restore lifecycle.
const repoOverrideEnvName = "CHARLY_REPO_OVERRIDE"

// bedRunOpts carries the per-run knobs (sourced from `charly check run` flags).
type bedRunOpts struct {
	Keep      bool // don't tear the bed down after the run (--keep)
	NoRebuild bool // skip the fresh-update R10 re-verify step (--no-rebuild)
}

// stepResult captures one step's outcome for the summary.yml.
type stepResult struct {
	Name     string
	Duration time.Duration
	OK       bool
}

// bedRunResult captures one bed's full run outcome.
type bedRunResult struct {
	Bed    string
	CalVer string
	Step   []stepResult
	OK     bool
	// FailExitCode is the exit code of the FIRST failed step (0 = none).
	// CheckFailExitCode (2) means a check step reported failing checks; anything
	// else is an infra failure. The caller maps it to the process exit code so
	// `charly check run <bed>` distinguishes "checks failed" from "couldn't run".
	FailExitCode int
	// SkippedPrereq marks a bed that never ran because a required HOST prerequisite
	// is absent. Not a failure — the caller emits CheckSkippedExitCode + SkipReason.
	// OK stays true, so callers MUST check SkippedPrereq before OK.
	SkippedPrereq bool
	SkipReason    string
}

// summaryStatus formats a bool as a human-readable status word.
func summaryStatus(ok bool) string {
	if ok {
		return "PASS"
	}
	return "FAIL"
}

// withRunTag appends `--tag <tag>` to a step argv when tag is non-empty — the bed's
// per-run image tag (#75) every box build + pod deploy in the run passes so
// concurrent beds building the same fixture image name never collide.
func withRunTag(args []string, tag string) []string {
	if tag == "" {
		return args
	}
	return append(args, "--tag", tag)
}

// runTaggedImageRef returns the exact OCI image reference produced by the
// bed's `box build --tag`. Artifact verification must consume this reference,
// not re-resolve the untagged logical box name: an older locally cached bed
// image may otherwise be selected between the build and check steps.
func runTaggedImageRef(image, tag string) string {
	if image == "" || tag == "" {
		return image
	}
	return image + ":" + tag
}

// runCheckBed executes the canonical R10 sequence for one check bed and writes
// per-step logs + summary.yml to .check/<name>/<calver>/. Returns the result struct
// (always non-nil once setup succeeds) and the first error encountered.
//
//nolint:gocyclo // canonical R10 bed sequence (build→check→deploy→check-live→update→teardown) woven from interdependent inline closures over a shared mutable result + the check-bed host session; contiguous-block extraction is not behavior-preserving
func runCheckBed(ctx context.Context, ex *sdk.Executor, name string, opts bedRunOpts) (*bedRunResult, error) {
	// setup — the host opens the session (locks/lease/env/GPU-prereq) and returns
	// the BedDescriptor the sequence drives from.
	d, err := bedHostBuild(ex, ctx, spec.CheckBedRequest{Op: "setup", Bed: name})
	if err != nil {
		return nil, err
	}

	res := &bedRunResult{Bed: name, CalVer: d.Calver, OK: true}

	// GPU-prereq skip: setup acquired NOTHING (no session inserted), so run NO other
	// op — write the prereq-skip summary + return CheckSkippedError (exit 3).
	if d.PrereqSkip != nil {
		res.SkippedPrereq = true
		res.SkipReason = d.PrereqSkip.Reason
		res.Step = append(res.Step, stepResult{Name: "prereq-gpu-skipped", OK: true})
		writeBedSummary(d.LogDir, res)
		return res, &CheckSkippedError{Msg: fmt.Sprintf("charly check run %s: skipped (%s)", name, res.SkipReason)}
	}

	// teardown runs on EVERY exit path after a successful setup — it releases the
	// session's locks/lease/env (NOT the deployed target). res.OK controls the
	// preempt-lease disposition (Release vs ReleaseFailed).
	defer func() {
		_, _ = bedHostBuild(ex, ctx, spec.CheckBedRequest{Op: "teardown", Bed: name, OK: res.OK})
	}()

	// Acceptance-depth gating comes from the descriptor (the box's check_level rung,
	// resolved host-side): RunBuild → build-context acceptance (check box); RunRuntime
	// → deploy/runtime acceptance (check live + feature run --no-agent); RunAgent → +
	// the prose-step agent grader (feature run WITHOUT --no-agent).
	featureRunArgs := func() []string {
		args := []string{"check", "feature", "run", name}
		if !d.RunAgent {
			args = append(args, "--no-agent")
		}
		return args
	}

	// bestEffort runs a `charly` subcommand host-side, discarding the result (the
	// pre-run cleanups that clear a lingering target from an interrupted run).
	bestEffort := func(argv ...string) {
		_, _ = bedCli(ex, ctx, true, argv...)
	}

	// waitReady drives the "wait-ready" session op (the host reads the node kind to
	// pick waitForVmSshReady vs waitForContainerReady). Best-effort.
	waitReady := func() {
		_, _ = bedHostBuild(ex, ctx, spec.CheckBedRequest{Op: "wait-ready", Bed: name})
	}

	// phase records an IN-PROCESS phase (member bring-up / teardown — ops that do not
	// shell out to a `charly` subcommand) in the summary with its real duration.
	phase := func(stepName string, fn func() error) error {
		t0 := time.Now()
		fmt.Fprintf(os.Stderr, "charly check run %s: [%s] START\n", name, stepName)
		err := fn()
		dur := time.Since(t0)
		res.Step = append(res.Step, stepResult{Name: stepName, Duration: dur, OK: err == nil})
		if err != nil {
			res.OK = false
			if res.FailExitCode == 0 {
				res.FailExitCode = 1
			}
			fmt.Fprintf(os.Stderr, "charly check run %s: [%s] FAIL after %s: %v\n", name, stepName, dur.Round(time.Millisecond), err)
			return err
		}
		fmt.Fprintf(os.Stderr, "charly check run %s: [%s] PASS after %s\n", name, stepName, dur.Round(time.Millisecond))
		return nil
	}

	// step records a step's outcome (a `charly` subcommand over the cli seam) and
	// writes its log file. Returns the run error so the caller can short-circuit.
	step := func(stepName string, argv ...string) error {
		t0 := time.Now()
		logPath := filepath.Join(d.LogDir, stepName+".log")
		command := checkStepCommandSummary(argv)
		fmt.Fprintf(os.Stderr, "charly check run %s: [%s] START (%s; log: %s)\n", name, stepName, command, logPath)
		if writeErr := os.WriteFile(logPath, []byte("status: RUNNING\ncommand: "+command+"\n"), 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "charly check run %s: [%s] cannot initialize log %s: %v\n", name, stepName, logPath, writeErr)
		}
		reply, cerr := bedCliCombined(ex, ctx, argv...)
		dur := time.Since(t0)
		ok := cerr == nil && reply.ExitCode == 0
		res.Step = append(res.Step, stepResult{Name: stepName, Duration: dur, OK: ok})
		if !ok {
			res.OK = false
			if res.FailExitCode == 0 {
				// First failure wins; capture the sub-charly exit code so the caller
				// can tell a check-check failure (2) from an infra failure (1).
				if cerr != nil {
					res.FailExitCode = 1
				} else {
					res.FailExitCode = reply.ExitCode
				}
			}
		}
		if writeErr := os.WriteFile(logPath, []byte(cliStepLog(reply)), 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "charly check run %s: writing %s: %v\n", name, logPath, writeErr)
		}
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "charly check run %s: [%s] FAIL after %s: %v (log: %s)\n", name, stepName, dur.Round(time.Millisecond), cerr, logPath)
			return fmt.Errorf("%s (%s) failed after %s: %w; log: %s", stepName, command, dur.Round(time.Millisecond), cerr, logPath)
		}
		if reply.ExitCode != 0 {
			detail := strings.TrimSpace(reply.Error)
			if detail == "" {
				detail = strings.TrimSpace(reply.Stdout)
			}
			fmt.Fprintf(os.Stderr, "charly check run %s: [%s] FAIL after %s: exit %d: %s (log: %s)\n", name, stepName, dur.Round(time.Millisecond), reply.ExitCode, detail, logPath)
			return fmt.Errorf("%s (%s) exited %d after %s: %s; log: %s", stepName, command, reply.ExitCode, dur.Round(time.Millisecond), detail, logPath)
		}
		fmt.Fprintf(os.Stderr, "charly check run %s: [%s] PASS after %s (log: %s)\n", name, stepName, dur.Round(time.Millisecond), logPath)
		return nil
	}

	// cleanup tears the disposable bed's DEPLOYED TARGET down (suppressed by --keep).
	// Runtime and member cleanup are independently recorded, and either failure fails the bed; the
	// session's locks/lease/env are still released by the teardown defer on every path.
	cleanup := func() error {
		if opts.Keep {
			return nil
		}
		var targetErr error
		switch {
		case d.IsVM:
			targetErr = step("cleanup", "vm", "destroy", d.VMTemplate, "--domain", d.BedDomain, "--if-exists")
		case d.IsGroup:
			// A targetless group has NO root container — members-down is the whole teardown.
		case d.IsExternal:
			targetErr = step("cleanup", "bundle", "del", name)
		default:
			targetErr = step("cleanup", "remove", name, "--purge")
		}
		membersErr := phase("cleanup-members", func() error {
			_, err := bedHostBuild(ex, ctx, spec.CheckBedRequest{Op: "members-down", Bed: name})
			return err
		})
		if targetErr != nil {
			return targetErr
		}
		return membersErr
	}

	// deployed flips true once the bed's target actually exists (after deploy-add).
	deployed := false
	// fail is the SINGLE failure tail: record the summary, LEAVE THE BED RUNNING for
	// debugging (the check-live failure is already on record), and return the error.
	fail := func(format string, args ...any) (*bedRunResult, error) {
		res.OK = false
		if res.FailExitCode == 0 {
			res.FailExitCode = 1 // infra failure; a checks-failure (2) is set by step()
		}
		writeBedSummary(d.LogDir, res)
		if deployed {
			printDebugRetentionNotice(os.Stderr, name, d)
		}
		return res, fmt.Errorf(format, args...)
	}

	// GROUP beds have no root image — build EACH member's substrate BEFORE members-up (the host
	// bringUpMembers assumes pre-built images). Per-member coordinates ride the descriptor's Members
	// (the host-resolved {Key, IsVM, Image, From}). A VM member builds its disk (`vm build <from>`,
	// ENTITY-scoped — bringUpMembers does the per-member `vm create --domain` + ssh-wait); a pod / k8s
	// member builds its box image (+ RunBuild-gated `check box`); a kind:local member carries no image
	// (applies candies in place). Mirrors the core runCheckBed group loop. libvirt was already started
	// by the check-bed setup op (vm/group beds), so no per-member start is needed here.
	if d.IsGroup {
		for _, m := range d.Members {
			if m.IsVM {
				if err := step("vm-build-"+m.Key, "vm", "build", m.From); err != nil {
					return fail("vm build member %s (%s): %w", m.Key, m.From, err)
				}
				continue
			}
			if m.Image == "" {
				continue // kind:local member — applies candies in place, no image
			}
			if err := step("image-build-"+m.Key, withRunTag([]string{"box", "build", m.Image, "--dev-local-pkg"}, d.ImageTag)...); err != nil {
				return fail("image build member %s (%s): %w", m.Key, m.Image, err)
			}
			if d.RunBuild {
				if err := step("check-image-"+m.Key, "check", "box", runTaggedImageRef(m.Image, d.ImageTag)); err != nil {
					return fail("check box member %s (%s): %w", m.Key, m.Image, err)
				}
			}
		}
	}

	// isInPlace unifies local + in-place-external: they apply candies in place during
	// `charly bundle add` (no container/VM lifecycle — no `charly config`/`charly
	// start`, teardown via `charly bundle del`).
	isInPlace := d.IsLocal || d.IsExternal

	// Steps 1+2: image build + check box (pod beds only; VM substrate is a
	// cloud_image and kind:local/external have no image to build/check).
	if !d.IsVM && !d.IsLocal && !d.IsExternal && d.Image != "" {
		// Disposable check beds ALWAYS bake the IN-DEVELOPMENT charly toolchain via
		// --dev-local-pkg — so a bed tests the code under development.
		if err := step("image-build", withRunTag([]string{"box", "build", d.Image, "--dev-local-pkg"}, d.ImageTag)...); err != nil {
			return fail("image build %s: %w", d.Image, err)
		}
		if d.RunBuild {
			if err := step("check-image", "check", "box", runTaggedImageRef(d.Image, d.ImageTag)); err != nil {
				return fail("check box %s: %w", d.Image, err)
			}
		}
	}

	// Step 3: bring up the bed.
	switch {
	case d.IsVM:
		// This bed's libvirt domain is named after the DEPLOY (BedDomain), not the
		// shared kind:vm entity (VMTemplate) — #33/P33. `vm build` builds the shared
		// base off the ENTITY; every `charly vm …` that touches THIS domain passes
		// --domain <BedDomain>.
		bestEffort("vm", "destroy", d.VMTemplate, "--domain", d.BedDomain, "--if-exists")
		if err := step("vm-build", "vm", "build", d.VMTemplate); err != nil {
			return fail("vm build %s: %w", d.VMTemplate, err)
		}
		if err := step("vm-create", "vm", "create", d.VMTemplate, "--domain", d.BedDomain); err != nil {
			return fail("vm create %s: %w", d.VMTemplate, err)
		}
		deployed = true // VM domain exists — keep it on any later failure
		waitReady()
		if err := step("deploy-add", "bundle", "add", name, d.VMTemplate); err != nil {
			return fail("bundle add %s: %w", name, err)
		}
		// Deploy the VM's nested HOST-ROOTED (kind:local) children only (d.LocalChildKeys, the
		// host-resolved deployNestedLocalChildren subset). A VM's nested CONTAINER children are
		// deployed IN-GUEST by plugin-deploy-vm's PostApply, so a host-side re-deploy would be wrong.
		for _, childKey := range d.LocalChildKeys {
			if err := step("deploy-"+childKey, "bundle", "add", name+"."+childKey); err != nil {
				return fail("deploy nested local child %s.%s: %w", name, childKey, err)
			}
		}
	case d.IsGroup:
		// Group bed: no root container — the members (subject + driver) ARE the deployment. Clear
		// any lingering bed + stale members from a prior run; bringUpMembers (the members-up op in
		// the runtime block below) then deploys each member (config+start per pod member, bundle add
		// per local member). There is no root deploy-add/config/start.
		bestEffort("remove", name, "--purge")
		_, _ = bedHostBuild(ex, ctx, spec.CheckBedRequest{Op: "members-down", Bed: name})
		deployed = true // members will be brought up — keep state on a later failure
	default:
		// Pod beds → image ref; kind:local beds → local template ref; an EXTERNAL
		// deploy substrate composes its candies via add_candy: and carries no ref.
		addArgs := []string{"bundle", "add", name}
		switch {
		case d.IsExternal:
			// no ref — add_candy: is the workload
		case d.IsLocal:
			addArgs = append(addArgs, d.LocalRef)
		default:
			addArgs = append(addArgs, d.Image)
		}
		addArgs = append(addArgs, "--node-only")
		// Best-effort tear-down of any lingering bed from a previous interrupted run.
		if d.IsExternal {
			bestEffort("bundle", "del", name)
		} else {
			bestEffort("remove", name, "--purge")
		}
		// Clear any sibling members left over from a previous interrupted run.
		_, _ = bedHostBuild(ex, ctx, spec.CheckBedRequest{Op: "members-down", Bed: name})
		addArgs = withRunTag(addArgs, d.ImageTag)
		if err := step("deploy-add", addArgs...); err != nil {
			return fail("bundle add %s: %w", name, err)
		}
		deployed = true // target registered — keep it on any later failure
		// kind:local + external apply candies in place during deploy add; pod beds
		// need `charly config` + `charly start`.
		if !isInPlace {
			if err := step("config", withRunTag([]string{"config", name}, d.ImageTag)...); err != nil {
				return fail("config %s: %w", name, err)
			}
			if err := step("start", withRunTag([]string{"start", name}, d.ImageTag)...); err != nil {
				return fail("start %s: %w", name, err)
			}
			waitReady()
			// Deploy any nested children onto the started substrate, pre-order.
			for _, childKey := range d.ChildKeys {
				if err := step("deploy-"+childKey, "bundle", "add", name+"."+childKey); err != nil {
					return fail("deploy nested child %s.%s: %w", name, childKey, err)
				}
			}
		}
	}

	// checkLiveTree runs each `charly check live` exactly once against the bed's substrate AND every
	// nested child through the multi-hop chain (bedCheckLiveRefs, resolved host-side
	// into d.CheckLiveRefs). Readiness synchronization happens before this function;
	// an acceptance failure is evidence and is never hidden by a timed retry.
	// stepLabel disambiguates initial vs rebuild.
	checkLiveTree := func(stepLabel string) error {
		for i, ref := range d.CheckLiveRefs {
			label := stepLabel
			if i > 0 {
				label = stepLabel + "-" + ref[len(name)+1:] // childKey after "<name>."
			}
			if err := step(label, "check", "live", ref); err != nil {
				return err
			}
		}
		return nil
	}

	// Step 4: deploy/runtime acceptance — gated out at check_level: none|build.
	// Members are instruments for the runtime probes, so bring-up is gated with them.
	if d.RunRuntime {
		if err := phase("bring-up-members", func() error {
			_, e := bedHostBuild(ex, ctx, spec.CheckBedRequest{Op: "members-up", Bed: name})
			return e
		}); err != nil {
			return fail("bring up peers for %s: %w", name, err)
		}
		if err := checkLiveTree("check-live"); err != nil {
			return fail("check live %s: %w", name, err)
		}

		// Step 4b: ADE acceptance — run the bed image's baked plan steps. Pod beds only.
		if !d.IsVM && !d.IsLocal && !d.IsExternal && d.Image != "" {
			if err := step("feature-run", featureRunArgs()...); err != nil {
				return fail("feature run %s: %w", name, err)
			}
		}
	}

	// Step 5: fresh-update re-verify (the R10 acceptance gate). Suppressed by --no-rebuild.
	if !opts.NoRebuild && d.IsGroup {
		// Group bed: NO root container to `charly update` — a generic `charly update <bed>` would
		// mis-resolve a TARGETLESS group as a default-pod deploy ("target pod not connected"). The R10
		// fresh-rebuild gate instead re-builds each member image, tears the members down, re-brings
		// them up, and re-check-lives — mirroring the initial group deploy (the old runCheckBed group
		// rebuild arm). VM/local members carry no Image and are skipped (as on the initial build).
		for _, m := range d.Members {
			if m.Image == "" {
				continue
			}
			if err := step("update-image-"+m.Key, withRunTag([]string{"box", "build", m.Image, "--dev-local-pkg"}, d.ImageTag)...); err != nil {
				return fail("rebuild member image %s (%s): %w", m.Key, m.Image, err)
			}
		}
		if err := phase("rebuild-members-down", func() error {
			_, e := bedHostBuild(ex, ctx, spec.CheckBedRequest{Op: "members-down", Bed: name})
			return e
		}); err != nil {
			return fail("tear down members for fresh rebuild of %s: %w", name, err)
		}
		if d.RunRuntime {
			if err := phase("re-bring-up-members", func() error {
				_, e := bedHostBuild(ex, ctx, spec.CheckBedRequest{Op: "members-up", Bed: name})
				return e
			}); err != nil {
				return fail("re-bring up members for %s: %w", name, err)
			}
			if err := checkLiveTree("check-live-rebuild"); err != nil {
				return fail("check live (fresh rebuild) %s: %w", name, err)
			}
		}
	} else if !opts.NoRebuild {
		if err := step("update", "update", name); err != nil {
			return fail("update %s: %w", name, err)
		}
		// For a nested bed, the fresh rebuild discards the substrate's children, so
		// re-apply + re-check-live to actually re-verify on the rebuild.
		if d.RunRuntime && !isInPlace && len(d.ChildKeys) > 0 {
			if d.IsVM {
				// `charly update` recreated the domain; the qcow2 disk (and the nested
				// pod's persistent in-guest quadlet) persists, so it auto-starts on the
				// fresh boot — just wait for ssh, then the rebuild check-live proves it.
				waitReady()
			} else {
				waitReady()
				for _, childKey := range d.ChildKeys {
					if err := step("redeploy-"+childKey, "bundle", "add", name+"."+childKey); err != nil {
						return fail("re-deploy nested child %s.%s (fresh rebuild): %w", name, childKey, err)
					}
				}
			}
			if err := checkLiveTree("check-live-rebuild"); err != nil {
				return fail("check live (fresh rebuild) %s: %w", name, err)
			}
		}
		// Re-run the bed image's baked plan steps on the fresh rebuild (pod beds).
		if d.RunRuntime && !d.IsVM && !d.IsLocal && !d.IsExternal && d.Image != "" {
			waitReady()
			if err := step("feature-run-rebuild", featureRunArgs()...); err != nil {
				return fail("feature run (fresh rebuild) %s: %w", name, err)
			}
		}
	}

	// Step 6: tear down (suppressed by --keep). Cleanup is part of the acceptance contract.
	if err := cleanup(); err != nil {
		return fail("clean up %s: %w", name, err)
	}

	writeBedSummary(d.LogDir, res)
	if !res.OK {
		return res, fmt.Errorf("bed %s: one or more steps failed", name)
	}
	return res, nil
}

func cliStepLog(reply spec.CliReply) string {
	output := reply.Stdout
	if reply.Error == "" {
		return output
	}
	if output != "" && output[len(output)-1] != '\n' {
		output += "\n"
	}
	return output + reply.Error + "\n"
}

// checkStepCommandSummary returns enough context to identify a blocked HostBuild("cli")
// boundary without echoing arbitrary command arguments (which may contain credentials).
func checkStepCommandSummary(argv []string) string {
	if len(argv) == 0 {
		return "charly <missing-command>"
	}
	words := []string{"charly", argv[0]}
	if len(argv) > 1 {
		switch argv[0] {
		case "check", "box", "bundle", "vm":
			words = append(words, argv[1])
		}
	}
	return strings.Join(words, " ")
}

// printDebugRetentionNotice tells the operator that a FAILED bed was left running for
// inspection, with the target-appropriate inspect + destroy commands.
func printDebugRetentionNotice(w *os.File, name string, d spec.CheckBedReply) {
	// The bed ran with CHARLY_REPO_OVERRIDE set (testing the LOCAL checkout's candies
	// + plugins), so carry the same override in the inspect hint (still active here —
	// the session set it) so the command reproduces the bed's actual state.
	live := "charly check live " + name
	if ov := os.Getenv(repoOverrideEnvName); ov != "" {
		live = repoOverrideEnvName + "='" + ov + "' " + live
	}
	switch {
	case d.IsVM:
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — VM %q left running for debugging.\n"+
			"  inspect: %s | charly vm ssh %s\n"+
			"  destroy: charly vm destroy %s\n", name, d.VMTemplate, live, d.VMTemplate, d.VMTemplate)
	case d.IsLocal:
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — local apply left in place for debugging.\n"+
			"  destroy: charly remove %s\n", name, name)
	case d.IsGroup:
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — group members left up for debugging.\n"+
			"  inspect: %s\n"+
			"  destroy: charly remove %s (members tear down with the group)\n", name, live, name)
	case d.IsExternal:
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — external deploy apply left in place for debugging.\n"+
			"  destroy: charly bundle del %s\n", name, name)
	default: // pod
		fmt.Fprintf(w, "\n[charly check run] bed %q FAILED — pod left running for debugging.\n"+
			"  inspect: %s | podman exec charly-%s sh\n"+
			"  destroy: charly remove %s\n", name, live, name, name)
	}
}

// writeBedSummary emits a YAML summary alongside the per-step logs. Hand-rolled to
// keep the file dependency-free and diff-friendly.
func writeBedSummary(dir string, res *bedRunResult) {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "bed: %s\n", res.Bed)
	fmt.Fprintf(&buf, "calver: %s\n", res.CalVer)
	fmt.Fprintln(&buf, "steps:")
	var total time.Duration
	for _, s := range res.Step {
		fmt.Fprintf(&buf, "  - name: %s\n", s.Name)
		fmt.Fprintf(&buf, "    duration_seconds: %d\n", int(s.Duration.Round(time.Second)/time.Second))
		fmt.Fprintf(&buf, "    ok: %t\n", s.OK)
		total += s.Duration
	}
	fmt.Fprintf(&buf, "total_seconds: %d\n", int(total.Round(time.Second)/time.Second))
	fmt.Fprintf(&buf, "ok: %t\n", res.OK)

	path := filepath.Join(dir, "summary.yml")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "charly check run %s: writing %s: %v\n", res.Bed, path, err)
	}
}

// vmDomainIdentity normalizes a deploy/bundle name into its per-deploy VM DOMAIN
// IDENTITY (the plugin-local alias for vmshared.VmDomainIdentity), used by the
// iterate VM-sandbox dispatch (`charly vm ssh <identity>`).
func vmDomainIdentity(deployName string) string {
	return vmshared.VmDomainIdentity(deployName)
}
