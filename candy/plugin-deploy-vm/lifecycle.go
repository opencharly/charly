package deployvm

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// lifecycle.go — the host-side VM venue lifecycle, IMPLEMENTED in the plugin (M4b, clean). The plugin
// runs ON the host (co-located) but out-of-process; it does the WHOLE venue lifecycle itself over
// GENERIC seams — sdk/kit for the ssh-config stanza + guest readiness waits + charly delivery,
// HostBuild("cli") for `charly vm …`, and the reverse channel for guest ops. Core provides ONLY the
// resolved DATA (spec.LifecyclePrepareInput, shipped by the host vm preresolver — the same DATA-seam
// shape as the sanctioned in-core k8s/android preresolvers). NO vm lifecycle logic remains in core.

// lifecycleParams are the params the host proxy ships for a vm lifecycle Op. node is the canonical
// BundleNode JSON; prepare is the resolved spec.LifecyclePrepareInput (PrepareVenue only); opts is
// polymorphic (LifecycleOpts/LogsOpts/RebuildOpts), decoded per-op.
type lifecycleParams struct {
	Name      string          `json:"name"`
	Dir       string          `json:"dir"`
	Node      json.RawMessage `json:"node"`
	Opts      json.RawMessage `json:"opts"`
	Prepare   json.RawMessage `json:"prepare"`
	KeepImage bool            `json:"keep_image"`
	Cmd       []string        `json:"cmd"`
	Plan      json.RawMessage `json:"plan"` // host-resolved spec.PodLiveStdioPlan (F12 OpAttach)
}

// isLifecycleOp reports whether op is a substrate-lifecycle Op (vs. the OpExecute deploy walk).
func isLifecycleOp(op string) bool {
	switch op {
	case sdk.OpPrepareVenue, sdk.OpArtifactKey, sdk.OpPostApply, sdk.OpTeardownExecutor,
		sdk.OpPostTeardown, sdk.OpStart, sdk.OpStop, sdk.OpStatus, sdk.OpLogs, sdk.OpShell,
		sdk.OpAttach, sdk.OpRebuild:
		return true
	}
	return false
}

// invokeLifecycle handles a vm substrate-lifecycle Op over the reverse channel.
func invokeLifecycle(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm %s: executor: %w", req.GetOp(), err)
	}
	var p lifecycleParams
	_ = json.Unmarshal(req.GetParamsJson(), &p)
	var host spec.HostEnv
	_ = json.Unmarshal(req.GetEnvJson(), &host)

	switch req.GetOp() {
	case sdk.OpPrepareVenue:
		return vmPrepareVenue(ctx, exec, p, host)
	case sdk.OpPostApply:
		return vmPostApply(ctx, exec, p, host)
	case sdk.OpArtifactKey:
		// Artifacts (+ the k3s ClusterProfile) key by the shared ENTITY, not the per-deploy domain:
		// one k3s cluster per VM is reached by several beds, so its profile lands under the shared
		// vm:<entity> name the `cluster:` refs use. This is DELIBERATELY the entity, not domainIdentity.
		return marshalReply(map[string]string{"key": "vm:" + vmEntity(p)})
	case sdk.OpTeardownExecutor:
		return marshalReply(spec.VenueDescriptor{Kind: "ssh", Host: kit.VmSshAlias(domainIdentity(p)), ConnectTimeout: 10})
	case sdk.OpPostTeardown:
		return vmPostTeardown(ctx, exec, p, host)
	case sdk.OpStart:
		return cliOK(vmCli(ctx, exec, false, false, "vm", "start", vmEntity(p), "--domain", domainIdentity(p)))
	case sdk.OpStop:
		return cliOK(vmCli(ctx, exec, false, false, "vm", "stop", vmEntity(p), "--domain", domainIdentity(p)))
	case sdk.OpStatus:
		return vmStatus(ctx, exec, domainIdentity(p))
	case sdk.OpLogs:
		return cliOK(vmCli(ctx, exec, false, false, "vm", "console", vmEntity(p), "--domain", domainIdentity(p)))
	case sdk.OpShell:
		// `charly vm ssh` keys the connection off the managed ssh alias (charly-<domain>), so it takes
		// the DOMAIN IDENTITY as its positional — no --domain flag (it resolves no entity spec).
		return cliOK(vmCli(ctx, exec, false, false, append([]string{"vm", "ssh", domainIdentity(p)}, p.Cmd...)...))
	case sdk.OpAttach:
		return vmAttach(ctx, exec, p)
	case sdk.OpRebuild:
		return vmRebuild(ctx, exec, p)
	}
	return nil, fmt.Errorf("plugin-deploy-vm: unhandled lifecycle op %q", req.GetOp())
}

