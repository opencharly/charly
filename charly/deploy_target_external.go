package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// externalDeployTarget is the UnifiedDeployTarget adapter for an OUT-OF-PROCESS deploy
// provider (a grpcProvider whose class is deploy but which is Invoke-only, not a typed
// DeployTargetProvider). It is the E3-deploy consumer of the E3b reverse channel: the
// full external deploy LIFECYCLE rides over the already-built ExecutorService reverse
// channel —
//
//   - Add Invokes the provider (OpExecute) with the deployment's InstallPlans in
//     op.Params and a venue descriptor in op.Env, the host's executor served on the
//     go-plugin broker (grpcProvider.InvokeWithExecutor) so the plugin runs the
//     deployment's shell/SSH ops on the real venue it cannot hold across the process
//     boundary, then DECODES the structured DeployReply and writes its ReverseOps +
//     record into the ledger via the SAME install_ledger.go path a built-in Add uses;
//   - Test runs the deploy-scope checks HOST-SIDE (the plugin is not involved — the
//     checks are in-proc CheckVerbProviders run against the executor);
//   - Update re-Invokes OpExecute with fresh plans (idempotent re-Add);
//   - Del replays the RECORDED ReverseOps from the ledger (no plugin call) — the
//     record-and-replay invariant: only recorded ops are reversed, never recomputed.
//
// Built-in deploy targets (local/vm/pod/k8s/android) use their typed ResolveTarget
// path instead.
type externalDeployTarget struct {
	name string
	prov *grpcProvider
	exec deploykit.DeployExecutor

	// node is the dispatch-merged BundleNode (set by ResolveTarget). A substrate with a
	// registered lifecycle hook (vm) needs it to resolve its kind:vm entity for the
	// host-side lifecycle (start/stop/rebuild/teardown bookkeeping). nil for a ref-based
	// deploy with no charly.yml entry.
	node *spec.BundleNode

	// nodeOnly mirrors `charly bundle add --node-only`: when true, Add does NOT run the
	// substrate's PostApply (vm: skip the nested target:pod children — the caller deploys
	// them via the dotted path). Set by the dispatcher from deployAddCmd.NodeOnly.
	nodeOnly bool

	// rebootable records whether this substrate owns a charly-managed venue that may be
	// rebooted mid-walk (a VM guest) — true iff a substrateLifecycle is registered for the
	// word. Threaded to the reverse server so a RebootStep reboots a VM guest but is
	// skip-and-noted on a host venue (local). Set by apply() before the Invoke.
	rebootable bool

	// paths is the ledger root for this deploy's records. nil →
	// DefaultLedgerPaths() (the operator ledger). Tests redirect it to a temp dir.
	paths *kit.LedgerPaths

	// revRunner is the ReverseRunner Del hands to runReverseOps. nil → reverse_ops
	// falls back to local exec.Command (sudo bash / bash) — the host-teardown path,
	// matching the executor (ShellExecutor) ResolveTarget gives an external deploy.
	// Tests inject a no-sudo runner.
	revRunner kit.ReverseRunner

	// KeepRepoChanges / KeepServices are the `charly bundle del --keep-…` teardown gates,
	// set by the del-command dispatcher (bundle_add_cmd.go) for the externalized local
	// substrate, then handed to teardownHostDeploy's ReverseExecutor in Del.
	KeepRepoChanges bool
	KeepServices    bool

	// KeepImage is the `charly bundle del --keep-image` gate, set by the del-command
	// dispatcher for the externalized pod substrate (suppress the <name>-overlay image
	// drop); handed to the substrate lifecycle hook's PostTeardown in Del. Inert for
	// substrates whose PostTeardown ignores it (vm) or that have no lifecycle hook.
	KeepImage bool

	// build is the host-ENGINE context (project Config + dir + DistroCfg) the RunHostStep
	// reverse leg needs when the plugin walks a plan carrying a host-engine step kind
	// (Builder / LocalPkgInstall resolve a short / namespace-qualified builder image and
	// fall back to a local `charly box build`; SystemPackages renders the format's
	// phase.install.host template from DistroCfg). Populated by Add from the DeployContext;
	// the zero value (no project context) is fine for a deploy whose plan has no host-engine step.
	build buildEngineContext
}

func (t *externalDeployTarget) Name() string                       { return t.name }
func (t *externalDeployTarget) Kind() string                       { return "host" } // ops run on the host venue via the reverse channel
func (t *externalDeployTarget) Executor() deploykit.DeployExecutor { return t.exec }

