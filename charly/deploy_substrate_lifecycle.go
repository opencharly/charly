package main

import (
	"context"
	"sync"

	"github.com/opencharly/sdk/spec"
)

// deploy_substrate_lifecycle.go — the per-substrate HOST-SIDE lifecycle hook for an
// EXTERNAL deploy substrate that owns a real venue lifecycle (Design A).
//
// local/android/k8s externalized cleanly because their venue has NO charly-owned
// lifecycle: externalDeployTarget errors on Start/Stop/Logs/Shell (like the host target),
// and Rebuild re-runs `charly bundle add`. A VM is different — charly boots / destroys /
// consoles / SSHes the domain, and `charly update <vm-bed>` MUST destroy+build+create+
// start+re-add the domain (the R10 fresh-rebuild gate). The generalization is this hook:
// the deploy WALK stays the external plugin's kit.WalkPlans over the executor reverse
// channel, while the host-only lifecycle (boot the VM + construct the guest SSH executor
// the reverse channel serves; reboot/destroy the domain; the ssh-config + charly.yml-entry
// + ephemeral bookkeeping) lives behind a registered hook. vm registers one; the others do
// not. The generic externalDeployTarget consults it — never branching on the substrate
// word, only on whether a hook is registered.
//
// pod AND vm are now BOTH WIRE-BACKED (M4) and IMPLEMENT their lifecycle Ops in the plugin over
// GENERIC seams: candy/plugin-deploy-pod builds the overlay via HostBuild("overlay") + drives the
// container lifecycle via HostBuild("cli"); candy/plugin-deploy-vm publishes the ssh-config stanza +
// guest waits + charly delivery via sdk/kit, boots/consoles the domain via HostBuild("cli"), and
// deploys nested pods over the reverse channel — self-serving any LoadUnified-coupled data its OWN
// OpPrepareVenue needs via the generic "deploy-entity-resolve" HostBuild seam (FINAL/K5 unit 6a,
// M4b — the former lifecyclePrepareHook host-side DATA-seam indirection, which used to
// pre-resolve spec.LifecyclePrepareInput and thread it via a "prepare" params key, is DELETED; the
// plugin resolves it itself now, exactly like the deployPreresolvers below already do for
// k8s/android). NO substrate registers a compiled-in lifecycle anymore, and NO vm lifecycle logic
// remains in core; local/android/k8s own no venue lifecycle at all. A residual host cleanup a
// plugin cannot do (vm's ephemeral-lifecycle teardown) stays behind a registered
// lifecyclePostTeardownHook the proxy consults GENERICALLY.
type substrateLifecycle interface {
	// PrepareVenue runs the host-side preflight for an Add/Update and returns the
	// DeployExecutor the reverse channel serves — for vm the guest *SSHExecutor (after
	// resolving the kind:vm entity, publishing the managed ssh-config stanza, auto-booting
	// the domain, waiting for sshd + cloud-init + the package lock, and ensuring the charly
	// binary is in the guest); for pod a host-local ShellExecutor AFTER building the overlay
	// container image host-side (the plugin then walks nothing). node may be nil on the
	// Update path (re-resolved from the tree by name, like the preresolvers). plans is the
	// deployment's compiled InstallPlan set: vm IGNORES it (the plugin walks the plans
	// in-guest over the returned executor), while pod CONSUMES it to build the overlay
	// (its add_candy overlay plans; empty for a pod with no add_candy). It persists any
	// substrate runtime state (vm: VmDeployState). Skipped on a dry-run by the caller.
	PrepareVenue(ctx context.Context, name, dir string, node *spec.BundleNode, plans []*spec.InstallPlan, opts spec.EmitOpts) (spec.DeployExecutor, error)

	// ArtifactKey returns the name candy artifacts (+ the k3s ClusterProfile) are keyed
	// under for this deploy — for vm "vm:<entity>", NOT the deploy name, because one k3s
	// cluster per VM is reached by several beds and its profile must land under the shared
	// "vm-<entity>" name the `cluster:` refs use. Empty → the caller keys by the deploy name.
	ArtifactKey(name string, node *spec.BundleNode) string

	// PostApply runs host orchestration AFTER the plan walk (vm: nested target:pod children
	// as persistent in-guest quadlets via plugin-deploy-vm's PostApply). Add only — Update is a
	// walk-only idempotent re-apply (matching the prior in-proc VmUnifiedTarget.Update).
	PostApply(ctx context.Context, name, dir string, node *spec.BundleNode, exec spec.DeployExecutor, opts spec.EmitOpts) error

	// VenueExecutor returns the live DeployExecutor reaching this running deploy's venue — for vm the
	// guest *SSHExecutor against the managed alias (NO boot; the guest is expected up, and a down guest
	// makes the op a guest-side no-op), nil for pod (the caller keeps its ResolveTarget-selected host
	// executor for local host:local/remote). TWO consumers: `charly bundle del` replays the recorded
	// ReverseOps over it, AND the F12 Attach leg runs the interactive/live-stdio session over it (vm →
	// `ssh -t <alias>`). It invokes the wire Op sdk.OpTeardownExecutor — kept under its original name
	// for wire stability (teardown was merely its FIRST consumer; the method name reflects the contract).
	VenueExecutor(name string, node *spec.BundleNode) (spec.DeployExecutor, error)

	// PostTeardown runs host cleanup AFTER teardown (vm: RemoveVmSshStanza +
	// removeVmDeployEntry + ephemeral lifecycle teardown; pod: `charly remove` + drop the
	// <name>-overlay images + ephemeral teardown). keepImage is the `charly bundle del
	// --keep-image` gate — honored by pod (suppress the overlay-image drop), ignored by vm.
	// Best-effort by convention.
	PostTeardown(name string, node *spec.BundleNode, keepImage bool) error

	// The LifecycleTarget bodies (charly start/stop/status/logs/shell + the `charly update`
	// Rebuild). For vm these shell out to the existing `charly vm` command family; Rebuild
	// does destroy+build+create+start+`charly bundle add` (the R10 fresh-rebuild gate).
	Start(ctx context.Context, name string, node *spec.BundleNode) error
	Stop(ctx context.Context, name string, node *spec.BundleNode) error
	Status(ctx context.Context, name string, node *spec.BundleNode) (StatusInfo, error)
	Logs(ctx context.Context, name string, node *spec.BundleNode, opts LogsOpts) error
	Shell(ctx context.Context, name string, node *spec.BundleNode, cmd []string) error
	// Attach runs the F12 interactive/live-stdio session (`charly shell` / `charly cmd`) over the
	// substrate's live venue executor (pod → host ShellExecutor + `podman exec/run -it`; vm → guest
	// SSHExecutor + `ssh -t`). Distinct from Shell (the #57 `charly service` capture leg). NO arbiter
	// bracket (an interactive session claims no exclusive resource).
	Attach(ctx context.Context, name string, node *spec.BundleNode, cmd []string, tty bool) error
	Rebuild(ctx context.Context, name string, node *spec.BundleNode, opts RebuildOpts) error
}

