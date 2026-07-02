package kit

// walk.go — WalkPlans: the OUT-OF-PROCESS deploy-plan walk. An external deploy/step plugin
// receives the host's InstallPlan VIEWS (the serializable per-step IR) and EXECUTES each
// step on the venue over the reverse channel. The split is fixed:
//
//   - PLUGIN-RENDERABLE kinds (Op write/cmd/mkdir/link/setcap/download/copy, File,
//     ShellHook, ShellSnippet, ServicePackaged, ServiceCustom, RepoChange) the plugin
//     renders + executes ITSELF via the F2 legs (RunSystem/RunUser/PutFile/GetFile), using
//     the SHARED pure render helpers (render.go / profile.go).
//   - HOST-ENGINE kinds (Builder, LocalPkgInstall, SystemPackages, an act-verb Op,
//     ExternalPlugin, and Reboot) the plugin CANNOT execute itself — they need in-core host
//     machinery (podman/makepkg, the project DistroConfig, the provider registry, a nested
//     broker, the deterministic boot_id reboot poll) — so it dials RunHostStep and the host
//     runs them against the same venue executor (Reboot only on a charly-owned VM guest).
//
// Teardown ops: for the plugin-renderable kinds the host pre-computed step.Reverse() into
// view.ReverseOps (Fork A), so the plugin ECHOES them — the Reverse() rule stays ONCE in
// package main (R3). The host-engine kinds return their reverse ops from RunHostStep. The
// caller folds the combined slice into its DeployReply.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/charly/charly/spec"
)

// DeployExecutor is the reverse-channel surface WalkPlans drives. charly's plugin SDK
// *Executor satisfies it structurally (identical method set), so a plugin passes its
// sdk.Executor straight through — kit need not import the SDK (no cycle).
type DeployExecutor interface {
	// Venue returns the host executor's stable venue identifier.
	Venue(ctx context.Context) (string, error)
	// RunSystem runs a root (sudo) script on the venue; optsJSON is a marshalled EmitOpts (nil ok).
	RunSystem(ctx context.Context, script string, optsJSON []byte) error
	// RunUser runs an unprivileged script on the venue.
	RunUser(ctx context.Context, script string, optsJSON []byte) error
	// PutFile places content at a path on the venue (ownerRoot → root:root). Binary-safe.
	PutFile(ctx context.Context, remotePath string, content []byte, mode uint32, ownerRoot bool) error
	// GetFile reads a venue file back to the host (asRoot reads via sudo).
	GetFile(ctx context.Context, path string, asRoot bool) ([]byte, error)
	// RunCapture runs a command on the venue, returning stdout/stderr/exit separately.
	RunCapture(ctx context.Context, script string) (stdout, stderr string, exit int, err error)
	// RunHostStep drives a HOST-ENGINE step on the host engine + applies onto the venue,
	// returning the step's recorded reverse ops.
	RunHostStep(ctx context.Context, step spec.InstallStepView, optsJSON []byte) ([]spec.ReverseOp, error)
}

// WalkOpts tunes the walk. All fields optional — WalkPlans probes the venue for the shell +
// home it needs (the env.d managed-block finalizer) when they are not supplied.
type WalkOpts struct {
	// Shell overrides the detected venue login shell for the managed-block finalizer.
	Shell ShellKind
	// Home overrides the detected venue home. When empty WalkPlans probes `$HOME`.
	Home string
}

// scope mirrors spec.ScopeSystem for the system-vs-user privilege decision.
func isSystem(s spec.Scope) bool { return s == spec.ScopeSystem }

