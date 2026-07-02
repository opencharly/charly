package kit

// step_keyword.go — the plan step-keyword constants, shared by charly core
// (description_spec.go) AND out-of-tree plugin candies via the importable kit
// (R3 — ONE copy across the module boundary). The StepKeyword TYPE lives
// in spec (union_types.go); these are the wire-keyword constant values. Core +
// candy each alias them.

import "github.com/opencharly/charly/charly/spec"

const (
	KwRun        spec.StepKeyword = "run"
	KwCheck      spec.StepKeyword = "check"
	KwAgentRun   spec.StepKeyword = "agent-run"
	KwAgentCheck spec.StepKeyword = "agent-check"
	KwInclude    spec.StepKeyword = "include"
)
