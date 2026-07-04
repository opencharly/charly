package main

import (
	"fmt"

	"github.com/opencharly/sdk/kit"
)

// kit_aliases.go — package-main bindings onto generic helpers that live ONCE in the importable
// host-engine kit (github.com/opencharly/sdk/kit), shared with the out-of-tree
// plugin candies that also import kit. These thin aliases keep core's call sites unchanged after
// FU-11 consolidated the former core↔plugin pure-helper duplication (shellSingleQuote was already
// byte-identical to kit.ShellQuote; wrapContainerCommand moved into kit).
var (
	shellSingleQuote = kit.ShellQuote
	// shellQuote is the brevity alias used across the build / deploy / notify call sites
	// (formerly defined in wl.go, FU-14 folded onto kit.ShellQuote); it moved here when the
	// `wl` verb externalized and wl.go was deleted. (The externalized `charly tmux` shell-back
	// quoter now lives in candy/plugin-tmux, importing kit.ShellQuote directly — R3.)
	shellQuote           = kit.ShellQuote
	wrapContainerCommand = kit.WrapContainerCommand
)

// --- generic yaml.v3 / path helpers that live ONCE in the importable host-engine
// kit (shared with the out-of-tree plugin candies that also import kit). These thin
// aliases keep core's call sites unchanged. ---
var (
	fileExists           = kit.FileExists
	dirExists            = kit.DirExists
	sortStrings          = kit.SortStrings
	firstNonEmpty        = kit.FirstNonEmpty
	mapValue             = kit.MapValue
	nodeShapedValue      = kit.NodeShapedValue
	firstYAMLVersionLine = kit.FirstYAMLVersionLine
	isGitSubmoduleDir    = kit.IsGitSubmoduleDir

	scalarNode            = kit.ScalarNode
	findMappingValue      = kit.FindMappingValue
	opUnifyCandidateFiles = kit.OpUnifyCandidateFiles
)

// EnvdDir is exported (used across deploy code); alias the kit copy.
func EnvdDir(hostHome string) string { return kit.EnvdDir(hostHome) }

// --- op→shell render helpers (moved into kit when the local deploy target externalized) ---

// renderOpCommand turns a non-copy OpStep into a shell command. The structured verbs
// (command/plugin:command/mkdir/link/setcap/write/download) render via the SHARED pure
// kit.RenderOpCommand; an act-`plugin:` verb (a builtin ProvisionActor) renders via the
// in-proc registry (resolveProvisionScript) — the SAME seam the build/runtime act paths
// use (R3). copy is staged via the executor's PutFile, never rendered. The ONE op→shell
// render copy is kit's; the in-proc deploy path calls this wrapper, an out-of-process
// deploy plugin's kit.WalkPlans calls kit.RenderOpCommand directly.
func renderOpCommand(s *OpStep) (string, error) {
	if s.Op == nil {
		return "", fmt.Errorf("renderOpCommand: nil op")
	}
	if s.Op.Copy != "" {
		return "", fmt.Errorf("copy: task must be staged via PutFile, not rendered")
	}
	if cmd, handled := kit.RenderOpCommand(s.Op, s.CtxPath, s.CandyVars); handled {
		return cmd, nil
	}
	// Not a pure-renderable verb → an act-`plugin:` verb whose ProvisionActor shell needs
	// the in-proc registry. ok=false means the verb has no act form (a run: step naming a
	// non-act verb has no install path — a hard authoring error).
	script, ok := resolveProvisionScript(s.Op, s.Distros)
	if !ok {
		return "", fmt.Errorf("run: plugin verb %q is not act-capable (no ProvisionActor)", s.Op.Plugin)
	}
	return script, nil
}

// parseTaskMode parses a candy task mode string ("0644","0o755") into a uint32 file mode (re-export).
func parseTaskMode(mode string, def uint32) uint32 { return kit.ParseTaskMode(mode, def) }

// shQuoteArg single-quotes an argument for POSIX shell embedding (re-export).
func shQuoteArg(v string) string { return kit.ShQuoteArg(v) }
