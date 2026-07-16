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

// --- the tag-expression filter surface (`--tag` / `--tag-exclude`), which lives ONCE in
// kit so a plugin candy can filter a plan by tag with the SAME grammar the check engine
// uses. Core's call sites (planTagFilter, RunPlan, EffectiveTags) are unchanged.
//
// Only what core actually CALLS is aliased. kit.NormalizeTag and kit.CombineTagFilters are
// part of the kit surface a plugin candy uses, but core reaches neither: normalizeTag's only
// callers (EffectiveTags + the lexer) moved to kit with it, and CombineTagFilters is a CLI
// composition helper core does not invoke. Aliasing them here would be dead code. ---
type TagExpr = kit.TagExpr

var (
	ParseTagExpr  = kit.ParseTagExpr
	EffectiveTags = kit.EffectiveTags
)

// --- the check-result reporters (`charly check` text/JSON/TAP/JUnit output),
// all in kit so a plugin candy can format a plan's results identically. ---
var (
	FormatStepResultsText  = kit.FormatStepResultsText
	FormatStepResultsJSON  = kit.FormatStepResultsJSON
	FormatStepResultsTAP   = kit.FormatStepResultsTAP
	FormatStepResultsJUnit = kit.FormatStepResultsJUnit
)

// --- generic yaml.v3 / path helpers that live ONCE in the importable host-engine
// kit (shared with the out-of-tree plugin candies that also import kit). These thin
// aliases keep core's call sites unchanged. ---
var (
	fileExists           = kit.FileExists
	dirExists            = kit.DirExists
	sortStrings          = kit.SortStrings
	mapValue             = kit.MapValue
	firstYAMLVersionLine = kit.FirstYAMLVersionLine
	isGitSubmoduleDir    = kit.IsGitSubmoduleDir

	scalarNode       = kit.ScalarNode
	findMappingValue = kit.FindMappingValue
	mappingRoot      = kit.MappingRoot
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

// shQuoteArg single-quotes an argument for POSIX shell embedding (re-export).
func shQuoteArg(v string) string { return kit.ShQuoteArg(v) }

// --- the plan-execution helpers (P5-unit-3), all in sdk/kit so a plugin candy that runs a
// plan shares the retry loop, the per-run capture context, and the acceptance-depth ladder.
// Core's call sites are unchanged. ---
type ScenarioContext = kit.ScenarioContext

var (
	ResolveCheckLevel = kit.ResolveCheckLevel
	CheckLevelReaches = kit.CheckLevelReaches
)

const (
	CheckLevelBuild   = kit.CheckLevelBuild
	CheckLevelNoAgent = kit.CheckLevelNoAgent
	CheckLevelAgent   = kit.CheckLevelAgent
	DefaultCheckLevel = kit.DefaultCheckLevel
)

// --- the ${NAME[:arg]} check-variable expansion grammar (P5-unit-4), in sdk/kit so a plugin
// candy that runs a plan expands ${VAR}s with the SAME grammar the check engine uses. ---
// ExpandTestVars is NOT aliased — its only charly/ caller is checkvars_test.go, which calls
// kit.ExpandTestVars directly. kit.ExpandOpVars (formerly aliased here as opExpandVars) has
// no charly/ caller at all, test or production; the binding was removed rather than kept as a
// caller-less export. Aliasing a name only a test uses would itself be a caller-less-in-
// production export.
var (
	TestVarRefs       = kit.TestVarRefs
	IsRuntimeOnlyVar  = kit.IsRuntimeOnlyVar
	collectAnyStrings = kit.CollectAnyStrings
)

// --- the check-engine PLAN WALK (P5): the RunOne/RunPlan/runUnit loop + its result model
// carriers + pure step helpers live ONCE in sdk/kit (planrun.go / planspec.go) so ANY plugin
// candy that runs a plan drives the SAME walk the check engine uses. The Runner is the host
// driver (planrun_adapter.go); the grammar the walk consults (VerbCatalog) + the verb dispatch
// (the provider registry) stay in core behind the kit.PlanContext / kit.VerbResolver seams.
// Only what core actually CALLS is aliased — StepID (the function) has no core caller (only the
// StepResult.StepID field), so it is not aliased. ---
type (
	LabeledDescription  = kit.LabeledDescription
	LabelDescriptionSet = kit.LabelDescriptionSet
	GraderRequest       = kit.GraderRequest
	StepGrader          = kit.StepGrader
)

var (
	keywordOf       = kit.KeywordOf
	EffectiveStepID = kit.EffectiveStepID
	filterHostVars  = kit.FilterHostVars
)
