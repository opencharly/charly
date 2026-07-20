package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// CheckEndpoint is a host-reachable TCP address for a port that lives inside the
// venue. For a container it's the published port mapping; for a VM / ssh-host
// it's a `ssh -L` local-forward (closed via Close); for the local host it's the
// port directly. The port-protocol verbs (cdp / vnc / mcp) use it so the same
// "reach service port N" works on every venue (R3).
type CheckEndpoint struct {
	Addr    string // "127.0.0.1:NNNN"
	cleanup func()
}

// Close tears down any underlying ssh -L forward. Safe to call on a nil/no-op
// endpoint.
func (e *CheckEndpoint) Close() {
	if e != nil && e.cleanup != nil {
		e.cleanup()
	}
}

// resolveCheckEndpoint returns a host-reachable address for the given in-venue
// TCP port. The caller MUST Close() the returned endpoint when done (no-op for
// container/local venues; tears down the ssh forward for VM/ssh-host).
func resolveCheckEndpoint(venue *CheckVenue, port int) (*CheckEndpoint, error) {
	switch venue.Kind {
	case "container":
		addr, err := containerPublishedAddr(venue.Engine, venue.Name, port)
		if err != nil {
			return nil, err
		}
		return &CheckEndpoint{Addr: addr}, nil
	case "host":
		// Local host → the port directly; ssh-host → an ssh -L forward.
		if se, ok := venue.Exec.(*kit.SSHExecutor); ok {
			return sshForwardEndpoint(se, port)
		}
		return &CheckEndpoint{Addr: fmt.Sprintf("127.0.0.1:%d", port)}, nil
	case "vm":
		return sshForwardEndpoint(&kit.SSHExecutor{Host: kit.VmSshAlias(venue.VMName), ConnectTimeout: 10}, port)
	}
	return nil, fmt.Errorf("cannot resolve a port endpoint for venue kind %q", venue.Kind)
}

// containerPublishedAddr returns the host "ip:port" that maps to <port> inside a
// running container via `<engine> port`, normalizing 0.0.0.0 / [::] to
// 127.0.0.1. Host-networked containers (no mappings) fall back to 127.0.0.1:port.
// Shared by cdp / vnc / mcp (replaces their per-verb copies — R3).
func containerPublishedAddr(engine, containerName string, port int) (string, error) {
	out, err := exec.Command(engine, "port", containerName, strconv.Itoa(port)).Output()
	if err != nil {
		if isHostNetworked(engine, containerName) {
			return fmt.Sprintf("127.0.0.1:%d", port), nil
		}
		return "", fmt.Errorf("no port mapping found for %d in %s", port, containerName)
	}
	return kit.ParsePublishedPort(string(out), port)
}

// sshForwardEndpoint opens a `ssh -NT -L 127.0.0.1:<rand>:127.0.0.1:<port>`
// forward into the SSH target using the same credential-free system-ssh path as
// SSHExecutor (ssh-config / managed alias supply the user/port/key). A bounded
// readiness probe waits for the local listener — a readiness probe, not a blind
// sleep (R4).
func sshForwardEndpoint(e *kit.SSHExecutor, port int) (*CheckEndpoint, error) {
	// Reserve a free local port, then release it for ssh to bind.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("reserving local port: %w", err)
	}
	localPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)

	timeout := e.ConnectTimeout
	if timeout <= 0 {
		timeout = 10
	}
	args := []string{
		"-N", "-T",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "LogLevel=ERROR",
		"-o", fmt.Sprintf("ConnectTimeout=%d", timeout),
		"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", localPort, port),
	}
	if e.Port > 0 {
		args = append(args, "-p", strconv.Itoa(e.Port))
	}
	args = append(args, e.Args...)
	dest := e.Host
	if e.User != "" {
		dest = e.User + "@" + e.Host
	}
	args = append(args, dest)

	cmd := exec.Command("ssh", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting ssh -L forward to %s: %w", dest, err)
	}
	cleanup := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}

	// Readiness probe (poll.go WaitCapped): the ssh -L listener binds right after
	// authentication. CALLER cap = ConnectTimeout+5s (preserved); the 300ms dial
	// is the per-attempt probe. FATAL fast-fail if ssh has exited (auth/forward
	// failure) — note cmd.ProcessState is only populated after Wait (cleanup), so
	// this remains best-effort, as before.
	cfg := loadedReadiness().WaitCapped(fmt.Sprintf("ssh-forward %s", dest), PollLocal, time.Duration(timeout+5)*time.Second)
	perr := pollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		if c, derr := net.DialTimeout("tcp", localAddr, 300*time.Millisecond); derr == nil {
			_ = c.Close()
			return true, 0, nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return false, 0, ErrPollFatal // ssh died (auth/forward failure)
		}
		return false, 0, nil
	})
	if perr == nil {
		return &CheckEndpoint{Addr: localAddr, cleanup: cleanup}, nil
	}
	cleanup()
	return nil, fmt.Errorf("ssh -L forward to %s:%d did not become ready: %w", dest, port, perr)
}