// WalkPlans executes every plan's steps on the venue and returns the combined teardown ops
// (plugin-renderable kinds echo the host-computed view.ReverseOps; host-engine kinds return
// theirs from RunHostStep). The caller folds them into its DeployReply.
func WalkPlans(ctx context.Context, exec DeployExecutor, plans []spec.InstallPlanView, opts WalkOpts) ([]spec.ReverseOp, error) {
	var reverse []spec.ReverseOp
	sawShellHook := false
	for _, p := range plans {
		for _, step := range p.Steps {
			ops, err := walkStep(ctx, exec, step)
			if err != nil {
				return nil, fmt.Errorf("walk step %q (candy=%s): %w", step.Kind, step.CandyName, err)
			}
			reverse = append(reverse, ops...)
			if step.Kind == "ShellHook" {
				sawShellHook = true
			}
		}
	}
	// env.d managed-block finalizer: ensure the venue's shell init sources the env.d dir.
	// Only when a ShellHook step actually wrote an env.d file (no env contributions → no
	// managed block, matching the in-proc target's behaviour).
	if sawShellHook {
		if err := ensureVenueManagedBlock(ctx, exec, opts); err != nil {
			return nil, fmt.Errorf("env.d managed block: %w", err)
		}
	}
	return reverse, nil
}

// walkStep executes ONE step view on the venue and returns its teardown ops.
func walkStep(ctx context.Context, exec DeployExecutor, step spec.InstallStepView) ([]spec.ReverseOp, error) {
	switch step.Kind {
	// ---- HOST-ENGINE kinds → RunHostStep (the host runs the in-core machinery). ----
	case "Builder", "LocalPkgInstall", "SystemPackages":
		return exec.RunHostStep(ctx, step, nil)
	case "ExternalPlugin":
		return exec.RunHostStep(ctx, step, nil)
	case "Reboot":
		// A `reboot: true` layer. The host owns the reboot engine (record the guest boot_id,
		// fire the reboot, poll until the boot_id changes + the executor reconnects — a
		// deterministic primitive the leaf plugin cannot replicate). The host is VENUE-AWARE:
		// it reboots a charly-owned VM guest but skip-and-notes a host venue (target:local),
		// so this routing is uniform while the reboot policy stays host-side. No teardown op.
		return exec.RunHostStep(ctx, step, nil)
	case "Op":
		// An act-verb Op (a builtin ProvisionActor: Plugin set and not the `command` verb)
		// needs the in-proc registry — route to RunHostStep. Every other Op variant is
		// plugin-renderable below.
		if step.Op != nil && step.Op.Plugin != "" && step.Op.Plugin != "command" {
			return exec.RunHostStep(ctx, step, nil)
		}
		return walkOp(ctx, exec, step)

	// ---- PLUGIN-RENDERABLE kinds → executed here via the F2 legs; echo view.ReverseOps. ----
	case "File":
		return walkFile(ctx, exec, step)
	case "ShellHook":
		return walkShellHook(ctx, exec, step)
	case "ShellSnippet":
		return walkShellSnippet(ctx, exec, step)
	case "ServicePackaged":
		return walkServicePackaged(ctx, exec, step)
	case "ServiceCustom":
		return walkServiceCustom(ctx, exec, step)
	case "RepoChange":
		return walkRepoChange(ctx, exec, step)
	case "ApkInstall":
		// Only an android deploy installs apk packages; on any other venue it is a no-op
		// (the in-proc targets record a skip). No teardown.
		return nil, nil
	default:
		// An EXTERNAL (plugin-contributed) step kind (F3): "external:<word>". Like every other
		// host-engine kind it routes to RunHostStep — the host reconstructs the opaque step from
		// the view (its declared Scope/Venue/Gate + Payload) and dispatches it to the serving
		// class:step plugin over the reverse channel (the leaf plugin cannot run another plugin).
		// ONE open arm for EVERY external word — no per-kind case (the open default arm).
		if strings.HasPrefix(step.Kind, "external:") {
			return exec.RunHostStep(ctx, step, nil)
		}
		return nil, fmt.Errorf("WalkPlans: unsupported step kind %q", step.Kind)
	}
}

