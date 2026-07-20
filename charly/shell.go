package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// isTerminal reports whether stdout is connected to a terminal.
// Package-level var for testability.
var isTerminal = defaultIsTerminal

func defaultIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// containerRunning checks if a container with the given name is currently running.
var containerRunning = defaultContainerRunning

func defaultContainerRunning(engine, name string) bool {
	binary := kit.EngineBinary(engine)
	cmd := exec.Command(binary, "container", "inspect",
		"--format", "{{.State.Running}}", name)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

// containerExists reports whether a container with the given name is present in the engine's
// storage, RUNNING OR STOPPED (unlike containerRunning, which is false for a stopped container). A
// bare `container inspect` succeeds for any existing container, so its exit status is the signal.
var containerExists = func(engine, name string) bool {
	binary := kit.EngineBinary(engine)
	return exec.Command(binary, "container", "inspect", name).Run() == nil
}

// forceTTY overrides isTerminal() when set to true (e.g., by --tty flag).
// Allows automation tools like Claude Code to force TTY allocation.
var forceTTY bool

// podShellCmd is the host-side reconstruction of the former ShellCmd (now command:shell in
// candy/plugin-pod) — hostBuildPodShell (host_build_pod_shell.go) runs its Run() body VERBATIM.
// TRACKED P13-KERNEL EXIT: dispatchLifecycleTarget/LifecycleTarget (deploy_target_unified.go,
// pod_lifecycle_verb.go) are registered P13-KERNEL migration inventory (see start.go's header) —
// this resolver moves through the same venue-scoped-executor-session seam when that wave lands.
type podShellCmd struct {
	Box          string
	Tag          string
	Command      string
	Build        bool
	TTY          bool
	Env          []string
	EnvFile      string
	Instance     string
	VolumeFlag   []string
	Bind         []string
	NoAutoDetect bool
}

func (c *podShellCmd) Run() error {
	// Remote refs (@github.com/...) are handled exclusively by `charly box pull`.
	// Users must pull first, then run shell on the short name.
	if spec.IsRemoteImageRef(kit.StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first, then 'charly shell <image-name>'", c.Box)
	}
	c.Box, c.Instance = deploykit.CanonicalizeDeployArg(c.Box, c.Instance)

	// `charly shell` routes through the unified LifecycleTarget → OpAttach (F12): the plugin
	// self-resolves the venue command (candy/plugin-deploy-pod's resolve_f12.go) and runs it over the
	// served venue executor via RunInteractive (stdio host-held). The per-invocation CLI extras ride
	// the ctx (podShellOpts) into the attach-plan hook; Interactive/WrapPTY are computed HERE (host
	// isTerminal() against the REAL terminal) since the plugin's own stdio is not the operator's.
	lt, err := dispatchLifecycleTarget("shell", c.Box, c.Instance)
	if err != nil {
		return err
	}
	opts := podShellOpts{
		Tag:          c.Tag,
		EnvFile:      c.EnvFile,
		Env:          c.Env,
		VolumeFlag:   c.VolumeFlag,
		Bind:         c.Bind,
		NoAutoDetect: c.NoAutoDetect,
		// HOST-resolved NOW (never re-derived plugin-side — an out-of-process plugin's own
		// os.Stdout is not the operator's terminal): --tty forces interactive; wrap_pty additionally
		// wraps in script(1) when forced without a real terminal (an automation tool).
		Interactive: c.TTY || isTerminal(),
		WrapPTY:     c.TTY && !isTerminal(),
	}
	var cmd []string
	if c.Command != "" {
		cmd = []string{c.Command}
	}
	return lt.Attach(withPodShellOpts(context.Background(), opts), cmd, true)
}

// resolveShellImageRef builds the full image reference from registry, name, and tag. Thin
// delegate to kit.ResolveShellImageRef (P14: the logic moved to sdk/kit so candy/plugin-box's
// `merge` command can call it too, R3 single source) — every existing core call site keeps this
// name unchanged.
func resolveShellImageRef(registry, name, tag string) string {
	return kit.ResolveShellImageRef(registry, name, tag)
}

// exec_LookPath wraps os/exec.LookPath to avoid importing os/exec in syscall code.
var exec_LookPath = defaultLookPath

func defaultLookPath(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("executable not found: %s", name)
}
