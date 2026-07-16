package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/kit"
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
	binary := EngineBinary(engine)
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
	binary := EngineBinary(engine)
	return exec.Command(binary, "container", "inspect", name).Run() == nil
}

// forceTTY overrides isTerminal() when set to true (e.g., by --tty flag).
// Allows automation tools like Claude Code to force TTY allocation.
var forceTTY bool

// ShellCmd starts a bash shell in a container image
type ShellCmd struct {
	Box             string   `arg:"" help:"Box name or remote ref (github.com/org/repo/box[@version])"`
	Tag             string   `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	Command         string   `short:"c" help:"Command to execute instead of interactive shell"`
	Build           bool     `long:"build" help:"Force local build instead of pulling from registry"`
	TTY             bool     `long:"tty" help:"Force TTY allocation (for automation tools that lack a real terminal)"`
	Env             []string `short:"e" long:"env" sep:"none" help:"Set container env var (KEY=VALUE)"`
	EnvFile         string   `long:"env-file" help:"Load env vars from file"`
	Instance        string   `short:"i" long:"instance" help:"Instance name for running multiple containers of the same box"`
	VolumeFlag      []string `long:"volume" short:"v" help:"Configure volume backing (name:type[:path])"`
	Bind            []string `long:"bind" help:"Bind volume to host path (name or name=path)"`
	AutoDetectFlags `embed:""`
}

func (c *ShellCmd) Run() error {
	// Remote refs (@github.com/...) are handled exclusively by `charly box pull`.
	// Users must pull first, then run shell on the short name.
	if IsRemoteImageRef(StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first, then 'charly shell <image-name>'", c.Box)
	}
	c.Box, c.Instance = canonicalizeDeployArg(c.Box, c.Instance)

	// `charly shell` routes through the unified LifecycleTarget → OpAttach (F12): the host resolves the
	// venue command (resolvePodShellPlan, #59 inventory), the owning plugin runs it over the served
	// venue executor via RunInteractive (stdio host-held). The per-invocation CLI extras ride the ctx
	// (podShellOpts) into the attach-plan hook; tty=true selects the interactive `charly shell` resolver
	// (its `-it`-vs-`-i` decision reads ForceTTY/isTerminal internally).
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
		ForceTTY:     c.TTY,
	}
	var cmd []string
	if c.Command != "" {
		cmd = []string{c.Command}
	}
	return lt.Attach(withPodShellOpts(context.Background(), opts), cmd, true)
}

// resolveShellImageRef builds the full image reference from registry,
// name, and tag. When `tag` is empty, it resolves to the newest local
// CalVer for the given short name via `ResolveNewestLocalCalVer` —
// this is the CalVer-only contract (`/charly-build:build` "Cache Efficiency").
// Callers that explicitly want a specific tag pass it; callers whose
// `--tag` flag is empty get the newest CalVer without extra work.
//
// When `registry` is set AND `tag` is empty, there's no way to guess
// a remote CalVer without a registry-list call, so the caller gets
// `<registry>/<name>` back with no tag suffix — the engine will
// resolve it locally first (matching any single local tag) or error.
func resolveShellImageRef(registry, name, tag string) string {
	if tag == "" {
		// Try local CalVer resolution. Best-effort: if nothing local
		// matches, fall back to a tagless ref so the engine's own
		// resolution path can error with its canonical message.
		if resolved, err := kit.ResolveNewestLocalCalVer("podman", name); err == nil && resolved != "" {
			return resolved
		}
		if registry != "" {
			return fmt.Sprintf("%s/%s", registry, name)
		}
		return name
	}
	if registry != "" {
		return fmt.Sprintf("%s/%s:%s", registry, name, tag)
	}
	return fmt.Sprintf("%s:%s", name, tag)
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
