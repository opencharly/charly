package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// vm_deploy_lifecycle.go — the HOST-SIDE lifecycle hook for the EXTERNAL `vm` deploy
// substrate (Design A). The plan WALK runs out-of-process in candy/plugin-deploy-vm
// (kit.WalkPlans over the guest SSHExecutor the reverse channel serves); this hook owns
// ONLY what the host must do that the plugin cannot: boot the domain + build the guest
// SSH executor, the ssh-config / charly.yml-entry / ephemeral bookkeeping, the nested
// pod-in-guest orchestration, and the `charly vm` lifecycle (start/stop/console/ssh +
// the destroy+build+create+start+re-add Rebuild that `charly update <vm-bed>` routes
// through — the R10 fresh-rebuild gate).
//
// It carries NO per-deploy mutable state (registered as a stateless singleton, like the
// deploy preresolvers) — every method re-resolves what it needs from (name, dir, node).

// vmSubstrateLifecycle implements substrateLifecycle for the `vm` word.
type vmSubstrateLifecycle struct{}

// register at package-var init (before any init(), race-free with the rest of the F1 wiring).
var _ = func() bool {
	registerSubstrateLifecycle("vm", vmSubstrateLifecycle{})
	return true
}()

// PrepareVenue runs the full host-side VM preflight and returns the guest *SSHExecutor the
// reverse channel serves. It LIFTS the prior VmUnifiedTarget.Add preflight (resolve entity,
// ssh-config stanza, auto-boot) + the prior VmDeployTarget.Emit preflight (WaitForSSH /
// WaitForCloudInit / WaitForPackageLock / EnsureCharlyInGuest) into one host-side step that
// runs BEFORE the plugin walks. {{.Home}} is resolved against the guest home by the generic
// externalDeployTarget.apply (its prepareReverseState calls exec.ResolveHome on THIS
// executor → the guest), so this hook ships no substrate payload.
// plans is ignored: the vm plugin WALKS the deployment's plans in-guest over the returned
// SSHExecutor (the reverse channel), so the host hook ships no plan payload — only pod
// consumes plans (to build its overlay host-side).
func (vmSubstrateLifecycle) PrepareVenue(ctx context.Context, name, dir string, node *BundleNode, _ []*InstallPlan, opts EmitOpts) (DeployExecutor, error) {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	if node == nil {
		tree, err := resolveTreeRoot(dir)
		if err != nil {
			return nil, fmt.Errorf("vm deploy %q: resolve deploy node: %w", name, err)
		}
		n, ok := tree[name]
		if !ok {
			return nil, fmt.Errorf("vm deploy %q: no deploy entry", name)
		}
		node = &n
	}

	vmName, err := vmEntityForAdd(node, name)
	if err != nil {
		return nil, err
	}

	// Load the kind:vm entity from charly.yml.
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !ok || uf.VM == nil {
		return nil, fmt.Errorf("vm deploy %q: no charly.yml or no kind:vm entities declared", name)
	}
	spec, ok := uf.VM[vmName]
	if !ok {
		return nil, fmt.Errorf("vm deploy %q: no kind:vm entity named %q in charly.yml", name, vmName)
	}

	// Ephemeral lifecycle hook (FIRST action — panic-safe TTL ordering). Consumes the
	// MERGED node (never a charly.yml re-read).
	registerEphemeralIfMarked(node, name)

	// Load existing VmDeployState (runtime state: instance-id, ssh_port, disk path).
	var state *VmDeployState
	if dc := loadDeployConfigForRead("charly bundle add vm"); dc != nil {
		if entry, exists := dc.Bundle[name]; exists && entry.VmState != nil {
			state = entry.VmState
		}
	}
	if state == nil {
		state = &VmDeployState{}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home dir: %w", err)
	}
	stateDir := filepath.Join(home, ".local", "share", "charly", "vm", "charly-"+vmName)

	sshUser := resolveVmSshUser(spec)
	sshPort, err := resolveVmSshPort(spec, vmName)
	if err != nil {
		return nil, err
	}
	sshKeyPath := filepath.Join(stateDir, "id_ed25519")
	knownHostsPath := filepath.Join(stateDir, "known_hosts")

	// Publish (or refresh) the managed ssh-config Host stanza for this VM + ensure the
	// Include line is present in ~/.ssh/config. The alias keys off the unprefixed VM name.
	if err := WriteVmSshStanza(home, VmSshStanza{
		Alias:          VmSshAlias(vmName),
		Hostname:       "127.0.0.1",
		Port:           sshPort,
		User:           sshUser,
		IdentityFile:   sshKeyPath,
		KnownHostsFile: knownHostsPath,
	}); err != nil {
		return nil, fmt.Errorf("publishing ssh-config stanza: %w", err)
	}
	if err := EnsureSshConfigInclude(home); err != nil {
		return nil, fmt.Errorf("ensuring ssh-config include: %w", err)
	}

	smbiosOn, cloudInitOn := ResolveKeyInjectionChannels(spec)

	// Build the DeployExecutor against the managed ssh-config alias. For a nested VM, the
	// same alias works inside the parent's venue.
	alias := VmSshAlias(vmName)
	var exec DeployExecutor = &SSHExecutor{Host: alias, ConnectTimeout: 10}
	if opts.ParentExec != nil {
		exec = &NestedExecutor{
			Parent: opts.ParentExec,
			Jump:   NestedJump{Kind: JumpSSH, Target: alias},
		}
	}

	// Auto-boot: if the VM isn't reachable on its SSH port, `charly vm build` + `charly vm
	// create` to boot it. No-op in DryRun (caller skips PrepareVenue then), when nested, and
	// when CHARLY_DEPLOY_NO_AUTOBOOT is set.
	if err := autoBootVmIfNeeded(vmName, sshPort, opts); err != nil {
		return nil, err
	}

	// Boot-readiness preflight on the guest BEFORE the plugin walks (LIFTED from the prior
	// VmDeployTarget.Emit). The plugin's kit.WalkPlans assumes sshd is up + the package lock
	// is free + the charly binary is present.
	if sshExec, isSSH := execAsSSH(exec); isSSH {
		fmt.Fprintf(os.Stderr, "Waiting for sshd on %s...\n", sshExec.Host)
		if err := sshExec.WaitForSSH(ctx); err != nil {
			return nil, fmt.Errorf("vm deploy %q: wait-for-sshd: %w", name, err)
		}
		if spec.Source.Kind == "cloud_image" || spec.CloudInit != nil {
			fmt.Fprintln(os.Stderr, "Waiting for cloud-init to finish in guest...")
			if err := sshExec.WaitForCloudInit(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: cloud-init wait returned %v (continuing)\n", err)
			}
			if err := sshExec.WaitForPackageLock(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: package-lock wait returned %v (continuing)\n", err)
			}
		}
	}

	// Ensure the charly binary is present in the guest per VmCharlyInstall.Strategy.
	msg, err := EnsureCharlyInGuest(ctx, spec, exec, opts)
	if err != nil {
		return nil, fmt.Errorf("vm deploy %q: ensure charly in guest: %w", name, err)
	}
	fmt.Fprintln(os.Stderr, msg)

	// Persist the VM runtime state to charly.yml (connection info known at venue-prep time;
	// independent of walk success — a partial deploy still re-adds idempotently).
	state.SshUser = sshUser
	state.SshPort = sshPort
	if state.Backend == "" {
		state.Backend = "auto"
	}
	state.KeyInjectionResolved = &VmKeyInjectionResolved{SMBIOS: smbiosOn, CloudInit: cloudInitOn}
	state.CharlyInstallStrategy = string(ResolveCharlyInstallStrategy(spec))
	if err := saveVmDeployState(name, vmName, state); err != nil {
		return nil, fmt.Errorf("persisting VmDeployState: %w", err)
	}

	fmt.Fprintf(os.Stderr, "VM venue ready %s (ssh: %s@127.0.0.1:%d)\n", name, sshUser, sshPort)
	return exec, nil
}

