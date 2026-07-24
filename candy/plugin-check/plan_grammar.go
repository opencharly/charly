package check

// plan_grammar.go — K1-unblock W3 Unit B: the plugin-side kit.PlanGrammar, mirroring
// charly/planrun_adapter.go's hostPlanGrammar + charly/checkspec.go's opEffectiveDo/opInContext/
// opEffectiveContexts. Confirmed portable by reading the core originals in full: all three
// functions are pure over spec.Op + the portable spec.VerbCatalog vocabulary table — zero
// core-only dependency (no provider registry, no *Config, no LoadUnified). Ported, not aliased,
// since charly/checkspec.go's versions are unexported.

import (
	"fmt"
	"slices"

	"github.com/opencharly/sdk/spec"
)

// pluginPlanGrammar implements kit.PlanGrammar. Stateless — a package-level value suffices.
type pluginPlanGrammar struct{}

// EffectiveDo resolves op's do-mode: the keyword-stamped intentDo wins, else the verb's
// VerbCatalog default, else DoAssert. Ported unchanged from charly/checkspec.go's opEffectiveDo.
func (pluginPlanGrammar) EffectiveDo(op *spec.Op) spec.DoMode {
	switch spec.DoMode(op.IntentDo) {
	case spec.DoAct, spec.DoAssert, spec.DoInstruct:
		return spec.DoMode(op.IntentDo)
	}
	verb, err := op.Kind()
	if err == nil {
		if vs, ok := spec.VerbCatalog[verb]; ok && vs.DefaultDo != "" {
			return vs.DefaultDo
		}
	}
	return spec.DoAssert
}

// InContext reports whether op is legal in the run's active context: runtime=true → the live
// (runtime) context, runtime=false → the box (build) context. Ported unchanged from
// charly/planrun_adapter.go's hostPlanGrammar.InContext (which itself delegates to
// checkspec.go's opInContext/opEffectiveContexts).
func (pluginPlanGrammar) InContext(op *spec.Op, runtime bool) bool {
	wantCtx := spec.CtxBuild
	if runtime {
		wantCtx = spec.CtxRuntime
	}
	return slices.Contains(effectiveContexts(op), wantCtx)
}

// ContextsLabel is op's effective-contexts list pre-formatted for the context-skip message — the
// SAME %v rendering the core hostPlanGrammar uses, so the message is byte-identical.
func (pluginPlanGrammar) ContextsLabel(op *spec.Op) string {
	return fmt.Sprintf("%v", effectiveContexts(op))
}

// effectiveContexts returns op's resolved execution contexts: an explicit Context wins, else the
// verb's VerbCatalog default, else nil. Ported unchanged from charly/checkspec.go's
// opEffectiveContexts.
func effectiveContexts(op *spec.Op) []spec.ExecContext {
	if len(op.Context) > 0 {
		out := make([]spec.ExecContext, 0, len(op.Context))
		for _, s := range op.Context {
			out = append(out, spec.ExecContext(s))
		}
		return out
	}
	if verb, err := op.Kind(); err == nil {
		if vs, ok := spec.VerbCatalog[verb]; ok {
			return vs.Contexts
		}
	}
	return nil
}
