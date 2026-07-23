package check

// venue.go — K1-unblock W3 Unit A: the venue classifier + executor builder relocated from
// charly/check_venue.go. Every dependency this file had on core-only state (LoadUnified,
// resolveTreeRoot, the core-private providerRegistry via nodeTraits' synthetic-node fallback) is
// replaced by the resolved-project envelope (HostBuild("resolved-project")) already fetched by
// resolvedProject (checkproject.go) — a loader-stamped node ALWAYS carries a non-nil .Descent (via
// the host's stampBundleDescents pass), so the plugin-side nodeTraits below never needs the
// registry-backed synthetic-node fallback the core version carries for un-stamped nodes built
// outside the loader (this package never builds one). Everything else — the poll/readiness/SSH
// forwarding machinery, the container/VM/host classification, the dotted-path tree walk — was
// ALREADY portable via existing sdk/vmshared + sdk/kit primitives; the mechanical rename
// loadedReadiness()→kit.ReadinessProvider() / pollUntil→vmshared.PollUntil /
// ErrPollFatal→vmshared.ErrPollFatal / PollLocal→vmshared.PollLocal / vmDomainIdentity→
// vmshared.VmDomainIdentity is the WHOLE of the "no new mechanism" claim for this file (RDD-
// confirmed by reading sdk/vmshared_aliases.go's own alias table before this move).
//
// This file is a self-contained LIBRARY of venue-resolution building blocks; Unit B (the
// "check-run-execute" HostBuild leaf) is what WIRES the live-check gather orchestration
// (checkLiveGather/checkLivePod/checkLiveVM/checkLiveLocal/checkLiveGroup, still host-resident in
// charly/check_cmd.go pending that mechanism) to call into it. Landing this file alone is
// necessary-but-not-sufficient — see the K1-unblock wave notes; it ships wired together with Unit
// B in the same cutover, never as an independently-provable increment (the CALLERS that construct
// a live check run stay host-resident until Unit B's mechanism exists).

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// CheckEndpoint is a host-reachable TCP address for a port that lives inside the venue. For a
// container it's the published port mapping; for a VM / ssh-host it's a `ssh -L` local-forward
// (closed via Close); for the local host it's the port directly.
type CheckEndpoint struct {
	Addr    string
	cleanup func()
}

// Close tears down any underlying ssh -L forward. Safe to call on a nil/no-op endpoint.
func (e *CheckEndpoint) Close() {
	if e != nil && e.cleanup != nil {
		e.cleanup()
	}
}

// resolveCheckEndpoint returns a host-reachable address for the given in-venue TCP port. The
// caller MUST Close() the returned endpoint when done.
func resolveCheckEndpoint(venue *CheckVenue, port int) (*CheckEndpoint, error) {
	switch venue.Kind {
	case "container":
		addr, err := containerPublishedAddr(venue.Engine, venue.Name, port)
		if err != nil {
			return nil, err
		}
		return &CheckEndpoint{Addr: addr}, nil
	case "host":
		if se, ok := venue.Exec.(*kit.SSHExecutor); ok {
			return sshForwardEndpoint(se, port)
		}
		return &CheckEndpoint{Addr: fmt.Sprintf("127.0.0.1:%d", port)}, nil
	case "vm":
		return sshForwardEndpoint(&kit.SSHExecutor{Host: kit.VmSshAlias(venue.VMName), ConnectTimeout: 10}, port)
	}
	return nil, fmt.Errorf("cannot resolve a port endpoint for venue kind %q", venue.Kind)
}

// containerPublishedAddr returns the host "ip:port" that maps to <port> inside a running
// container via `<engine> port`, normalizing 0.0.0.0 / [::] to 127.0.0.1.
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

