package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// CollectHooks collects and concatenates hooks from all candies in a box's candy chain.
// Hooks from multiple candies are concatenated in candy order. The host-side half: resolve the
// box's FULL candy chain (base-inheriting — a *Config/walkBaseChain concern, genuinely core,
// unchanged from before the W9 split). The concatenation itself is deploykit.MergeCandyHooks, the
// pure R-item every OCI-label-collector build-render consumer can share (host today, an
// out-of-process build/deploy plugin tomorrow).
func CollectHooks(cfg *Config, layers map[string]*Candy, boxName string) *HooksConfig {
	allCandyNames, _ := cfg.boxCandyChain(layers, boxName)

	candies := make([]spec.CandyReader, 0, len(allCandyNames))
	for _, name := range allCandyNames {
		if layer, ok := layers[name]; ok {
			candies = append(candies, layer)
		}
	}
	return deploykit.MergeCandyHooks(candies)
}

// RunHook executes a hook script inside a running container.
// Environment variables are passed via -e flags.
// Returns nil on success, error on failure.
func RunHook(engine, containerName, hookScript string, envVars []string) error {
	if hookScript == "" {
		return nil
	}

	args := []string{"exec"}
	args = append(args, "-e", "CHARLY_CONTAINER_NAME="+containerName)
	for _, env := range envVars {
		args = append(args, "-e", env)
	}
	args = append(args, containerName, "sh", "-c", hookScript)

	cmd := exec.Command(engine, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	fmt.Fprintf(os.Stderr, "Running hook in %s...\n", containerName)
	return cmd.Run()
}

// removeVolumes removes all named volumes matching the image/instance prefix.
func removeVolumes(engine, boxName, instance string) {
	// Same per-deploy prefix the create side uses (deployVolumePrefix), so purge
	// removes exactly this deploy's volumes and never a same-image sibling's.
	prefix := deploykit.DeployVolumePrefix(boxName, instance)

	out, err := exec.Command(engine, "volume", "ls", "--format", "{{.Name}}", "--filter", "name="+prefix).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: listing volumes: %v\n", err)
		return
	}

	for name := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if name == "" {
			continue
		}
		rm := exec.Command(engine, "volume", "rm", name)
		rm.Stderr = os.Stderr
		if err := rm.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: removing volume %s: %v\n", name, err)
		} else {
			fmt.Fprintf(os.Stderr, "Removed volume %s\n", name)
		}
	}
}