// vmAttach runs the F12 interactive session (`charly shell <vm-deploy>`) IN the guest: the host serves
// the guest *SSHExecutor (via the vm VenueExecutor) and resolved the in-guest command into p.Plan
// (#PodLiveStdioPlan — empty script ⇒ a bare login shell, `ssh -t <alias>`). The plugin runs it over
// exec.RunInteractive, whose SSHExecutor leg wraps it in `ssh -t <alias> [script]` with the operator's
// terminal inherited host-side. The exit round-trips as spec.PodExecReply.ExitCode → *sdk.ExitCodeError.
func vmAttach(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	var plan spec.PodLiveStdioPlan
	if err := json.Unmarshal(p.Plan, &plan); err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm attach: decode plan: %w", err)
	}
	exit, err := exec.RunInteractive(ctx, plan.Script)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm attach: %w", err)
	}
	return marshalReply(spec.PodExecReply{ExitCode: exit})
}

// domainIdentity resolves the per-deploy DOMAIN IDENTITY from the deploy name (p.Name) — the token
// the libvirt domain (charly-<identity>), the per-domain disk overlay + state dir, the managed ssh
// alias, and the ssh-port ledger key off. It is DISTINCT from the disk/spec-source ENTITY
// (vmEntity): several beds may share one entity, so keying the domain by the DEPLOY name makes them
// collision-free by construction. The host preresolver derives the SAME value from the SAME deploy
// name via vmshared.VmDomainIdentity, so the domain the two name always agrees.
func domainIdentity(p lifecycleParams) string {
	return vmshared.VmDomainIdentity(p.Name)
}

// vmEntity resolves the kind:vm entity from the shipped node: node.From (the `vm:` cross-ref) wins,
// else a legacy "vm:<name>" deploy-key prefix, else the deploy name.
func vmEntity(p lifecycleParams) string {
	var node spec.BundleNode
	_ = json.Unmarshal(p.Node, &node)
	if node.From != "" {
		return string(node.From)
	}
	if strings.HasPrefix(p.Name, "vm:") {
		return strings.TrimPrefix(strings.SplitN(p.Name, "/", 2)[0], "vm:")
	}
	return p.Name
}

// vmCli asks the HOST to run `charly <argv>` via the generic "cli" host-builder (the vm analog of
// pod's podCli). capture returns stdout; bestEffort swallows a non-zero exit.
func vmCli(ctx context.Context, exec *sdk.Executor, capture, bestEffort bool, argv ...string) (spec.CliReply, error) {
	reqJSON, err := json.Marshal(spec.CliRequest{Argv: argv, Capture: capture, BestEffort: bestEffort})
	if err != nil {
		return spec.CliReply{}, err
	}
	resJSON, err := exec.HostBuild(ctx, "cli", reqJSON)
	if err != nil {
		return spec.CliReply{}, err
	}
	var r spec.CliReply
	if uerr := json.Unmarshal(resJSON, &r); uerr != nil {
		return spec.CliReply{}, uerr
	}
	if r.Error != "" {
		return r, fmt.Errorf("charly %s: %s", strings.Join(argv, " "), r.Error)
	}
	return r, nil
}

