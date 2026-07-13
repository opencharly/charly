package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
	// Set global forceTTY so buildShellArgs/buildExecArgs pick it up
	forceTTY = c.TTY

	// Remote refs (@github.com/...) are handled exclusively by `charly box pull`.
	// Users must pull first, then run shell on the short name.
	if IsRemoteImageRef(StripURLScheme(c.Box)) {
		return fmt.Errorf("remote refs are not accepted here; run 'charly box pull %s' first, then 'charly shell <image-name>'", c.Box)
	}
	c.Box, c.Instance = canonicalizeDeployArg(c.Box, c.Instance)

	var detected DetectedDevices
	if !c.NoAutoDetect {
		detected = DetectHostDevices()
		LogDetectedDevices(detected)
	}

	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	engine := rt.RunEngine

	// Ensure NVIDIA CDI specs exist for nested container GPU access
	if detected.GPU && engine == "podman" {
		EnsureCDI()
	}

	// Load charly.yml for volume backing config + later use (env merge,
	// agent forwarding, metadata overlay).
	dc := loadDeployConfigForRead("charly shell")
	var deployVolumes []DeployVolumeConfig
	if overlay, ok := dc.Lookup(c.Box, c.Instance); ok {
		deployVolumes = overlay.Volume
	}

	// Resolve the deploy key → declared image short-name via THE shared
	// resolver (deploy.go); the same one charly config / start / check live use,
	// so no command diverges when key != image. c.Box stays the deploy-KEY;
	// only the image ref uses the resolved name.
	deployBoxName := resolveDeployBoxName(c.Box, c.Instance)
	// Resolve from image labels (+ charly.yml overlay). No charly.yml.
	imageRef := resolveShellImageRef("", deployBoxName, c.Tag)
	if err := EnsureImage(imageRef, rt); err != nil {
		return err
	}
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("image %s has no embedded metadata; rebuild with latest charly", imageRef)
	}
	engine = ResolveBoxEngineFromMeta(meta, rt.RunEngine)
	MergeDeployOntoMetadata(meta, dc, c.Box, c.Instance)

	uid := meta.UID
	gid := meta.GID
	home := meta.Home
	ports := meta.Port
	security := meta.Security
	network := meta.Network
	deployEnv := meta.Env
	var deployEnvFile string

	cliVolumes := parseVolumeFlagsStandalone(c.VolumeFlag, c.Bind)
	volumes, bindMounts := ResolveVolumeBacking(c.Box, c.Instance, meta.Volume, mergeVolumeConfigs(deployVolumes, cliVolumes), meta.Home, rt.EncryptedStoragePath, rt.VolumesPath)

	envAccepts := meta.EnvAccept
	envRequires := meta.EnvRequire
	if meta.Registry != "" {
		imageRef = resolveShellImageRef(meta.Registry, deployBoxName, c.Tag)
	}

	shellCtrName := containerNameInstance(c.Box, c.Instance)
	shellAccepted := AcceptedEnvSet(envAccepts, envRequires)
	shellGlobalEnv := dc.GlobalEnvForImage(deployKey(c.Box, c.Instance), shellCtrName, shellAccepted)
	envVars, err := ResolveEnvVars(shellGlobalEnv, deployEnv, deployEnvFile, workspaceBindHost(bindMounts), c.EnvFile, c.Env)
	if err != nil {
		return err
	}

	// Resolve agent forwarding (SSH/GPG socket mounts)
	var deployBox *BundleNode
	if overlay, ok := dc.Lookup(c.Box, c.Instance); ok {
		deployBox = &overlay
	}
	agentFwd := ResolveAgentForwarding(rt, deployBox, home)

	// If the container is already running, exec into it instead of starting a new one
	name := containerNameInstance(c.Box, c.Instance)
	if containerRunning(engine, name) {
		// Exec path: inject env vars only (can't add volumes to running container)
		execEnv := append(slices.Clone(envVars), agentFwd.Env...)
		workDir := resolveWorkingDir(volumes, bindMounts, home, c.Box, c.Instance)
		args := buildExecArgs(engine, name, uid, gid, c.Command, execEnv, workDir)
		enginePath, err := findExecutable(EngineBinary(engine))
		if err != nil {
			return err
		}
		return execCommand(enginePath, args)
	}

	// Verify bind mounts
	if err := verifyBindMounts(bindMounts, c.Box); err != nil {
		return err
	}

	// Merge auto-detected devices into security config
	if !security.Privileged {
		security.Devices = appendUnique(security.Devices, detected.Devices...)
		if detected.AMDGPU {
			security.GroupAdd = appendGroupsForAMDGPU(security.GroupAdd)
		}
	}
	envVars = appendAutoDetectedEnv(envVars, detected)

	// Inject agent forwarding mounts and env (new container path)
	for _, v := range agentFwd.Volumes {
		security.Mounts = appendUnique(security.Mounts, v)
	}
	envVars = append(envVars, agentFwd.Env...)

	// Resolve network (default to shared "charly" network)
	resolvedNetwork, err := ResolveNetwork(network, engine)
	if err != nil {
		return err
	}

	workDir := resolveWorkingDir(volumes, bindMounts, home, c.Box, c.Instance)
	args := buildShellArgs(engine, imageRef, uid, gid, ports, volumes, bindMounts, detected.GPU, c.Command, rt.BindAddress, envVars, security, workDir, resolvedNetwork)

	// Find engine binary
	enginePath, err := findExecutable(EngineBinary(engine))
	if err != nil {
		return err
	}

	// Replace process with engine
	return execCommand(enginePath, args)
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
		if resolved, err := ResolveNewestLocalCalVer("podman", name); err == nil && resolved != "" {
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

// buildShellArgs constructs the container run argument list.
func buildShellArgs(engine, imageRef string, uid, gid int, ports []string, volumes []VolumeMount, bindMounts []ResolvedBindMount, gpu bool, command string, bindAddr string, envVars []string, security SecurityConfig, workingDir string, network ...string) []string {
	binary := EngineBinary(engine)
	interactive := "-i"
	if forceTTY || isTerminal() {
		interactive = "-it"
	}
	args := []string{
		binary, "run", "--rm", interactive,
		"-w", workingDir,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
	}
	if len(network) > 0 && network[0] != "" {
		args = append(args, "--network", network[0])
	}
	if gpu {
		args = append(args, GPURunArgs(engine)...)
	}
	args = append(args, SecurityArgs(security)...)
	for _, port := range ports {
		args = append(args, "-p", localizePort(port, bindAddr))
	}
	for _, vol := range volumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", vol.VolumeName, vol.ContainerPath))
	}
	for _, bm := range bindMounts {
		args = append(args, "-v", fmt.Sprintf("%s:%s", bm.HostPath, bm.ContPath))
	}
	for _, m := range security.Mounts {
		if after, ok := strings.CutPrefix(m, "tmpfs:"); ok {
			// tmpfs:/path:options → --tmpfs /path:options
			args = append(args, "--tmpfs", after)
		} else {
			args = append(args, "-v", m)
		}
	}
	if engine == "podman" && len(bindMounts) > 0 {
		args = append(args, fmt.Sprintf("--userns=keep-id:uid=%d,gid=%d", uid, gid))
	}
	for _, e := range envVars {
		args = append(args, "-e", e)
	}
	args = append(args, "--entrypoint", "bash", imageRef)
	if command != "" {
		args = append(args, "-c", command)
	}
	return args
}

