package appium

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// container.go is the host-side container introspection the appium verb needs to reach
// the running Appium server: it resolves the container's host-published 4723 port and
// stages an APK into the container for install-app. charly's core resolveContainer /
// InspectContainer / findHostPort live in package main (unimportable by this
// out-of-tree module), so this is a MINIMAL inline reimplementation over the container
// engine CLI — the only charly-internal symbols the dep-shed required inlining.
//
// The HOST-AUTHORITATIVE container NAME (charly-<box>[_<instance>], with charly's
// registry-ref stripping applied) is computed by the host and shipped in CheckEnv.
// ContainerName — this plugin never recomputes charly's naming convention. The ENGINE
// defaults to podman (the project default; override with CHARLY_PLUGIN_ENGINE for a
// docker host).

const appiumContainerPort = 4723

// engineBinary returns the container engine CLI. Defaults to podman (the charly
// project default); CHARLY_PLUGIN_ENGINE overrides for a docker host.
func engineBinary() string {
	if e := strings.TrimSpace(os.Getenv("CHARLY_PLUGIN_ENGINE")); e != "" {
		return e
	}
	return "podman"
}

// inspection is the subset of `<engine> inspect` output the port resolver reads.
type inspection struct {
	HostConfig struct {
		NetworkMode string `json:"NetworkMode"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"Ports"`
	} `json:"NetworkSettings"`
}

// inspectContainer runs `<engine> inspect <name>` and decodes the single-element array.
func inspectContainer(engine, name string) (*inspection, error) {
	out, err := exec.Command(engine, "inspect", name).Output()
	if err != nil {
		return nil, fmt.Errorf("%s inspect %s: %w", engine, name, err)
	}
	var arr []inspection
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, fmt.Errorf("decode %s inspect %s: %w", engine, name, err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("%s inspect %s: empty result (container not found)", engine, name)
	}
	return &arr[0], nil
}

// findHostPort returns the host port the container port is published on. A
// host-networked container exposes the container port AS the host port.
func (insp *inspection) findHostPort(containerPort int) (int, error) {
	if strings.EqualFold(insp.HostConfig.NetworkMode, "host") {
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
	return 0, fmt.Errorf("container port %d not published on host (NetworkSettings.Ports has no binding); declare `port: [%d]` on the image", containerPort, containerPort)
}

// appiumBaseURL resolves the running container's Appium server URL: reads
// HOST_PORT:4723 from `<engine> inspect`, prepends 127.0.0.1 + the base path.
func appiumBaseURL(env *checkEnv, basePath string) (string, error) {
	if env.ContainerName == "" {
		return "", fmt.Errorf("appium: no container name in check env (box=%q) — the verb needs a running pod", env.Box)
	}
	insp, err := inspectContainer(engineBinary(), env.ContainerName)
	if err != nil {
		return "", err
	}
	port, err := insp.findHostPort(appiumContainerPort)
	if err != nil {
		return "", err
	}
	if basePath == "" {
		basePath = appiumBasePath
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, basePath), nil
}

// stageAPKIntoContainer copies a host APK into the container (mirroring `adb install` /
// the in-tree appium install-app) so the in-container Appium server can read it via
// mobile:installApp's appPath. Returns the in-container path and a cleanup func.
func stageAPKIntoContainer(containerName, hostAPK string) (remote string, cleanup func(), err error) {
	engine := engineBinary()
	base := hostAPK
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	remote = "/tmp/charly-appium-" + base
	if out, cpErr := exec.Command(engine, "cp", hostAPK, containerName+":"+remote).CombinedOutput(); cpErr != nil {
		return "", nil, fmt.Errorf("staging APK into %s: %w: %s", containerName, cpErr, strings.TrimSpace(string(out)))
	}
	cleanup = func() { _ = exec.Command(engine, "exec", containerName, "rm", "-f", remote).Run() }
	return remote, cleanup, nil
}