// vmPrepareVenue runs the FULL host-side VM preflight itself (ssh-config stanza, auto-boot, guest
// readiness waits, charly delivery) over generic seams, and returns the guest SSH venue descriptor +
// the VmDeployState patch the host persists. Consumes only the host-resolved LifecyclePrepareInput.
func vmPrepareVenue(ctx context.Context, exec *sdk.Executor, p lifecycleParams, host spec.HostEnv) (*pb.InvokeReply, error) {
	var in spec.LifecyclePrepareInput
	if err := json.Unmarshal(p.Prepare, &in); err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm prepare-venue: decode prepare input: %w", err)
	}
	if in.VM == nil {
		return nil, fmt.Errorf("plugin-deploy-vm prepare-venue: no resolved VmSpec in prepare input")
	}
	var opts spec.LifecycleOpts
	_ = json.Unmarshal(p.Opts, &opts)

	// (a) publish the managed ssh-config Host stanza + the Include line (host file I/O the co-located
	// plugin does directly), so `ssh <alias>` resolves before any wait.
	if err := kit.WriteVmSshStanza(host.Home, kit.VmSshStanza{
		Alias:          in.Alias,
		Hostname:       "127.0.0.1",
		Port:           in.SSHPort,
		User:           in.SSHUser,
		IdentityFile:   in.SSHKeyPath,
		KnownHostsFile: in.KnownHostsPath,
	}); err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm prepare-venue: publish ssh-config stanza: %w", err)
	}
	if err := kit.EnsureSshConfigInclude(host.Home); err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm prepare-venue: ensure ssh-config include: %w", err)
	}

	// (b) auto-boot: TCP-probe the SSH port; if unreachable, `charly vm build` + `charly vm create`
	// via the cli seam. Skipped on DryRun / when CHARLY_DEPLOY_NO_AUTOBOOT is set.
	if !opts.DryRun && os.Getenv("CHARLY_DEPLOY_NO_AUTOBOOT") == "" {
		addr := fmt.Sprintf("127.0.0.1:%d", in.SSHPort)
		if conn, derr := net.DialTimeout("tcp", addr, 2*time.Second); derr != nil {
			fmt.Fprintf(os.Stderr, "VM %q not reachable on %s — auto-booting via charly vm build/create...\n", in.Entity, addr)
			// build the shared ENTITY base disk; create this DEPLOY's own domain (--domain) — a
			// per-domain overlay + state so sibling beds sharing the entity never collide.
			if _, err := vmCli(ctx, exec, false, false, "vm", "build", in.Entity); err != nil {
				return nil, fmt.Errorf("auto-boot build %s: %w", in.Entity, err)
			}
			if _, err := vmCli(ctx, exec, false, false, "vm", "create", in.Entity, "--domain", domainIdentity(p)); err != nil {
				return nil, fmt.Errorf("auto-boot create %s: %w", in.Entity, err)
			}
		} else {
			_ = conn.Close()
		}
	}

	// (c) guest-readiness waits over the host ssh surface (BEFORE the reverse channel serves a guest
	// executor — WaitForSSH must poll a possibly-not-up sshd). The managed alias supplies user/port/key.
	ssh := kit.SSHArgs{Host: in.Alias, ConnectTimeout: 10}
	// Inject the readiness-configured poll into kit's WaitFor* (kit is stdlib-only and cannot own the
	// readiness/poll subsystem; the plugin legitimately imports vmshared, so it wraps pollUntil + the
	// resolved remote bounds). vmshared.ResolveReadiness(nil) reads the host-threaded CHARLY_READINESS_* env.
	rr, _ := vmshared.ResolveReadiness(nil)
	poll := func(label string) kit.PollFunc {
		return func(pctx context.Context, cond kit.PollCond) error {
			return vmshared.PollUntil(pctx, rr.WaitCapped(label, vmshared.PollRemote, 0), vmshared.PollCondition(cond))
		}
	}
	var notes []string
	if !opts.DryRun {
		fmt.Fprintf(os.Stderr, "Waiting for sshd on %s...\n", in.Alias)
		if err := kit.WaitForSSH(ctx, ssh, poll("ssh-ready")); err != nil {
			return nil, fmt.Errorf("plugin-deploy-vm prepare-venue: wait-for-sshd: %w", err)
		}
		if in.VM.Source.Kind == "cloud_image" || in.VM.CloudInit != nil {
			if err := kit.WaitForCloudInit(ctx, ssh, poll("cloud-init")); err != nil {
				return nil, fmt.Errorf("plugin-deploy-vm prepare-venue: wait-for-cloud-init: %w", err)
			}
			if err := kit.WaitForPackageLock(ctx, ssh, poll("pkg-lock")); err != nil {
				return nil, fmt.Errorf("plugin-deploy-vm prepare-venue: wait-for-package-lock: %w", err)
			}
		}

		// (d) ensure charly is in the guest (host-surface scp against the alias).
		msg, err := kit.EnsureCharlyInGuest(ctx, ssh, host.CharlyBin, host.Version, charlyInstallStrategy(in.VM))
		if err != nil {
			return nil, fmt.Errorf("plugin-deploy-vm prepare-venue: ensure charly in guest: %w", err)
		}
		notes = append(notes, msg)
	}

	// (e) the VmDeployState patch (the host persists it via saveDeployState — the plugin can't touch
	// charly.yml). Carry the prior instance-id/disk/seed forward (re-render stability).
	smbios, cloudInit := vmshared.ResolveKeyInjectionChannels(in.VM)
	state := spec.VmDeployState{
		SshUser:               in.SSHUser,
		SshPort:               in.SSHPort,
		Backend:               in.VM.Backend,
		KeyInjectionResolved:  &spec.VmKeyInjectionResolved{SMBIOS: smbios, CloudInit: cloudInit},
		CharlyInstallStrategy: string(kit.ResolveCharlyInstallStrategy(charlyInstallStrategy(in.VM))),
	}
	if in.PriorState != nil {
		state.InstanceID = in.PriorState.InstanceID
		state.DiskPath = in.PriorState.DiskPath
		state.SeedIso = in.PriorState.SeedIso
	}
	stateJSON, _ := json.Marshal(map[string]any{"Target": "vm", "VmState": &state, "VmCrossRef": in.Entity})

	return marshalReply(spec.PrepareVenueReply{
		Venue: spec.VenueDescriptor{Kind: "ssh", Host: in.Alias, ConnectTimeout: 10},
		State: stateJSON,
		Notes: notes,
	})
}

