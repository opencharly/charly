package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"

	"github.com/opencharly/sdk/deploykit"
)

// vm_deploy_state.go — the charly.yml persistence half of the former bundle_add_cmd_vm.go
// (P13-KERNEL): a plugin cannot touch the project's charly.yml directly (deploy_add_shared.go /
// vm_lifecycle_preresolve.go's PrepareVenue seam persists the plugin's returned state HERE), so
// these stay behind the config-persist HostBuild seam. The pure VM-spec helpers (SSH user/port
// resolution) + the SSH ReverseRunner adapter moved to sdk/vmshared + sdk/kit respectively — see
// vmshared.ResolveCloudInitSSHUser + kit.SSHReverseRunner + kit.ResolveVmSshPort.

// vmNameFromDeployName extracts the VM entity name from a deploy-key
// in the legacy "vm:<name>[/<instance>]" form. Callers that hold a
// schema-v4 deploy key (whose entity comes from the node's `vm:` field)
// resolve the entity via vmEntityForAdd instead; this helper handles the
// prefixed form (legacy refs + the "vm:<entity>" key the del path builds
// for ledger/teardown keying). The `instance` suffix is preserved for
// future per-instance addressing but currently unused.
func vmNameFromDeployName(deployName string) (string, error) {
	if !strings.HasPrefix(deployName, "vm:") {
		return "", fmt.Errorf("VM deploy name must start with 'vm:' (got %q)", deployName)
	}
	rest := strings.TrimPrefix(deployName, "vm:")
	if rest == "" {
		return "", fmt.Errorf("VM deploy name missing vm-name portion (got %q)", deployName)
	}
	if before, _, ok := strings.Cut(rest, "/"); ok {
		return before, nil
	}
	return rest, nil
}

// splitVmAddress detects the "vm:"-prefixed CLI ADDRESSING form (`charly bundle add/del
// vm:<name>` / `vm:<parent.child>`) and returns the address with that prefix stripped, plus
// whether it was present. "vm:" here is an ADDRESSING HINT — "resolve this via the vm
// substrate" — NEVER an identity itself; a caller that needs the plain (tree-lookup /
// ledger-identity) form strips it via this helper, one which needs the sanitized dc.Bundle
// key form still applies "vm:"+vmshared.VmDomainIdentity(...) separately (a DIFFERENT
// canonical form — see vmLifecyclePostTeardown).
//
// The single source for this ONE convention (RCA #9, FINAL/K5 unit 6a, live-probe-caught):
// resolveDeployNodeByPath (RCA #8), resolveDelNode, and hostBuildDeployNodeDelDispatch's name
// normalization (RCA #9) each independently re-implemented `strings.HasPrefix(x, "vm:")`
// before this helper existed — the THIRD such reimplementation is what triggered pulling it
// out. NOT the same job as vmNameFromDeployName (which extracts the VM ENTITY and errors when
// the prefix is ABSENT — a different, already-established, unchanged contract) or
// vmshared.VmDomainIdentity (which sanitizes dots/slashes for a domain-identity STRING,
// unconditionally, prefix or not — also unchanged).
func splitVmAddress(name string) (plain string, isVm bool) {
	if strings.HasPrefix(name, "vm:") {
		return strings.TrimPrefix(name, "vm:"), true
	}
	return name, false
}

// resolveVmSshPort picks the host-side SSH port forward, reusing the persisted
// vm_state.ssh_port (idempotent across rebuilds) when ssh.port_auto is set — the
// project-config READ is the one core-coupled bit; the resolution/allocation
// decision itself is the shared kit.ResolveVmSshPort (R3 with candy/plugin-vm's
// own persisted-state read, over its host seam).
func resolveVmSshPort(sp *spec.ResolvedVm, vmName string) (int, error) {
	var persisted int
	if sp.SSH != nil && sp.SSH.PortAuto {
		if entry, ok := deploykit.LoadDeployConfigForRead("charly vm ssh-port").LookupKey("vm:" + vmName); ok && entry.VmState != nil && entry.VmState.SshPort > 0 {
			persisted = entry.VmState.SshPort
		}
	}
	return kit.ResolveVmSshPort(sp, vmName, persisted)
}

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
		if vmName, perr := vmNameFromDeployName(deployName); perr == nil {
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
	// subsequent load (validateDeploymentName's dot-rejection, charly/unified.go). One-touch
	// cleanup on the next write for this domain — no new migration machinery.
	if pruned := pruneStaleVmDottedTwin(dc, deployName); pruned != "" {
		fmt.Fprintf(os.Stderr, "note: pruned a stale per-host overlay entry %q for domain %q — left by a prior version's now-eliminated dotted-key vm-state write (canonical entry: %q)\n", pruned, vmshared.VmDomainIdentity(deployName), deployName)
	}

	return saveBundleConfigNodeForm(dc)
}