// venueRunSilent runs a command on the venue discarding output, returning an
// error on non-zero exit (availability probes + fire-and-forget actions).
func venueRunSilent(ex deploykit.DeployExecutor, script string) error {
	_, _, exit, err := ex.RunCapture(context.Background(), script)
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("command exited %d", exit)
	}
	return nil
}

// venueHasTool reports whether `tool` is on PATH on the venue.
func venueHasTool(ex deploykit.DeployExecutor, tool string) bool {
	_, _, exit, err := ex.RunCapture(context.Background(), "command -v "+tool+" >/dev/null 2>&1")
	return err == nil && exit == 0
}

// check_venue.go — the single venue resolver shared by every `charly check` verb.
//
// The declarative runner (`charly check live`) and the former in-core interactive verbs
// (the live-container probes, all now externalized to out-of-process plugins) historically
// diverged: the runner classified the target (container / VM / local / nested) and built a
// DeployExecutor chain, while the interactive verbs hardcoded `resolveContainer()` +
// `podman exec`, so they only ever worked against a running container — a VM target was
// impossible.
//
// resolveCheckVenue is the ONE classifier + executor builder both paths use, so the host's
// executor (attached to an EXEC-based external verb over the reverse channel) and the
// host-side endpoint pre-resolution (a port-based external verb) work identically against a
// container, a VM (over the
// managed ssh-config alias), an ssh/local host, or a dotted nested path — the
// "same underlying mechanism" guarantee. The verbs then run their tool
// invocations through venue.Exec.RunCapture / GetFile / PutFile, exactly like
// the declarative runner already probes every venue through RunCapture.

// CheckVenue carries a resolved execution venue for an `charly check` verb target.
//
// Exec is the DeployExecutor every verb runs its tool commands through.
// Kind mirrors DeployExecutor.Kind() ("container" | "vm" | "host"). Engine and
// Name are set only for the container venue (some verbs still need the raw
// `podman port` / container name for host-side port mapping); they are empty
// for VM / host venues, which reach published ports via an SSH local-forward
// instead.
type CheckVenue struct {
	Exec     deploykit.DeployExecutor
	Kind     string
	Engine   string // container engine ("podman"/"docker"); "" for vm/host
	Name     string // container name (container venue) or vm entity name (vm venue)
	Instance string
	VMName   string // kind:vm entity name when Kind == "vm"; "" otherwise
}

// IsContainer reports whether the venue is a running container — the only
// venue where `podman port` host-side mapping applies.
func (v *CheckVenue) IsContainer() bool { return v != nil && v.Kind == "container" }

// resolveCheckVenue maps an `charly check` verb's <name> argument to an execution
// venue, mirroring the live-check dispatch order (checkLiveGather) so the SAME name
// resolves the SAME way for declarative and interactive verbs:
//
//	"."                         → the local host (ShellExecutor).
//	kind:vm entity / target:vm  → SSHExecutor over the managed charly-<vm> alias
//	  deploy / dotted vm child     (or a ResolveDeployChain chain when dotted).
//	target:local deploy         → ShellExecutor / SSHExecutor per host: field.
//	otherwise                   → a running container (ContainerChain).
//
// A missing/unreadable charly.yml is not fatal — name simply falls through
// to the container path (matching checkVmTarget returning false on a
// load error).
func resolveCheckVenue(name, instance string) (*CheckVenue, error) {
	// "." is the local host — lets a check verb run in-place against the host venue,
	// which is also the in-guest delegation target (host SSHes into the VM
	// and runs `charly check live . …` there with the live session env).
	if name == "." {
		return &CheckVenue{Exec: kit.ShellExecutor{}, Kind: "host"}, nil
	}

	dir, _ := os.Getwd()
	if uf, ok, err := LoadUnified(dir); err == nil && ok && uf != nil {
		if domainID, isVM := checkVmTarget(uf, name); isVM {
			var exec deploykit.DeployExecutor = &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}
			// Dotted nested path (vm.inner-pod): build the full chain so the
			// verb lands inside the leaf's venue, not the parent VM's shell.
			if strings.Contains(name, ".") {
				if roots, _ := resolveTreeRoot(dir); roots != nil {
					if _, chain, chainErr := deploykit.ResolveDeployChain(roots, name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
						exec = chain
					}
				}
			}
			return &CheckVenue{Exec: exec, Kind: "vm", Name: domainID, VMName: domainID, Instance: instance}, nil
		}
		if node, isLocal := checkLocalTarget(uf, name); isLocal {
			exec, err := deploykit.RootExecutorForDeployNode(&node)
			if err != nil {
				return nil, err
			}
			return &CheckVenue{Exec: exec, Kind: "host", Name: name, Instance: instance}, nil
		}
		// Dotted pod-in-pod path (e.g. cache.migrate): build the full nested
		// chain so the verb lands inside the leaf pod, not the root container.
		// (The dotted VM case is handled above; this is the non-VM nesting the
		// position-derived venue produces.) The leaf container is
		// `charly-<flat-path>` (NestedContainerName) for host-side port mapping.
		if strings.Contains(name, ".") {
			if roots, _ := resolveTreeRoot(dir); roots != nil {
				if _, chain, chainErr := deploykit.ResolveDeployChain(roots, name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
					return &CheckVenue{
						Exec:     chain,
						Kind:     "container",
						Engine:   "podman",
						Name:     "charly-" + kit.NestedContainerName(name),
						Instance: instance,
					}, nil
				}
			}
		}
	}

	// Default: a running container.
	engine, containerName, err := resolveContainer(name, instance)
	if err != nil {
		return nil, err
	}
	return &CheckVenue{
		Exec:     deploykit.ContainerChain(engine, containerName),
		Kind:     "container",
		Engine:   engine,
		Name:     containerName,
		Instance: instance,
	}, nil
}

