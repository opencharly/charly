package vm

import (
	"fmt"
	"os"
	"path/filepath"
)

// runVmSpecCreate is the VmCreateCmd.Run branch for kind:vm entities.
// Produces either a libvirt domain (via RenderDomain + virDomainDefine)
// or a QEMU process (via RenderQemuArgv + exec) depending on the
// resolved backend. Pre-conditions: `charly vm build <vm-name>` has run,
// placing disk.qcow2 (+ seed.iso for cloud_image sources) under the per-VM
// disk dir output/qcow2/<vm>/.
//
//nolint:gocyclo // flat sequential vm-create orchestration; extraction relocates, not clarifies
func (c *VmCreateCmd) runVmSpecCreate(vmName string, spec *VmSpec, backend string, claimantNode *BundleNode, resources map[string]*ResolvedResource, vmState *VmDeployState) error {
	// entity = the kind:vm ENTITY (the disk/spec SOURCE — the shared read-only base every per-deploy
	// overlay backs onto). domainID = the per-deploy DOMAIN IDENTITY: the DEPLOY name when the deploy
	// path passes --domain (so sibling beds sharing one entity get distinct, collision-free domains),
	// else the entity itself for a direct `charly vm create <entity>` (behavior unchanged).
	entity := vmName
	domainID := domainOr(vmName, c.Domain)
	perDomain := domainID != entity
	name := domainID
	if c.Instance != "" {
		name = domainID + "-" + c.Instance
	}
	vmDomainName := "charly-" + name

	// Merge this host's per-domain instance override (~/.local/share/charly/vm/
	// <domain>/instance.yml) onto the spec BEFORE any rendering. Its `libvirt:`
	// overlay carries the host-specific GPU <hostdev> + host-path virtiofs
	// shares the committed vm.yml deliberately omits, so the portable entity
	// attaches this host's real devices for a live run. No-op when absent.
	ovr, err := LoadVmInstanceOverride(vmDomainName)
	if err != nil {
		return fmt.Errorf("loading instance override for %s: %w", vmDomainName, err)
	}
	// GPU auto-allocation: when the claimant (the deploy/bed that references
	// this VM via requires_exclusive) needs a `resource:` GPU from the embedded build vocabulary and no
	// hostdev is already configured, detect a matching card, persist its
	// <hostdev> block into this domain's instance.yml, and fold it into ovr —
	// or FAIL HARD if the required card is absent. See gpu_allocate.go.
	ovr, err = autoAllocateExclusiveGPUs(spec, ovr, claimantNode, resources, vmDomainName, backend)
	if err != nil {
		return err
	}
	ovr.ApplyToVmSpec(spec)

	// #Vm's required-with-default fields (firmware/network-mode/cpu-mode) were already materialized on
	// spec host-side by the config-resolve seam (hostConfigResolve applies applyCueDefaults to reply.VM),
	// so the former in-handler applyCueDefaults call is redundant. The instance override above only
	// touches libvirt: overlays (never a defaulted field), so the seam-applied defaults are unaffected.

	// Per-domain state dir (id_ed25519, NVRAM, known_hosts, and — on the deploy path — the disk
	// overlay + per-domain seed). Keyed by the DOMAIN, so N beds sharing one entity never collide.
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	vmStateDir := filepath.Join(home, ".local", "share", "charly", "vm", vmDomainName)
	if err := os.MkdirAll(vmStateDir, 0o755); err != nil {
		return err
	}

	// Locate the built BASE disk in the ENTITY's shared per-entity dir. A deploy boots a per-domain
	// copy-on-write OVERLAY of it (so N beds sharing one entity never write the same qcow2 — the
	// P33 disk isolation); a direct create (domainID == entity) boots the base directly (unchanged).
	baseQcow2 := filepath.Join(vmDiskDir(entity), "disk.qcow2")
	if _, err := os.Stat(baseQcow2); err != nil {
		return fmt.Errorf("disk.qcow2 not found at %s — run `charly vm build %s` first", baseQcow2, entity)
	}
	baseAbs, _ := filepath.Abs(baseQcow2)
	qcow2Abs := baseAbs
	if perDomain {
		overlay := filepath.Join(vmStateDir, "disk.qcow2")
		// Fresh overlay on every (re)create: a create fires only on first boot or after a destroy,
		// so the domain always boots clean off the CURRENT base (the disposable-bed / fresh-rebuild
		// contract). qemu-img refuses an existing target, so drop a stale overlay first.
		_ = os.Remove(overlay)
		if err := qemuImgCreateOverlay(baseAbs, overlay); err != nil {
			return fmt.Errorf("creating per-domain disk overlay for %s: %w", vmDomainName, err)
		}
		qcow2Abs, _ = filepath.Abs(overlay)
	}

	// Resolve the seed ISO path. A deploy renders its OWN per-domain seed (per-domain ssh key +
	// instance-id — two beds must NEVER share one seed, or their cloud-init keys/instance-ids
	// collide); a direct create regenerates the entity's base seed in place (unchanged).
	seedISOAbs := ""
	if perDomain {
		if spec.Source.Kind == "cloud_image" && spec.CloudInit != nil {
			seedISOAbs = filepath.Join(vmStateDir, "seed.iso")
		}
	} else {
		baseSeed := filepath.Join(vmDiskDir(entity), "seed.iso")
		if _, err := os.Stat(baseSeed); err == nil {
			seedISOAbs, _ = filepath.Abs(baseSeed)
		}
	}

	// For cloud_image sources, always (re)render the seed ISO so vm.yml edits
	// (cloud_init packages/runcmd/network-config/etc.) take effect on `charly vm
	// create` without forcing an explicit `charly vm build`. The qcow2 disk is
	// left alone — only the seed ISO is cheap to rebuild. On the deploy path this
	// renders the per-domain seed (with the per-domain ssh key) from scratch.
	if spec.Source.Kind == "cloud_image" && seedISOAbs != "" {
		// existingState (the prior instance-id) comes from the config-resolve seam's VmState.
		if err := RegenerateSeedISO(spec, seedISOAbs, vmStateDir, vmState); err != nil {
			return fmt.Errorf("regenerating seed ISO: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Regenerated cloud-init seed ISO from vm.yml\n")
	}
	pubKey, err := resolveSSHPubKeyForSpec(spec, vmStateDir)
	if err != nil {
		return err
	}

	// Apply D13 key-injection resolution. The credential targets the
	// SSH user the spec asks for — root for bootc/legacy paths, the
	// named user for cloud_image / bootstrap VMs. The image MUST
	// already have the user account created (via cloud_image base or
	// bootloader install template); this only delivers the per-VM key.
	smbiosOn, _ := ResolveKeyInjectionChannels(spec)
	var smbiosCreds []string
	if smbiosOn && pubKey != "" {
		smbiosCreds = append(smbiosCreds, SmbiosCredForSSH(resolveVmSshUser(spec), "", pubKey))
	}

	// Resolve D17 firmware paths + per-VM NVRAM.
	ovmfCode, nvramPath, err := ResolveOvmfForSpec(spec, vmStateDir)
	if err != nil {
		return fmt.Errorf("resolving firmware: %w", err)
	}

	// Compose VmRuntimeParams. SSH port resolves here so ssh.port_auto can
	// allocate (or reuse the persisted) host port before the libvirt forward
	// is rendered. Keyed by the DOMAIN IDENTITY (not the entity) so every deploy
	// sharing one entity gets its OWN auto-allocated host port — two beds
	// forwarding the same host port to their guests' :22 is the collision this
	// cutover removes.
	sshPort, err := resolveVmSshPort(spec, domainID)
	if err != nil {
		return fmt.Errorf("resolving SSH port: %w", err)
	}
	// For ssh.port_auto, persist the resolved port NOW so deploy-add's
	// reachability probe (and every later read) reuses THIS exact port. The
	// auto-allocation must be stable across the vm-create → deploy-add sequence;
	// without persisting here deploy-add re-resolves, finds no persisted port,
	// allocates a DIFFERENT one, probes the wrong port, declares the VM
	// unreachable, and auto-boots `charly vm create` into an "already exists" error.
	// The ledger key is vm:<domainID> (entity carried as the `vm:` cross-ref), so
	// the preresolver's resolveVmSshPort(spec, domainID) reads back the SAME entry.
	if spec.SSH != nil && spec.SSH.PortAuto {
		st := &VmDeployState{}
		if vmState != nil {
			st = vmState
		}
		st.SshPort = sshPort
		if err := hostConfigPersist("vm:"+domainID, entity, st, false); err != nil {
			return fmt.Errorf("persisting auto-allocated ssh port: %w", err)
		}
	}
	rt := VmRuntimeParams{
		Name:              vmDomainName,
		QCOW2Path:         qcow2Abs,
		SeedISOPath:       seedISOAbs,
		NVRAMPath:         nvramPath,
		OVMFCodePath:      ovmfCode,
		HostArch:          hostArchRuntime(),
		HostCPUVendor:     detectRuntimeHostVendor(),
		SMBIOSCredentials: smbiosCreds,
		RamMB:             parseRAMtoMB(resolveVmRam(spec)),
		Cpus:              resolveVmCpus(spec),
		SshPort:           sshPort,
		VmStateDir:        vmStateDir,
	}
	// ExtraPortForwards intentionally empty — spec.Network.PortForwards is
	// already read by the renderers directly. Populating rt here would
	// duplicate every entry.

	// Backend dispatch → the in-package engine (RenderDomainXML + defineAndStartDomain / qemu exec).
	// spec + rt (OVMF/NVRAM/smbios/ports) are fully resolved above; the engine renders + creates.
	//
	// Two-phase create for EGRESS validation: phase 1 (ValidateOnly) RENDERS + RETURNS the libvirt
	// domain XML; ValidateXMLEgress validates it via verb:egress (the egress subsystem lives in
	// candy/plugin-egress, reached in-proc — vm_egress_shim.go); only then phase 2 creates. The
	// cloud-init seed is egress-validated above (RegenerateSeedISO → RenderCloudInit → the
	// vmshared.ValidateEgress hook → verb:egress). QEMU returns no XML, so its validate pass is a no-op.
	baseReq := vmCreateReq{
		Spec: spec, RT: rt, VmDomainName: vmDomainName, Home: home,
		VmName: entity, Name: name, Backend: backend, VmStateDir: vmStateDir,
	}
	validateReq := baseReq
	validateReq.ValidateOnly = true
	rawV, okV := invokeVmCreate(validateReq)
	if !okV {
		return fmt.Errorf("vm plugin unavailable (go-libvirt create is out-of-process)")
	}
	if e := vmPluginOpError(rawV); e != "" {
		return fmt.Errorf("rendering VM %s for egress validation: %s", vmDomainName, e)
	}
	if xmlStr := vmCreateRenderedXML(rawV); xmlStr != "" {
		if err := ValidateXMLEgress("libvirt_domain_xml", "vm:"+vmName, xmlStr); err != nil {
			return fmt.Errorf("egress validation of VM %s domain XML: %w", vmDomainName, err)
		}
	}
	raw, ok := invokeVmCreate(baseReq)
	if !ok {
		return fmt.Errorf("vm plugin unavailable (go-libvirt create is out-of-process)")
	}
	if e := vmPluginOpError(raw); e != "" {
		return fmt.Errorf("creating VM %s: %s", vmDomainName, e)
	}
	// Host-side post-create concerns the plugin's create no longer does (they manage the
	// operator's systemd linger + ~/.config/charly/ssh_config — host territory).
	if backend == "libvirt" && spec.Autostart {
		ensureBootAutostartPrereqs(vmDomainName)
	}
	// On the deploy path (domain named after the deploy, not the entity), prune the pre-P33
	// entity-keyed ssh alias so it does not linger as an orphan after the naming cutover.
	if perDomain {
		migrateStaleEntityAlias(home, entity)
	}
	if err := publishVmSshAlias(home, name, spec, rt); err != nil {
		return fmt.Errorf("publishing ssh-config alias: %w", err)
	}
	return nil
}

// migrateStaleEntityAlias best-effort prunes the pre-P33 managed ssh-config stanza for the
// ENTITY-keyed alias (charly-<entity>). Before this cutover a deploy's VM was named after the
// kind:vm ENTITY; it is now named after the DEPLOY (domain identity), so on the first create under
// the new naming the old entity-keyed alias is removed. Only the recoverable ssh stanza is pruned —
// the stale libvirt DOMAIN charly-<entity> (if any) is left for the operator to reclaim
// (`charly vm destroy <entity>` / `charly vm list --clean-orphans`), because auto-destroying it is
// unsafe when charly-<entity> is a legitimately direct-created operator VM. Idempotent + silent when
// no stale alias is present (the common case — bed-only entities were never aliased under charly-<entity>).
func migrateStaleEntityAlias(home, entity string) {
	staleAlias := VmSshAlias(entity)
	aliases, _ := ListVmSshAliases(home)
	present := false
	for _, a := range aliases {
		if a == staleAlias {
			present = true
			break
		}
	}
	if !present {
		return
	}
	if _, err := RemoveVmSshStanza(home, staleAlias); err != nil {
		fmt.Fprintf(os.Stderr, "note: pruning stale ssh alias %s: %v\n", staleAlias, err)
		return
	}
	fmt.Fprintf(os.Stderr, "note: pruned pre-P33 ssh alias %s (this VM is now named after its deploy); reclaim any leftover domain with `charly vm destroy %s` or `charly vm list --clean-orphans`\n", staleAlias, entity)
}

// publishVmSshAlias writes (or refreshes) the managed ssh-config Host
// stanza for this VM and ensures the Include line is present in the
// user's ~/.ssh/config. Idempotent — safe to call on every `charly vm
// create` invocation including reruns.
//
// Clears the per-VM known_hosts file as part of the refresh: each
// `charly vm create` boots a fresh guest (or recreates a destroyed one)
// whose sshd regenerates its host key on first boot. The stale entry
// in known_hosts from a previous incarnation would trigger ssh's
// "REMOTE HOST IDENTIFICATION HAS CHANGED" rejection — and because
// the stanza sets `StrictHostKeyChecking accept-new`, ssh accepts
// brand-new keys but REFUSES changed ones. Clearing on every create
// matches the disposable-VM semantics: the on-disk state machine
// resets to empty when the domain is recreated. The first ssh after
// vm create writes the new key into known_hosts.
//
// Without this fix, dispatcher loops that destroy + recreate VMs
// (`charly check run check-k3s-vm`, `charly update <vm-bed>`) fail at the post-create
// SSH step with "Host key verification failed", which surfaces in
// the vm deploy's SSHExecutor.WaitForSSH preflight as "Could not resolve hostname" — see the
// 2026-05-06 R10 follow-up RCA.
// domainName is the FULL per-domain identity (domainID[-instance]) — the same token the per-domain
// state dir (charly-<domainName>) and the libvirt domain use, so the published alias, its identity
// file, and its known_hosts all point at THIS domain's own state (never a sibling's).
func publishVmSshAlias(home, domainName string, spec *VmSpec, rt VmRuntimeParams) error {
	stateDir := filepath.Join(home, ".local", "share", "charly", "vm", "charly-"+domainName)
	knownHostsPath := filepath.Join(stateDir, "known_hosts")
	// Best-effort: ignore "no such file" on first-create.
	_ = os.Remove(knownHostsPath)
	stanza := VmSshStanza{
		Alias:          VmSshAlias(domainName),
		Hostname:       "127.0.0.1",
		Port:           rt.SshPort,
		User:           resolveVmSshUser(spec),
		IdentityFile:   filepath.Join(stateDir, "id_ed25519"),
		KnownHostsFile: knownHostsPath,
	}
	if err := WriteVmSshStanza(home, stanza); err != nil {
		return err
	}
	return EnsureSshConfigInclude(home)
}