// walkOp renders + runs a plugin-renderable Op. copy stages a candy file via PutFile;
// every other structured verb renders to a shell command run by scope. Echoes the
// host-computed reverse ops.
func walkOp(ctx context.Context, exec DeployExecutor, step spec.InstallStepView) ([]spec.ReverseOp, error) {
	op := step.Op
	if op == nil {
		return nil, nil
	}
	if op.Copy != "" {
		src := filepath.Join(step.CandyDir, op.Copy)
		dst := step.To
		if dst == "" {
			dst = op.To
		}
		if dst == "" {
			dst = op.Copy
		}
		content, err := os.ReadFile(src)
		if err != nil {
			return nil, fmt.Errorf("read copy source %s: %w", src, err)
		}
		if err := exec.PutFile(ctx, dst, content, ParseTaskMode(op.Mode, 0o644), isSystem(step.Scope)); err != nil {
			return nil, err
		}
		return step.ReverseOps, nil
	}
	cmd, handled := RenderOpCommand(op, step.CtxPath, step.CandyVars)
	if !handled {
		return nil, fmt.Errorf("op has no plugin-renderable verb (an act-`plugin:` verb must route to RunHostStep): %+v", op)
	}
	if cmd == "" {
		return step.ReverseOps, nil
	}
	if err := runByScope(ctx, exec, step.Scope, cmd); err != nil {
		return nil, err
	}
	return step.ReverseOps, nil
}

// walkFile reads the source file on the host and places it on the venue.
func walkFile(ctx context.Context, exec DeployExecutor, step spec.InstallStepView) ([]spec.ReverseOp, error) {
	content, err := os.ReadFile(step.Source)
	if err != nil {
		return nil, fmt.Errorf("read file source %s: %w", step.Source, err)
	}
	mode := step.Mode
	if mode == 0 {
		mode = 0o644
	}
	if err := exec.PutFile(ctx, step.Dest, content, mode, isSystem(step.Scope)); err != nil {
		return nil, err
	}
	return step.ReverseOps, nil
}

// walkShellHook renders the env.d body and places it at the host-resolved EnvFile.
func walkShellHook(ctx context.Context, exec DeployExecutor, step spec.InstallStepView) ([]spec.ReverseOp, error) {
	dest := step.EnvFile
	if dest == "" {
		// The host populates EnvFile in prepareReverseState; an empty value means no venue
		// home was resolved — nothing to write.
		return step.ReverseOps, nil
	}
	body := RenderEnvdBody(step.CandyName, step.EnvVars, step.PathAdd)
	if err := exec.PutFile(ctx, dest, []byte(body), 0o644, false); err != nil {
		return nil, err
	}
	return step.ReverseOps, nil
}

// walkShellSnippet writes a per-(candy,shell) init snippet: a whole-file drop-in
// (UseDropin) or a managed-block append into the existing rc file.
func walkShellSnippet(ctx context.Context, exec DeployExecutor, step spec.InstallStepView) ([]spec.ReverseOp, error) {
	ownerRoot := isSystem(step.Scope)
	if step.UseDropin {
		body := step.Snippet
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		if err := exec.PutFile(ctx, step.Destination, []byte(body), 0o644, ownerRoot); err != nil {
			return nil, err
		}
		return step.ReverseOps, nil
	}
	existing, err := exec.GetFile(ctx, step.Destination, ownerRoot)
	if err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("read %s: %w", step.Destination, err)
	}
	updated := ReplaceOrAppendManagedBlock(string(existing), strings.TrimRight(step.Snippet, "\n"), step.Marker)
	if err := exec.PutFile(ctx, step.Destination, []byte(updated), 0o644, ownerRoot); err != nil {
		return nil, err
	}
	return step.ReverseOps, nil
}

// walkServicePackaged writes the optional drop-in and enables the packaged unit.
func walkServicePackaged(ctx context.Context, exec DeployExecutor, step spec.InstallStepView) ([]spec.ReverseOp, error) {
	if step.OverridesText != "" && step.OverridesPath != "" {
		if err := exec.PutFile(ctx, step.OverridesPath, []byte(step.OverridesText), 0o644, isSystem(step.TargetScope)); err != nil {
			return nil, err
		}
		if err := reloadDaemon(ctx, exec, step.TargetScope); err != nil {
			return nil, err
		}
	}
	if step.Enable {
		if err := enableUnit(ctx, exec, step.Unit, step.TargetScope); err != nil {
			return nil, err
		}
	}
	return step.ReverseOps, nil
}

