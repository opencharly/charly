package main

// android_deploy_cmd.go — HOST-SIDE kind:android device resolution.
//
// `target: android` is an EXTERNAL deploy substrate served out-of-process by
// candy/plugin-adb (deploy:android — see android_deploy_preresolve.go). These
// helpers resolve a kind:android DEVICE (an in-pod emulator or a remote adb
// endpoint) to its adb endpoint host-side WITHOUT goadb (engine inspect only)
// for the deploy:android preresolver. The `charly status` android collector
// (K5: candy/plugin-substrate/status_android_collect.go) used to share this
// exact code as the in-core AndroidCollector; it now carries its OWN
// plugin-local re-implementation of the same resolution LOGIC (a plugin
// cannot import charly/ types), so this is no longer literally shared code —
// it is the deploy-time consumer alone. The goadb wire talk + the app
// install/uninstall live in candy/plugin-adb; this file does only
// device-endpoint resolution.

import (
	"fmt"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// AndroidDevice is a resolved install target — enough for the deploy:android
// preresolver to address a specific Android device (an in-pod emulator or a remote
// adb endpoint) over the wire, and for the status collector to derive presence. It
// is a pure HOST-SIDE handle; the goadb wire talk lives in candy/plugin-adb, fed the
// AdbAddr/Engine/Container/Serial/creds via spec.AndroidDeployVenue.
type AndroidDevice struct {
	Engine    string // container engine (podman|docker) — only for in-pod (Container != "")
	Container string // emulator pod container name; "" => host/endpoint venue
	AdbAddr   string // "host:port" of the device's adb server (resolved host-side via engine inspect)
	Serial    string // adb serial (default emulator-5554)

	// GoogleEmail / GoogleToken are the apkeep google-play credentials, resolved
	// from the credential store. Used ONLY on the host venue (the in-pod venue reads
	// them from the container env). Empty when unset.
	GoogleEmail string
	GoogleToken string
}

// serial returns the device serial, defaulting to the emulator serial.
func (d AndroidDevice) serial() string {
	if d.Serial != "" {
		return d.Serial
	}
	return "emulator-5554"
}

// adbServerPort is the in-container adb-server port the emulator publishes
// (container :5037 → the host's HOST_PORT:5037). The host resolves the mapped
// host port via engine inspect; the goadb wire talk itself lives out-of-core in
// candy/plugin-adb.
const adbServerPort = 5037

// adbAddrForContainer resolves the "127.0.0.1:<host-port>" adb-server address for
// an already-known running container (its published 5037). It is host-side only
// (engine inspect, no goadb), so it stays in core to build the spec.AndroidDeployVenue
// the deploy:android plugin consumes. Used by resolveAndroidDevice for the in-pod /
// nested device.
func adbAddrForContainer(engine, containerName string) (string, error) {
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
// container port. Looks for both "5037" and "5037/tcp" keys because podman inspect
// emits the protocol-suffixed form. Relocated from the deleted charly/adb.go — it
// is pure engine-inspect arithmetic (no goadb), shared by adbAddrForContainer and
// resolveAndroidHostPortRef's nested ${HOST_PORT:N} resolution (R3).
func findHostPort(insp *kit.ContainerInspection, containerPort int) (int, error) {
	// Host-networked containers expose the container port AS the host port.
	if insp.IsHostNetworked() {
		return containerPort, nil
	}
	keys := []string{
		fmt.Sprintf("%d/tcp", containerPort),
		fmt.Sprintf("%d", containerPort),
	}
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
	return 0, fmt.Errorf("container port %d not published on host (NetworkSettings.Ports has no binding); declare `port: [%d]` on the image or publish via charly.yml `port:`", containerPort, containerPort)
}

// findAndroidSpec resolves a kind:android device by name from the unified
// config (sibling of findK8sSpec).
func findAndroidSpec(dir, name string) *ResolvedAndroid {
	uf, ok, err := LoadUnified(dir)
	if err != nil || !ok || uf == nil || uf.Android == nil {
		return nil
	}
	return lookupAndroidSpec(uf, name)
}

// lookupAndroidSpec resolves a kind:android device by name from the unified
// config (K5: relocated from the deleted status_collect_adb.go — the status
// collector's own android lookup moved to
// candy/plugin-substrate/status_android_collect.go's androidSpecFor, which
// resolves against the resolved-project envelope instead of *UnifiedFile;
// this copy is the SEPARATE deploy-time consumer, findAndroidSpec above).
func lookupAndroidSpec(uf *UnifiedFile, name string) *ResolvedAndroid {
	if uf == nil || uf.Android == nil || name == "" {
		return nil
	}
	body, ok := uf.Android[name]
	if !ok {
		return nil
	}
	r, err := resolveAndroidViaPlugin(body)
	if err != nil {
		return nil
	}
	return r
}

// resolveAndroidDevice builds the AndroidDevice install handle from the spec
// and deploy context. Endpoint devices target a remote adb server (apkeep on
// the host); image devices target an in-pod emulator (apkeep in-pod). For a
// nested deploy (dotted path), the in-pod container is the PARENT pod
// (charly-<flat-parent-path>); for a top-level deploy it resolves by image name.
func resolveAndroidDevice(spec *ResolvedAndroid, node *spec.BundleNode, path string) (AndroidDevice, error) {
	serial := spec.EffectiveSerial()

	// Remote/physical endpoint — host-side apkeep + goadb.
	if spec.IsEndpoint() {
		email, token := resolveAndroidGoogleCreds(spec.GoogleAccount)
		// A nested endpoint device may address its parent emulator pod's
		// published adb port dynamically via ${HOST_PORT:N} instead of a
		// hard-coded host port (decouples the device from the bed's fixed
		// publish). Resolve it against the parent pod's NetworkSettings.Ports.
		addr, err := resolveAndroidHostPortRef(spec.Adb.Host, path, node)
		if err != nil {
			return AndroidDevice{}, err
		}
		return AndroidDevice{
			AdbAddr:     addr,
			Serial:      serial,
			GoogleEmail: email,
			GoogleToken: token,
		}, nil
	}

	if spec.Box == "" {
		return AndroidDevice{}, fmt.Errorf("kind:android device has neither box: nor adb: declared")
	}

	engine := "podman"
	if node != nil && node.Engine == "docker" {
		engine = "docker"
	}
	var container string
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		// Nested under a pod — the emulator runs in the PARENT pod container.
		parent := path[:i]
		container = "charly-" + kit.NestedContainerName(parent)
		engine = kit.EngineBinary(engine)
		if !containerRunning(engine, container) {
			return AndroidDevice{}, fmt.Errorf("parent pod container %s is not running (start it before deploying the android device)", container)
		}
	} else {
		// Top-level — resolve the running container by the device's image.
		eng, name, err := deploykit.ResolveContainer(spec.Box, "")
		if err != nil {
			return AndroidDevice{}, err
		}
		engine, container = eng, name
	}

	addr, err := adbAddrForContainer(engine, container)
	if err != nil {
		return AndroidDevice{}, err
	}
	return AndroidDevice{Engine: engine, Container: container, AdbAddr: addr, Serial: serial}, nil
}

// resolveAndroidHostPortRef substitutes a single ${HOST_PORT:N} token in a
// nested endpoint device's adb host with the PARENT pod's host-mapped port for
// container port N — read from NetworkSettings.Ports via the same findHostPort
// the image-device branch uses (R3). The parent pod is derived from the deploy
// path (path[:lastDot]), exactly like the image branch. Returns addr unchanged
// when it carries no ${HOST_PORT:N} reference (a literal host:port endpoint).
func resolveAndroidHostPortRef(addr, path string, node *spec.BundleNode) (string, error) {
	const marker = "${HOST_PORT:"
	before, after, ok := strings.Cut(addr, marker)
	if !ok {
		return addr, nil
	}
	rest := after
	before0, after0, ok0 := strings.Cut(rest, "}")
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
	if !containerRunning(engine, container) {
		return "", fmt.Errorf("parent pod container %s is not running (start it before deploying the android endpoint device)", container)
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

// resolveAndroidGoogleCreds reads the apkeep google-play credentials from the
// credential store using the device's google_account secret-key refs (or the
// GOOGLE_ACCOUNT_EMAIL / GOOGLE_AAS_TOKEN defaults). Empty when unset — the
// google-play path errors clearly if it needs them.
func resolveAndroidGoogleCreds(ga *AndroidGoogleAccount) (email, token string) {
	emailKey, tokenKey := "GOOGLE_ACCOUNT_EMAIL", "GOOGLE_AAS_TOKEN"
	if ga != nil {
		if ga.EmailSecret != "" {
			emailKey = ga.EmailSecret
		}
		if ga.TokenSecret != "" {
			tokenKey = ga.TokenSecret
		}
	}
	store := DefaultCredentialStore()
	email, _ = store.Get("charly/secret", emailKey)
	token, _ = store.Get("charly/secret", tokenKey)
	return email, token
}