// charlyInstallStrategy extracts spec.cloud_init.charly_install.strategy ("" → auto).
func charlyInstallStrategy(vm *spec.ResolvedVm) string {
	if vm != nil && vm.CloudInit != nil && vm.CloudInit.CharlyInstall != nil {
		return vm.CloudInit.CharlyInstall.Strategy
	}
	return ""
}

// vmPostApply deploys each nested target:pod child as a PERSISTENT in-guest quadlet (the three-seam
// interleave: host `box build` + `vm cp-box` via the cli seam; guest `from-box` over the LIVE guest
// executor). exec is the guest executor the proxy serves for PostApply.
func vmPostApply(ctx context.Context, exec *sdk.Executor, p lifecycleParams, host spec.HostEnv) (*pb.InvokeReply, error) {
	var node spec.BundleNode
	_ = json.Unmarshal(p.Node, &node)
	if len(node.Children) == 0 {
		return marshalReply(struct{}{})
	}
	// `vm cp-box` reaches the guest over the managed ssh alias (charly-<domain>), so it addresses the
	// running VM by its DOMAIN IDENTITY, not the entity.
	domain := domainIdentity(p)

	// Deliver the HOST's own charly to a /tmp path OUTSIDE $PATH (the from-box authority — never the
	// guest's possibly-stale PATH charly), invoked by explicit path. One delivery for every child.
	charlyCmd := "/tmp/charly-" + host.Version
	content, err := os.ReadFile(host.CharlyBin)
	if err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm post-apply: read host charly %s: %w", host.CharlyBin, err)
	}
	if err := exec.PutFile(ctx, charlyCmd, content, 0o755, false); err != nil {
		return nil, fmt.Errorf("plugin-deploy-vm post-apply: deliver host charly into guest: %w", err)
	}

	for _, childKey := range sortedChildKeys(node.Children) {
		child := node.Children[childKey]
		if child == nil || child.Image == "" {
			continue
		}
		switch child.Target {
		case "", "pod", "container":
		default:
			continue // android / k8s / vm children are not in-guest pods
		}
		asRef := "localhost/charly-" + childKey + ":latest"
		fmt.Fprintf(os.Stderr, "Deploying nested pod %s.%s (%s) as a persistent in-guest quadlet...\n", domain, childKey, child.Image)
		if _, err := vmCli(ctx, exec, false, false, "box", "build", child.Image); err != nil {
			return nil, fmt.Errorf("build nested image %s (%s): %w", childKey, child.Image, err)
		}
		if _, err := vmCli(ctx, exec, false, false, "vm", "cp-box", domain, child.Image, "--as", asRef, "--rootless"); err != nil {
			return nil, fmt.Errorf("cp-box nested %s -> guest: %w", childKey, err)
		}
		script := fmt.Sprintf(
			"sudo loginctl enable-linger \"$(id -un)\" >/dev/null 2>&1 || true\n"+
				"export XDG_RUNTIME_DIR=\"/run/user/$(id -u)\"\n"+
				"%s bundle from-box %s %s",
			charlyCmd, asRef, childKey)
		if err := exec.RunUser(ctx, script, nil); err != nil {
			return nil, fmt.Errorf("deploy nested pod %s in guest: %w", childKey, err)
		}
		fmt.Fprintf(os.Stderr, "Nested pod %s.%s deployed (persistent in-guest quadlet)\n", domain, childKey)
	}
	return marshalReply(struct{}{})
}

