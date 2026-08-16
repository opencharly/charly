package check

// bed_run.go — the R10 acceptance-sequence engine for disposable test beds (P12:
// relocated from charly/check_bed_run.go), driving bed_session.go's plugin-side session
// + HostBuild("cli").
//
// A check bed is a `disposable: true` deploy. runCheckBed drives the canonical
// sequence against it:
//
//	build → check box → deploy add → config → start → check live →
//	fresh update (R10 acceptance gate) → tear down
//
// The lock / lease / repo-override-env / deploy-config-isolation / GPU-prereq lifecycle is fully
// plugin-side now (#55 W3 B2-full — bed_session.go's bedSetup/bedTeardown; the former "check-bed"
// HostBuild seam is deleted). bedSetup returns the node-derived BedDescriptor the kind-blind
// sequence below drives from. Every `charly` subcommand still rides HostBuild("cli") (the one
// genuine clause-3 cli-reentry leg); the plugin owns the sequence LOGIC, the per-step .log +
// summary.yml writes, and the exit-code classification. Readiness waits (waitReady, below) and
// members-up/-down (deploykit.BringUpMembers/TearDownMembers, #55 W3 A4) call spec/exec and
// sdk/deploykit directly — no host round-trip for either, using data already in hand from the
// setup reply.
//
// #33: the current post-rebase sequence passes `--domain <bedDomain>` on `charly vm
// create/destroy/start` while `charly vm build` stays ENTITY-scoped (VMTemplate).
// Preserved EXACTLY — d.BedDomain for --domain, d.VMTemplate for the build/entity arg.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
)

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

// bedAdd builds a `charly fleet add` argv for a BED deploy. Every deploy a bed makes goes through
// it, so the bed-only flags are declared once instead of at six call sites (R3).
//
// --dev-local-pkg is the deploy-side twin of the flag every bed image build already passes. Without
// it, a localpkg candy whose package source cannot be found takes the deploy path's benign skip and
// the bed installs nothing — which is how check-fedora-vm stopped building its rpm against an
// uninitialized pkg/fedora submodule and failed later at a live `rpm -q` that explained none of it.
// A bed exists to prove the in-development package builds and installs, so on a bed that condition
// must be loud.
func bedAdd(args ...string) []string {
	return append([]string{"fleet", "add"}, append(args, "--dev-local-pkg")...)
}