// substrateLifecycles maps an external deploy SUBSTRATE word → its host-side lifecycle
// hook. Populated at package-var init time (before any init(), like
// registerDedicatedBuiltin), so the lookup is race-free.
var (
	substrateLifecyclesMu sync.RWMutex
	substrateLifecycles   = map[string]substrateLifecycle{}
)

// registerPluginSubstrateLifecycle records a WIRE-BACKED lifecycle for an external deploy substrate
// at plugin-load (F6), idempotently: a plugin reconnect REPLACES the prior wire-backed hook (the new
// grpcProvider carries the live conn). It never SHADOWS a compiled-in lifecycle, but after M4 there
// are NONE — pod + vm both externalized (candy/plugin-deploy-{pod,vm}), their compiled-in
// registrations DELETED — so both plugins' wire-backed hooks register cleanly (nothing to shadow).
// The shadow guard remains for a future compiled-in singleton (a package-init registration
// path would panic on duplicates; this wire-backed one replaces idempotently at runtime).
func registerPluginSubstrateLifecycle(word string, l substrateLifecycle) {
	if word == "" || l == nil {
		return
	}
	substrateLifecyclesMu.Lock()
	defer substrateLifecyclesMu.Unlock()
	if existing, ok := substrateLifecycles[word]; ok {
		if _, isWire := existing.(grpcSubstrateLifecycle); !isWire {
			return // a compiled-in lifecycle owns this word — never shadow it
		}
	}
	substrateLifecycles[word] = l
}

// substrateLifecycleFor returns the registered lifecycle hook for an external substrate
// word, if any. externalDeployTarget consults it; a substrate with no hook (local, android,
// k8s) keeps the generic host-venue behaviour.
func substrateLifecycleFor(word string) (substrateLifecycle, bool) {
	substrateLifecyclesMu.RLock()
	defer substrateLifecyclesMu.RUnlock()
	l, ok := substrateLifecycles[word]
	return l, ok
}