func (t *externalDeployTarget) ledgerPaths() (*kit.LedgerPaths, error) {
	if t.paths != nil {
		return t.paths, nil
	}
	return kit.DefaultLedgerPaths()
}

// deployID is the deterministic ledger key for this external deploy — derived
// from the deploy name (no image / candy set), so Add and Del agree on which
// record to write / read without scanning the ledger by Target (the external
// target's Kind()=="host" would otherwise collide with a real host deploy in
// the local deploy target.Del's `Target=="host"` scan).
func (t *externalDeployTarget) deployID() string {
	return deploykit.ComputeDeployID(t.name, nil, nil)
}

// Add applies the deployment via the out-of-process provider, then records the returned
// teardown ops + provenance into the ledger. For a deployment carrying candies that declare
// secrets / artifacts (now reached by ANY external substrate — local most of all), it does
// the host-side secret injection BEFORE the wire payload is projected, and the candy-artifact
// retrieval + --verify AFTER the plugin applied — the SAME shared helpers the in-proc
// local/vm targets use (R3). All are no-ops for a deployment whose candies declare neither
// (the android/k8s/example substrates), so the path stays generic.
func (t *externalDeployTarget) Add(ctx context.Context, dctx *DeployContext, plans []*deploykit.InstallPlan, opts deploykit.EmitOpts) error {
	var node *spec.BundleNode
	var dir string
	if dctx != nil {
		node = dctx.Node
		dir = dctx.Dir
		// Capture the host-engine context so the RunHostStep leg can resolve a builder
		// image + run the host build, and render a SystemPackages step's host install
		// template from DistroCfg, when the plugin walks a host-engine step.
		t.build = buildEngineContext{Cfg: dctx.Cfg, ProjectDir: dctx.Dir, DistroCfg: dctx.DistroCfg}
	}

	// Host-side secret injection BEFORE wireView (Part 3): resolve candy secret_requires /
	// secret_accepts and inject them into each OpStep's env so the projected views carry the
	// resolved values (the plugin runs the steps with secrets already present). The SAME
	// prepareCandySecrets the in-proc local/vm Add uses (R3). candyList feeds artifact
	// retrieval below; secretEnv feeds artifact-path substitution.
	var candyList []*Candy
	var secretEnv map[string]string
	if dir != "" {
		var serr error
		candyList, secretEnv, serr = prepareCandySecrets(plans, dir)
		if serr != nil {
			return fmt.Errorf("external deploy %q: loading candies for secret resolution: %w", t.name, serr)
		}
	}

	// apply runs the substrate's host-side venue preparation (vm: boot + build the guest SSH
	// executor → t.exec), then projects the views + Invokes the plugin walk.
	if err := t.apply(ctx, node, dir, plans, opts); err != nil {
		return err
	}
	if opts.DryRun {
		return nil
	}

	// Retrieve candy artifacts (+ k3s post-hook) host-side — guarded no-op when no candy
	// declares an artifact: block. artifactEnv = secretEnv overlaid with the node's env:.
	// The artifact KEY is substrate-supplied (vm → "vm:<entity>" for the k3s ClusterProfile
	// naming); the generic default is the deploy name.
	artifactEnv := buildArtifactEnv(secretEnv, node)
	artifactKey := t.name
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		if k := life.ArtifactKey(t.name, node); k != "" {
			artifactKey = k
		}
	}
	// artifactKey is ENTITY-scoped (the shared per-VM k3s cluster cache dir + context);
	// t.name is the real DEPLOY name (domain identity) the k3s kubeconfig port-forward
	// lookup keys off (the auto-forward allocation is persisted per-deploy at vm-create).
	if err := retrieveArtifactsAndK3s(ctx, t.exec, candyList, artifactKey, t.name, artifactEnv, opts); err != nil {
		return fmt.Errorf("external deploy %q: retrieving candy artifacts: %w", t.name, err)
	}

	// Substrate host orchestration AFTER the walk (vm: nested target:pod children as
	// persistent in-guest quadlets). Skipped under --node-only (the caller deploys children
	// via the dotted path). No-op for substrates without a lifecycle hook.
	if !t.nodeOnly {
		if life, ok := substrateLifecycleFor(t.prov.word); ok {
			if err := life.PostApply(ctx, t.name, dir, node, t.exec, opts); err != nil {
				return fmt.Errorf("external deploy %q: post-apply: %w", t.name, err)
			}
		}
	}

	// --verify: run the deployment's deploy-scope check probes on the venue we just deployed
	// to. Default (Verify=false) is a no-op. Reuses checkLocalDeployScope so external local
	// `--verify` sources + runs probes identically to `charly check live` (R3).
	//
	// SKIPPED for a substrate with a lifecycle hook (vm): the in-Add --verify resolves a
	// deploy's checks against the venue executor, which is correct for a flat host/local deploy
	// but WRONG for a VM bed — (1) its libvirt:/spice: probes resolve the live domain from the
	// per-deploy DOMAIN IDENTITY (charly-<domain>, via the check runner's VmTargetName), which the
	// in-Add path does not thread; and (2) its FLATTENED plan carries the nested-child (member)
	// checks, which are not yet deployed at Add time (the bed runner deploys members AFTER bundle
	// add, then check-lives the whole tree). The in-proc VM target never ran an in-Add --verify for
	// exactly this reason — verification is the bed runner's separate `charly check live` phase,
	// which resolves the per-deploy domain + runs after the full tree is up. So a lifecycle substrate
	// defers verification to check-live (R3 parity).
	if opts.Verify {
		if _, isLifecycle := substrateLifecycleFor(t.prov.word); isLifecycle {
			fmt.Fprintf(os.Stderr, "external deploy %q: --verify deferred to `charly check live` (the %s substrate verifies its live venue post-deploy, with the venue's runtime identity)\n", t.name, t.prov.word)
		} else {
			fails, verr := checkLocalDeployScope(dir, node, t.name, "", "", nil, t.exec, "text")
			if verr != nil {
				return fmt.Errorf("external deploy %q: --verify: %w", t.name, verr)
			}
			if fails > 0 {
				return fmt.Errorf("external deploy %q: --verify: %d deploy-scope check(s) failed", t.name, fails)
			}
		}
	}
	return nil
}