// ArtifactKey keys candy artifacts (+ the k3s ClusterProfile) under "vm:<entity>", NOT the
// deploy name: one k3s cluster per VM is reached by several beds, so its profile must land
// under the shared "vm-<entity>" name the `cluster:` refs use (cf. check-k3s-vm /
// check-k8s-deploy). Passing the deploy key wrote the kubeconfig under the wrong profile.
func (vmSubstrateLifecycle) ArtifactKey(name string, node *BundleNode) string {
	entity := vmEntityForLifecycle(name, node)
	return "vm:" + entity
}

// PostApply deploys nested target:pod children as persistent in-guest quadlets, AFTER the
// plan walk (so the VM's own candies + any kernel-driver reboot are already applied). The
// generic Add skips this when --node-only is set; deployNestedPodsInGuest itself no-ops on
// a nested VM / dry-run / no children.
func (vmSubstrateLifecycle) PostApply(ctx context.Context, name, dir string, node *BundleNode, exec DeployExecutor, opts EmitOpts) error {
	if opts.DryRun || opts.ParentExec != nil || node == nil || len(node.Children) == 0 {
		return nil
	}
	vmName := vmEntityForLifecycle(name, node)
	if err := deployNestedPodsInGuest(vmName, node, exec, opts); err != nil {
		return fmt.Errorf("deploying nested pods in guest: %w", err)
	}
	return nil
}

