package check

// substitute.go — ${TOKEN} substitution for the harness (P12: relocated from
// charly/check_substitute.go).
//
// Substitution precedence (definitive):
//
//	well-known tokens (PROMPT, ITERATION, SCORE_DELTA, ...) →
//	score.env[KEY] → ai.env[KEY] → os.Getenv(KEY) → ""
//
// "Well-known" is a fixed set in lookupHarnessToken; everything else falls through
// the env chain. Substitution is single-pass (no recursive expansion).

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"

	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// SubstContext carries every variable Substitute can expand.
type SubstContext struct {
	// Run identity
	RunID     string
	ScoreName string
	AgentName string

	// Workspace + target
	WorkspacePath string
	TargetImage   string
	TargetKind    string // "pod" | "vm" | "host"
	TargetName    string // pod or vm name (empty when TargetKind == "host")

	// Iteration loop state
	Iteration        int
	PlateauIteration int
	PlateauCounter   int
	BestScore        int
	ScoreDelta       int
	AttemptsLeft     int

	// Prompt + filter
	Prompt     string // rendered prompt text (for ${PROMPT})
	PromptFile string // when PromptVia == "file"
	Tag        string // tag filter expression

	// MCP endpoint (drives the canonical ${MCP_ENDPOINT} substitution)
	MCPEndpoint string

	// Persistent NOTES.md content (drives ${NOTES})
	Notes string

	// Full plan (check:/agent-check: + agent-run:) rendered as YAML (drives ${PLAN})
	Plan string

	// Still-unsolved check:/agent-check: subset rendered as YAML (drives ${CHECKS})
	Checks string

	// Progressive-scoring phase state. Zero-valued and ignored otherwise.
	Phase      int    // 1-indexed current phase number
	PhaseTotal int    // total number of phases
	PhaseIntro string // pre-rendered phase preamble

	// Deployment name the harness scores against (drives ${DEPLOYMENT})
	Deploy string

	// Timing
	Deadline string // RFC3339 string, or "" when no deadline
	Timeout  string // per-AI resolved timeout string

	// EnvChain is walked in order for any token not in the well-known set.
	EnvChain []map[string]string
}

// AppendEnv appends a single env map to the chain.
func (c *SubstContext) AppendEnv(m map[string]string) {
	if len(m) == 0 {
		return
	}
	c.EnvChain = append(c.EnvChain, m)
}

// harnessTokenRe matches ${IDENT} where IDENT follows shell convention.
var harnessTokenRe = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// Substitute replaces every ${TOKEN} in `in` using ctx. Single-pass — no recursive
// expansion. Unresolved tokens become "".
func Substitute(in string, ctx *SubstContext) string {
	if ctx == nil {
		ctx = &SubstContext{}
	}
	return harnessTokenRe.ReplaceAllStringFunc(in, func(match string) string {
		return lookupHarnessToken(match[2:len(match)-1], ctx)
	})
}

// SubstituteArgv applies Substitute to every element of argv.
func SubstituteArgv(argv []string, ctx *SubstContext) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		out[i] = Substitute(a, ctx)
	}
	return out
}

// SubstituteEnv applies Substitute to every value in env.
func SubstituteEnv(env map[string]string, ctx *SubstContext) map[string]string {
	if env == nil {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = Substitute(v, ctx)
	}
	return out
}

// lookupHarnessToken resolves one token name (well-known table → EnvChain →
// os.Getenv → "").
func lookupHarnessToken(name string, ctx *SubstContext) string {
	switch name {
	case "PROMPT":
		return ctx.Prompt
	case "PROMPT_FILE":
		return ctx.PromptFile
	case "WORKSPACE":
		return ctx.WorkspacePath
	case "TARGET_IMAGE":
		return ctx.TargetImage
	case "TARGET_KIND":
		return ctx.TargetKind
	case "TARGET_NAME":
		return ctx.TargetName
	case "RUN_ID":
		return ctx.RunID
	case "SCORE_NAME":
		return ctx.ScoreName
	case "AI_NAME":
		return ctx.AgentName
	case "ITERATION":
		return intTok(ctx.Iteration)
	case "PLATEAU_ITERATION":
		return intTok(ctx.PlateauIteration)
	case "PLATEAU_COUNTER":
		return intTok(ctx.PlateauCounter)
	case "BEST_SCORE":
		return intTok(ctx.BestScore)
	case "SCORE_DELTA":
		return intTok(ctx.ScoreDelta)
	case "ATTEMPTS_LEFT":
		return intTok(ctx.AttemptsLeft)
	case "MCP_ENDPOINT":
		return ctx.MCPEndpoint
	case "NOTES":
		return ctx.Notes
	case "PLAN":
		return ctx.Plan
	case "CHECKS":
		return ctx.Checks
	case "PHASE":
		return intTok(ctx.Phase)
	case "PHASE_TOTAL":
		return intTok(ctx.PhaseTotal)
	case "PHASE_INTRO":
		return ctx.PhaseIntro
	case "DEPLOYMENT":
		return ctx.Deploy
	case "TAG":
		return ctx.Tag
	case "DEADLINE":
		return ctx.Deadline
	case "TIMEOUT":
		return ctx.Timeout
	}

	for _, m := range ctx.EnvChain {
		if v, ok := m[name]; ok {
			return v
		}
	}
	return os.Getenv(name)
}