// Update re-applies the deployment over the wire — an idempotent re-Add (mirrors
// the local deploy target.Update's re-Emit). The unified Update signature carries no
// DeployContext, so the venue descriptor carries only the deploy name; a substrate
// preresolver (if any) re-resolves the node from the tree by name.
func (t *externalDeployTarget) Update(ctx context.Context, plans []*deploykit.InstallPlan, opts UpdateOpts) error {
	return t.apply(ctx, t.node, "", plans, deploykit.EmitOpts{
		DryRun:           opts.DryRun,
		AllowRepoChanges: opts.AllowRepoChanges,
		AllowRootTasks:   opts.AllowRootTasks,
		WithServices:     opts.WithServices,
		AssumeYes:        opts.AssumeYes,
	})
}

// apply is the shared Add/Update body: run the substrate's host-side venue preparation
// (vm: boot + build the guest SSH executor the reverse channel serves), marshal the plans +
// venue (with any substrate-specific preresolved payload), Invoke the provider with the
// host executor on the broker, decode the reply, and (unless DryRun) persist the teardown
// ops + record to the ledger.
func (t *externalDeployTarget) apply(ctx context.Context, node *spec.BundleNode, dir string, plans []*deploykit.InstallPlan, opts deploykit.EmitOpts) error {
	dryRun := opts.DryRun
	// Substrate venue preparation (Design A): a substrate with a registered lifecycle hook
	// (vm) runs its host-side preflight (boot the domain, WaitForSSH/CloudInit, charly-in-
	// guest, persist state) and returns the executor the reverse channel must serve — the
	// guest SSHExecutor, NOT the ResolveTarget placeholder. Skipped on a dry-run (no live
	// venue). The generic target never branches on the substrate word; only on whether a
	// hook is registered. A hook'd substrate is rebootable (a RebootStep reboots its guest).
	if !dryRun {
		if life, ok := substrateLifecycleFor(t.prov.word); ok {
			exec, err := life.PrepareVenue(ctx, t.name, dir, node, plans, opts)
			if err != nil {
				return fmt.Errorf("external deploy %q: prepare venue: %w", t.name, err)
			}
			if exec != nil {
				t.exec = exec
			}
			t.rebootable = true
		} else if opts.ParentExec != nil {
			// A NESTED external deploy with no lifecycle hook (a `target: local` child under a
			// vm/pod) runs the walk in the PARENT'S venue (the guest / container), NOT the host:
			// opts.ParentExec is the already-composed executor chain into the parent (built by
			// the dispatch's ancestor walk — host→ssh-vm for a nested-local-in-vm child like
			// arch-host). The ResolveTarget placeholder (ShellExecutor for host:local) is the
			// FLAT-deploy executor; the nested case overrides it here. The in-proc
			// LocalDeployTarget composed opts.ParentExec the same way — externalDeployTarget
			// must too, or a nested-local-in-vm child wrongly applies on the operator host (R3 —
			// the nested-deploy parity the local externalization dropped). A lifecycle substrate
			// (vm) composes ParentExec inside PrepareVenue (NestedExecutor over the parent), so
			// this branch is the non-lifecycle (local/android/k8s) path only.
			t.exec = opts.ParentExec
		}
	}
	// Fork-A host-side pre-pass (live only): resolve {{.Home}} against the venue home and
	// capture the deploy-time-stateful reverse state (ShellHook.EnvFile, ServicePackaged
	// .PriorEnabled) on the LIVE venue BEFORE projecting the views, so each step's
	// host-computed Reverse() (carried in InstallStepView.ReverseOps) is faithful. The
	// plugin ECHOES those ops; the Reverse() rule stays ONCE in package main (R3). A
	// dry-run has no live venue, so it skips this (no teardown is recorded on a dry-run).
	if !dryRun {
		if err := t.prepareReverseState(ctx, plans); err != nil {
			return fmt.Errorf("external deploy %q: %w", t.name, err)
		}
	}
	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		if p != nil {
			views = append(views, deploykit.WireView(p))
		}
	}
	params, err := json.Marshal(views)
	if err != nil {
		return fmt.Errorf("external deploy %q: marshal plans: %w", t.name, err)
	}
	// Venue descriptor: the deploy name + the merged deploy-node env (reusing the
	// shared buildArtifactEnv flattener, R3). The plugin reads it to locate where
	// to apply its effects on the venue.
	venue := spec.DeployVenue{DeployName: t.name, Env: buildArtifactEnv(nil, node)}
	// Substrate preresolution (F1): a registered host-side preresolver (e.g. the
	// android one — resolve the live device endpoint + collect the apk install specs)
	// produces the substrate-specific payload the plugin needs but cannot resolve
	// itself. Skipped on a dry-run (it requires a LIVE venue — engine inspect on the
	// running pod). The generic target never branches on the substrate; only the
	// registered preresolver body is substrate-specific.
	if !dryRun {
		if pre, ok := deployPreresolverFor(t.prov.word); ok {
			payload, perr := pre(t.name, dir, node, plans)
			if perr != nil {
				return fmt.Errorf("external deploy %q: preresolve substrate %q: %w", t.name, t.prov.word, perr)
			}
			venue.Substrate = payload
		}
	}
	envJSON, err := json.Marshal(venue)
	if err != nil {
		return fmt.Errorf("external deploy %q: marshal venue: %w", t.name, err)
	}
	if dryRun {
		// A dry-run does NOT Invoke the provider — Invoke IS the apply (the plugin
		// runs ops on the venue via the reverse channel), so a no-side-effect
		// dry-run stops here after validating the wire payload marshalled.
		fmt.Printf("[dry-run] external deploy %s (target=%s): would apply %d plan(s) via the reverse channel\n",
			t.name, t.prov.word, len(views))
		return nil
	}
	res, err := t.prov.InvokeWithExecutor(ctx,
		&Operation{Reserved: t.prov.word, Op: OpExecute, Params: params, Env: envJSON}, t.exec, t.build, t.rebootable, nil)
	if err != nil {
		return err
	}
	var reply spec.DeployReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return fmt.Errorf("external deploy %q: decode reply: %w", t.name, err)
		}
	}
	// Host-side teardown record (computeDeployID + the plugin's reverse ops) — read by Del.
	if err := t.recordDeploy(reply); err != nil {
		return err
	}
	// Venue-side self-contained ledger (deploy + per-candy layer records written THROUGH the
	// executor into the venue's ~/.config/opencharly/installed/) — for a remote venue (a VM
	// guest, or a nested target:local-in-guest child) this is the zero-operator-side-effects
	// record the prior in-proc VM target wrote via *Via over the SSH executor, and what the bed's guest-ledger
	// probes assert. A host-local venue is a no-op (recordDeploy already wrote the operator-side
	// ledger). See recordVenueLedger.
	return t.recordVenueLedger(plans)
}