// TeardownExecutor returns the guest *SSHExecutor `charly bundle del` replays the recorded
// ReverseOps over (the same managed alias; NO boot — the guest is expected up). The generic
// externalDeployTarget.Del derives an *sshReverseRunner from it so teardown runs IN THE
// GUEST, not on the operator host.
func (vmSubstrateLifecycle) TeardownExecutor(name string, node *BundleNode) (DeployExecutor, error) {
	vmName := vmEntityForLifecycle(name, node)
	return &SSHExecutor{Host: VmSshAlias(vmName), ConnectTimeout: 10}, nil
}

// PostTeardown runs the host-side VM cleanup AFTER teardown: the ephemeral lifecycle
// teardown, the charly.yml deploy-entry removal, and the managed ssh-config stanza removal
// (stripping the Include line when it was the last managed alias). Best-effort.
// keepImage is ignored: a vm has no synthesized overlay image to drop (the `--keep-image`
// gate is pod-specific).
func (vmSubstrateLifecycle) PostTeardown(name string, node *BundleNode, _ bool) error {
	vmName := vmEntityForLifecycle(name, node)

	if dcNode, ok := loadDeployConfigForRead("vm target ephemeral-teardown").LookupKey(name); ok && dcNode.IsEphemeral() {
		if tdErr := TeardownEphemeralLifecycle(&dcNode, name); tdErr != nil {
			fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle teardown: %v\n", tdErr)
		}
	}

	// The charly.yml entry + the persisted vm_state key off "vm:<entity>" for a schema-v4
	// bed whose deploy key differs from the entity (e.g. check-k3s-vm → vm: k3s-vm).
	entryKey := name
	if !strings.HasPrefix(name, "vm:") && node != nil && node.From != "" {
		entryKey = "vm:" + node.From
	}
	if rerr := removeVmDeployEntry(entryKey); rerr != nil {
		fmt.Fprintf(os.Stderr, "note: charly.yml cleanup: %v\n", rerr)
	}

	if home, herr := os.UserHomeDir(); herr == nil {
		remaining, rerr := RemoveVmSshStanza(home, VmSshAlias(vmName))
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "note: ssh-config stanza cleanup: %v\n", rerr)
		}
		if remaining == 0 {
			if rerr := RemoveSshConfigInclude(home); rerr != nil {
				fmt.Fprintf(os.Stderr, "note: ssh-config include cleanup: %v\n", rerr)
			}
		}
	}
	return nil
}

