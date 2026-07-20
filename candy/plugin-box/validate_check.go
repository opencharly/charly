package box

// validate_check.go — the Op-level plan validation (task #60, Unit C; former charly validate_check.go).
// validateOps validates every Op embedded in a candy/box plan step; validateCheck runs the per-op
// context/do-mode/runtime-var/kube-lowercase checks. The verb CATALOG (context legality + default
// do-mode) is DERIVED from spec.OpVerbs (ruling d) — no host VerbCatalog. The act-capability arm
// (opActsInBuildDeploy) CONSUMES the host-projected D-data word set ResolvedProject.ActCapableVerbs
// (the distinct plugin words whose act form has a build/deploy install path — the host type-asserts
// ProvisionActor/TypedStep/BuildEmitter + connected/declared externals + command exactly as core's
// opActsInBuildDeploy does), so the builtin-rejection behaviour is preserved WITHOUT the plugin ever
// dialing the host registry. No "assume act-capable" default.

import (
	"fmt"
	"regexp"
	"slices"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// lowercaseCheckVarPattern matches a ${name} token whose identifier begins lowercase — never a check
// variable (the expander only recognizes UPPERCASE), so it reaches the verb literally.
var lowercaseCheckVarPattern = regexp.MustCompile(`\$\{[a-z][a-zA-Z0-9_]*\}`)

// verbSpec is the per-verb legality metadata: the contexts the verb may run in + its default do-mode.
type verbSpec struct {
	contexts  []spec.ExecContext
	defaultDo spec.DoMode
}

func (s verbSpec) hasContext(ctx spec.ExecContext) bool {
	return slices.Contains(s.contexts, ctx)
}

// verbCatalog is DERIVED from spec.OpVerbs (ruling d): every install verb (OpVerbs minus "plugin")
// acts in build+deploy (DoAct); "plugin" is an assert legal at build+deploy+runtime. It replaces the
// host VerbCatalog — the context/do-mode legality is a pure function of the CUE-sourced verb vocabulary.
var verbCatalog = buildVerbCatalog()

func buildVerbCatalog() map[string]verbSpec {
	m := make(map[string]verbSpec, len(spec.OpVerbs))
	for _, v := range spec.OpVerbs {
		if v == "plugin" {
			m[v] = verbSpec{contexts: []spec.ExecContext{spec.CtxBuild, spec.CtxDeploy, spec.CtxRuntime}, defaultDo: spec.DoAssert}
		} else {
			m[v] = verbSpec{contexts: []spec.ExecContext{spec.CtxBuild, spec.CtxDeploy}, defaultDo: spec.DoAct}
		}
	}
	return m
}

// installVerbSet is spec.OpVerbs minus "plugin" — the verbs whose act form is a build/deploy install path.
var installVerbSet = buildInstallVerbSet()

func buildInstallVerbSet() map[string]bool {
	m := make(map[string]bool, len(spec.OpVerbs))
	for _, v := range spec.OpVerbs {
		if v != "plugin" {
			m[v] = true
		}
	}
	return m
}

// validateOps validates every Op embedded in a candy/box plan step (agent-*/include steps carry no
// verb and are skipped). The step keyword's do-mode is stamped onto the op first (mirroring runUnit) so
// the act-form rules are keyword-aware.
func validateOps(vc *vctx, e *vErr) {
	validatePlanOps := func(plan []spec.Step, who string) {
		for i := range plan {
			op := &plan[i].Op
			if len(op.VerbsSet()) == 0 {
				continue // agent / include / verb-less step
			}
			op.IntentDo = string(spec.StepDoMode(&plan[i]))
			validateCheck(vc, op, fmt.Sprintf("%s step[%d]", who, i), e)
		}
	}
	for name := range vc.models {
		validatePlanOps(vc.models[name].Plan, fmt.Sprintf("candy %q", name))
	}
	for name := range vc.boxes {
		validatePlanOps(vc.boxes[name].Plan, fmt.Sprintf("box %q", name))
	}
}

// validateCheck runs the per-op rules: exactly-one-verb, context legality, act-form legality, build-
// context runtime-var references, and the kube lowercase-${} guard.
func validateCheck(vc *vctx, c *spec.Op, loc string, e *vErr) {
	verb, err := c.Kind()
	if err != nil {
		e.Add("%s: %v", loc, err)
		return
	}
	if vs, ok := verbCatalog[verb]; ok {
		for _, ctx := range opEffectiveContexts(c) {
			if !vs.hasContext(ctx) {
				e.Add("%s: verb %q is not legal in context %q", loc, verb, ctx)
			}
		}
	}

	// A do:act op must have a real install path in each build/deploy context it claims.
	if opEffectiveDo(c) == spec.DoAct && !opActsInBuildDeploy(vc, c, verb) {
		for _, ctx := range opEffectiveContexts(c) {
			if ctx == spec.CtxBuild || ctx == spec.CtxDeploy {
				e.Add("%s: verb %q cannot act (do: act) in %s context — its act form is runtime-only (use context: [runtime]); create files in build/deploy with the write/copy verbs", loc, verb, ctx)
			}
		}
	}

	// Runtime-only variable references are illegal in a build-legal op.
	if opInContext(c, spec.CtxBuild) {
		for _, r := range collectCheckRefs(c) {
			if kit.IsRuntimeOnlyVar(r) {
				e.Add("%s: references runtime-only variable ${%s} but scope is build — mark as scope: deploy or use scope:deploy-only attributes", loc, r)
			}
		}
	}

	// A lowercase ${...} in a kube identifier field never resolves (the expander is UPPERCASE-only).
	if c.Plugin == "kube" {
		for k, v := range c.PluginInput {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if m := lowercaseCheckVarPattern.FindString(s); m != "" {
				e.Add("%s: %s contains %s — check variables are UPPERCASE (e.g. ${DEPLOY_NAME}); a lowercase ${...} never resolves and is passed through literally", loc, k, m)
			}
		}
	}
}

// opEffectiveContexts returns the op's resolved execution contexts: an explicit context: wins, else the
// verb's catalog default, else nil.
func opEffectiveContexts(c *spec.Op) []spec.ExecContext {
	if len(c.Context) > 0 {
		out := make([]spec.ExecContext, 0, len(c.Context))
		for _, s := range c.Context {
			out = append(out, spec.ExecContext(s))
		}
		return out
	}
	if verb, err := c.Kind(); err == nil {
		if vs, ok := verbCatalog[verb]; ok {
			return vs.contexts
		}
	}
	return nil
}

// opEffectiveDo returns the op's resolved do-mode: the keyword-stamped intent_do wins, else the verb's
// catalog default, else DoAssert.
func opEffectiveDo(c *spec.Op) spec.DoMode {
	switch spec.DoMode(c.IntentDo) {
	case spec.DoAct, spec.DoAssert, spec.DoInstruct:
		return spec.DoMode(c.IntentDo)
	}
	if verb, err := c.Kind(); err == nil {
		if vs, ok := verbCatalog[verb]; ok && vs.defaultDo != "" {
			return vs.defaultDo
		}
	}
	return spec.DoAssert
}

// opInContext reports whether the op is legal in ctx per its effective contexts.
func opInContext(c *spec.Op, ctx spec.ExecContext) bool {
	return slices.Contains(opEffectiveContexts(c), ctx)
}

// opActsInBuildDeploy reports whether a do:act op with this verb has a build/deploy install path. A
// non-plugin verb acts iff it is an install verb (spec.OpVerbs minus "plugin"). A `plugin:` verb acts
// iff it is `plugin: command` OR its word is a member of the host-projected ResolvedProject.ActCapableVerbs
// D-data set (the WORD SET the host fills by type-asserting ProvisionActor/TypedStep/BuildEmitter +
// connected/declared externals + command, exactly as core's opActsInBuildDeploy does). No registry dial,
// no "assume act-capable" default — a plugin word absent from ActCapableVerbs is a build/deploy do:act
// rejection, matching core's builtin-rejection behaviour.
func opActsInBuildDeploy(vc *vctx, c *spec.Op, verb string) bool {
	if verb != "plugin" {
		return installVerbSet[verb]
	}
	if c.Plugin == "command" {
		return true
	}
	var actCapable []string
	if vc.env != nil {
		actCapable = vc.env.ActCapableVerbs
	}
	return slices.Contains(actCapable, c.Plugin)
}

// collectCheckRefs returns every ${NAME[:arg]} referenced across the op's string fields + plugin_input.
func collectCheckRefs(c *spec.Op) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		for _, r := range kit.TestVarRefs(s) {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	for _, p := range c.StringFields() {
		add(*p)
	}
	for _, s := range kit.CollectAnyStrings(c.PluginInput) {
		add(s)
	}
	return out
}
