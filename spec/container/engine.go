// Package container is the spec fabric slice for container-engine host helpers — resolving
// which engine binary to invoke, its GPU run-args, and auto-detecting the installed engine.
// RELOCATED from sdk/kit (#55 fabric-primitive extraction). It carries os/exec (the engine
// auto-detect shells `LookPath`) in its OWN slice (Rule 2) so a consumer needing only value
// types never drags os/exec. charly core inlines from here; sdk/kit re-exports the same symbols
// so existing kit.EngineBinary / kit.GPURunArgs / kit.DetectEngine call sites are untouched.
package container

import (
	"fmt"
	"os/exec"
)

// EngineBinary returns the CLI binary name for a container engine. "auto" resolves via
// DetectEngine (podman preferred), falling back to "docker".
func EngineBinary(engine string) string {
	switch engine {
	case "podman":
		return "podman"
	case "auto":
		if detected, err := DetectEngine(); err == nil {
			return detected
		}
		return "docker"
	default:
		return "docker"
	}
}

// GPURunArgs returns the engine-specific run flags that expose all host GPUs to a container.
func GPURunArgs(engine string) []string {
	switch engine {
	case "podman":
		return []string{"--device", "nvidia.com/gpu=all"}
	default:
		return []string{"--gpus", "all"}
	}
}

// DetectEngine auto-detects the container engine: prefers podman, falls back to docker.
func DetectEngine() (string, error) {
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman", nil
	}
	if _, err := exec.LookPath("docker"); err == nil {
		return "docker", nil
	}
	return "", fmt.Errorf("no container engine found (install podman or docker)")
}
