package main

// shell_profile.go — host-side shell profile integration.
//
// On `charly bundle add host`, each installed candy contributes a set of
// env vars and PATH additions (from the candy manifest's env: + path_append:).
// We materialize them as `~/.config/opencharly/env.d/<candy>.env` files
// and insert a managed block in the user's shell init so those files
// get sourced at login.
//
// Shell detection:
//   - bash → ~/.bashrc. A bash login shell sources the FIRST of
//     ~/.bash_profile / ~/.bash_login / ~/.profile; when ~/.bash_profile
//     exists (the Arch/CachyOS default, which does `. ~/.bashrc`) ~/.profile
//     is NEVER read, so a block placed there silently never loads. ~/.bashrc
//     is sourced by interactive shells directly AND by login shells via the
//     default ~/.bash_profile, so the env.d block loads in the user's terminal.
//   - zsh  → ~/.zshenv (sourced for every zsh invocation type).
//   - fish → ~/.config/fish/conf.d/opencharly.fish (conf.d is idiomatic).
//
// Managed-block fence:
//
//   # opencharly:begin (managed by charly; do not edit inside this block)
//   <sourcing loop>
//   # opencharly:end
//
// On `charly bundle del host`, if no candies remain deployed the managed
// block is removed from the shell init file.
//
// TRACKED P13-KERNEL EXIT (DEPLOY-wave W2 audit, 2026-07-20): DetectLoginShell/
// WriteEnvdFile/ManagedBlockBody/ShellInitFilePath/markersForTag are DEAD in production
// (only host_infra_test.go references them — verified by grep, not assumed); sdk/kit/
// profile.go already carries the live equivalents (DetectShellFromPath/RenderEnvdBody/
// ManagedBlockBody/ShellInitFilePath/MarkersForTag), consumed by kit.WalkPlans since the
// local/vm deploy targets externalized. EnvdFilePath/RemoveEnvdFile's own former core
// caller relocated to sdk/deploykit.TeardownHostDeploy in the 4/5 sdk lift —
// RemoveEnvdFile stays here (it is the ONE non-portable leaf that call needs) and is
// wired in via the init() below, a package-var seam mirroring deploykit.CompileServiceSteps.
// This file's confirmed-dead functions + the host_infra_test.go coverage trim still ride
// the P13-KERNEL walk-port exit alongside their OWN remaining consumer, never a solo cleanup.

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

func init() { deploykit.RemoveEnvdFile = RemoveEnvdFile }

// ShellKind classifies the user's login shell.
type ShellKind string

const (
	ShellBash ShellKind = "bash"
	ShellZsh  ShellKind = "zsh"
	ShellFish ShellKind = "fish"
)

// DetectLoginShell inspects $SHELL (or /etc/passwd as fallback) and
// returns the detected shell. Unknown shells default to bash — that's
// the POSIX-safest choice.
func DetectLoginShell() ShellKind {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		if u, err := user.Current(); err == nil {
			shellPath = getShellFromPasswd(u.Username)
		}
	}
	base := filepath.Base(shellPath)
	switch base {
	case "zsh":
		return ShellZsh
	case "fish":
		return ShellFish
	case "bash", "sh", "":
		return ShellBash
	}
	return ShellBash
}

// getShellFromPasswd is a stub that can be upgraded to parse
// /etc/passwd directly. The os/user package doesn't expose the shell
// field cross-platform, so we rely on $SHELL as the primary source.
func getShellFromPasswd(_ string) string { return "" }

// ---------------------------------------------------------------------------
// env.d file writing.
// ---------------------------------------------------------------------------

// EnvdFilePath returns the env file path for a given candy.
func EnvdFilePath(hostHome, candyName string) string {
	return filepath.Join(EnvdDir(hostHome), candyName+".env")
}

// WriteEnvdFile creates (or overwrites) the env.d entry for a candy.
// Content is rendered from the ShellHookStep's EnvVars + PathAdd.
func WriteEnvdFile(hostHome, candyName string, envVars map[string]string, pathAdd []string) (string, error) {
	dir := EnvdDir(hostHome)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("WriteEnvdFile mkdir: %w", err)
	}
	path := EnvdFilePath(hostHome, candyName)
	body := renderEnvdBody(candyName, envVars, pathAdd)
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		return "", fmt.Errorf("WriteEnvdFile %s: %w", path, err)
	}
	return path, nil
}

