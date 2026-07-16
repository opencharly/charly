package substratekind

// status_android.go — the ANDROID substrate's OpStatus (K5: relocated from
// charly/status_collect_adb.go). Enumerates every declared `target: android`
// deploy node from the MERGED project + per-machine deploy tree and derives
// each device's status host-side (no goadb — mirrors the original design).
//
// Every input this needs turned out to be reachable WITHOUT a new seam:
//   - the PROJECT deploy tree via HostBuild("resolved-project") — the SAME
//     seam status_k8s.go uses (proven live by the K5 lane-A landing).
//   - the PER-MACHINE overlay (~/.config/charly/charly.yml) via
//     deploykit.LoadBundleConfig() DIRECTLY — no host round-trip at all, so
//     "per-machine state" does NOT block this move (contra the original
//     K5-gated framing on this file's charly-core predecessor).
//   - its own android substrate-template resolve (resolve.go, in-package,
//     no cross-plugin hop — the SAME pattern k8s already uses).
//   - the credential store (apkeep google-play creds) via
//     InvokeProvider("verb","credential",...) — the SAME peer-plugin-dispatch
//     pattern status_vm.go already uses for verb:libvirt.
//   - container/engine inspection via sdk/kit (EngineBinary,
//     ContainerNameInstance, InspectContainer, ResolveRuntime — all already
//     sdk-direct aliases on the charly-core predecessor) plus a small local
//     containerRunning/resolveContainer port (plain podman/docker exec, no
//     sdk blocker at all) and a minimal remote-box-name resolver (a few
//     lines of pure string parsing, ported rather than imported since the
//     full @github ref resolver lives in the still-core loader/build cone).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// androidDeployNode is one declared target:android deploy node together with
// its dotted deploy path (the path resolveAndroidDeviceHandle needs to locate
// the in-pod PARENT container for a nested device).
type androidDeployNode struct {
	path string
	node *spec.Deploy
}

// androidDevice is the resolved install handle for one android device (K5: a
// plugin-local copy of charly/android_deploy_cmd.go's AndroidDevice — not an
// alias, since charly/ types aren't importable from a plugin module).
type androidDevice struct {
	Engine      string
	Container   string
	AdbAddr     string
	Serial      string
	GoogleEmail string
	GoogleToken string
}

// collectAndroidStatus serves the android substrate's OpStatusCollect.
func collectAndroidStatus(ctx context.Context, req spec.SubstrateStatusRequest) (spec.SubstrateStatusReply, error) {
	rp, err := fetchResolvedProject(ctx)
	if err != nil {
		return spec.SubstrateStatusReply{}, fmt.Errorf("android status-collect: %w", err)
	}
	// Best-effort: absence of a per-machine overlay is normal (mirrors
	// NewCollector's own graceful handling of a missing/invalid charly.yml).
	perMachine, _ := deploykit.LoadBundleConfig()

	nodes := collectAndroidDeployNodes(rp, perMachine)
	if len(nodes) == 0 {
		return spec.SubstrateStatusReply{}, nil
	}
	rows := make([]spec.DeploymentStatus, 0, len(nodes))
	for _, n := range nodes {
		rows = append(rows, collectAndroidOne(ctx, rp, n, req.RunMode))
	}
	return spec.SubstrateStatusReply{Rows: rows}, nil
}

