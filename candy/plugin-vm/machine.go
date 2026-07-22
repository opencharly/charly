package vm

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// podmanMachineInfo represents the JSON output of `podman machine list --format json`.
type podmanMachineInfo struct {
	Name    string `json:"Name"`
	Running bool   `json:"Running"`
}

// IsRootless checks if podman is running in rootless mode.
func IsRootless() (bool, error) {
	out, err := exec.Command("podman", "info", "--format", "{{.Host.Security.Rootless}}").Output()
	if err != nil {
		return false, fmt.Errorf("podman info: %w", err)
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// EnsureMachine creates and starts a podman machine named "charly" if needed.
// Returns the rootful connection name to use with `podman --connection`.
// When created with --rootful, podman creates two connections:
// "charly" (rootless) and "charly-root" (rootful). We return "charly-root".
func EnsureMachine() (string, error) {
	const machineName = "charly"
	const rootfulConn = "charly-root"

	// Check for gvproxy (required for podman machine networking)
	if err := checkGvproxy(); err != nil {
		return "", err
	}

	machines, err := listMachines()
	if err != nil {
		return "", err
	}

	for _, m := range machines {
		if m.Name == machineName {
			if m.Running {
				return rootfulConn, nil
			}
			fmt.Fprintf(os.Stderr, "Starting podman machine %q...\n", machineName)
			if err := runPodmanMachine("start", machineName); err != nil {
				return "", fmt.Errorf("starting podman machine: %w", err)
			}
			return rootfulConn, nil
		}
	}

	// Machine doesn't exist — create it
	fmt.Fprintf(os.Stderr, "Creating rootful podman machine %q...\n", machineName)
	if err := runPodmanMachine("init", "--rootful", "--now", machineName); err != nil {
		return "", fmt.Errorf("creating podman machine: %w", err)
	}
	return rootfulConn, nil
}

func listMachines() ([]podmanMachineInfo, error) {
	out, err := exec.Command("podman", "machine", "list", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("podman machine list: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" || strings.TrimSpace(string(out)) == "null" {
		return nil, nil
	}
	var machines []podmanMachineInfo
	if err := json.Unmarshal(out, &machines); err != nil {
		return nil, fmt.Errorf("parsing podman machine list: %w", err)
	}
	return machines, nil
}

func checkGvproxy() error {
	// Check PATH first, then known system locations
	if _, err := exec.LookPath("gvproxy"); err == nil {
		return nil
	}
	for _, path := range []string{"/usr/libexec/podman/gvproxy", "/usr/local/libexec/podman/gvproxy", "/usr/lib/podman/gvproxy"} {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	return fmt.Errorf("gvproxy not found — required for podman machine.\nInstall it: %s", InstallHint("gvproxy"))
}

func runPodmanMachine(args ...string) error {
	cmdArgs := append([]string{"machine"}, args...)
	cmd := exec.Command("podman", cmdArgs...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
