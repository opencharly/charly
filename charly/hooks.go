package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/deploykit"
)

// CollectHooks collects and concatenates hooks from all candies in a box's candy chain.
// Hooks from multiple candies are concatenated in candy order.
func CollectHooks(cfg *Config, layers map[string]*Candy, boxName string) *HooksConfig {
	allCandyNames, _ := cfg.boxCandyChain(layers, boxName)

	var postEnable, preRemove []string
	for _, candyName := range allCandyNames {
		layer, ok := layers[candyName]
		if !ok {
			continue
		}
		if layer.hooks == nil {
			continue
		}
		if layer.hooks.PostEnable != "" {
			postEnable = append(postEnable, strings.TrimSpace(layer.hooks.PostEnable))
		}
		if layer.hooks.PreRemove != "" {
			preRemove = append(preRemove, strings.TrimSpace(layer.hooks.PreRemove))
		}
	}

	if len(postEnable) == 0 && len(preRemove) == 0 {
		return nil
	}

	return &HooksConfig{
		PostEnable: strings.Join(postEnable, "\n"),
		PreRemove:  strings.Join(preRemove, "\n"),
	}
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