// collectAndroidDeployNodes is the SINGLE enumeration of every target:android
// deploy node, merging the resolved-project's deploy tree with the
// per-machine overlay (local wins per key, mirroring resolveTreeRoot's
// MergeDeployConfigs precedence), then pre-order walking every root so nested
// devices are discovered with their full dotted path.
func collectAndroidDeployNodes(rp *spec.ResolvedProject, perMachine *deploykit.BundleConfig) []androidDeployNode {
	projectBundle := make(map[string]deploykit.BundleNode, len(rp.Deploy))
	for name, node := range rp.Deploy {
		if node != nil {
			projectBundle[name] = deploykit.BundleNode(*node)
		}
	}
	merged := deploykit.MergeDeployConfigs(&deploykit.BundleConfig{Bundle: projectBundle}, perMachine)
	if merged == nil || merged.Bundle == nil {
		return nil
	}

	names := make([]string, 0, len(merged.Bundle))
	for name := range merged.Bundle {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []androidDeployNode
	for _, name := range names {
		root := merged.Bundle[name]
		_ = deploykit.BundleWalkPreOrder(&root, name, func(path string, node *deploykit.BundleNode) error {
			if node != nil && node.Target == "android" {
				n := spec.Deploy(*node)
				out = append(out, androidDeployNode{path: path, node: &n})
			}
			return nil
		})
	}
	return out
}

// collectAndroidOne builds the status row for one declared android device
// node. It resolves the kind:android spec + the device handle, then derives
// status host-side (containerRunning for an in-pod device, "declared" for an
// endpoint — no goadb). Resolution failures degrade to an "absent" row —
// never an error that would drop the whole substrate.
func collectAndroidOne(ctx context.Context, rp *spec.ResolvedProject, dn androidDeployNode, runMode string) spec.DeploymentStatus {
	row := spec.DeploymentStatus{
		Kind:    spec.SubstrateAndroid,
		Source:  "adb",
		Image:   dn.path,
		Status:  "absent",
		RunMode: runMode,
	}

	aspec := androidSpecFor(rp, dn.node.From)
	if aspec == nil {
		row.Container = dn.node.From
		return row
	}
	row.Container = aspec.EffectiveSerial()
	if aspec.IsEndpoint() {
		row.Network = "endpoint " + aspec.Adb.Host
	} else if aspec.Box != "" {
		row.Network = "in-pod " + aspec.Box
	}

	dev, err := resolveAndroidDevice(ctx, aspec, dn.node, dn.path)
	if err != nil {
		return row
	}

	if dev.Engine != "" && dev.Container != "" {
		row.Network = "in-pod (" + dev.Container + ")"
		if containerRunning(dev.Engine, dev.Container) {
			row.Status = "running"
		}
	} else {
		row.Status = "declared"
	}
	return row
}

// androidSpecFor resolves the kind:android template referenced by `from`
// against the resolved-project's android template bodies, via this SAME
// provider's own template-resolve leg (resolve.go) — an in-package call,
// never a cross-plugin Invoke.
func androidSpecFor(rp *spec.ResolvedProject, from string) *spec.ResolvedAndroid {
	if rp.Templates == nil || from == "" {
		return nil
	}
	body, ok := rp.Templates.Android[from]
	if !ok {
		return nil
	}
	out, err := resolveSubstrateTemplate(spec.SubstrateTemplateResolveRequest{Android: &spec.AndroidResolveInput{Android: body}})
	if err != nil {
		return nil
	}
	var reply spec.AndroidResolveReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return nil
	}
	return reply.Resolved
}

// resolveAndroidDevice builds the androidDevice install handle from the spec
// and deploy context. Endpoint devices target a remote adb server; image
// devices target an in-pod emulator. For a nested deploy (dotted path), the
// in-pod container is the PARENT pod; for a top-level deploy it resolves by
// image name. Faithful port of charly/android_deploy_cmd.go's
// resolveAndroidDevice.
func resolveAndroidDevice(ctx context.Context, aspec *spec.ResolvedAndroid, node *spec.Deploy, path string) (androidDevice, error) {
	serial := aspec.EffectiveSerial()

	if aspec.IsEndpoint() {
		email, token := resolveAndroidGoogleCreds(ctx, aspec.GoogleAccount)
		addr, err := resolveAndroidHostPortRef(aspec.Adb.Host, path, node)
		if err != nil {
			return androidDevice{}, err
		}
		return androidDevice{AdbAddr: addr, Serial: serial, GoogleEmail: email, GoogleToken: token}, nil
	}

	if aspec.Box == "" {
		return androidDevice{}, fmt.Errorf("kind:android device has neither box: nor adb: declared")
	}

	engine := "podman"
	if node != nil && node.Engine == "docker" {
		engine = "docker"
	}
	var container string
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		parent := path[:i]
		container = "charly-" + kit.NestedContainerName(parent)
		engine = kit.EngineBinary(engine)
		if !containerRunning(engine, container) {
			return androidDevice{}, fmt.Errorf("parent pod container %s is not running", container)
		}
	} else {
		eng, name, err := resolveContainer(aspec.Box)
		if err != nil {
			return androidDevice{}, err
		}
		engine, container = eng, name
	}

	addr, err := adbAddrForContainer(engine, container)
	if err != nil {
		return androidDevice{}, err
	}
	return androidDevice{Engine: engine, Container: container, AdbAddr: addr, Serial: serial}, nil
}

