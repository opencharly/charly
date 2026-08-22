package spec

// checklevel.go — the per-box acceptance-depth ladder, RELOCATED to the spec contract module
// (#55 CHECK-ENGINE cone Option A — a pure string-ladder classifier candy/plugin-check's bed
// session (bed_session.go, #55 W3 B2-full) reaches importing zero kit). sdk/kit re-exports each
// symbol (sdk/kit/checklevel.go) so every existing kit.CheckLevel* / kit.CheckLevelReaches /
// kit.ResolveCheckLevel / kit.IsValidCheckLevel call site (charly core + plugins + sdk) is
// untouched. New consumers reference spec.* directly.
//
// CheckLevel controls how deep `charly check run <bed>` drives a box's acceptance, gated by
// the do:/context: axes of its Op steps:
//
//	none    — skip acceptance entirely
//	build   — build-context ops only (charly check box)
//	noagent — build + deploy + runtime act/assert, NO do: instruct (default)
//	agent   — also run do: instruct steps through the agent grader
//
// Authored as BoxConfig.CheckLevel; baked into the ai.opencharly.check_level capability label
// so the bed runner reads the rung from the built image without the source repo. Lives in spec
// so a plugin candy that drives acceptance shares the one ladder.

// The per-box acceptance-depth rungs.
const (
	CheckLevelNone    = "none"
	CheckLevelBuild   = "build"
	CheckLevelNoAgent = "noagent"
	CheckLevelAgent   = "agent"
)

// DefaultCheckLevel is the rung applied when a box declares no check_level.
const DefaultCheckLevel = CheckLevelNoAgent

// checkLevelRank orders the ladder so a runner can ask "does this rung reach
// build/deploy/runtime/agent depth?" by numeric comparison instead of a scattered string
// switch (R3 — one source of truth for the ordering).
var checkLevelRank = map[string]int{
	CheckLevelNone:    0,
	CheckLevelBuild:   1,
	CheckLevelNoAgent: 2,
	CheckLevelAgent:   3,
}

// ResolveCheckLevel normalizes an authored check_level to a canonical rung, applying the
// default for the empty value. An unknown value is returned verbatim so the validator flags
// it — never silently defaulted.
func ResolveCheckLevel(level string) string {
	if level == "" {
		return DefaultCheckLevel
	}
	return level
}

// IsValidCheckLevel reports whether level is one of the four canonical rungs.
func IsValidCheckLevel(level string) bool {
	_, ok := checkLevelRank[level]
	return ok
}

// CheckLevelReaches reports whether a box resolved to `have` runs at least as deep as `want`
// — e.g. CheckLevelReaches(boxLevel, CheckLevelNoAgent) gates the runtime live pass,
// CheckLevelReaches(boxLevel, CheckLevelAgent) gates the agent grader. Both operands are
// normalized through ResolveCheckLevel first.
func CheckLevelReaches(have, want string) bool {
	return checkLevelRank[ResolveCheckLevel(have)] >= checkLevelRank[ResolveCheckLevel(want)]
}