// recordVenueLedger writes the deploy record + a per-candy layer record INTO THE VENUE via
// the executor, so a remote venue (a VM guest, or a nested target:local deploy whose venue is
// the guest) carries the self-contained ~/.config/opencharly/installed/{deploys,layers}
// ledger the prior in-proc VM target wrote — the "zero-operator-side-effects" design, and the
// records + dirs the bed's guest-ledger probes (ah-deploy-recorded / ah-ledger-deploys-dir /
// ah-ledger-layers-dir) assert. The *Via writers resolve `~` in the VENUE shell (the guest)
// and create BOTH ledger dirs.
//
// A HOST-LOCAL venue (ShellExecutor) is a no-op: recordDeploy already wrote the operator-side
// ledger at the same paths, so the venue IS the operator host. The host-side teardown record
// (recordDeploy: computeDeployID + reverse ops) is SEPARATE and drives `charly bundle del`
// (the venue ledger is provenance, never replayed) — so this venue write is purely additive
// and never disturbs teardown. Idempotent on re-apply (Update): the *Via writers overwrite.
func (t *externalDeployTarget) recordVenueLedger(plans []*deploykit.InstallPlan) error {
	if t.exec == nil {
		return nil
	}
	if _, isLocal := t.exec.(kit.ShellExecutor); isLocal {
		return nil // host-local venue — recordDeploy already wrote the operator-side ledger
	}
	paths, err := t.ledgerPaths()
	if err != nil {
		return err
	}
	// The guest deploy-record id is provenance only (Del replays the HOST record by
	// computeDeployID, never this one), so any non-empty egress-valid id serves: prefer the
	// plans' shared DeployID, else the deterministic host deployID (computeDeployID(name)).
	id := t.deployID()
	for _, p := range plans {
		if p != nil && p.DeployID != "" {
			id = p.DeployID
			break
		}
	}
	deployRec := &kit.DeployRecord{
		DeployID:   id,
		Image:      t.name,
		Target:     t.prov.word,
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, p := range plans {
		if p == nil || p.Candy == "" {
			continue
		}
		ver := p.Version
		if verr := kit.AddCandyDeploymentVia(t.exec, paths, p.Candy, id, func(rec *kit.CandyRecord) {
			rec.Version = ver
		}); verr != nil {
			return fmt.Errorf("external deploy %q: venue ledger candy %s: %w", t.name, p.Candy, verr)
		}
		deployRec.Candy = append(deployRec.Candy, p.Candy)
	}
	// WriteDeployRecordVia creates the deploys/ dir even when no candy wrote a layer record
	// (a boot-only deploy), so the guest-ledger dir probes still pass.
	if werr := kit.WriteDeployRecordVia(t.exec, paths, deployRec); werr != nil {
		return fmt.Errorf("external deploy %q: venue ledger deploy record: %w", t.name, werr)
	}
	return nil
}

// prepareReverseState is the Fork-A host-side pre-pass: it resolves the venue home and
// captures the two deploy-time-stateful reverse inputs on the LIVE venue so the
// host-computed step.Reverse() (carried in InstallStepView.ReverseOps for the plugin to
// echo) is faithful:
//
//   - ShellHookStep.EnvFile: the env.d path the plugin will write into, derived from the
//     resolved venue home — so the recorded teardown op (ReverseOpRemoveEnvdFile) targets
//     the right file. plan.ResolveHome also expands {{.Home}} in EnvVars / PathAdd /
//     ShellSnippet Destination / FileStep.Dest so the plugin receives ABSOLUTE paths.
//   - ServicePackagedStep.PriorEnabled: probed via `systemctl is-enabled` on the venue, so
//     teardown re-enables a unit that was already enabled before the deploy.
//
// The venue exec (ResolveHome / systemctl is-enabled) runs ONLY when a plan actually
// carries a step that consumes it — a home-token-bearing step (ShellHook/ShellSnippet/
// File) needs the resolved venue home, and a ServicePackagedStep needs the is-enabled
// probe. A plan carrying NEITHER (an android apk-only device plan; a k8s plan with no
// such steps) is skipped entirely: calling ResolveHome unconditionally there was a live
// `podman exec` / `ssh` that HARD-FAILS (exit 125 "no such container" / exit 255 ssh)
// when the venue isn't reachable yet — never a "no-op", despite the old comment's claim.
func (t *externalDeployTarget) prepareReverseState(ctx context.Context, plans []*deploykit.InstallPlan) error {
	needsHome := false
	needsServiceProbe := false
	for _, p := range plans {
		if p == nil {
			continue
		}
		for _, step := range p.Steps {
			switch step.(type) {
			case *deploykit.ShellHookStep, *deploykit.ShellSnippetStep, *deploykit.FileStep:
				needsHome = true
			case *deploykit.ServicePackagedStep:
				needsServiceProbe = true
			}
		}
	}
	if !needsHome && !needsServiceProbe {
		return nil
	}
	var home string
	if needsHome {
		h, err := t.exec.ResolveHome(ctx, "")
		if err != nil {
			return fmt.Errorf("resolve venue home: %w", err)
		}
		home = h
	}
	for _, p := range plans {
		if p == nil {
			continue
		}
		if home != "" {
			deploykit.ResolveHome(p, home)
		}
		for _, step := range p.Steps {
			switch s := step.(type) {
			case *deploykit.ShellHookStep:
				if s.EnvFile == "" && home != "" {
					s.EnvFile = EnvdFilePath(home, s.CandyName)
				}
			case *deploykit.ServicePackagedStep:
				s.PriorEnabled = venueUnitEnabled(ctx, t.exec, s.Unit, s.TargetScope)
			}
		}
	}
	return nil
}

// reverseRunnerForExecutor derives the ReverseRunner a `charly bundle del` replays the
// recorded ReverseOps over, from the deploy's executor (Δ2). An explicit runner (a test
// injection) wins. Otherwise: an *SSHExecutor venue (a vm guest, or a `local: {host:
// user@machine}` remote) replays IN THE GUEST/on-the-remote via an sshReverseRunner; any
// other venue (ShellExecutor for host:local) returns nil → reverse_ops falls back to local
// exec.Command. This is the SAME generalization that makes a vm teardown run in the guest
// AND a local-remote teardown run on the remote (R3) — previously a local-remote teardown
// wrongly ran on the operator host.
func reverseRunnerForExecutor(exec deploykit.DeployExecutor, existing kit.ReverseRunner) kit.ReverseRunner {
	if existing != nil {
		return existing
	}
	if sshExec, isSSH := exec.(*kit.SSHExecutor); isSSH {
		return &sshReverseRunner{exec: sshExec}
	}
	return nil
}

// venueUnitEnabled reports whether a systemd unit is enabled on the venue — the
// executor-backed analogue of systemctlIsEnabled (which uses local exec.Command). Used by
// prepareReverseState to capture ServicePackaged.PriorEnabled before the plugin enables the
// unit, so teardown restores the prior state. A probe failure (executor error / non-zero
// exit) reports "not enabled" (the safe default — teardown then disables, never spuriously
// re-enables).
func venueUnitEnabled(ctx context.Context, exec deploykit.DeployExecutor, unit string, scope spec.Scope) bool {
	cmd := "systemctl is-enabled --quiet " + shQuoteArg(unit)
	if scope == spec.ScopeUser {
		cmd = "systemctl --user is-enabled --quiet " + shQuoteArg(unit)
	}
	_, _, exit, err := exec.RunCapture(ctx, cmd)
	return err == nil && exit == 0
}

// recordDeploy persists the external deploy's teardown ops + provenance into the
// ledger via the SAME install_ledger.go path a built-in Add uses: one CandyRecord
// carrying the ReverseOps, plus a DeployRecord keyed on deployID() that names the
// candy. Idempotent across re-applies (Update): the candy's ReverseOps are
// REPLACED, not appended, so a re-apply never doubles the teardown.
func (t *externalDeployTarget) recordDeploy(reply spec.DeployReply) error {
	paths, err := t.ledgerPaths()
	if err != nil {
		return err
	}
	candy := reply.Record.Candy
	if candy == "" {
		candy = t.name // fall back to the deploy name so teardown always has a key
	}
	id := t.deployID()
	reverseOps := reply.ReverseOps
	// Fill each ReverseOpPackageRemove op's host-venue removal command from the
	// deploy's DistroConfig BEFORE it lands in the ledger — the host renders it
	// (it has the DistroConfig; the out-of-process plugin does not). The aur
	// builder (kit.BuilderReverse) records a ReverseOpPackageRemove with an EMPTY
	// UninstallCmd, deferring to exactly this host-side render; without it the
	// `charly bundle del` teardown fails loudly (reversePackageRemove errors on an
	// empty command). t.build.DistroCfg is the SAME DistroConfig the deploy compile
	// used (set by Add from the DeployContext); it is populated for every external
	// local/vm deploy that routes through here. R3: the SAME shared filler the
	// in-proc local/vm deploy targets called before externalization — idempotent
	// (an op already carrying a command, or a non-package-remove op, is skipped;
	// a nil DistroConfig is a no-op).
	kit.FillReverseUninstallCmds(reverseOps, func(format string, packages []string) string {
		if t.build.DistroCfg == nil {
			return ""
		}
		fd := t.build.DistroCfg.FindFormat(format)
		if fd == nil || strings.TrimSpace(fd.UninstallTemplate) == "" {
			return ""
		}
		ctx := &spec.InstallContext{Packages: append([]string(nil), packages...)}
		rendered, err := buildkit.RenderTemplate(format+"-uninstall", fd.UninstallTemplate, ctx)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(rendered)
	})
	if err := kit.AddCandyDeployment(paths, candy, id, func(rec *kit.CandyRecord) {
		rec.Version = reply.Record.Version
		rec.ReverseOps = append([]spec.ReverseOp(nil), reverseOps...) // replace (idempotent)
	}); err != nil {
		return fmt.Errorf("external deploy %q: record candy: %w", t.name, err)
	}
	return kit.WriteDeployRecord(paths, &kit.DeployRecord{
		DeployID:   id,
		Image:      t.name,
		Target:     t.prov.word, // the deploy WORD (e.g. "exampledeploy") — NOT "host"
		Candy:      []string{candy},
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// Test runs the deploy-scope checks against the host venue. The plugin is NOT
// involved — the checks are in-proc CheckVerbProviders run against t.exec, the
// SAME runUnifiedTargetChecks path the host/pod/vm targets use (R3).
func (t *externalDeployTarget) Test(ctx context.Context, checks []spec.Op, opts TestOpts) error {
	return runUnifiedTargetChecks(ctx, t.exec, t.Kind(), t.name, checks, opts)
}

// Del replays the RECORDED ReverseOps for this deploy (no plugin call): reads the
// ledger record by deployID() and reverses it via teardownHostDeploy — the SAME
// host-teardown helper the local deploy target.Del uses (R3). Only recorded ops are
// replayed, never recomputed from a manifest.
func (t *externalDeployTarget) Del(ctx context.Context, opts DelOpts) error {
	paths, err := t.ledgerPaths()
	if err != nil {
		return err
	}
	rec, err := kit.ReadDeployRecord(paths, t.deployID())
	if err != nil {
		return err
	}
	if rec == nil {
		return nil // nothing recorded — idempotent teardown
	}
	if opts.DryRun {
		fmt.Printf("[dry-run] would tear down external deploy %s (target=%s, %d candies)\n",
			rec.DeployID, rec.Target, len(rec.Candy))
		return nil
	}

	// Resolve the teardown executor + reverse runner. A substrate with a lifecycle hook
	// (vm) supplies the guest SSHExecutor so the recorded ReverseOps replay IN THE GUEST.
	// Otherwise t.exec (the ResolveTarget-selected executor) drives it: Shell for
	// host:local, SSH for host:user@machine. The reverse runner is DERIVED from that
	// executor (Δ2): when it is an *SSHExecutor the teardown wraps it in an sshReverseRunner
	// so the ops run over SSH on the venue, not on the operator host — the SAME generalization
	// that makes a `local: {host: user@machine}` remote teardown replay remotely (R3). nil
	// runner → reverse_ops falls back to local exec.Command (the host:local path).
	exec := t.exec
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		if e, lerr := life.VenueExecutor(t.name, t.node); lerr != nil {
			return fmt.Errorf("external deploy %q: teardown executor: %w", t.name, lerr)
		} else if e != nil {
			exec = e
		}
	}
	runner := reverseRunnerForExecutor(exec, t.revRunner)
	re := &hostReverseExec{
		DryRun:          opts.DryRun,
		KeepRepoChanges: t.KeepRepoChanges,
		KeepServices:    t.KeepServices,
		Runner:          runner,
	}
	if err := teardownHostDeploy(paths, rec, os.Getenv("HOME"), re); err != nil {
		return err
	}

	// Substrate host-side teardown cleanup (vm: ssh-config stanza + charly.yml entry +
	// ephemeral lifecycle; pod: `charly remove` + drop the <name>-overlay images +
	// ephemeral). No-op for substrates without a lifecycle hook. t.KeepImage gates pod's
	// overlay-image drop (vm ignores it).
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		if err := life.PostTeardown(t.name, t.node, t.KeepImage); err != nil {
			return fmt.Errorf("external deploy %q: post-teardown: %w", t.name, err)
		}
	}
	fmt.Printf("Removed external deploy %s (%s)\n", rec.DeployID, rec.Target)
	return nil
}

// ErrNotSupportedOnExternal is returned by lifecycle methods that have no meaning
// for an external (out-of-process) deploy target. Like the host target it runs on
// the host venue with no separate runtime to start/stop or journal to stream;
// mirrors ErrNotSupportedOnHost.
var ErrNotSupportedOnExternal = errors.New("lifecycle operation not supported on external deploy target")

// Rebuild re-creates the deployment. A substrate with a lifecycle hook (vm) delegates to
// it — for vm: `charly vm destroy`+`build`+`create`+`start`+`charly bundle add` (re-creating
// the domain, the WHOLE point of Design A, since `charly update <vm-bed>` — the disposable
// bed's fresh-rebuild R10 gate — routes here). A hookless substrate (local/android/k8s) uses
// the refresh path: re-run `charly bundle add <name>`, an idempotent re-apply. The Disposable
// gate is checked by the caller's classification logic, so this method does not re-validate.
func (t *externalDeployTarget) Rebuild(ctx context.Context, opts RebuildOpts) error {
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		return life.Rebuild(ctx, t.name, t.node, opts)
	}
	if opts.DryRun {
		fmt.Printf("dry-run: charly bundle add %s\n", t.name)
		return nil
	}
	return runCharlySubcommand("bundle", "add", t.name)
}

// Status reports the deploy's runtime state. A substrate with a lifecycle hook (vm) reports
// the live VM state (`charly vm list`); a hookless substrate reports ledger presence
// ("running" when a deploy record exists). Mirrors the local deploy target.Status.
func (t *externalDeployTarget) Status(ctx context.Context) (StatusInfo, error) {
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		return life.Status(ctx, t.name, t.node)
	}
	paths, err := t.ledgerPaths()
	if err != nil {
		return StatusInfo{}, err
	}
	rec, err := kit.ReadDeployRecord(paths, t.deployID())
	if err != nil || rec == nil {
		return StatusInfo{State: "stopped", Healthy: false}, nil
	}
	return StatusInfo{
		State:   "running",
		Healthy: true,
		Details: map[string]string{"target": rec.Target, "candies": fmt.Sprintf("%d", len(rec.Candy))},
	}, nil
}

