package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// CmdCmd runs a single command in a running container with optional notification.
type CmdCmd struct {
	Box      string `arg:"" help:"Box name"`
	Command  string `arg:"" help:"Command to execute"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Notify   bool   `long:"notify" negatable:"" default:"true" help:"Send desktop notification on completion (--no-notify to disable)"`
	Sidecar  string `long:"sidecar" help:"Run in the named SIDECAR container (charly-<box>[-<instance>]-<sidecar>) instead of the app container"`
}

func (c *CmdCmd) Run() error {
	c.Box, c.Instance = canonicalizeDeployArg(c.Box, c.Instance)
	resolve := func() (string, string, error) {
		if c.Sidecar != "" {
			return resolveSidecarContainer(c.Box, c.Instance, c.Sidecar)
		}
		return resolveContainer(c.Box, c.Instance)
	}
	engine, name, err := resolve()
	if err != nil {
		return err
	}

	// Resolve agent forwarding env vars for exec
	rt, rtErr := ResolveRuntime()
	var agentEnv []string
	if rtErr == nil {
		var deployBox *BundleNode
		if overlay, ok := loadDeployConfigForRead("charly cmd").Lookup(c.Box, c.Instance); ok {
			deployBox = &overlay
		}
		// Use host user's home as a reasonable default for GPG socket path.
		// For exec, sockets are already mounted — this only affects env vars.
		hostHome, _ := os.UserHomeDir()
		agentFwd := ResolveAgentForwarding(rt, deployBox, hostHome)
		agentEnv = agentFwd.Env
	}

	start := time.Now()
	args := []string{engine, "exec"}
	for _, e := range agentEnv {
		args = append(args, "-e", e)
	}
	args = append(args, name, "sh", "-c", c.Command)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	runErr := cmd.Run()
	elapsed := time.Since(start).Truncate(time.Millisecond)

	if c.Notify {
		status := "completed"
		if runErr != nil {
			status = "failed"
		}
		sendVenueNotification(ContainerChain(engine, name),
			fmt.Sprintf("charly: command %s", status),
			fmt.Sprintf("%s (%s)", c.Command, elapsed))
	}

	return runErr
}