// resolveAndroidHostPortRef substitutes a single ${HOST_PORT:N} token in a
// nested endpoint device's adb host with the PARENT pod's host-mapped port
// for container port N. Returns addr unchanged when it carries no
// ${HOST_PORT:N} reference (a literal host:port endpoint).
func resolveAndroidHostPortRef(addr, path string, node *spec.Deploy) (string, error) {
	const marker = "${HOST_PORT:"
	before, after, ok := strings.Cut(addr, marker)
	if !ok {
		return addr, nil
	}
	before0, after0, ok0 := strings.Cut(after, "}")
	if !ok0 {
		return "", fmt.Errorf("adb host %q: malformed ${HOST_PORT:N} (no closing brace)", addr)
	}
	var ctrPort int
	if _, err := fmt.Sscanf(before0, "%d", &ctrPort); err != nil || ctrPort <= 0 {
		return "", fmt.Errorf("adb host %q: ${HOST_PORT:N} requires a positive container port", addr)
	}
	i := strings.LastIndexByte(path, '.')
	if i < 0 {
		return "", fmt.Errorf("adb host %q uses ${HOST_PORT:%d} but the device is not nested under a pod", addr, ctrPort)
	}
	engine := "podman"
	if node != nil && node.Engine == "docker" {
		engine = "docker"
	}
	engine = kit.EngineBinary(engine)
	container := "charly-" + kit.NestedContainerName(path[:i])
	if !containerRunning(engine, container) {
		return "", fmt.Errorf("parent pod container %s is not running", container)
	}
	insp, err := kit.InspectContainer(engine, container)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", container, err)
	}
	hp, err := findHostPort(insp, ctrPort)
	if err != nil {
		return "", err
	}
	return before + fmt.Sprintf("%d", hp) + after0, nil
}

// adbAddrForContainer resolves a container's published adb server port
// (5037) to a host-reachable "127.0.0.1:<port>" address.
func adbAddrForContainer(engine, containerName string) (string, error) {
	const adbServerPort = 5037
	insp, err := kit.InspectContainer(engine, containerName)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", containerName, err)
	}
	if insp == nil {
		return "", fmt.Errorf("inspect %s: nil result", containerName)
	}
	port, err := findHostPort(insp, adbServerPort)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("127.0.0.1:%d", port), nil
}

// findHostPort returns the first host-side port number bound to the given
// container port. Looks for both "N" and "N/tcp" keys because podman inspect
// emits the protocol-suffixed form. Pure engine-inspect arithmetic, ported
// verbatim from charly/android_deploy_cmd.go.
func findHostPort(insp *kit.ContainerInspection, containerPort int) (int, error) {
	if insp.IsHostNetworked() {
		return containerPort, nil
	}
	keys := []string{fmt.Sprintf("%d/tcp", containerPort), fmt.Sprintf("%d", containerPort)}
	for _, k := range keys {
		binds, ok := insp.NetworkSettings.Ports[k]
		if !ok || len(binds) == 0 {
			continue
		}
		var port int
		if _, err := fmt.Sscanf(binds[0].HostPort, "%d", &port); err == nil && port > 0 {
			return port, nil
		}
	}
	return 0, fmt.Errorf("container port %d not published on host", containerPort)
}

// resolveContainer resolves the running container for a TOP-LEVEL (non-nested)
// android box device, verifying it is running. Ported from
// charly/container.go's resolveContainer, simplified to the instance=""
// call shape resolveAndroidDevice always uses.
func resolveContainer(box string) (engine, name string, err error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return "", "", err
	}
	boxName := remoteRefName(box)
	runEngine := deployEngineForBox(boxName, rt.RunEngine)
	engine = kit.EngineBinary(runEngine)
	name = kit.ContainerNameInstance(boxName, "")
	if !containerRunning(engine, name) {
		return "", "", fmt.Errorf("container %s is not running", name)
	}
	return engine, name, nil
}

