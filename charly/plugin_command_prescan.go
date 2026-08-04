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
// -C/--dir/CHARLY_PROJECT_DIR/--repo resolution. Kong populates CHARLY_PROJECT_DIR from the --dir
// flag, so the env is the reliable proxy; a minimal os.Args scan covers the bare flag forms.
//
// Precedence: CHARLY_PROJECT_DIR, then -C/--dir, then --repo / CHARLY_PROJECT_REPO, then cwd.
// Returns "" only when every source is exhausted and os.Getwd() also fails.
//
// --repo IS resolved here. It used to be deliberately skipped, on the reasoning that resolving it
// would fetch a remote repo on every charly invocation and that a remote project's command plugins
// could simply go un-pre-registered. Both halves were wrong: the fetch is attempted ONLY when the
// flag or env var is actually present (a bare invocation never touches the network), and "not
// pre-registered" is not a benign gap — the grammar is frozen by kong.Parse, so an unregistered
// word is an unknown verb, which made --repo unable to reach the very commands it exists to serve.
func projectDirPreParse() string {
	if d := os.Getenv("CHARLY_PROJECT_DIR"); d != "" {
		return d
	}
	if d := scanDirFlag(os.Args); d != "" {
		return d
	}
	// --repo / CHARLY_PROJECT_REPO must resolve HERE, not only at the post-parse chdir in main().
	// The prescan is what puts an external command word into the Kong grammar, and the grammar is
	// frozen by kong.Parse — which runs BEFORE that chdir. Resolving the repo only afterwards left
	// `charly --repo <owner/repo> <word>` reporting an unknown verb from any directory that was not
	// already a project, because the word was looked for in the ORIGINAL cwd. Reading charly.yml
	// worked (it happens after the chdir); finding the verb did not.
	//
	// Resolution can clone, so it is attempted only when the flag or env var is actually present.
	// A failure returns "" and falls through to cwd: main() resolves the same spec moments later
	// and reports the error properly, so a broken --repo must not also lose the local grammar.
	if spec := scanRepoFlag(os.Args); spec != "" {
		if d, err := ResolveProjectRepo(spec); err == nil {
			return d
		}
		// Deliberately NO early return: fall through to cwd. An unresolvable --repo must not
		// also take the LOCAL grammar away — main() resolves the same spec moments later and
		// reports "cannot resolve --repo" properly, which is the error the user needs. Returning
		// "" here instead left the prescan registering nothing, so from inside a real project
		// `charly --repo <bad> mcp …` died on `unexpected argument mcp, did you mean "cp"?` —
		// the unknown-verb dead end this whole cutover exists to remove, reproduced in its own
		// error path.
	} else if envSpec := os.Getenv("CHARLY_PROJECT_REPO"); envSpec != "" {
		if d, err := ResolveProjectRepo(envSpec); err == nil {
			return d
		}
	}
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return ""
}

// scanRepoFlag finds a --repo flag in raw argv (before kong parses), in both "--repo <spec>" and
// "--repo=<spec>" forms. Returns "" if absent. Mirrors scanDirFlag; the two are mutually exclusive
// and main() rejects the combination after parsing.
func scanRepoFlag(args []string) string {
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--repo":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "--repo="):
			return strings.TrimPrefix(a, "--repo=")
		}
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