// RemoveEnvdFile deletes an env.d entry. Silently succeeds when absent.
func RemoveEnvdFile(hostHome, candyName string) error {
	err := os.Remove(EnvdFilePath(hostHome, candyName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// renderEnvdBody produces a deterministic, shell-agnostic POSIX-sh
// fragment. Sorted keys + path entries guarantee stable output across
// runs (tests compare directly).
func renderEnvdBody(candyName string, envVars map[string]string, pathAdd []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# opencharly env for layer %s — managed by charly; do not edit\n", candyName)
	keys := make([]string, 0, len(envVars))
	for k := range envVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, shQuoteEnv(envVars[k]))
	}
	if len(pathAdd) > 0 {
		// Build PATH prepend entries. Double-quote the value so $PATH
		// EXPANDS at sourcing time — single-quoting (shQuoteEnv) makes
		// each candy's env.d set PATH to the literal string
		// "/some/dir:$PATH", which the next candy's env.d then clobbers
		// with its own literal "/other/dir:$PATH", losing every prior
		// candy's PATH entries. Candy-supplied paths are absolute, so
		// double-quoting is safe; we only escape characters with
		// special meaning inside double quotes.
		parts := append([]string(nil), pathAdd...)
		parts = append(parts, `$PATH`)
		fmt.Fprintf(&b, "export PATH=\"%s\"\n", shDoubleQuotePath(strings.Join(parts, ":")))
	}
	return b.String()
}

// shDoubleQuotePath escapes a PATH-list value for use INSIDE double
// quotes. Only the four chars with special meaning inside POSIX-sh
// double quotes need escaping: $ ` " \. We DON'T escape $ — the
// caller wants $PATH to expand. So escape just the others. PATH
// entries from the candy manifest are absolute paths and won't contain those
// characters in practice; this is purely defensive.
func shDoubleQuotePath(v string) string {
	r := strings.NewReplacer(`\`, `\\`, "`", "\\`", `"`, `\"`)
	return r.Replace(v)
}

// shQuoteEnv single-quotes a value for POSIX sh. Inside single quotes
// nothing needs escaping except the single quote itself.
func shQuoteEnv(v string) string {
	if v == "" {
		return `''`
	}
	if !strings.ContainsAny(v, " \t\n\"'$*?[](){}<>|&;`\\!") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// ---------------------------------------------------------------------------
// Managed block insertion / removal.
// ---------------------------------------------------------------------------

const (
	managedBlockBegin = "# opencharly:begin (managed by charly; do not edit inside this block)"
	managedBlockEnd   = "# opencharly:end"
)

// ManagedBlockBody returns the shell-specific loop that sources the
// env.d directory. POSIX-sh for bash/zsh, fish syntax for fish.
func ManagedBlockBody(shell ShellKind, hostHome string) string {
	envdGlob := filepath.Join(EnvdDir(hostHome), "*.env")
	switch shell {
	case ShellFish:
		return fmt.Sprintf(`for f in %s
  if test -r $f
    source $f
  end
end`, envdGlob)
	default: // bash/zsh POSIX
		// Glob is intentionally UNQUOTED so the shell expands *.env.
		// Quoting (`"%s"`) suppresses expansion — the loop then iterates
		// once over the literal pattern, the `[ -r ]` test fails, and
		// no env files get sourced. The path is charly-controlled so there
		// are no word-splitting concerns from spaces.
		return fmt.Sprintf(`for f in %s; do [ -r "$f" ] && . "$f"; done`, envdGlob)
	}
}

// ShellInitFilePath returns the path to the init file we write the
// managed block into, for each detected shell.
func ShellInitFilePath(shell ShellKind, hostHome string) string {
	switch shell {
	case ShellZsh:
		return filepath.Join(hostHome, ".zshenv")
	case ShellFish:
		return filepath.Join(hostHome, ".config", "fish", "conf.d", "opencharly.fish")
	case ShellBash:
		// ~/.bashrc, NOT ~/.profile. A bash login shell sources the FIRST of
		// ~/.bash_profile / ~/.bash_login / ~/.profile — so when ~/.bash_profile
		// exists (the Arch/CachyOS default, which does `. ~/.bashrc`), ~/.profile
		// is NEVER read and an env.d block placed there silently never loads
		// (the env.d PATH additions, e.g. ~/.npm-global/bin for the AI CLIs,
		// went missing on the operator VM despite being installed). ~/.bashrc IS
		// sourced by interactive shells directly AND by a login shell through
		// the default ~/.bash_profile, so the block loads in the user's terminal.
		return filepath.Join(hostHome, ".bashrc")
	default:
		// sh / unknown: POSIX login reads ~/.profile.
		return filepath.Join(hostHome, ".profile")
	}
}

// The env.d-sourcing managed block is written by the deploy walk's finalizer: in-proc by
// the OCI build path's helpers, and for the externalized local/vm deploys by the
// out-of-process kit.WalkPlans (its ensureVenueManagedBlock, sharing ManagedBlockBody /
// ShellInitFilePath via the kit aliases — R3; the block-splice itself is kit's, sdk/kit/profile.go). The former
// in-proc managed-block writers were retired when target:vm
// (the last in-proc caller) externalized. The GLOBAL env.d block's teardown is the
// symmetric concern of that same out-of-process walk; the per-candy `shell_snippet:`
// block is stripped on teardown by reverseRemoveManaged → RemoveManagedBlockAt.

// markersForTag returns the begin/end fence pair for a given marker tag.
// Empty tag yields the global-block fence (used for env.d sourcing and
// the VM ssh-config Include); non-empty tag yields a per-candy fence so
// multiple candies can coexist in one rc file.
func markersForTag(marker string) (begin, end string) {
	if marker == "" {
		return managedBlockBegin, managedBlockEnd
	}
	return fmt.Sprintf("# opencharly:begin %s (managed by charly; do not edit inside this block)", marker),
		fmt.Sprintf("# opencharly:end %s", marker)
}

// markersForTag's remaining consumer is host_infra_test.go; the managed-block
// stripping itself moved to kit.RemoveManagedBlockAt (kit/profile.go) in P4.

// EnvdDir is exported (used across deploy code); thin wrapper over the kit copy.
func EnvdDir(hostHome string) string { return kit.EnvdDir(hostHome) }
