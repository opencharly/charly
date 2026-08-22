package container

// probe.go — two pure container-engine host probes (#55 CHECK-ENGINE cone Option A: relocated
// from sdk/kit's container_probe.go / ports.go so the check host-endpoint resolver
// (spec/checkhost) reaches them while spec imports zero kit). Both are genuine `<engine>` shell-outs
// / string parses with no project-loader dependency — fabric primitives, exactly the charter of this
// slice (EngineBinary/DetectEngine). sdk/kit re-exports both so kit.IsHostNetworked /
// kit.ParsePublishedPort call sites are untouched.

import (
	"fmt"
	"os/exec"
	"strings"
)

// IsHostNetworked checks if a running container uses --network host.
func IsHostNetworked(engine, containerName string) bool {
	cmd := exec.Command(engine, "inspect", "--format",
		"{{.HostConfig.NetworkMode}}", containerName)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "host"
}

// ParsePublishedPort extracts the host "ip:port" from `<engine> port` output for an in-container
// port, normalizing 0.0.0.0 / [::] to 127.0.0.1.
func ParsePublishedPort(output string, port int) (string, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", fmt.Errorf("no port mapping found for %d", port)
	}
	hostPort := strings.TrimSpace(lines[0])
	hostPort = strings.Replace(hostPort, "0.0.0.0", "127.0.0.1", 1)
	if after, ok := strings.CutPrefix(hostPort, "[::]:"); ok {
		hostPort = "127.0.0.1:" + after
	}
	return hostPort, nil
}
