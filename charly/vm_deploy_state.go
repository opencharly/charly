package main

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"

	"github.com/opencharly/sdk/deploykit"
)

// vm_deploy_state.go — the charly.yml persistence half of the former bundle_add_cmd_vm.go
// (P13-KERNEL): a plugin cannot touch the project's charly.yml directly, so these stay behind the
// config-persist HostBuild seam. RCA #6 (FINAL/K5 unit 6a) eliminated the second writer this
// comment used to describe (a PrepareVenue-reply persist through substrate_lifecycle_grpc.go) —
// candy/plugin-vm's own hostConfigPersist is the SOLE vm_state writer now, one writer, one key.
//
// FLOOR-SLIM Unit 3: every PURE helper this file used to define directly moved to sdk —
// vmshared.VmNameFromDeployName / vmshared.SplitVmAddress (pure string parsing; vmshared, since
// deploykit already imports vmshared and the reverse direction would cycle) and
// deploykit.ResolveVmSshPort / deploykit.PruneStaleVmDottedTwin / deploykit.VmDeployEntryKeys
// (operate on deploykit's own *BundleConfig, so they live there instead). Every local call site
// below now calls the sdk form directly. Only the pluginPrimaries-registry-coupled write path
// (saveVmDeployState/removeVmDeployEntry, both behind acquireDeployConfigLock) stays here.

// saveVmDeployState writes the updated VmDeployState into
// ~/.config/charly/charly.yml for the given deploy name. Idempotent —
// overwrites the deploy.<name>.vm_state block. vmEntity is the kind:vm entity
// the deploy targets ("" → derive from a legacy "vm:<name>" deployName prefix);
// it is persisted as the entry's `vm:` cross-ref so a bundle-keyed entry (a
// kind:check VM bed, whose deploy key e.g. `check-k3s-vm` differs from
// `vm:<entity>`) carries the linkage teardown needs to find + remove it.
func saveVmDeployState(deployName, vmEntity string, state *spec.VmDeployState) error {
	// Serialize the load→modify→save against concurrent charly processes — the
	// SAME blocking deploy-config lock saveDeployState / cleanDeployEntry use
	// (filelock.go). Without it two parallel `charly vm create` persist-auto-port
	// writers (or a vm-create racing a `charly bundle add vm:<name>`) load → modify
	// → save the shared ~/.config/charly/charly.yml and silently drop each other's
	// entry. Reentrancy audit: the only deploy-config-lock holders are
	// saveDeployState + cleanDeployEntry, neither of which calls saveVmDeployState;
	// its callers (vm_create_spec.go persist-auto-port, the vm lifecycle hook's PrepareVenue,
	// removeVmDeployEntry's siblings) hold NO deploy-config lock — so acquiring
	// here is exactly one level, no self-deadlock (flock is per-open-fd, blocking).
	unlock, lockErr := acquireDeployConfigLock()
	if lockErr != nil {
		return fmt.Errorf("locking charly.yml for vm-state write: %w", lockErr)
	}
	defer func() { _ = unlock() }()

	// Load existing charly.yml (or start fresh).
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		return fmt.Errorf("loading charly.yml: %w", err)
	}
	if dc == nil {
		dc = &deploykit.BundleConfig{}
	}
	if dc.Bundle == nil {
		dc.Bundle = map[string]spec.BundleNode{}
	}

	entry, exists := dc.Bundle[deployName]
	if !exists {
		entry = spec.BundleNode{}
	}
	entry.Target = "vm"
	// Persist the `vm:` cross-ref so the per-host entry is a well-formed bundle
	// node AND so teardown can resolve a bundle-keyed entry back to its VM entity.
	// Precedence: the explicit vmEntity (the canonical mapping the caller resolved,
	// e.g. check-k3s-vm → k3s-vm) → a legacy "vm:<entity>" deployName prefix →
	// PRESERVE the existing entry.From (never clobber a known cross-ref with "").
	switch {
	case vmEntity != "":
		entry.From = vmEntity
	default:
		if vmName, perr := vmshared.VmNameFromDeployName(deployName); perr == nil {
			entry.From = vmName
		}
	}
	// Ephemeral-registration ordering contract (RCA #7, FINAL/K5 unit 6a, live-probe-caught):
	// registerEphemeralIfMarked (vm_lifecycle_preresolve.go) persists .VmState.Ephemeral under
	// THIS SAME canonical key BEFORE `charly vm create`'s own state writes run (e.g. the
	// port_auto persist) — RCA #6's key unification made this THE common case (the two writers
	// never collided before, since Writer B's now-eliminated dual key hid the interaction). A
	// caller's `state` here is NEVER told about ephemeral registration (that is a SEPARATE
	// concern candy/plugin-bundle's ephemeral family owns), so a wholesale `entry.VmState =
	// state` would silently ERASE a just-registered ephemeral block whenever the incoming state
	// carries none. PRESERVE it explicitly — this is NOT a general deep-merge; every OTHER
	// VmState field is still a full overwrite, exactly as before.
	var priorEphemeral *spec.EphemeralRuntime
	if entry.VmState != nil {
		priorEphemeral = entry.VmState.Ephemeral
	}
	entry.VmState = state
	if entry.VmState != nil && entry.VmState.Ephemeral == nil && priorEphemeral != nil {
		entry.VmState.Ephemeral = priorEphemeral
	}
	dc.Bundle[deployName] = entry

	// Self-healing prune (RCA #6, FINAL/K5 unit 6a): remove a stale dotted-key twin ELIMINATED
	// Writer B (candy/plugin-deploy-vm's PrepareVenue, see substrate_lifecycle_grpc.go) left behind
	// for THIS SAME domain in an existing overlay — nothing writes one anymore, but pre-fix
	// overlays (real users', every bed record until now) still carry it, and it poisons every
	// subsequent load (spec.ValidateDeploymentName's dot-rejection). One-touch
	// cleanup on the next write for this domain — no new migration machinery.
	if pruned := deploykit.PruneStaleVmDottedTwin(dc, deployName); pruned != "" {
		fmt.Fprintf(os.Stderr, "note: pruned a stale per-host overlay entry %q for domain %q — left by a prior version's now-eliminated dotted-key vm-state write (canonical entry: %q)\n", pruned, vmshared.VmDomainIdentity(deployName), deployName)
	}

	return saveBundleConfigNodeForm(dc)
}

