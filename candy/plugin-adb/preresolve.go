package adb

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// preresolve.go — the `deploy:android` PRERESOLVE leg (F6, FINAL/K5 unit 6a): relocated from
// charly/android_deploy_preresolve.go + android_deploy_cmd.go. Resolves the live device endpoint +
// the apk install specs and returns a spec.AndroidDeployVenue — the SAME payload the host used to
// build directly, now assembled plugin-side. The two genuinely LoadUnified/credential-store-coupled
// touches (resolving the deploy-tree node when it's nil, and resolving the kind:android entity +
// its google-play credentials) reach the host via the GENERIC "deploy-entity-resolve" HostBuild
// seam (host_build_deploy_entity_resolve.go) — everything else (engine-inspect, ${HOST_PORT:N}
// resolution, the committed-APK path walk) is pure/portable and runs here directly.

// androidBootDeadline / androidInstallDeadline / androidInstallInterval — unchanged from the
// pre-move host constants; shipped to the deploy walk as AndroidDeployVenue.BootTimeout /
// InstallDeadline / InstallInterval (no magic numbers cross the wire un-named).
const (
	androidBootDeadline    = 5 * time.Minute
	androidInstallDeadline = 180 * time.Second
	androidInstallInterval = 5 * time.Second
)

// androidPreresolveParams decodes the host's marshalDeployOpParams envelope (name/dir/node/plans —
// the SAME ad-hoc shape every OpPreresolve dispatch already carries, unchanged by this move).
type androidPreresolveParams struct {
	Name  string                   `json:"name"`
	Dir   string                   `json:"dir"`
	Node  *spec.Deploy             `json:"node"`
	Plans []*deploykit.InstallPlan `json:"plans"`
}

// invokeAndroidPreresolve serves Invoke(OpPreresolve) for deploy:android.
func invokeAndroidPreresolve(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("deploy:android preresolve: reach host reverse channel: %w", err)
	}
	var p androidPreresolveParams
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &p); err != nil {
			return nil, fmt.Errorf("deploy:android preresolve: decode params: %w", err)
		}
	}

	node := p.Node
	if node == nil {
		var reply spec.DeployEntityResolveReply
		if err := hostEntityResolve(ctx, exec, spec.DeployEntityResolveRequest{Kind: "deploy", Name: p.Name, Dir: p.Dir}, &reply); err != nil {
			return nil, fmt.Errorf("deploy:android preresolve: resolve deploy %q: %w", p.Name, err)
		}
		node = reply.Node
	}
	if node == nil || node.From == "" {
		return nil, fmt.Errorf("deploy %q: target=android requires `android:` (kind:android device reference)", p.Name)
	}

	var entReply spec.DeployEntityResolveReply
	if err := hostEntityResolve(ctx, exec, spec.DeployEntityResolveRequest{Kind: "android", Name: node.From, Dir: p.Dir}, &entReply); err != nil {
		return nil, fmt.Errorf("deploy %q: resolving kind:android device %q: %w", p.Name, node.From, err)
	}
	var res spec.AndroidEntityResolution
	if err := json.Unmarshal(entReply.EntityJSON, &res); err != nil {
		return nil, fmt.Errorf("deploy %q: decode android entity resolution: %w", p.Name, err)
	}
	var spc spec.ResolvedAndroid
	if err := json.Unmarshal(res.SpecJSON, &spc); err != nil {
		return nil, fmt.Errorf("deploy %q: decode kind:android spec: %w", p.Name, err)
	}

	dev, err := resolveAndroidDevice(&spc, node, p.Name, res.GoogleEmail, res.GoogleToken)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: resolving android device %q: %w", p.Name, node.From, err)
	}

	installs, err := collectAndroidInstalls(p.Plans)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: %w", p.Name, err)
	}

	venue := spec.AndroidDeployVenue{
		AdbAddr:         dev.AdbAddr,
		Engine:          dev.Engine,
		Container:       dev.Container,
		Serial:          dev.serial(),
		GoogleEmail:     dev.GoogleEmail,
		GoogleToken:     dev.GoogleToken,
		Installs:        installs,
		BootTimeout:     androidBootDeadline.String(),
		InstallDeadline: androidInstallDeadline.String(),
		InstallInterval: androidInstallInterval.String(),
	}
	out, err := json.Marshal(venue)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal android venue: %w", p.Name, err)
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}

