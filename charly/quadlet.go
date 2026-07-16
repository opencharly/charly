// quadlet.go — the RESIDUAL host-side quadlet surface after P11. The pod
// config-WRITE MECHANISM (GenerateQuadlet + its [Unit]/[Container]/[Service]/
// [Install] emitters + the QuadletConfig/CollectedSecret/ResolvedBindMount/
// ResolvedSidecar resolved-runtime types + the pure size/port/tunnel helpers, and
// the cloudflare tunnel-unit emitter) relocated to sdk/deploykit
// (deploykit_pod_aliases.go re-points the package-main call sites). What stays here
// is host-I/O: the on-disk quadlet/systemd path + filename helpers.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk/kit"
)

// quadletDir returns the user-level quadlet directory.
func quadletDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "containers", "systemd"), nil
}

// systemdUserDir returns the user-level systemd unit directory (~/.config/systemd/user/).
func systemdUserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// quadletFilename returns the quadlet filename for an image.
func quadletFilename(boxName string) string {
	return kit.ContainerName(boxName) + ".container"
}

// quadletFilenameInstance returns the quadlet filename for an image with optional instance.
func quadletFilenameInstance(boxName, instance string) string {
	return kit.ContainerNameInstance(boxName, instance) + ".container"
}

// serviceName returns the systemd service name for an image.
func serviceName(boxName string) string {
	return kit.ContainerName(boxName) + ".service"
}

// serviceNameInstance returns the systemd service name for an image with optional instance.
func serviceNameInstance(boxName, instance string) string {
	return kit.ContainerNameInstance(boxName, instance) + ".service"
}

// quadletExists checks whether a .container file exists for the given image.
func quadletExists(boxName string) (bool, error) {
	return quadletExistsInstance(boxName, "")
}

// quadletExistsInstance checks whether a .container file exists for the given image/instance.
func quadletExistsInstance(boxName, instance string) (bool, error) {
	qdir, err := quadletDir()
	if err != nil {
		return false, err
	}
	qpath := filepath.Join(qdir, quadletFilenameInstance(boxName, instance))
	_, err = os.Stat(qpath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