// sortedChildKeys returns the nested-child keys in stable order.
func sortedChildKeys(children map[string]*spec.Deploy) []string {
	keys := make([]string, 0, len(children))
	for k := range children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// vmStatus reads `charly vm list` and walks for this VM's domain row (want = "charly-<domain>", the
// per-deploy domain identity — NOT the shared entity).
func vmStatus(ctx context.Context, exec *sdk.Executor, domain string) (*pb.InvokeReply, error) {
	r, err := vmCli(ctx, exec, true, true, "vm", "list")
	if err != nil {
		return marshalReply(map[string]any{"State": "unknown"})
	}
	want := "charly-" + domain
	for _, line := range strings.Split(r.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != want {
			continue
		}
		state := fields[len(fields)-1]
		return marshalReply(map[string]any{
			"State":   state,
			"Healthy": state == "running",
			"Details": map[string]string{"backend": fields[1], "domain": fields[0]},
		})
	}
	return marshalReply(map[string]any{"State": "stopped", "Healthy": false})
}

// vmRebuild destroys + (optionally) rebuilds + recreates + starts the VM, THEN re-applies the deploy's
// candies (+ nested pods) via `charly bundle add <name>` — the path `charly update <vm-bed>` routes
// through (the disposable bed's fresh-rebuild R10 gate). Each leg is a cli-seam `charly` subcommand.
func vmRebuild(ctx context.Context, exec *sdk.Executor, p lifecycleParams) (*pb.InvokeReply, error) {
	var ropts struct {
		DryRun       bool `json:"DryRun"`
		RebuildImage bool `json:"RebuildImage"`
	}
	_ = json.Unmarshal(p.Opts, &ropts)
	entity := vmEntity(p)       // disk/spec SOURCE (the `vm build` arg + the create's entity positional)
	domain := domainIdentity(p) // per-deploy DOMAIN IDENTITY (destroy/create/start THIS domain)
	if ropts.DryRun {
		return marshalReply(struct{}{})
	}
	_, _ = vmCli(ctx, exec, false, true, "vm", "destroy", entity, "--domain", domain, "--if-exists")
	if ropts.RebuildImage {
		if _, err := vmCli(ctx, exec, false, false, "vm", "build", entity); err != nil {
			return nil, err
		}
	}
	if _, err := vmCli(ctx, exec, false, false, "vm", "create", entity, "--domain", domain); err != nil {
		return nil, err
	}
	// `vm create` already starts the domain; this is the ensure-running guard for a
	// backend that left it defined-but-off. `vm start` is idempotent (an already-running
	// domain is a clean success), so its error is real and must not be discarded.
	if _, err := vmCli(ctx, exec, false, false, "vm", "start", entity, "--domain", domain); err != nil {
		return nil, err
	}
	if _, err := vmCli(ctx, exec, false, false, "bundle", "add", p.Name); err != nil {
		return nil, err
	}
	return marshalReply(struct{}{})
}

// vmPostTeardown removes the managed ssh-config stanza (host file I/O the co-located plugin does),
// stripping the Include line when it was the last managed alias, and ships the charly.yml deploy-entry
// keys for the host to remove (the plugin cannot touch charly.yml; the ephemeral teardown stays a host
// hook). Everything keys off the per-deploy DOMAIN IDENTITY so a teardown removes ONLY this deploy's
// artifacts — never a sibling bed's (the collision this cutover eliminates).
func vmPostTeardown(ctx context.Context, exec *sdk.Executor, p lifecycleParams, host spec.HostEnv) (*pb.InvokeReply, error) {
	domain := domainIdentity(p)

	// Destroy the libvirt/qemu DOMAIN — `bundle del`'s ONLY domain-teardown owner. The Del path
	// replays the in-guest ReverseOps and removes host config, but nothing else tore down the venue,
	// so a non-ephemeral vm deploy leaked a running domain (#69b). Keyed by the per-deploy DOMAIN
	// IDENTITY (--domain) so it removes ONLY this deploy's domain, never a sibling bed's; --keep-deploy
	// leaves the charly.yml entry cleanup to the RemoveEntries below (single owner). Best-effort: a
	// deploy whose domain is already stopped/gone must not fail the whole teardown (`vm destroy`
	// hard-errors on an absent domain, and bestEffort swallows it).
	_, _ = vmCli(ctx, exec, false, true, "vm", "destroy", vmEntity(p), "--domain", domain, "--keep-deploy", "--if-exists")

	if remaining, err := kit.RemoveVmSshStanza(host.Home, kit.VmSshAlias(domain)); err != nil {
		fmt.Fprintf(os.Stderr, "note: ssh-config stanza cleanup: %v\n", err)
	} else if remaining == 0 {
		if err := kit.RemoveSshConfigInclude(host.Home); err != nil {
			fmt.Fprintf(os.Stderr, "note: ssh-config include cleanup: %v\n", err)
		}
	}

	// Two entries carry this deploy's state, both keyed by the deploy (never the shared entity):
	//   - the deploy-state entry the proxy persisted under the deploy name (p.Name), and
	//   - the port/instance-id entry vm:<domain> runVmSpecCreate persisted.
	// Removing them by domain (not vm:<entity>) avoids removeVmDeployEntry's From-scan over-matching
	// sibling beds that share the entity.
	entries := []string{p.Name}
	if portKey := "vm:" + domain; portKey != p.Name {
		entries = append(entries, portKey)
	}
	return marshalReply(spec.PostTeardownReply{RemoveEntries: entries})
}

// cliOK returns an empty-struct reply, propagating a cli error.
func cliOK(_ spec.CliReply, err error) (*pb.InvokeReply, error) {
	if err != nil {
		return nil, err
	}
	return marshalReply(struct{}{})
}

// marshalReply marshals v into a *pb.InvokeReply.ResultJson.
func marshalReply(v any) (*pb.InvokeReply, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: b}, nil
}