// walkServiceCustom writes the rendered unit file and enables it.
func walkServiceCustom(ctx context.Context, exec DeployExecutor, step spec.InstallStepView) ([]spec.ReverseOp, error) {
	if step.UnitPath == "" || step.UnitText == "" {
		return nil, fmt.Errorf("service %s: no unit text rendered", step.Name)
	}
	if err := exec.PutFile(ctx, step.UnitPath, []byte(step.UnitText), 0o644, isSystem(step.TargetScope)); err != nil {
		return nil, err
	}
	if err := reloadDaemon(ctx, exec, step.TargetScope); err != nil {
		return nil, err
	}
	if step.Enable {
		if err := enableUnit(ctx, exec, step.Name, step.TargetScope); err != nil {
			return nil, err
		}
	}
	return step.ReverseOps, nil
}

// walkRepoChange writes a repo config file (root-owned, system scope).
func walkRepoChange(ctx context.Context, exec DeployExecutor, step spec.InstallStepView) ([]spec.ReverseOp, error) {
	if err := exec.PutFile(ctx, step.File, []byte(step.Content), 0o644, true); err != nil {
		return nil, err
	}
	return step.ReverseOps, nil
}

// ---- helpers ----

// runByScope runs a rendered command on the venue as root (system) or user.
func runByScope(ctx context.Context, exec DeployExecutor, scope spec.Scope, script string) error {
	if isSystem(scope) {
		return exec.RunSystem(ctx, script, nil)
	}
	return exec.RunUser(ctx, script, nil)
}

// enableUnit runs `systemctl [--user] enable --now <unit>` on the venue.
func enableUnit(ctx context.Context, exec DeployExecutor, unit string, scope spec.Scope) error {
	if scope == spec.ScopeUser {
		return exec.RunUser(ctx, "systemctl --user enable --now "+ShQuoteArg(unit), nil)
	}
	return exec.RunSystem(ctx, "systemctl enable --now "+ShQuoteArg(unit), nil)
}

// reloadDaemon runs `systemctl [--user] daemon-reload` so a freshly-written unit/drop-in is
// picked up before enable.
func reloadDaemon(ctx context.Context, exec DeployExecutor, scope spec.Scope) error {
	if scope == spec.ScopeUser {
		return exec.RunUser(ctx, "systemctl --user daemon-reload", nil)
	}
	return exec.RunSystem(ctx, "systemctl daemon-reload", nil)
}

// ensureVenueManagedBlock inserts/updates the env.d-sourcing managed block in the venue's
// shell init file (the finalizer the in-proc target runs after every deploy). Probes the
// venue's $HOME + $SHELL when not supplied in opts.
func ensureVenueManagedBlock(ctx context.Context, exec DeployExecutor, opts WalkOpts) error {
	home := opts.Home
	if home == "" {
		out, _, _, err := exec.RunCapture(ctx, `printf %s "$HOME"`)
		if err != nil {
			return fmt.Errorf("probe venue home: %w", err)
		}
		home = strings.TrimSpace(out)
	}
	if home == "" {
		return fmt.Errorf("venue home unresolved")
	}
	shell := opts.Shell
	if shell == "" {
		out, _, _, err := exec.RunCapture(ctx, `printf %s "${SHELL:-/bin/bash}"`)
		if err != nil {
			return fmt.Errorf("probe venue shell: %w", err)
		}
		shell = DetectShellFromPath(strings.TrimSpace(out))
	}
	path := ShellInitFilePath(shell, home)
	body := ManagedBlockBody(shell, home)
	existing, err := exec.GetFile(ctx, path, false)
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	updated := ReplaceOrAppendManagedBlock(string(existing), body, "")
	return exec.PutFile(ctx, path, []byte(updated), 0o644, false)
}

// isNotFound recognizes a "file does not exist" error from GetFile (os.ErrNotExist or the
// common ssh/cat "No such file or directory" text).
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "No such file or directory") || strings.Contains(msg, "no such file")
}
