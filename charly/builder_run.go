package main

// builder_run.go — `podman run <builder>` wrapper for the local deploy target.
//
// The host target delegates compile-needing steps (pixi/npm/cargo/aur)
// to the existing multi-stage builder images. BuilderRun spawns the
// builder container with the correct flags:
//
//   --user $(id -u):$(id -g)   match host uid/gid so artifacts are owned
//                               correctly at bind-mount time
//   -v $HOME/.pixi:$HOME/.pixi:rw
//   -v $HOME/.cargo:$HOME/.cargo:rw
//   -v $HOME/.npm-global:$HOME/.npm-global:rw
//   -v $HOME/.cache/charly:$HOME/.cache/charly:rw
//   -v <candy-dir>:/work:ro
//   -e HOME=$HOME
//   -e PIXI_CACHE_DIR=$HOME/.cache/charly/pixi
//   -e NPM_CONFIG_PREFIX=$HOME/.npm-global
//   -e CARGO_HOME=$HOME/.cargo
//   -w /work
//
// The invoked shell command comes from the builder's phase.install.host cell
// (renderBuilderScript, with {{.Home}} resolved to the host user's home) — the
// deploy-time host analog of the build-time multi-stage (now kit.BuilderResolve).
//
// For aur specifically, the bind-mount set is different: the output
// /tmp/aur-pkgs/ is mounted to a tmpdir so the caller can run
// `sudo pacman -U <tmpdir>/*.pkg.tar.zst` on the host afterwards.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BuilderRunOpts describes one invocation. ScriptBody is the rendered
// stage template content (already {{.Home}}-substituted).
type BuilderRunOpts struct {
	Engine       string // "podman" or "docker"; default "podman"
	BuilderImage string // full image ref, e.g. "ghcr.io/opencharly/fedora-builder:latest"
	CandyDir     string // absolute path to candy source (bind-mounted as /work)
	ScriptBody   string // shell script contents to pass to bash -c

	// Cfg + ProjectDir feed `EnsureImagePresent` so a builder image
	// missing from local podman storage AND unavailable on the
	// registry (401, network outage, image not yet pushed) falls
	// back to a local `charly box build <basename>` when the basename
	// matches a project charly.yml entry. The canonical "every command
	// uses one ensure path" contract — see charly/ensure_image.go.
	Cfg        *Config
	ProjectDir string

	// Bind-mounts. Keys are container paths; values are host paths.
	// Set by the caller based on the builder kind — pixi/npm/cargo use
	// the same HOME-subdir layout, aur uses a tmpdir for /tmp/aur-pkgs.
	BindMounts map[string]string

	// Env vars to set inside the container (in addition to HOME).
	Env map[string]string

	// HostHome is the invoking user's absolute home dir. Set via HOME=
	// inside the container so path-baking (pixi shebangs, etc.) resolves
	// to a path that's valid both inside (via bind-mount) and outside.
	HostHome string

	// DryRun returns the command line that would run without executing.
	// Used for --dry-run deploy.
	DryRun bool

	// RunAsRoot spawns the container as UID 0 instead of the host's
	// UID. Needed for builders whose script body uses `sudo` against
	// users that don't exist in the builder image's /etc/passwd —
	// e.g. AUR's makepkg+yay flow inside a non-OCI-staged builder
	// image. Under rootless podman, root-in-container maps to the
	// host's UID, so file ownership in bind-mounts stays correct.
	RunAsRoot bool
}

// BuilderRun runs the configured builder container. Returns the
// command's combined stdout+stderr on success; on failure, returns the
// output plus the exec error.
func BuilderRun(ctx context.Context, opts BuilderRunOpts) ([]byte, error) {
	engine := opts.Engine
	if engine == "" {
		engine = "podman"
	}

	// `podman run` needs the CONCRETE storage key, not a namespace-qualified or
	// short builder ref (e.g. the cachyos project's aur builder "charly.arch-builder"
	// is not a podman image — podman reports "image not known"). Resolve it via
	// the SAME resolver EnsureImagePresent uses for its present-check, then run
	// podman against the resolved ref. EnsureImagePresent (below) is still called
	// with the ORIGINAL ref so its resolve + pull + build-from-charly.yml fallback
	// for short names keeps working. resolveImageRefForEnsure is side-effect-free,
	// so it is safe in the dry-run path too.
	origImage := opts.BuilderImage
	if opts.BuilderImage != "" {
		if resolved, rerr := resolveImageRefForEnsure(opts.BuilderImage, opts.Cfg, opts.ProjectDir); rerr == nil && resolved != "" {
			opts.BuilderImage = resolved
		}
	}

	args := buildBuilderRunArgs(opts)

	if opts.DryRun {
		// Build a human-readable command line. Log-only; never touch the
		// host filesystem.
		shellified := engine + " " + shellJoin(args)
		fmt.Fprintln(os.Stderr, "[dry-run] "+shellified)
		fmt.Fprintln(os.Stderr, "[dry-run] script body:")
		for line := range strings.SplitSeq(opts.ScriptBody, "\n") {
			fmt.Fprintln(os.Stderr, "[dry-run]   "+line)
		}
		return nil, nil
	}

	// Ensure the builder image is present in local podman storage
	// before we hand it to `podman run`. Going through the canonical
	// ensure path means a 401 / unreachable-registry / not-yet-pushed
	// failure falls back to a local `charly box build` when the image
	// is project-buildable, instead of `podman run`'s implicit
	// auto-pull blowing up with a stale auth error.
	if origImage != "" {
		if err := EnsureImagePresent(ctx, origImage, opts.Cfg, opts.ProjectDir); err != nil {
			return nil, fmt.Errorf("BuilderRun: ensure %s: %w", origImage, err)
		}
	}

	cmd := exec.CommandContext(ctx, engine, args...)
	cmd.Stdin = strings.NewReader(opts.ScriptBody)
	// Stream stdout+stderr live to the operator's terminal AND
	// capture into a buffer so the caller has the full output for
	// post-step diagnostics (e.g. failure detection when an exit-0
	// builder produces zero artifacts). Without live streaming, long
	// AUR builds appeared frozen and silent yay failures (where
	// yay returns 0 but installs nothing) had no audit trail.
	var captured bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stderr, &captured)
	cmd.Stderr = io.MultiWriter(os.Stderr, &captured)
	if err := cmd.Run(); err != nil {
		return captured.Bytes(), fmt.Errorf("BuilderRun: %w\nOutput:\n%s", err, captured.Bytes())
	}
	return captured.Bytes(), nil
}

