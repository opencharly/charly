package deploypod

// hostname_parity_test.go — the container-hostname parity across every deploy path.
// directPodmanArgs already runs containers with --hostname <name>; the quadlet path
// now emits Hostname=<name>; this test pins buildStartArgs (the direct-mode `charly
// start` argv) to the same --hostname <name>, so the in-container hostname is always
// the charly-network DNS name — load-bearing for in-container services that derive
// their reachable URL from the hostname (e.g. the AgentTeams controller children
// reach by container name) under every start path.

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

func TestBuildStartArgs_HostnameParity(t *testing.T) {
	argv := buildStartArgs(
		"podman",
		"ghcr.io/opencharly/demo:latest",
		1000, 1000,
		nil,                             // ports
		"charly-demo",                   // name
		[]deploykit.VolumeMount{},       // volumes
		[]deploykit.ResolvedBindMount{}, // bindMounts
		false, "",                       // gpu, bindAddr
		nil, // envVars
		spec.SecurityConfig{},
		nil,          // entrypoint (direct mode appends the init command separately)
		"/home/user", // workingDir
		"charly",     // network
	)
	found := false
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == "--hostname" && argv[i+1] == "charly-demo" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("buildStartArgs missing --hostname <name> parity with directPodmanArgs/quadlet: %v", argv)
	}
}