// hostEntityResolve Invokes the "deploy-entity-resolve" HostBuild seam and decodes the reply.
func hostEntityResolve(ctx context.Context, exec *sdk.Executor, req spec.DeployEntityResolveRequest, out *spec.DeployEntityResolveReply) error {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return err
	}
	resJSON, err := exec.HostBuild(ctx, "deploy-entity-resolve", reqJSON)
	if err != nil {
		return err
	}
	return json.Unmarshal(resJSON, out)
}

// androidDevice is a resolved install target — enough to address a specific Android device (an
// in-pod emulator or a remote adb endpoint) over the wire. GoogleEmail/GoogleToken arrive
// HOST-RESOLVED (the credential-store touch stays behind the "deploy-entity-resolve" seam).
type androidDevice struct {
	Engine      string
	Container   string
	AdbAddr     string
	Serial      string
	GoogleEmail string
	GoogleToken string
}

func (d androidDevice) serial() string {
	if d.Serial != "" {
		return d.Serial
	}
	return "emulator-5554"
}

// adbAddrForContainer resolves the "127.0.0.1:<host-port>" adb-server address for an
// already-running container (its published adbServerPort) — reuses container.go's existing
// inspectContainer/findHostPort (the plugin's pre-existing inline `<engine> inspect` reimplementation,
// R3: one copy, not a second kit.InspectContainer-based one for this new caller).
func adbAddrForContainer(engine, containerName string) (string, error) {
	insp, err := inspectContainer(engine, containerName)
	if err != nil {
		return "", err
	}
	port, err := insp.findHostPort(adbServerPort)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("127.0.0.1:%d", port), nil
}

// resolveAndroidDevice builds the androidDevice install handle from the spec and deploy context.
// email/token arrive already host-resolved (the credential-store touch happened behind the
// "deploy-entity-resolve" seam, before this call).
func resolveAndroidDevice(spc *spec.ResolvedAndroid, node *spec.Deploy, path, email, token string) (androidDevice, error) {
	serial := spc.EffectiveSerial()

	if spc.IsEndpoint() {
		addr, err := resolveAndroidHostPortRef(spc.Adb.Host, path, node)
		if err != nil {
			return androidDevice{}, err
		}
		return androidDevice{
			AdbAddr:     addr,
			Serial:      serial,
			GoogleEmail: email,
			GoogleToken: token,
		}, nil
	}

	if spc.Box == "" {
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
		if !kit.ContainerRunning(engine, container) {
			return androidDevice{}, fmt.Errorf("parent pod container %s is not running (start it before deploying the android device)", container)
		}
	} else {
		eng, name, err := deploykit.ResolveContainer(spc.Box, "")
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

// resolveAndroidHostPortRef substitutes a single ${HOST_PORT:N} token in a nested endpoint
// device's adb host with the PARENT pod's host-mapped port for container port N.
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
		return "", fmt.Errorf("adb host %q uses ${HOST_PORT:%d} but the device is not nested under a pod (deploy path %q has no parent to read the published port from)", addr, ctrPort, path)
	}
	engine := "podman"
	if node != nil && node.Engine == "docker" {
		engine = "docker"
	}
	engine = kit.EngineBinary(engine)
	container := "charly-" + kit.NestedContainerName(path[:i])
	if !kit.ContainerRunning(engine, container) {
		return "", fmt.Errorf("parent pod container %s is not running (start it before deploying the android endpoint device)", container)
	}
	insp, err := inspectContainer(engine, container)
	if err != nil {
		return "", err
	}
	hp, err := insp.findHostPort(ctrPort)
	if err != nil {
		return "", err
	}
	return before + fmt.Sprintf("%d", hp) + after0, nil
}

// collectAndroidInstalls walks the deploy's compiled plans for ApkInstallStep entries and flattens
// them into the wire install list, rewriting committed-APK relative paths to ABSOLUTE host paths.
func collectAndroidInstalls(plans []*deploykit.InstallPlan) ([]spec.ApkPackageSpec, error) {
	var installs []spec.ApkPackageSpec
	for _, p := range plans {
		if p == nil {
			continue
		}
		for _, step := range p.Steps {
			apkStep, ok := step.(*deploykit.ApkInstallStep)
			if !ok {
				continue
			}
			for _, ap := range apkStep.Packages {
				if ap.Apk != "" {
					abs, err := kit.ResolveApkPath(ap.Apk, apkStep.CandyDir)
					if err != nil {
						return nil, fmt.Errorf("candy %q: %w", apkStep.CandyName, err)
					}
					ap.Apk = abs
				} else if ap.Package == "" {
					return nil, fmt.Errorf("apk entry in candy %q has neither package: nor apk: declared", apkStep.CandyName)
				}
				installs = append(installs, ap)
			}
		}
	}
	return installs, nil
}
