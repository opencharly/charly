package main

import (
	"os/exec"
	"strings"
)

// container.go — host-only container-network introspection. The former resolveContainer (a bare
// duplicate of deploykit.ResolveContainer) dissolved into that deploykit twin (CHECK-wave
// container-resolve dedup) — every caller now calls deploykit.ResolveContainer directly.

// isHostNetworked checks if a running container uses --network host.
func isHostNetworked(engine, containerName string) bool {
	cmd := exec.Command(engine, "inspect", "--format",
		"{{.HostConfig.NetworkMode}}", containerName)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "host"
}