// intTok stringifies an int for substitution.
func intTok(n int) string {
	return fmt.Sprintf("%d", n)
}

// ---------------------------------------------------------------------------
// ${EVAL_NONCE_<NAME>} substitution — per-run randomized nonces the AI never
// sees. Plan authors use these tokens in steps that require cross-pod traffic at
// scoring time so the AI cannot pre-set the expected key/value via shortcut paths.
// ---------------------------------------------------------------------------

// nonceTokenRe matches ${EVAL_NONCE_<NAME>} where NAME is uppercase alphanumeric +
// underscore.
var nonceTokenRe = regexp.MustCompile(`\$\{EVAL_NONCE_([A-Z0-9_]+)\}`)

// GenerateHarnessNonces walks the plan steps via yaml.Marshal, finds every unique
// ${EVAL_NONCE_<NAME>} reference, and assigns each NAME a fresh 16-hex-char value
// from crypto/rand (64 bits of entropy). Returns an empty map if none are found.
func GenerateHarnessNonces(plan []spec.Step) (map[string]string, error) {
	data, err := yaml.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("marshal plan for nonce discovery: %w", err)
	}
	nonces := map[string]string{}
	for _, m := range nonceTokenRe.FindAllSubmatch(data, -1) {
		name := string(m[1])
		if _, seen := nonces[name]; seen {
			continue
		}
		buf := make([]byte, 8) // 8 bytes → 16 hex chars
		if _, err := rand.Read(buf); err != nil {
			return nil, fmt.Errorf("generate nonce %q: %w", name, err)
		}
		nonces[name] = hex.EncodeToString(buf)
	}
	return nonces, nil
}

// SubstituteStepNonces returns a new slice of plan steps with all
// ${EVAL_NONCE_<NAME>} tokens replaced by nonces[NAME]. Tokens whose NAME isn't in
// the map are left untouched (they surface at scoring time as failed verbs).
//
// Implemented via a JSON round-trip (json.Marshal → regex replace on the JSON bytes
// → json.Unmarshal): every shorthand wire type carried by a Step's inline Op
// (spec.Matcher / spec.MatcherList / spec.PortScope / spec.EphemeralLifetime) ships
// SYMMETRIC MarshalJSON + UnmarshalJSON — the shorthand-aware read path the core
// loader's CUE re-decode (decodeEntityViaCUE, package-main and not plugin-importable)
// exists to provide — so a self-contained JSON round-trip round-trips the plan
// faithfully with no loader coupling. Re-stamps Op.Origin after round-trip (it is
// json:"-" so it would otherwise drop).
func SubstituteStepNonces(plan []spec.Step, nonces map[string]string) ([]spec.Step, error) {
	if len(nonces) == 0 {
		return plan, nil
	}
	data, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("marshal plan: %w", err)
	}
	substituted := nonceTokenRe.ReplaceAllFunc(data, func(match []byte) []byte {
		sub := nonceTokenRe.FindSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		if v, ok := nonces[string(sub[1])]; ok {
			return []byte(v)
		}
		return match
	})
	var out []spec.Step
	if err := json.Unmarshal(substituted, &out); err != nil {
		return nil, fmt.Errorf("decode substituted plan: %w", err)
	}
	for i := range out {
		if i < len(plan) {
			out[i].Origin = plan[i].Origin
		}
	}
	return out, nil
}
