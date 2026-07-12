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
	name := vmName
	if c.Instance != "" {
		name = vmName + "-" + c.Instance
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

	// Locate prebuilt disk + seed ISO in this VM's OWN per-VM disk dir, so a
	// fresh create can never adopt a sibling VM's stale disk/seed (whose
	// embedded SSH key would mismatch this VM's id_ed25519).
	qcow2 := filepath.Join(vmDiskDir(vmName), "disk.qcow2")
	if _, err := os.Stat(qcow2); err != nil {
		return fmt.Errorf("disk.qcow2 not found at %s — run `charly vm build %s` first", qcow2, vmName)
	}
	qcow2Abs, _ := filepath.Abs(qcow2)

	seedISO := filepath.Join(vmDiskDir(vmName), "seed.iso")
	seedISOAbs := ""
	if _, err := os.Stat(seedISO); err == nil {
		seedISOAbs, _ = filepath.Abs(seedISO)
	}

	// Resolve SSH pubkey (honoring spec.SSH.KeySource).
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	vmStateDir := filepath.Join(home, ".local", "share", "charly", "vm", vmDomainName)
	if err := os.MkdirAll(vmStateDir, 0o755); err != nil {
		return err
	}

	// For cloud_image sources, always regenerate the seed ISO so vm.yml
	// edits (cloud_init packages/runcmd/network-config/etc.) take effect on
	// `charly vm create` without forcing an explicit `charly vm build`. The qcow2
	// disk is left alone — only the seed ISO is cheap to rebuild.
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
	// is rendered.
	sshPort, err := resolveVmSshPort(spec, vmName)
	if err != nil {
		return fmt.Errorf("resolving SSH port: %w", err)
	}
	// For ssh.port_auto, persist the resolved port NOW so deploy-add's
	// reachability probe (and every later read) reuses THIS exact port. The
	// auto-allocation must be stable across the vm-create → deploy-add sequence;
	// without persisting here deploy-add re-resolves, finds no persisted port,
	// allocates a DIFFERENT one, probes the wrong port, declares the VM
	// unreachable, and auto-boots `charly vm create` into an "already exists" error.
	if spec.SSH != nil && spec.SSH.PortAuto {
		st := &VmDeployState{}
		if vmState != nil {
			st = vmState
		}
		st.SshPort = sshPort
		if err := hostConfigPersist("vm:"+vmName, vmName, st, false); err != nil {
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
		VmName: vmName, Name: name, Backend: backend, VmStateDir: vmStateDir,
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
	if err := publishVmSshAlias(home, vmName, name, spec, rt); err != nil {
		return fmt.Errorf("publishing ssh-config alias: %w", err)
	}
	return nil
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
func publishVmSshAlias(home, vmName, deployName string, spec *VmSpec, rt VmRuntimeParams) error {
	stateDir := filepath.Join(home, ".local", "share", "charly", "vm", "charly-"+vmName)
	knownHostsPath := filepath.Join(stateDir, "known_hosts")
	// Best-effort: ignore "no such file" on first-create.
	_ = os.Remove(knownHostsPath)
	stanza := VmSshStanza{
		Alias:          VmSshAlias(deployName),
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
