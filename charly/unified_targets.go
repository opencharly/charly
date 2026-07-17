package main

// unified_targets.go — The unified deploy-target abstraction.
//
// UnifiedDeployTarget/LifecycleTarget via externalDeployTarget (the out-of-process
// substrate adapter — ALL FIVE substrates local/vm/pod/k8s/android externalized), and the
// ResolveTarget dispatcher. There are no in-proc UnifiedDeployTarget adapters left; the
// pod-overlay render + the VM disk build that once lived in core are now invoked host-side
// from each substrate's lifecycle hook — the pod-overlay render moved to candy/plugin-deploy-pod
// (P11c), reached via HostBuild("overlay") prep + the "step-emit"/"oci-emit-step" per-step dispatch.
//
// Each adapter wraps an existing legacy target via struct embedding.
// Methods on the adapter take precedence over inherited legacy methods
// (Go's outer-struct shadowing), so Name()/Kind()/Executor()/Add() are
// defined once here without touching the legacy files.
//
// Add()/Del()/Test()/Update() and the lifecycle methods (Start/Stop/
// Status/Logs/Shell/Rebuild) are the canonical implementations: Add()
// CONSTRUCTS its live embedded target from the DeployContext and runs
// the kind-specific deploy; Del() walks the ledger per kind. ResolveTarget(node, name) returns the right
// adapter; the cmd files (deploy_add_cmd.go) carry no per-kind dispatch
// switch — they build the DeployContext and call target.Add / target.Del.

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// runUnifiedTargetChecks runs a deploy-scope check list via a live-mode Runner
// over exec, filtering to opts.OnlyIDs when set and reporting per-check failures
// to stderr. kind ("pod"/"vm"/"host", from the adapter's Kind()) labels both the
// no-executor and the summary errors; nodeName is the deploy identifier. Shared
// by Pod/Vm/the local deploy target.Test — the three were byte-identical bar the
// kind/name labels (R3).
func runUnifiedTargetChecks(ctx context.Context, exec spec.DeployExecutor, kind, nodeName string, checks []spec.Op, opts TestOpts) error {
	onlyIDs := make(map[string]bool, len(opts.OnlyIDs))
	for _, id := range opts.OnlyIDs {
		onlyIDs[id] = true
	}
	filtered := checks
	if len(onlyIDs) > 0 {
		filtered = filtered[:0]
		for _, c := range checks {
			if onlyIDs[c.ID] {
				filtered = append(filtered, c)
			}
		}
	}
	if exec == nil {
		return fmt.Errorf("%s %q: no executor configured", kind, nodeName)
	}
	runner := newCheckRunner(kit.RunnerConfig{Exec: exec, Mode: RunModeLive})
	results := runner.Run(ctx, filtered)
	failed := 0
	for _, r := range results {
		if r.Status == TestFail {
			failed++
			id := ""
			if r.Op != nil {
				id = r.Op.ID
			}
			fmt.Fprintf(os.Stderr, "FAIL %s: %s\n", id, r.Message)
			if opts.StopOnFail {
				return fmt.Errorf("test stopped at first failure: %s", id)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d %s check(s) failed", failed, kind)
	}
	return nil
}

// ---------------------------------------------------------------------------
// the local deploy target — adapter over the local deploy target.
//
// Stubbed Add/Name/Kind/Executor live here. The lifecycle and management
// methods (Del, Test, Update, Start, Stop, Status, Logs, Shell, Rebuild)
// live in unified_targets_host.go (C11 / Phase 3 implementation).
// ---------------------------------------------------------------------------

// target:local has NO in-proc UnifiedDeployTarget — it externalized into the
// candy/plugin-deploy-local out-of-process plugin (an externalizedDeploySubstrate, like
// android/k8s). ResolveTarget routes a `local:` substrate to externalDeployTarget over the
// E3b reverse channel; the plugin's kit.WalkPlans executes the InstallPlan on the venue
// (the plugin-renderable kinds via the F2 reverse legs, the host-engine kinds via
// RunHostStep). The executor is chosen by the node's host: field (ShellExecutor for
// host:local, SSHExecutor for host:user@machine) — see ResolveTarget.

// ---------------------------------------------------------------------------
// target:vm has NO in-proc UnifiedDeployTarget — it externalized into the
// candy/plugin-deploy-vm out-of-process plugin (an externalizedDeploySubstrate, like
// local/android/k8s). ResolveTarget routes a `vm:` substrate to externalDeployTarget over
// the E3b reverse channel; the plugin's kit.WalkPlans executes the InstallPlan inside the
// GUEST (the plugin-renderable kinds via the F2 reverse legs, the host-engine kinds via
// RunHostStep). Unlike the other externalized substrates, vm owns a real venue lifecycle, so
// it registers a substrateLifecycle (vm_deploy_lifecycle.go) — the host-side hook that boots
// the domain, builds the guest SSHExecutor the reverse channel serves, deploys nested pods,
// and owns Start/Stop/Status/Logs/Shell/Rebuild + the teardown bookkeeping.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// target:pod has NO in-proc UnifiedDeployTarget — it externalized into the
// candy/plugin-deploy-pod out-of-process plugin (an externalizedDeploySubstrate, like
// local/vm/android/k8s). ResolveTarget routes a `pod:` substrate to externalDeployTarget
// over the E3b reverse channel. Unlike vm, pod's plugin WALKS NOTHING: pod bakes its install
// steps INTO the image at build time, so its substrateLifecycle (the external
// candy/plugin-deploy-pod, M4) builds the overlay container image HOST-SIDE in PrepareVenue
// via HostBuild("overlay") → the core prep+resolve seam (build_overlay.go) + the candy's own
// render (deploykit.OCITarget walker + the "step-emit"/"oci-emit-step" per-step dispatch —
// P11c dissolved the former in-core overlay render into the candy), like vm builds its disk
// host-side) and owns the container lifecycle (config/start/remove + the `charly update`
// rebuild gate). The former in-core pod overlay target struct is GONE (moved to the candy);
// only the adapter + the in-proc dedicated deploy provider were deleted.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// android and k8s are EXTERNAL deploy substrates (F1) — `target: android` /
// `target: k8s` resolve to externalDeployTarget over the E3b reverse channel,
// served out-of-process by candy/plugin-adb (deploy:android) and candy/plugin-kube
// (deploy:k8s). There is no in-proc android/k8s UnifiedDeployTarget; the
// substrate-specific inputs the host must resolve (the android device endpoint +
// apk specs; the k8s image Capabilities + cluster template, used to GENERATE the
// egress-validated Kustomize tree) are produced host-side by each substrate's
// registered deploy preresolver (android_deploy_preresolve.go /
// k8s_deploy_preresolve.go) and shipped in DeployVenue.Substrate. The plugin then
// drives the live external system (the device / the cluster via kubectl).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// ResolveTarget — the unified dispatcher.
//
// Looks up a charly.yml node by name, validates that `target:` is set,
// and returns the appropriate UnifiedDeployTarget adapter. This is the
// canonical entry point for every deploy verb (`charly bundle add` / `del`
// and `charly update`). The returned adapter carries identity only; its Add
// method CONSTRUCTS the live embedded target from the DeployContext.
// ---------------------------------------------------------------------------

// ResolveTarget returns the UnifiedDeployTarget for `name`, dispatching
// on the node's canonical target. The node MUST be the dispatch-merged
// BundleNode (project+operator field merge from resolveTreeRoot) —
// the adapter consumes node fields (Nested/Env/ephemeral/disposable)
// directly and NEVER re-reads them from disk.
//
// Errors:
//   - "no deployment X" — node absent / nil
//   - "X: missing required `target:`" — schema violation
//   - "X: unknown target Y" — Y is not a canonical substrate word (a typo)
//   - "X: target Y is a known substrate but its deploy provider is not connected" — Y is valid but
//     its out-of-process plugin is not compiled-in / failed to load (unresolvedDeployTargetError)
func ResolveTarget(node *spec.BundleNode, name string) (UnifiedDeployTarget, error) {
	if node == nil {
		return nil, fmt.Errorf("no deployment %q; run `charly bundle list`", name)
	}

	// Every deployment MUST carry target:. A missing target: is a hard error at load.
	if node.Target == "" {
		return nil, fmt.Errorf("deployment %q missing required `target:` field "+
			"(local|vm|pod|k8s|android)", name)
	}

	// Target dispatch is the provider registry (the switch is gone — C3).
	prov, ok := providerRegistry.ResolveDeploy(node.Target)
	if !ok {
		return nil, unresolvedDeployTargetError(name, node.Target)
	}
	if dp, ok := prov.(DeployTargetProvider); ok {
		return dp.ResolveTarget(node, name)
	}
	// An OUT-OF-PROCESS deploy provider (a grpcProvider, Invoke-only) drives the deploy
	// lifecycle via the E3b reverse channel — Add Invokes it with the host executor
	// served on the go-plugin broker (E3-deploy). Built-in targets take the typed path.
	//
	// The executor is chosen by the node's host: field via the SHARED selector
	// rootExecutorForDeployNode (R3 — the SAME logic the `charly check live` local path
	// uses): ShellExecutor for host:local/absent (this machine), SSHExecutor for
	// host:user@machine. This is what makes the externalized `local` substrate honor
	// `host: user@machine` (an SSH local deploy) without the generic target branching on
	// the substrate. android/k8s carry no host: field, so they resolve to ShellExecutor
	// (the host venue) — unchanged from the prior hardcoded ShellExecutor{}.
	if gp, ok := prov.(*grpcProvider); ok {
		exec, perr := deploykit.RootExecutorForDeployNode(node)
		if perr != nil {
			return nil, fmt.Errorf("deployment %q: %w", name, perr)
		}
		// node is stored so a substrate with a lifecycle hook (vm) can resolve its kind:vm
		// entity for the host-side lifecycle (PrepareVenue / Rebuild / teardown). For vm the
		// rootExecutorForDeployNode placeholder (ShellExecutor, no host: field) is REPLACED by
		// the guest SSHExecutor the lifecycle hook's PrepareVenue returns in apply().
		return &externalDeployTarget{name: name, prov: gp, exec: exec, node: node}, nil
	}
	return nil, fmt.Errorf("deployment %q: target %q has no in-process resolver and is not an out-of-proc plugin provider", name, node.Target)
}

// unresolvedDeployTargetError distinguishes the two ways ResolveDeploy(target) can fail: a
// genuinely UNKNOWN target word (a typo — not one of the canonical substrates), versus a KNOWN
// substrate word whose deploy provider is merely NOT CONNECTED (the deploy-substrate plugins are
// out-of-process and connected on demand via loadDeployPlugins — a target with no charly.yml entry,
// or a plugin that failed to load, leaves the registry entry absent). The former is a user error;
// the latter is a load/connect gap. Conflating them (the former "unknown target" text for both)
// misdirected the operator during the check-k3s-vm RCA.
func unresolvedDeployTargetError(name, target string) error {
	if externalizedDeploySubstrates[target] {
		return fmt.Errorf("deployment %q: target %q is a known substrate but its deploy provider "+
			"is not connected (the %s plugin candy is not compiled-in or failed to load)",
			name, target, externalDeploySubstratePlugins[target])
	}
	return fmt.Errorf("deployment %q: unknown target %q (want local|vm|pod|k8s|android)", name, target)
}

// compile-time assertion: the external-deploy adapter satisfies the interfaces it
// claims. If any method signature drifts, `go build` fails here. ALL FIVE substrates
// (local/vm/pod/k8s/android) have no in-proc UnifiedDeployTarget — they are external
// substrates (externalizedDeploySubstrates), resolved to externalDeployTarget.
var (
	_ UnifiedDeployTarget = (*externalDeployTarget)(nil)

	// externalDeployTarget is a LifecycleTarget so `charly update <name>` (the disposable
	// bed's fresh-rebuild R10 gate) can Rebuild it. For a substrate with a lifecycle hook
	// (vm: boot/destroy + domain re-create; pod: overlay rebuild + config/start) Rebuild +
	// Start/Stop/Status/Logs/Shell delegate to the hook; for a hookless substrate
	// (local/android/k8s) Rebuild re-applies and Start/Stop/Logs/Shell error like the host target.
	_ LifecycleTarget = (*externalDeployTarget)(nil)
)