// buildExecArgs constructs the container exec argument list for attaching to a running container.
func buildExecArgs(engine, name string, uid, gid int, command string, envVars []string, workingDir string) []string {
	binary := EngineBinary(engine)
	interactive := "-i"
	if forceTTY || isTerminal() {
		interactive = "-it"
	}
	args := []string{
		binary, "exec", interactive,
		"--user", fmt.Sprintf("%d:%d", uid, gid),
		"-w", workingDir,
	}
	for _, e := range envVars {
		args = append(args, "-e", e)
	}
	args = append(args, name, "bash")
	if command != "" {
		args = append(args, "-c", command)
	}
	return args
}

// execCommand runs the given args via syscall.Exec. When forceTTY is set
// and there is no real terminal, it wraps the command with `script` to
// provide a proper PTY so that programs requiring a TTY work correctly
// from automation tools.
func execCommand(path string, args []string) error {
	if forceTTY && !isTerminal() {
		// Wrap with script to provide a real PTY.
		// script -qefc "<cmd>" /dev/null
		//   -q: quiet (no "Script started" banner)
		//   -e: return child exit code
		//   -f: flush output after each write
		//   -c: command to run
		cmdStr := shellQuoteArgs(args)
		scriptPath, err := findExecutable("script")
		if err != nil {
			return fmt.Errorf("--tty requires 'script' (util-linux): %w", err)
		}
		scriptArgs := []string{"script", "-qefc", cmdStr, "/dev/null"}
		return syscall.Exec(scriptPath, scriptArgs, os.Environ())
	}
	return syscall.Exec(path, args, os.Environ())
}

// shellQuoteArgs joins args into a shell-safe command string.
func shellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'\\$`!#&|;(){}[]<>?*~") {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		} else {
			quoted[i] = arg
		}
	}
	return strings.Join(quoted, " ")
}

// findExecutable locates an executable in PATH.
func findExecutable(name string) (string, error) {
	path, err := exec_LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH", name)
	}
	return path, nil
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
