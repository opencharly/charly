package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// container.go is the host-side container introspection the adb verb needs to
// resolve a running emulator pod's host-published adb-server port (container
// :5037 → the host's HOST_PORT:5037). charly's core resolveContainer /
// InspectContainer / findHostPort live in package main (unimportable by this
// out-of-tree module), so this is a MINIMAL inline reimplementation over the
// container engine CLI — the same pattern candy/plugin-appium uses for :4723.
//
// The deploy + status seams pass an already-resolved AdbAddr (host:port) directly
// (computed core-side via podman inspect, which needs no goadb), so this inline
// resolver only runs for the CHECK verb, where the host ships a CheckEnv carrying
// the HOST-AUTHORITATIVE ContainerName (charly-<box>[_<instance>], registry-ref
// stripped) — this plugin never recomputes charly's naming convention. The ENGINE
// defaults to podman (the project default); CHARLY_PLUGIN_ENGINE overrides.

// adbServerPort is the in-container adb-server port the emulator publishes.
const adbServerPort = 5037

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