// Start, Stop, Logs, Shell delegate to the substrate's lifecycle hook when one is registered
// (vm: `charly vm start/stop/console/ssh`). A hookless substrate (local/android/k8s) has no
// charly-owned runtime / journal, so they error like the host target.
func (t *externalDeployTarget) Start(ctx context.Context) error {
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		return life.Start(ctx, t.name, t.node)
	}
	return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
}
func (t *externalDeployTarget) Stop(ctx context.Context) error {
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		return life.Stop(ctx, t.name, t.node)
	}
	return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
}
func (t *externalDeployTarget) Logs(ctx context.Context, opts LogsOpts) error {
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		return life.Logs(ctx, t.name, t.node, opts)
	}
	return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
}
func (t *externalDeployTarget) Shell(ctx context.Context, cmd []string) error {
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		return life.Shell(ctx, t.name, t.node, cmd)
	}
	return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
}

// Attach is the F12 interactive/live-stdio leg (`charly shell` / `charly cmd`). A substrate with a
// lifecycle hook (pod/vm) runs the resolved venue command over its live venue executor; a hookless
// substrate (local/android/k8s) has no interactive venue lifecycle and errors like the host target.
func (t *externalDeployTarget) Attach(ctx context.Context, cmd []string, tty bool) error {
	if life, ok := substrateLifecycleFor(t.prov.word); ok {
		return life.Attach(ctx, t.name, t.node, cmd, tty)
	}
	return fmt.Errorf("external deploy %q: %w", t.name, ErrNotSupportedOnExternal)
}