// deployEngineForBox mirrors charly/engine.go's ResolveBoxEngineForDeploy —
// the per-machine deploy config's own engine override wins, falling back to
// the global runtime engine.
func deployEngineForBox(boxName, globalEngine string) string {
	if entry, ok := deploykit.LoadDeployConfigForRead("resolveContainer").Lookup(boxName, ""); ok && entry.Engine != "" {
		return entry.Engine
	}
	return globalEngine
}

// remoteRefName mirrors charly/commands.go's resolveBoxName + the minimal
// slice of charly/refs.go's remote-ref parsing it needs: a "@host/org/repo/
// sub/path:version" ref resolves to its LAST path segment; a plain name
// passes through unchanged. The full @github fetch/build resolver stays
// core (K1/K3 loader cone) — this is pure string parsing, not fetch/build.
func remoteRefName(box string) string {
	ref := strings.TrimPrefix(strings.TrimPrefix(box, "https://"), "http://")
	if !strings.HasPrefix(ref, "@") {
		return box
	}
	ref = strings.TrimPrefix(ref, "@")
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		ref = ref[:idx]
	}
	if idx := strings.LastIndexByte(ref, '/'); idx != -1 {
		return ref[idx+1:]
	}
	return ref
}

// containerRunning checks if a container with the given name is currently
// running. Ported from charly/shell.go's defaultContainerRunning (plain
// podman/docker inspect — no sdk blocker at all). A package-level swappable
// var (mirrors status_vm.go's libvirtSessionAvailable/listLibvirtCharlyDomains)
// so a test can stub it and never touch the real podman/docker socket.
var containerRunning = defaultContainerRunning

func defaultContainerRunning(engine, name string) bool {
	cmd := exec.Command(engine, "container", "inspect", "--format", "{{.State.Running}}", name)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// resolveAndroidGoogleCreds reads the apkeep google-play credentials from the
// credential store (candy/plugin-secrets) via InvokeProvider("verb",
// "credential",...) — the SAME peer-plugin-dispatch pattern status_vm.go
// already uses for verb:libvirt. Empty when unset or unreachable (the
// google-play path errors clearly downstream if it needs them).
func resolveAndroidGoogleCreds(ctx context.Context, ga *spec.GoogleAccount) (email, token string) {
	emailKey, tokenKey := "GOOGLE_ACCOUNT_EMAIL", "GOOGLE_AAS_TOKEN"
	if ga != nil {
		if ga.EmailSecret != "" {
			emailKey = ga.EmailSecret
		}
		if ga.TokenSecret != "" {
			tokenKey = ga.TokenSecret
		}
	}
	email, _ = credentialGet(ctx, "charly/secret", emailKey)
	token, _ = credentialGet(ctx, "charly/secret", tokenKey)
	return email, token
}

// credentialInput/credentialReply mirror charly/credential_plugin.go's wire
// shape for verb:credential's OpRun (a plugin-local copy of the JSON tags,
// not an alias — charly/ types aren't importable from a plugin module).
type credentialInput struct {
	Method  string `json:"method"`
	Service string `json:"service,omitempty"`
	Key     string `json:"key,omitempty"`
}

type credentialReply struct {
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// credentialGet reaches candy/plugin-secrets' verb:credential over the host's
// InvokeProvider reverse leg (mirrors listLibvirtCharlyDomains's InvokeProvider
// pattern in status_vm.go). A missing/unreachable plugin degrades to an empty
// value, never an error that would drop the whole device row.
func credentialGet(ctx context.Context, service, key string) (string, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, 0)
	if err != nil {
		return "", err
	}
	reqJSON, err := json.Marshal(credentialInput{Method: "get", Service: service, Key: key})
	if err != nil {
		return "", err
	}
	out, err := exec.InvokeProvider(ctx, "verb", "credential", sdk.OpRun, reqJSON, nil)
	if err != nil {
		return "", err
	}
	var reply credentialReply
	if len(out) > 0 {
		if err := json.Unmarshal(out, &reply); err != nil {
			return "", err
		}
	}
	if reply.Error != "" {
		return "", errors.New(reply.Error)
	}
	return reply.Value, nil
}