// Start boots the VM via `charly vm start`.
func (vmSubstrateLifecycle) Start(_ context.Context, name string, node *BundleNode) error {
	return runCharlySubcommand("vm", "start", vmEntityForLifecycle(name, node))
}

// Stop graceful-shutdowns the VM via `charly vm stop`.
func (vmSubstrateLifecycle) Stop(_ context.Context, name string, node *BundleNode) error {
	return runCharlySubcommand("vm", "stop", vmEntityForLifecycle(name, node))
}

// Status reads `charly vm list` output and walks for this target's domain.
func (vmSubstrateLifecycle) Status(_ context.Context, name string, node *BundleNode) (StatusInfo, error) {
	want := "charly-" + vmEntityForLifecycle(name, node)
	out, err := captureCharlyStdout("vm", "list")
	if err != nil {
		return StatusInfo{State: "unknown"}, err
	}
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] != want {
			continue
		}
		state := fields[len(fields)-1]
		return StatusInfo{
			State:   state,
			Healthy: state == "running",
			Details: map[string]string{"backend": fields[1], "domain": fields[0]},
		}, nil
	}
	return StatusInfo{State: "stopped", Healthy: false}, nil
}

// Logs streams the VM's serial console via `charly vm console`.
func (vmSubstrateLifecycle) Logs(_ context.Context, name string, node *BundleNode, _ LogsOpts) error {
	return runCharlySubcommand("vm", "console", vmEntityForLifecycle(name, node))
}

// Shell sshes into the VM via `charly vm ssh` (interactive, or runs cmd non-interactively).
func (vmSubstrateLifecycle) Shell(_ context.Context, name string, node *BundleNode, cmd []string) error {
	args := make([]string, 0, 3+len(cmd))
	args = append(args, "vm", "ssh", vmEntityForLifecycle(name, node))
	args = append(args, cmd...)
	return runCharlySubcommand(args...)
}

// Rebuild destroys + (optionally) rebuilds the disk + recreates + starts the VM, THEN
// re-applies the deploy's candies (and nested pods) to the fresh guest via `charly bundle
// add <name>` — the shared layer-apply primitive (R3). This is the path `charly update
// <vm-bed>` routes through (the disposable bed's fresh-rebuild R10 gate); without the final
// re-add the guest would come back bare. The disposable check is the caller's.
func (vmSubstrateLifecycle) Rebuild(_ context.Context, name string, node *BundleNode, opts RebuildOpts) error {
	entity := vmEntityForLifecycle(name, node)
	if opts.DryRun {
		fmt.Printf("dry-run: charly vm destroy %s\n", entity)
		if opts.RebuildImage {
			fmt.Printf("dry-run: charly vm build %s\n", entity)
		}
		fmt.Printf("dry-run: charly vm create %s\n", entity)
		fmt.Printf("dry-run: charly vm start %s\n", entity)
		fmt.Printf("dry-run: charly bundle add %s\n", name)
		return nil
	}
	// Destroy is best-effort — the VM may not exist yet on a first build.
	_ = runCharlySubcommand("vm", "destroy", entity)
	if opts.RebuildImage {
		if err := runCharlySubcommand("vm", "build", entity); err != nil {
			return fmt.Errorf("charly vm build %s: %w", entity, err)
		}
	}
	if err := runCharlySubcommand("vm", "create", entity); err != nil {
		return fmt.Errorf("charly vm create %s: %w", entity, err)
	}
	stderr, startErr := runCharlySubcommandCapture("vm", "start", entity)
	if startErr != nil {
		if !isBenignAlreadyRunning(stderr) {
			fmt.Fprint(os.Stderr, stderr)
			return fmt.Errorf("charly vm start %s: %w", entity, startErr)
		}
	} else if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	// Re-apply the deploy's candies (+ nested pods) on the fresh guest via the shared
	// `charly bundle add <node>` path — the SAME primitive the local/pod Rebuild call (R3).
	if err := runCharlySubcommand("bundle", "add", name); err != nil {
		return fmt.Errorf("charly bundle add %s: %w", name, err)
	}
	return nil
}