// sshForwardEndpoint opens a `ssh -NT -L 127.0.0.1:<rand>:127.0.0.1:<port>` forward into the SSH
// target using the same credential-free system-ssh path as SSHExecutor. Readiness bounds come
// from kit.ReadinessProvider — in-process this resolves the SAME project defaults.readiness the
// host resolved, threaded to this plugin process via CHARLY_READINESS_* env at Connect (see
// charly/readiness_config.go's readinessPluginEnv); no new mechanism, and no host round-trip.
func sshForwardEndpoint(e *kit.SSHExecutor, port int) (*CheckEndpoint, error) {
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

	cfg := kit.ReadinessProvider().WaitCapped(fmt.Sprintf("ssh-forward %s", dest), vmshared.PollLocal, time.Duration(timeout+5)*time.Second)
	perr := vmshared.PollUntil(context.Background(), cfg, func(context.Context) (bool, float64, error) {
		if c, derr := net.DialTimeout("tcp", localAddr, 300*time.Millisecond); derr == nil {
			_ = c.Close()
			return true, 0, nil
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return false, 0, vmshared.ErrPollFatal
		}
		return false, 0, nil
	})
	if perr == nil {
		return &CheckEndpoint{Addr: localAddr, cleanup: cleanup}, nil
	}
	cleanup()
	return nil, fmt.Errorf("ssh -L forward to %s:%d did not become ready: %w", dest, port, perr)
}

// venueRunSilent runs a command on the venue discarding output, returning an error on non-zero
// exit.
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

// isHostNetworked reports whether the container runs with --network=host (no port mappings to
// probe).
func isHostNetworked(engine, containerName string) bool {
	out, err := exec.Command(engine, "inspect", "--format", "{{.HostConfig.NetworkMode}}", containerName).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "host"
}

// CheckVenue carries a resolved execution venue for an `charly check` verb target.
type CheckVenue struct {
	Exec     deploykit.DeployExecutor
	Kind     string
	Engine   string
	Name     string
	Instance string
	VMName   string
}

// IsContainer reports whether the venue is a running container.
func (v *CheckVenue) IsContainer() bool { return v != nil && v.Kind == "container" }