// buildBuilderRunArgs assembles the podman/docker run argv. Extracted
// for unit-testability — we assert the produced argv rather than
// actually spawning a container in tests.
func buildBuilderRunArgs(opts BuilderRunOpts) []string {
	uid := os.Getuid()
	gid := os.Getgid()

	args := []string{"run", "--rm"}
	// EnsureImagePresent handled the pull/build before this call, so
	// disable podman's implicit auto-pull. Without this flag, podman
	// would re-attempt the registry pull on its own and the same 401 /
	// auth / network failure that EnsureImagePresent already worked
	// around would resurface here.
	args = append(args, "--pull=never")
	if opts.RunAsRoot {
		args = append(args, "--user", "0:0")
	} else {
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
	}
	args = append(args, "-i") // bash -c reads the script from stdin

	// Deterministic bind-mount ordering so dry-run output is
	// reproducible. Sorted by container path.
	containerPaths := make([]string, 0, len(opts.BindMounts))
	for cpath := range opts.BindMounts {
		containerPaths = append(containerPaths, cpath)
	}
	sortStrings(containerPaths)
	for _, cpath := range containerPaths {
		hpath := opts.BindMounts[cpath]
		args = append(args, "-v", fmt.Sprintf("%s:%s:rw", hpath, cpath))
	}

	// Candy source always mounted read-only at /work.
	if opts.CandyDir != "" {
		args = append(args, "-v", fmt.Sprintf("%s:/work:ro", opts.CandyDir))
	}

	// HOME + extra env vars.
	if opts.HostHome != "" {
		args = append(args, "-e", "HOME="+opts.HostHome)
	}
	envKeys := make([]string, 0, len(opts.Env))
	for k := range opts.Env {
		envKeys = append(envKeys, k)
	}
	sortStrings(envKeys)
	for _, k := range envKeys {
		args = append(args, "-e", k+"="+opts.Env[k])
	}

	args = append(args, "-w", "/work")
	args = append(args, opts.BuilderImage, "bash", "-s")
	return args
}

// ---------------------------------------------------------------------------
// Helpers for building the standard bind-mount sets.
// ---------------------------------------------------------------------------

// UserScopeBindMounts returns the standard subdirs each user-scope
// builder needs bind-mounted: $HOME/.pixi, $HOME/.cargo,
// $HOME/.npm-global, $HOME/.cache/charly. Ensures the source dirs exist on
// the host (empty dirs — so podman's --mount doesn't reject them).
func UserScopeBindMounts(hostHome string) (map[string]string, error) {
	if hostHome == "" {
		return nil, fmt.Errorf("UserScopeBindMounts: empty HOME")
	}
	subdirs := []string{".pixi", ".cargo", ".npm-global", ".cache/charly"}
	out := make(map[string]string, len(subdirs))
	for _, sub := range subdirs {
		p := filepath.Join(hostHome, sub)
		if err := os.MkdirAll(p, 0755); err != nil {
			return nil, fmt.Errorf("UserScopeBindMounts mkdir %s: %w", p, err)
		}
		out[p] = p // same path inside and outside the container
	}
	return out, nil
}

// UserScopeEnv returns the env-var set that pixi/npm/cargo need to
// write into the bind-mounted directories rather than the builder's
// own home paths.
func UserScopeEnv(hostHome string) map[string]string {
	return map[string]string{
		"PIXI_CACHE_DIR":    filepath.Join(hostHome, ".cache", "charly", "pixi"),
		"RATTLER_CACHE_DIR": filepath.Join(hostHome, ".cache", "charly", "rattler"),
		"NPM_CONFIG_PREFIX": filepath.Join(hostHome, ".npm-global"),
		"CARGO_HOME":        filepath.Join(hostHome, ".cargo"),
	}
}

// shellJoin renders argv as a shell-safe string for log/dry-run output.
// Not security-sensitive (we aren't actually executing this via shell;
// exec.CommandContext is used).
func shellJoin(args []string) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		if strings.ContainsAny(a, " \t\n\"'$") {
			b.WriteByte('\'')
			b.WriteString(strings.ReplaceAll(a, "'", `'\''`))
			b.WriteByte('\'')
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}