// pruneStaleVmDottedTwin removes and returns any OTHER dc.Bundle key that is a dotted deploy name
// whose VmDomainIdentity sanitizes to the SAME domain identity as canonicalKey (also VmDomainIdentity-
// sanitized) — the "stale twin" ELIMINATED Writer B left behind (RCA #6, FINAL/K5 unit 6a): a nested
// (dotted) deploy's per-host state used to ALSO get written under its raw, unsanitized name, racing
// this canonical "vm:"+VmDomainIdentity(name)-keyed write, and poisoning the whole overlay on every
// subsequent load since a dotted key fails charly/unified.go's validateDeploymentName. Pulled out as
// its own pure function purely for testability (saveVmDeployState itself needs the seam-coupled
// deploykit.LoadBundleConfig, not unit-testable standalone). Returns "" when no twin is found.
func pruneStaleVmDottedTwin(dc *deploykit.BundleConfig, canonicalKey string) string {
	domainID := vmshared.VmDomainIdentity(canonicalKey)
	for k := range dc.Bundle {
		if k == canonicalKey || !strings.Contains(k, ".") {
			continue
		}
		if vmshared.VmDomainIdentity(k) == domainID {
			delete(dc.Bundle, k)
			return k
		}
	}
	return ""
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
	keys := vmDeployEntryKeys(dc, deployName)
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

// vmDeployEntryKeys resolves the per-host charly.yml bundle key(s) a VM teardown
// for deployName targets. It handles the case where a bundle's name differs from
// its `vm:<X>` runtime-state key:
//
//   - WRITE (HISTORICAL — ELIMINATED, RCA #6, FINAL/K5 unit 6a): the vm lifecycle hook's
//     PrepareVenue USED TO ALSO persist vm_state via the generic substrate_lifecycle_grpc.go
//     seam, keyed by the BUNDLE name (e.g. the kind:check VM bed bundle `check-k3s-vm`, which
//     cross-refs `vm: k3s-vm`) — a SECOND writer racing `charly vm create`'s canonical
//     `vm:<domain>`-keyed persist, and for a NESTED (dotted) deploy name, poisoning the whole
//     overlay on every subsequent load. candy/plugin-deploy-vm's PrepareVenue no longer ships
//     vm_state at all (one writer, one key — see substrate_lifecycle_grpc.go), and
//     saveVmDeployState self-heals a pre-fix dotted twin on its next write
//     (pruneStaleVmDottedTwin). This function's teardown-side handling of the bundle-name key
//     REMAINS — needed for a LEGACY overlay's leftover entry until it is torn down at least
//     once, and for the DIRECT `charly vm destroy <entity>` path below.
//   - REMOVE (deploy teardown): the vm plugin's OpPostTeardown ships BOTH the deploy-name key
//     and `vm:<domain>` directly (never `vm:<entity>`), so a teardown removes ONLY this deploy's
//     two entries and never a sibling bed sharing the entity. `charly vm destroy` builds
//     "vm:"+domainOr(box,--domain).
//
// The literal-key delete + the `vm:`-form From-scan below remain for the DIRECT
// `charly vm destroy <entity>` path and any legacy `vm:<name>` teardown key: they target the
// literal deployName key AND — when deployName is "vm:<X>" — every bundle whose `vm:` cross-ref
// names <X>. Because domain identities are unique and never equal an entity a sibling shares, the
// From-scan can no longer over-match sibling beds during a deploy teardown.
func vmDeployEntryKeys(dc *deploykit.BundleConfig, deployName string) []string {
	var keys []string
	seen := map[string]bool{}
	add := func(k string) {
		if seen[k] {
			return
		}
		if _, ok := dc.Bundle[k]; ok {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	add(deployName)
	// vmNameFromDeployName succeeds only for the prefixed "vm:<entity>" form; a
	// plain-name deployName therefore takes the literal-key path only (no scan,
	// so a non-prefixed name can never over-match unrelated bundles).
	if entity, perr := vmNameFromDeployName(deployName); perr == nil {
		for key, entry := range dc.Bundle {
			if entry.From == entity {
				add(key)
			}
		}
	}
	return keys
}