// configStartArgs builds the `charly config`/`charly start` argv for a pod bed's config+start
// steps. An add_candy: overlay bed's FRESH artifact to verify is the overlay `deploy-add` just
// built + persisted (resolved via the persisted resolved_image (FleetNode.ResolvedImage),
// correctly discriminated as a pod entry — the fleetDiscForEntity resolved_image fix) — NOT the base image's own --tag build
// ref. Passing --tag here would force config/start to deploy <base-image>:<imageTag> (an
// existing, but WRONG, un-overlaid reference), silently dropping every add_candy candy from the
// running container. A non-overlay bed (hasAddCandy=false) keeps --tag unchanged (no regression):
// its freshness proof IS the base image's own build tag, exactly as before this fix.
func configStartArgs(name, imageTag string, hasAddCandy bool) (configArgs, startArgs []string) {
	configArgs = []string{"config", name}
	startArgs = []string{"start", name}
	if !hasAddCandy {
		configArgs = withRunTag(configArgs, imageTag)
		startArgs = withRunTag(startArgs, imageTag)
	}
	return configArgs, startArgs
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
	// setup — opens the session (locks/lease/env/GPU-prereq) plugin-side and returns the
	// BedDescriptor the sequence drives from (#55 W3 B2-full: no more HostBuild round-trip).
	d, sess, err := bedSetup(ctx, ex, name, "")
	if err != nil {
		return nil, err
	}

	res := &bedRunResult{Bed: name, CalVer: d.Calver, OK: true}

	// GPU-prereq skip: setup acquired NOTHING (sess is nil), so run NO other op — write the
	// prereq-skip summary + return CheckSkippedError (exit 3).
	if d.PrereqSkip != nil {
		res.SkippedPrereq = true
		res.SkipReason = d.PrereqSkip.Reason
		res.Step = append(res.Step, stepResult{Name: "prereq-gpu-skipped", OK: true})
		writeBedSummary(d.LogDir, res)
		return res, &CheckSkippedError{Msg: fmt.Sprintf("charly check run %s: skipped (%s)", name, res.SkipReason)}
	}

	// bedNode is the bed-root FleetNode decoded once from d.NodeJSON — the members-up/-down
	// call sites below pass it directly to sdk/deploykit.BringUpMembers/TearDownMembers (#55 W3
	// A4), no HostBuild seam and no core-side session lookup needed anymore.
	var bedNode spec.FleetNode
	_ = json.Unmarshal(d.NodeJSON, &bedNode)

	// teardown runs on EVERY exit path after a successful setup — it releases the
	// session's locks/lease/env (NOT the deployed target). res.OK controls the
	// preempt-lease disposition (Release vs ReleaseFailed).
	defer func() {
		bedTeardown(ctx, ex, sess, res.OK)
	}()

	// bestEffort runs a `charly` subcommand host-side, discarding the result (the
	// pre-run cleanups that clear a lingering target from an interrupted run).
	bestEffort := func(argv ...string) {
		_, _ = bedCli(ex, ctx, true, argv...)
	}

	// Pre-run cleanup: clear any lingering target + sibling members left over from a previous
	// interrupted run, BEFORE anything seeds or reads this run's own overlay state. Hoisted out of
	// the Step-3 build/add/config/start switch below into its own block (#21 RCA — the K-wave
	// terminus RCA's preempt-live-pod defect): `remove --purge`/`fleet del` fan out to
	// deploykit.CleanDeployEntry per member, which DELETES the per-host overlay entry outright: with
	// this cleanup running AFTER persistBedDeployOverridePluginSide (as it did before this fix), it
	// silently destroyed the arbitration fields (Preemptible/RequiresExclusive/RequiresShared) the
	// persist call had just seeded — fields with no downstream re-writer (unlike ports/env/tunnel/
	// security, which `charly config` itself re-seeds on every run), so the loss was invisible for
	// every OTHER overlay field and every non-arbiter bed. VM beds never called CleanDeployEntry (their
	// cleanup is `vm destroy`, a different substrate teardown), so they were never exposed. Each arm's
	// cleanup body below is the SAME code the Step-3 switch used to run inline in each of its cases,
	// moved verbatim (R3 — no duplication, nothing left behind in Step 3).
	switch {
	case d.IsVM:
		bestEffort("vm", "destroy", d.VMTemplate, "--domain", d.BedDomain, "--if-exists")
	case d.IsGroup:
		bestEffort("remove", name, "--purge")
		_ = deploykit.TearDownMembers(&bedNode)
	default:
		if d.IsExternal {
			bestEffort("fleet", "del", name)
		} else {
			bestEffort("remove", name, "--purge")
		}
		_ = deploykit.TearDownMembers(&bedNode)
	}

	// Seed the per-host overlay with the bed ROOT's + each MEMBER's project-declared deploy-shaped
	// overrides PLUGIN-SIDE (#55 coneC-dsh β1 — the former host-side persistBedDeployOverrides wrapper
	// shed from charly core). The host seam threads the bed-root FleetNode (with nested peer Members)
	// as d.NodeJSON; persistBedDeployOverridePluginSide calls deploykit.PersistBedDeployOverrides with
	// plugin-side marshalNode + reader. MUST run AFTER the pre-run cleanup above and BEFORE anything
	// else reads the overlay (build/config/start): the pre-run cleanup deletes overlay entries via
	// `remove --purge`/CleanDeployEntry, and this persist's arbitration-role fields (Preemptible/
	// RequiresExclusive/RequiresShared) have no downstream re-writer — running it before cleanup let
	// cleanup silently destroy what it had just seeded (#21, first site). This call covers the
	// default (non-group) arm's own `charly config`/`charly start` steps below; the peer-members path
	// (BringUpMembers, both call sites) re-asserts the SAME invariant itself — see bringUpMembersFresh.
	persistBedDeployOverridePluginSide(ctx, ex, name, d)

	// bringUpMembersFresh persists this run's arbitration-role overlay fields IMMEDIATELY before every
	// deploykit.BringUpMembers call, rather than relying on whatever persist happened earlier in the
	// function. #21's SAME defect recurred at a SECOND site: the fresh-rebuild cycle's
	// rebuild-members-down (deploykit.TearDownMembers) purges each member via `remove --purge` →
	// CleanDeployEntry — identical to the pre-run cleanup's purge — deleting the overlay entries
	// re-bring-up-members then needs, with no re-persist in between. Folding persist+bring-up into one
	// call, used at BOTH BringUpMembers sites (Step 4's initial bring-up-members AND Step 5's
	// re-bring-up-members), makes the ordering structurally impossible to violate at either site,
	// instead of chasing each purge site with its own ad hoc re-persist call (R3 — one shared shape).
	bringUpMembersFresh := func() error {
		persistBedDeployOverridePluginSide(ctx, ex, name, d)
		return deploykit.BringUpMembers(&bedNode, d.ImageTag)
	}

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

	// waitReady picks WaitForVmSshReady vs WaitForContainerReady directly — no host round-trip
	// needed (#55 W3 B2): d.IsVM + d.BedDomain are already in hand from the setup reply, and
	// spec/exec's readiness gates are pure process-driving pollers with no session/registry
	// coupling (spec/exec/venue_wait.go's own header). Best-effort, matching the former op.
	waitReady := func() {
		if d.IsVM {
			// Wait on the per-deploy DOMAIN IDENTITY (charly-<BedDomain> is the live domain +
			// managed ssh alias, post-P33), NOT the shared kind:vm entity (d.VMTemplate).
			specexec.WaitForVmSshReady(d.BedDomain)
		} else {
			specexec.WaitForContainerReady(name)
		}
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
		// NESTED CHILDREN FIRST — the reverse of the deploy order above, which walks d.ChildKeys
		// pre-order issuing `fleet add <root>.<child>`. Teardown mirrors that exact collection with
		// `fleet del`, so the two halves cannot drift: a child the bed knows how to deploy is a
		// child it knows how to remove.
		//
		// This is an ADDITION, not a change to the root verb. The pod branch below keeps
		// `remove --purge`, which is the ONLY path that reaches purgeDeployArtifacts — named
		// volumes, gocryptfs volumes and the synthesized <name>-overlay images. `fleet del` cannot
		// purge: both purge call sites in candy/plugin-pod/remove_orchestration.go are gated on the
		// flag and candy/plugin-fleet never sets it, so swapping the root verb to `fleet del` would
		// have left three artifact classes behind on EVERY pod bed teardown.
		//
		// Without this loop a nested child simply survives the bed: `charly remove` is a container
		// verb with no deploy-tree walk, so check-sidecar-pod's ephemeral VM outlived every run —
		// contradicting the bed's own authored comment that it is "torn down as part of the bed's
		// own cleanup". No new tree walk is introduced here; d.ChildKeys is already host-resolved.
		for i := len(d.ChildKeys) - 1; i >= 0; i-- {
			childKey := d.ChildKeys[i]
			if err := step("cleanup-"+childKey, "fleet", "del", name+"."+childKey, "--assume-yes"); err != nil {
				// Warn, never fail: the root teardown below must still run, and a child that is
				// already gone (torn down mid-plan by the bed's own steps) is the common case.
				fmt.Fprintf(os.Stderr, "warning: tearing down nested child %s.%s: %v\n", name, childKey, err)
			}
		}

		var targetErr error
		switch {
		case d.IsVM:
			targetErr = step("cleanup", "vm", "destroy", d.VMTemplate, "--domain", d.BedDomain, "--if-exists")
		case d.IsGroup:
			// A targetless group has NO root container — members-down is the whole teardown.
		case d.IsExternal:
			targetErr = step("cleanup", "fleet", "del", name)
		default:
			targetErr = step("cleanup", "remove", name, "--purge")
		}
		membersErr := phase("cleanup-members", func() error {
			return deploykit.TearDownMembers(&bedNode)
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
	// ENTITY-scoped — bringUpMembers does the per-member `vm create --domain` + ssh-wait); a pod / kubernetes
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
	// `charly fleet add` (no container/VM lifecycle — no `charly config`/`charly
	// start`, teardown via `charly fleet del`).
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
		// --domain <BedDomain>. The pre-run `vm destroy --if-exists` now runs in the
		// hoisted pre-run-cleanup block above, before persist.
		if err := step("vm-build", "vm", "build", d.VMTemplate); err != nil {
			return fail("vm build %s: %w", d.VMTemplate, err)
		}
		if err := step("vm-create", "vm", "create", d.VMTemplate, "--domain", d.BedDomain); err != nil {
			return fail("vm create %s: %w", d.VMTemplate, err)
		}
		deployed = true // VM domain exists — keep it on any later failure
		waitReady()
		if err := step("deploy-add", bedAdd(name, d.VMTemplate)...); err != nil {
			return fail("fleet add %s: %w", name, err)
		}
		// Deploy the VM's nested HOST-ROOTED (kind:local) children only (d.LocalChildKeys, the
		// host-resolved deployNestedLocalChildren subset). A VM's nested CONTAINER children are
		// deployed IN-GUEST by plugin-deploy-vm's PostApply, so a host-side re-deploy would be wrong.
		for _, childKey := range d.LocalChildKeys {
			if err := step("deploy-"+childKey, bedAdd(name+"."+childKey)...); err != nil {
				return fail("deploy nested local child %s.%s: %w", name, childKey, err)
			}
		}
	case d.IsGroup:
		// Group bed: no root container — the members (subject + driver) ARE the deployment.
		// bringUpMembers (the members-up op in the runtime block below) deploys each member
		// (config+start per pod member, fleet add per local member). There is no root
		// deploy-add/config/start. The pre-run `remove --purge` + TearDownMembers now run in the
		// hoisted pre-run-cleanup block above, before persist.
		deployed = true // members will be brought up — keep state on a later failure
	default:
		// Pod beds → image ref; kind:local beds → local template ref; an EXTERNAL
		// deploy substrate composes its candies via add_candy: and carries no ref.
		positional := []string{name}
		switch {
		case d.IsExternal:
			// no ref — add_candy: is the workload
		case d.IsLocal:
			positional = append(positional, d.LocalRef)
		default:
			positional = append(positional, d.Image)
		}
		// Positionals first, then flags: bedAdd appends its own, so the ref must already be in
		// place before it is called.
		addArgs := append(bedAdd(positional...), "--node-only")
		// The pre-run tear-down of any lingering bed + sibling members from a previous interrupted
		// run now happens in the hoisted pre-run-cleanup block above, before persist.
		addArgs = withRunTag(addArgs, d.ImageTag)
		if err := step("deploy-add", addArgs...); err != nil {
			return fail("fleet add %s: %w", name, err)
		}
		deployed = true // target registered — keep it on any later failure
		// kind:local + external apply candies in place during deploy add; pod beds
		// need `charly config` + `charly start`.
		if !isInPlace {
			configArgs, startArgs := configStartArgs(name, d.ImageTag, d.HasAddCandy)
			if err := step("config", configArgs...); err != nil {
				return fail("config %s: %w", name, err)
			}
			if err := step("start", startArgs...); err != nil {
				return fail("start %s: %w", name, err)
			}
			waitReady()
			// Deploy any nested children onto the started substrate, pre-order.
			for _, childKey := range d.ChildKeys {
				if err := step("deploy-"+childKey, bedAdd(name+"."+childKey)...); err != nil {
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
		if err := phase("bring-up-members", bringUpMembersFresh); err != nil {
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
			return deploykit.TearDownMembers(&bedNode)
		}); err != nil {
			return fail("tear down members for fresh rebuild of %s: %w", name, err)
		}
		if d.RunRuntime {
			if err := phase("re-bring-up-members", bringUpMembersFresh); err != nil {
				return fail("re-bring up members for %s: %w", name, err)
			}
			if err := checkLiveTree("check-live-rebuild"); err != nil {
				return fail("check live (fresh rebuild) %s: %w", name, err)
			}
		}
	} else if !opts.NoRebuild {
		// The fresh-rebuild gate must verify the JUST-BUILT per-run image, not re-resolve
		// the untagged logical box name: `charly update` without --tag resolves "newest
		// local CalVer", and a bed-run tag (<bed>-<calver>) is NOT a plain CalVer, so the
		// resolver's tag-CalVer tiebreak is empty and the lexical fallback can select an
		// OLDER cached bed image (the check-live-rebuild stale-image defect). Pin the same
		// per-run tag the build + deploy-add steps used — the exact principle the
		// runTaggedImageRef comment above states for the check steps.
		if err := step("update", withRunTag([]string{"update", name}, d.ImageTag)...); err != nil {
			return fail("update %s: %w", name, err)
		}
		// EVERY runtime, non-in-place bed gets a genuine post-rebuild check-live pass — not just
		// ones with nested children. Before this fix, a childless bed's `checkLiveTree` call was
		// gated on `len(d.ChildKeys) > 0`, so its OWN deploy-level plan checks (a `check:` step
		// asserting the running container's state — e.g. check-pod-overlay's ripgrep-installed
		// assertion) were never re-evaluated after `charly update`: the SAME single check-live pass
		// that ran before `update` was the only one the whole 12-step bed run ever executed, so a
		// regression that broke ONLY the fresh-rebuild path (as distinct from the initial-deploy
		// path) would still show 100% green (a live-bed-validator finding on charly#186's
		// check-pod-overlay — "a gate that cannot fail on the change proves nothing", R10). For a
		// NESTED bed, the fresh rebuild additionally discards the substrate's children, so those
		// must be explicitly re-applied first. A VM's own update recreates the domain: a nested
		// target:pod child's persistent in-guest quadlet (installed by plugin-deploy-vm's PostApply)
		// auto-starts on the fresh boot, so it needs only a wait — but a nested target:local child
		// (deployed via a ONE-TIME InstallPlan walk over SSH, no persistent service) does NOT survive
		// the disk recreate and must be explicitly redeployed, exactly like the initial-deploy path
		// already does for it (d.LocalChildKeys, above). This loop was missing here entirely until the
		// K-wave terminus RCA (#20): check-live-rebuild's own gate used to be masked for VM beds by a
		// classification bug (the deleted bedExternalInPlace treated any externalized, non-container
		// substrate as "in-place" — including VM — so this whole block never ran for a VM bed and the
		// gap went unverified); #55 W3 B2-full's ExternalInPlaceVenue fix corrected that classification
		// as a side effect, which finally exercised this path and surfaced the missing redeploy.
		if d.RunRuntime && !isInPlace {
			if d.IsVM {
				waitReady()
				for _, childKey := range d.LocalChildKeys {
					if err := step("redeploy-"+childKey, bedAdd(name+"."+childKey)...); err != nil {
						return fail("re-deploy nested local child %s.%s (fresh rebuild): %w", name, childKey, err)
					}
				}
			} else {
				waitReady()
				for _, childKey := range d.ChildKeys {
					if err := step("redeploy-"+childKey, bedAdd(name+"."+childKey)...); err != nil {
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
		case "check", "box", "fleet", "vm":
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
	if ov := os.Getenv(proc.RepoOverrideEnv); ov != "" {
		live = proc.RepoOverrideEnv + "='" + ov + "' " + live
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
			"  destroy: charly fleet del %s\n", name, name)
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

// vmDomainIdentity normalizes a deploy/fleet name into its per-deploy VM DOMAIN
// IDENTITY (the plugin-local alias for vmshared.VmDomainIdentity), used by the
// iterate VM-sandbox dispatch (`charly vm ssh <identity>`).
func vmDomainIdentity(deployName string) string {
	return vmshared.VmDomainIdentity(deployName)
}
