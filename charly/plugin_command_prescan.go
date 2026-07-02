package main

// plugin_command_prescan.go — the EARLY (pre-kong.Parse) external-command-word prescan.
//
// An external (out-of-tree) COMMAND plugin contributes a `charly <word>` CLI subcommand.
// Kong must know that word to PARSE `charly <word> …`, but the plugin binary is only resolved
// AFTER the project dir is resolved from a Kong flag — chicken-and-egg (the same shape
// plugin_prescan.go solves for deploy substrates). The fix: before kong.Parse, cheaply read the
// project's declared command words (the byte-gated plugin prescan), so
// collectExternalCommandPlugins can put a grammar holder in place for each. The binary is NOT
// resolved here — that is paid only when the user actually runs the command
// (dispatchExternalCommand resolves the binary by word and syscall.Exec's it). Best-effort and
// ADDITIVE: a project with no command plugins (or no readable charly.yml) registers nothing, so
// the grammar is byte-for-byte unchanged and every existing charly invocation is unaffected.

import (
	"os"
	"path/filepath"
	"strings"
)

// prescanProjectCommandWords learns the external COMMAND words the pre-parse project directory
// declares, so collectExternalCommandPlugins can build a Kong grammar holder for each BEFORE
// kong.Parse. Reuses the byte-gated, best-effort plugin prescan (prescanDeclaredPluginWords →
// prescanPluginManifest, which registers ClassCommand words). Best-effort: a missing/unparsable
// charly.yml or a project with no command plugins registers nothing — the grammar is unchanged.
func prescanProjectCommandWords() {
	dir := projectDirPreParse()
	if dir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, UnifiedFileName))
	if err != nil {
		return
	}
	prescanDeclaredPluginWords(data, dir)
}

// projectDirPreParse resolves the project directory BEFORE kong.Parse, mirroring main's later
// -C/--dir/CHARLY_PROJECT_DIR resolution. Kong populates CHARLY_PROJECT_DIR from the --dir flag,
// so the env is the reliable proxy; a minimal os.Args scan covers the bare flag form. --repo is
// intentionally NOT resolved here (it would fetch a remote repo on every charly invocation); a
// remote-repo project's command plugins are simply not pre-registered. Returns "" only when
// os.Getwd() also fails.
func projectDirPreParse() string {
	if d := os.Getenv("CHARLY_PROJECT_DIR"); d != "" {
		return d
	}
	if d := scanDirFlag(os.Args); d != "" {
		return d
	}
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return ""
}

// scanDirFlag finds a -C / --dir project-dir flag in raw argv (before kong parses), in both
// "-C <dir>" / "--dir <dir>" and "-C=<dir>" / "--dir=<dir>" forms. Returns "" if absent.
func scanDirFlag(args []string) string {
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" || a == "--dir":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-C="):
			return strings.TrimPrefix(a, "-C=")
		case strings.HasPrefix(a, "--dir="):
			return strings.TrimPrefix(a, "--dir=")
		}
	}
	return ""
}