// vmEntityForLifecycle resolves the kind:vm entity name for a lifecycle op from (name,
// node): the node's `vm:` cross-ref (node.From) wins, then the deploy's persisted cross-ref
// (vmEntityForDeploy), then a legacy "vm:<entity>" prefix, then the deploy name itself.
func vmEntityForLifecycle(name string, node *BundleNode) string {
	if node != nil && node.From != "" {
		return node.From
	}
	if vm := vmEntityForDeploy(name); vm != "" {
		return vm
	}
	if strings.HasPrefix(name, "vm:") {
		if entity, err := vmNameFromDeployName(name); err == nil {
			return entity
		}
	}
	return name
}

// execAsSSH unwraps a DeployExecutor to its underlying *SSHExecutor when it is one (the
// preflight WaitFor* methods are SSH-specific). A NestedExecutor (vm-in-pod) is not unwrapped
// — its preflight runs through the chain at walk time; the nested case skips the host-side
// boot gates (the parent venue is already up and the inner sshd readiness is bounded by the
// reverse-channel call timeouts), matching the prior VmDeployTarget behaviour which probed
// `t.Exec.(*SSHExecutor)` and skipped the gates for a NestedExecutor.
func execAsSSH(exec DeployExecutor) (*SSHExecutor, bool) {
	s, ok := exec.(*SSHExecutor)
	return s, ok
}

// vmEntityForAdd resolves the kind:vm entity name for an add. Prefers the merged node's
// `vm:` cross-ref (the canonical mapping for a schema-v4 deploy where the key != entity,
// e.g. check-k3s-vm → vm: k3s-vm); falls back to stripping a legacy "vm:<name>" deploy-key
// prefix, then to the leaf of a nested dotted path (stack.myvm → myvm). Relocated here from
// the deleted unified_targets_vm.go.
func vmEntityForAdd(node *BundleNode, name string) (string, error) {
	if node != nil && node.From != "" {
		return node.From, nil
	}
	if strings.HasPrefix(name, "vm:") {
		return vmNameFromDeployName(name)
	}
	if strings.Contains(name, ".") {
		return pathLeaf(name), nil
	}
	return "", fmt.Errorf("vm deploy %q: no `vm:` cross-ref and key is not a legacy vm:<name> form", name)
}

// autoBootVmIfNeeded probes the VM's SSH port and, when unreachable, boots it via `charly vm
// build` + `charly vm create`. TCP probe — fast. No-op in DryRun, when nested (ParentExec
// set), and when CHARLY_DEPLOY_NO_AUTOBOOT is set. Relocated here from the deleted
// unified_targets_vm.go.
func autoBootVmIfNeeded(vmName string, sshPort int, opts EmitOpts) error {
	if opts.DryRun || opts.ParentExec != nil || os.Getenv("CHARLY_DEPLOY_NO_AUTOBOOT") != "" {
		return nil
	}
	sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)
	conn, dialErr := net.DialTimeout("tcp", sshAddr, 2*time.Second)
	if dialErr != nil {
		fmt.Fprintf(os.Stderr,
			"VM %q not reachable on %s — auto-booting via `charly vm build %s` + `charly vm create %s` (set CHARLY_DEPLOY_NO_AUTOBOOT=1 to skip)...\n",
			vmName, sshAddr, vmName, vmName)
		if bErr := runCharlySubcommand("vm", "build", vmName); bErr != nil {
			return fmt.Errorf("auto-boot: charly vm build %s: %w", vmName, bErr)
		}
		if cErr := runCharlySubcommand("vm", "create", vmName); cErr != nil {
			return fmt.Errorf("auto-boot: charly vm create %s: %w", vmName, cErr)
		}
	} else {
		_ = conn.Close()
	}
	return nil
}
