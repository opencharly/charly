package main

import (
	"context"
	"fmt"
	"sync"
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
// This is the lifecycle counterpart of the deployPreresolver seam (deploy_preresolve.go):
// a preresolver ships host-resolved DATA to the plugin (android device endpoint, k8s
// kustomize tree); a substrateLifecycle owns host-side VENUE lifecycle the plugin cannot.
// A substrate has at most ONE of each; vm and pod register a lifecycle hook today (vm owns
// the domain boot/destroy + guest SSH executor; pod owns the host-side overlay image build +
// the container config/start/remove lifecycle). local/android/k8s register none.
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
	PrepareVenue(ctx context.Context, name, dir string, node *BundleNode, plans []*InstallPlan, opts EmitOpts) (DeployExecutor, error)

	// ArtifactKey returns the name candy artifacts (+ the k3s ClusterProfile) are keyed
	// under for this deploy — for vm "vm:<entity>", NOT the deploy name, because one k3s
	// cluster per VM is reached by several beds and its profile must land under the shared
	// "vm-<entity>" name the `cluster:` refs use. Empty → the caller keys by the deploy name.
	ArtifactKey(name string, node *BundleNode) string

	// PostApply runs host orchestration AFTER the plan walk (vm: nested target:pod children
	// as persistent in-guest quadlets via deployNestedPodsInGuest). Add only — Update is a
	// walk-only idempotent re-apply (matching the prior in-proc VmUnifiedTarget.Update).
	PostApply(ctx context.Context, name, dir string, node *BundleNode, exec DeployExecutor, opts EmitOpts) error

	// TeardownExecutor returns the DeployExecutor a `charly bundle del` replays the recorded
	// ReverseOps over — for vm the guest *SSHExecutor against the managed alias (NO boot;
	// the guest is expected up, and a down guest makes teardown a guest-side no-op). nil →
	// the caller keeps the ResolveTarget-selected executor (local host:local/remote).
	TeardownExecutor(name string, node *BundleNode) (DeployExecutor, error)

	// PostTeardown runs host cleanup AFTER teardown (vm: RemoveVmSshStanza +
	// removeVmDeployEntry + ephemeral lifecycle teardown; pod: `charly remove` + drop the
	// <name>-overlay images + ephemeral teardown). keepImage is the `charly bundle del
	// --keep-image` gate — honored by pod (suppress the overlay-image drop), ignored by vm.
	// Best-effort by convention.
	PostTeardown(name string, node *BundleNode, keepImage bool) error

	// The LifecycleTarget bodies (charly start/stop/status/logs/shell + the `charly update`
	// Rebuild). For vm these shell out to the existing `charly vm` command family; Rebuild
	// does destroy+build+create+start+`charly bundle add` (the R10 fresh-rebuild gate).
	Start(ctx context.Context, name string, node *BundleNode) error
	Stop(ctx context.Context, name string, node *BundleNode) error
	Status(ctx context.Context, name string, node *BundleNode) (StatusInfo, error)
	Logs(ctx context.Context, name string, node *BundleNode, opts LogsOpts) error
	Shell(ctx context.Context, name string, node *BundleNode, cmd []string) error
	Rebuild(ctx context.Context, name string, node *BundleNode, opts RebuildOpts) error
}

// substrateLifecycles maps an external deploy SUBSTRATE word → its host-side lifecycle
// hook. Populated at package-var init time (before any init(), like registerDeployPreresolver
// + registerDedicatedBuiltin), so the lookup is race-free.
var (
	substrateLifecyclesMu sync.RWMutex
	substrateLifecycles   = map[string]substrateLifecycle{}
)

// registerSubstrateLifecycle records one COMPILED-IN substrate's lifecycle hook (pod/vm). Panics
// on a duplicate word (a startup invariant, like the preresolver + registry duplicate panics).
func registerSubstrateLifecycle(word string, l substrateLifecycle) {
	if word == "" || l == nil {
		panic("registerSubstrateLifecycle: empty word or nil lifecycle")
	}
	substrateLifecyclesMu.Lock()
	defer substrateLifecyclesMu.Unlock()
	if _, dup := substrateLifecycles[word]; dup {
		panic(fmt.Sprintf("registerSubstrateLifecycle: duplicate lifecycle for %q", word))
	}
	substrateLifecycles[word] = l
}

// registerPluginSubstrateLifecycle records a WIRE-BACKED lifecycle for an external deploy substrate
// at plugin-load (F6), idempotently: a plugin reconnect REPLACES the prior wire-backed hook (the new
// grpcProvider carries the live conn), but it never SHADOWS a compiled-in lifecycle (pod/vm) — those
// are DELETED, not shadowed, when M4 moves them to plugins. Unlike registerSubstrateLifecycle (the
// package-init, panic-on-dup path for compiled-in singletons), this runs at runtime.
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
