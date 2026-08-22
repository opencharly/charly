package container

// image_inspect.go — the container→image-ref + image-label inspectors, RELOCATED to the
// spec/container fabric slice (#55 CHECK-ENGINE cone Option A — pure podman/docker CLI
// invocations importing zero kit). The ONE container→image-ref inspector (ContainerImageRef) + the
// image-label reader (InspectImageLabels), shared between charly core's remaining callers
// (start.go, commands.go, service.go) and the candies — the former core caller,
// check_endpoint_resolve.go's resolveImageLabelFor, relocated to candy/plugin-check's
// resolve_endpoint.go (#55 W3 B7); it calls THESE functions directly now, same as the other
// candies. sdk/kit re-exports each (sdk/kit/container_image.go + sdk/kit/local_image.go) so every
// existing kit.ContainerImageRef / kit.ContainerImage / kit.InspectImageLabels call site is
// untouched. New consumers reference spec/container directly.

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	execc "github.com/opencharly/spec/exec"
)

// ContainerImageRef returns the image ref backing a running container (.Config.Image via
// `<engine> inspect`). THE single container→image-ref inspector — there is exactly one inspect
// implementation.
func ContainerImageRef(engine, containerName string) (string, error) {
	out, _, exit, err := execc.RunCaptureCmd(exec.Command(EngineBinary(engine), "inspect", "--format", "{{.Config.Image}}", containerName))
	if err != nil {
		return "", fmt.Errorf("inspecting container %s: %w", containerName, err)
	}
	if exit != 0 {
		return "", fmt.Errorf("inspect %s: exit %d", containerName, exit)
	}
	return strings.TrimSpace(out), nil
}

// ContainerImage returns the image ref for a running container, best-effort ("" on error). Thin
// wrapper over ContainerImageRef.
func ContainerImage(engine, containerName string) string {
	ref, _ := ContainerImageRef(engine, containerName)
	return ref
}

// InspectImageLabels reads a local image's OCI labels via engine inspect. Pure container-storage
// probe: no charly-core coupling.
func InspectImageLabels(engine, imageRef string) (map[string]string, error) {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "inspect", "--format", "{{json .Config.Labels}}", imageRef)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", imageRef, err)
	}

	trimmed := strings.TrimSpace(string(output))
	if trimmed == "null" || trimmed == "" {
		return nil, nil
	}

	var labels map[string]string
	if err := json.Unmarshal([]byte(trimmed), &labels); err != nil {
		return nil, fmt.Errorf("parsing labels from %s: %w", imageRef, err)
	}
	return labels, nil
}