// checkVmTarget reports whether `name` resolves to a VM venue and, if so, the per-deploy DOMAIN
// IDENTITY to SSH into (charly-<domainID> is the live libvirt domain + managed ssh alias). Covers a
// direct kind:vm entity (its own name IS the domain identity), a kind:deployment with target:vm (the
// deploy name, NOT its vm: entity — the domain is named after the deploy, P33), and a dotted path
// whose root segment is a target:vm deployment (the parent deploy owns the domain). Shared by
// checkLiveGather and resolveCheckVenue (R3 — one classifier, no per-call-site re-derivation).
func checkVmTarget(uf *UnifiedFile, name string) (domainID string, ok bool) {
	if uf == nil {
		return "", false
	}
	// Dotted: route through the root segment's VM substrate.
	if idx := strings.Index(name, "."); idx > 0 {
		root := name[:idx]
		if entry, present := uf.Bundle[root]; present && nodeTraits(&entry).Venue == "ssh" { // vm (ssh venue)
			return vmDomainIdentity(root), true
		}
		return "", false
	}
	if uf.VM != nil {
		if _, present := uf.VM[name]; present {
			return vmDomainIdentity(name), true
		}
	}
	if uf.Bundle != nil {
		if entry, present := uf.Bundle[name]; present && nodeTraits(&entry).Venue == "ssh" { // vm (ssh venue)
			return vmDomainIdentity(name), true
		}
	}
	return "", false
}

// checkLocalTarget reports whether `name` (or its dotted root segment) is a
// HOST-VENUE deployment — a `target: local` filesystem apply OR an EXTERNAL
// out-of-process deploy (whose externalDeployTarget runs its deploy-scope probes
// host-side via ShellExecutor, exactly like local) — and returns its node so the
// caller can build the host/ssh executor via rootExecutorForDeployNode. Shared by
// checkLiveGather and resolveCheckVenue (R3): one classifier routes an
// external deploy to the host path for BOTH the declarative `charly check live`
// and the interactive `charly check <verb>`, instead of the pod/container path
// (which would fail at resolveContainer with "container ... is not running").
func checkLocalTarget(uf *UnifiedFile, name string) (spec.BundleNode, bool) {
	if uf == nil || uf.Bundle == nil {
		return spec.BundleNode{}, false
	}
	root := name
	if idx := strings.Index(name, "."); idx > 0 {
		root = name[:idx]
	}
	// A HOST-VENUE substrate runs its deploy-scope check probes on the host — a SHELL venue
	// (local's own filesystem apply; k8s's host-side kubectl), a PARENT venue (android via
	// the parent pod's published adb port), or the NONE / external-in-place venue (an EXTERNAL
	// deploy-class substrate whose externalDeployTarget applies + probes host-side via
	// ShellExecutor, exactly like local). It is identified by the stamped venue TRAIT (P9),
	// never the kind word. The CONTAINER venue (pod) is NOT host-routed — its check venue is the
	// running container (published ports); a cdp/vnc/spice endpoint or a command/file probe
	// against a pod must resolve the container venue (the default path in resolveCheckVenue),
	// never the host venue (which would dial the raw container port on host loopback). The SSH
	// venue (vm) is NOT host-routed either — it is handled earlier by checkVmTarget (the guest
	// SSHExecutor). (Masked while pod beds used fixed ports H:C==9222:9222; surfaced once they
	// moved to auto-allocated host ports.)
	if entry, present := uf.Bundle[root]; present {
		if v := nodeTraits(&entry).Venue; v == "shell" || v == "parent" || v == "none" {
			return entry, true
		}
	}
	return spec.BundleNode{}, false
}
