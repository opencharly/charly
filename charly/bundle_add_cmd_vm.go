package main

import (
	"context"
	"fmt"
	"strings"
)

// sshReverseRunner adapts SSHExecutor to the ReverseRunner interface so
// reverse_ops.go handlers can run tear-down commands inside the VM
// without knowing about SSH. externalDeployTarget.Del derives one from the guest
// SSHExecutor the vm lifecycle hook's TeardownExecutor supplies, so a vm `charly bundle del`
// replays the recorded ReverseOps IN THE GUEST (Δ2).
type sshReverseRunner struct {
	exec *SSHExecutor
}

func (r *sshReverseRunner) RunSystem(script string) error {
	return r.exec.RunSystem(context.Background(), script, EmitOpts{})
}

func (r *sshReverseRunner) RunUser(script string) error {
	return r.exec.RunUser(context.Background(), script, EmitOpts{})
}

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

// resolveVmSshUser picks the SSH user for a spec. Precedence mirrors
// vmshared.ResolveCloudInitSSHUser: explicit spec.ssh.user → spec.source.base_user
// (adopt path for cloud images) → source-kind default ("root" for bootc).
// cloud_image sources with no base_user declared have no sensible
// default — callers treat "" as "user must supply --ssh-key none and
// manage identity out-of-band, or declare base_user in the spec".
func resolveVmSshUser(spec *VmSpec) string {
	if spec.SSH != nil && spec.SSH.User != "" {
		return spec.SSH.User
	}
	if spec.Source.BaseUser != "" {
		return spec.Source.BaseUser
	}
	if spec.Source.Kind == "bootc" {
		return "root"
	}
	return ""
}

// resolveVmSshPort picks the host-side SSH port forward.
//
//   - ssh.port_auto: true → reuse the persisted vm_state.ssh_port if one was
//     already allocated (idempotent across rebuilds), else allocate a free host
//     port via the shared pod-path allocator and let the caller persist it.
//   - ssh.port: N        → that fixed port.
//   - neither            → 2222.
func resolveVmSshPort(spec *VmSpec, vmName string) (int, error) {
	if spec.SSH != nil && spec.SSH.PortAuto {
		if entry, ok := loadDeployConfigForRead("charly vm ssh-port").LookupKey("vm:" + vmName); ok && entry.VmState != nil && entry.VmState.SshPort > 0 {
			return entry.VmState.SshPort, nil
		}
		alloc, err := AllocateAutoPorts([]int{22}, nil)
		if err != nil {
			return 0, fmt.Errorf("vm %q: ssh.port_auto allocation failed: %w", vmName, err)
		}
		return alloc[0].Host, nil
	}
	if spec.SSH != nil && spec.SSH.Port > 0 {
		return spec.SSH.Port, nil
	}
	return 2222, nil
}

// saveVmDeployState writes the updated VmDeployState into
// ~/.config/charly/charly.yml for the given deploy name. Idempotent —
// overwrites the deploy.<name>.vm_state block. vmEntity is the kind:vm entity
// the deploy targets ("" → derive from a legacy "vm:<name>" deployName prefix);
// it is persisted as the entry's `vm:` cross-ref so a bundle-keyed entry (a
// kind:check VM bed, whose deploy key e.g. `check-k3s-vm` differs from
// `vm:<entity>`) carries the linkage teardown needs to find + remove it.
func saveVmDeployState(deployName, vmEntity string, state *VmDeployState) error {
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
	dc, err := LoadBundleConfig()
	if err != nil {
		return fmt.Errorf("loading charly.yml: %w", err)
	}
	if dc == nil {
		dc = &BundleConfig{}
	}
	if dc.Bundle == nil {
		dc.Bundle = map[string]BundleNode{}
	}

	entry, exists := dc.Bundle[deployName]
	if !exists {
		entry = BundleNode{}
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
	entry.VmState = state
	dc.Bundle[deployName] = entry

	return SaveBundleConfig(dc)
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

	dc, err := LoadBundleConfig()
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
		if isAutoVmDeployEntry(entry) {
			delete(dc.Bundle, key)
		} else {
			dc.Bundle[key] = entry
		}
	}
	return SaveBundleConfig(dc)
}

// vmDeployEntryKeys resolves the per-host charly.yml bundle key(s) a VM teardown
// for deployName targets. It handles the case where a bundle's name differs from
// its `vm:<X>` runtime-state key:
//
//   - WRITE: the vm lifecycle hook's PrepareVenue persists vm_state via saveVmDeployState(dctx.Name)
//     — keyed by the BUNDLE name (e.g. the kind:check VM bed bundle `check-k3s-vm`,
//     which cross-refs `vm: k3s-vm`). (charly vm create's port_auto persist keys
//     by the prefixed DOMAIN-IDENTITY form `vm:<domain>` — the deploy name, not the entity.)
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
func vmDeployEntryKeys(dc *BundleConfig, deployName string) []string {
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