// resolveCheckVenue maps an `charly check` verb's <name> argument to an execution venue, off the
// resolved-project envelope's Deploy tree (rp.Deploy) instead of a direct LoadUnified/
// resolveTreeRoot call — the ONLY change from the core original, which read the SAME merged
// bundle tree via the host loader in-process.
func resolveCheckVenue(ex *sdk.Executor, ctx context.Context, dir, name, instance string) (*CheckVenue, error) {
	if name == "." {
		return &CheckVenue{Exec: kit.ShellExecutor{}, Kind: "host"}, nil
	}

	rp, err := resolvedProject(ex, ctx, dir)
	if err == nil && rp != nil {
		tree := derefDeployTree(rp.Deploy)
		if domainID, isVM := checkVmTarget(tree, name); isVM {
			var vexec deploykit.DeployExecutor = &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}
			if strings.Contains(name, ".") {
				if _, chain, chainErr := deploykit.ResolveDeployChain(tree, name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
					vexec = chain
				}
			}
			return &CheckVenue{Exec: vexec, Kind: "vm", Name: domainID, VMName: domainID, Instance: instance}, nil
		}
		if node, isLocal := checkLocalTarget(tree, name); isLocal {
			vexec, lerr := deploykit.RootExecutorForDeployNode(&node)
			if lerr != nil {
				return nil, lerr
			}
			return &CheckVenue{Exec: vexec, Kind: "host", Name: name, Instance: instance}, nil
		}
		if strings.Contains(name, ".") {
			if _, chain, chainErr := deploykit.ResolveDeployChain(tree, name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
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

	engine, containerName, cerr := deploykit.ResolveContainer(name, instance)
	if cerr != nil {
		return nil, cerr
	}
	return &CheckVenue{
		Exec:     deploykit.ContainerChain(engine, containerName),
		Kind:     "container",
		Engine:   engine,
		Name:     containerName,
		Instance: instance,
	}, nil
}

// derefDeployTree converts the envelope's map[string]*spec.BundleNode into the value-map shape
// the tree-walk helpers below (ported unchanged from charly/check_cmd.go + check_venue.go) share
// with every other resolved-project consumer in this package.
func derefDeployTree(m map[string]*spec.BundleNode) map[string]spec.BundleNode {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]spec.BundleNode, len(m))
	for k, v := range m {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

// nodeTraits returns the node's stamped deploy-descent descriptor. Every node reachable off the
// resolved-project envelope is loader-stamped (stampBundleDescents runs host-side before the
// envelope is filled), so — unlike the core original — this plugin-side version never needs the
// registry-backed synthetic-node fallback: this package never constructs a BundleNode outside the
// envelope.
func nodeTraits(node *spec.BundleNode) *spec.DescentDescriptor {
	if node != nil && node.Descent != nil {
		return node.Descent
	}
	return &spec.DescentDescriptor{}
}

// resolveLeafVenue walks a (possibly dotted) name to its LEAF node and reports the LEAF's own
// venue trait.
func resolveLeafVenue(tree map[string]spec.BundleNode, name string) (node spec.BundleNode, venue string, ok bool) {
	if len(tree) == 0 || !strings.Contains(name, ".") {
		return spec.BundleNode{}, "", false
	}
	n, found := resolveDeployNodeByPath(tree, name)
	if !found || n == nil {
		return spec.BundleNode{}, "", false
	}
	return *n, nodeTraits(n).Venue, true
}

// checkVmTarget reports whether `name` resolves to a VM venue and, if so, the per-deploy domain
// identity to SSH into.
func checkVmTarget(tree map[string]spec.BundleNode, name string) (domainID string, ok bool) {
	if idx := strings.Index(name, "."); idx > 0 {
		if _, venue, ok := resolveLeafVenue(tree, name); ok && venue == "ssh" {
			return vmshared.VmDomainIdentity(name), true
		}
		root := name[:idx]
		if entry, present := tree[root]; present && nodeTraits(&entry).Venue == "ssh" {
			return vmshared.VmDomainIdentity(root), true
		}
		return "", false
	}
	if entry, present := tree[name]; present && nodeTraits(&entry).Venue == "ssh" {
		return vmshared.VmDomainIdentity(name), true
	}
	return "", false
}

// checkLocalTarget reports whether `name` (or its dotted LEAF, or its dotted root segment) is a
// HOST-VENUE deployment, returning its node so the caller can build the host/ssh executor via
// deploykit.RootExecutorForDeployNode.
func checkLocalTarget(tree map[string]spec.BundleNode, name string) (spec.BundleNode, bool) {
	if len(tree) == 0 {
		return spec.BundleNode{}, false
	}
	if leaf, venue, ok := resolveLeafVenue(tree, name); ok {
		if venue == "shell" || venue == "parent" || venue == "none" {
			return leaf, true
		}
	}
	root := name
	if idx := strings.Index(name, "."); idx > 0 {
		root = name[:idx]
	}
	if entry, present := tree[root]; present {
		if v := nodeTraits(&entry).Venue; v == "shell" || v == "parent" || v == "none" {
			return entry, true
		}
	}
	return spec.BundleNode{}, false
}

// resolveDeployNodeByPath resolves a (possibly DOTTED) deploy name to its BundleNode, descending
// node.Children for each dotted segment. Ported unchanged from charly/check_cmd.go (pure, no
// core-only dependency).
func resolveDeployNodeByPath(tree map[string]spec.BundleNode, name string) (*spec.BundleNode, bool) {
	name, _ = vmshared.SplitVmAddress(name)
	parts := strings.Split(name, ".")
	root, ok := tree[parts[0]]
	if !ok {
		return nil, false
	}
	cur := &root
	for _, seg := range parts[1:] {
		child, ok := cur.Children[seg]
		if !ok || child == nil {
			return nil, false
		}
		cur = child
	}
	return cur, true
}
