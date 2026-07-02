package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// resolveContainer resolves engine + container name, verifying the container is running.
// Use "." as image name for local mode (returns empty engine and name).
func resolveContainer(box, instance string) (engine, name string, err error) {
	if box == "." {
		return "", "", nil
	}
	rt, err := ResolveRuntime()
	if err != nil {
		return "", "", err
	}
	boxName := resolveBoxName(box)
	runEngine := ResolveBoxEngineForDeploy(boxName, instance, rt.RunEngine)
	engine = EngineBinary(runEngine)
	name = containerNameInstance(boxName, instance)
	if !containerRunning(engine, name) {
		return "", "", fmt.Errorf("container %s is not running", name)
	}
	return engine, name, nil
}

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