// removeVmDeployEntry strips deploy.<deployName> from charly.yml.
func removeVmDeployEntry(deployName string) error {
	// Same shared-file serialization as saveVmDeployState / cleanDeployEntry: a VM
	// destroy mutating the overlay must not race a concurrent writer's
	// load→modify→save (the lost-update class). Reentrancy: its caller (the vm
	// destroy handler in candy/plugin-vm, reached via the config-persist seam)
	// holds no deploy-config lock — single-level acquire, no self-deadlock.
	unlock, lockErr := acquireDeployConfigLock()
	if lockErr != nil {
		return fmt.Errorf("locking charly.yml for vm-entry removal: %w", lockErr)
	}
	defer func() { _ = unlock() }()

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil || dc.Bundle == nil {
		return nil
	}
	keys := deploykit.VmDeployEntryKeys(dc, deployName)
	if len(keys) == 0 {
		return nil
	}
	// Destroying the VM invalidates only the RUNTIME state (vm_state). Clear
	// that, but PRESERVE every operator-authored per-host field (preemptible,
	// env, tunnel, port, security, add_candy, install_opts, …) so a
	// destroy→create cycle — which is exactly what `charly update <vm>` does
	// (the vm lifecycle hook's Rebuild shells `charly vm destroy` then `charly vm create`) —
	// never silently drops local config. (This is the root cause of the lost
	// `preemptible: {holds: [nvidia-gpu]}` on the operator workstation.)
	//
	// If, after clearing vm_state, the entry carries NOTHING operator-authored
	// beyond the fields saveVmDeployState auto-sets (target: vm + vm:), it was a
	// pure auto-created VM-state record — e.g. a disposable check-bed VM — so
	// delete it entirely (such entries must not accumulate; that's why
	// destroy cleaned up the entry in the first place). Otherwise keep the
	// now-stateless entry so its operator config survives.
	for _, key := range keys {
		entry := dc.Bundle[key]
		entry.VmState = nil
		if deploykit.IsAutoVmDeployEntry(entry) {
			delete(dc.Bundle, key)
		} else {
			dc.Bundle[key] = entry
		}
	}
	return saveBundleConfigNodeForm(dc)
}

// Note: the per-host bundle-key resolution logic (a `vm:`-form From-scan bridging a bundle-keyed
// bed entry to the DIRECT `charly vm destroy <entity>` path) moved to
// deploykit.VmDeployEntryKeys (FLOOR-SLIM Unit 3) — see that function's doc comment for the
// WRITE/REMOVE history this comment used to carry.
