package main

import (
	"slices"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// The ${NAME[:arg]} check-variable expansion grammar (ExpandTestVars / TestVarRefs /
// IsRuntimeOnlyVar / ExpandOpVars / ExpandAnyVars / CollectAnyStrings / the runtime-var
// prefixes) lives ONCE in sdk/kit (checkvars_expand.go) so a plugin candy that runs a plan
// expands ${VAR}s identically. package main references these directly as kit.X; the
// check SEMANTICS that consult VerbCatalog (opEffectiveDo / opActsInBuildDeploy) stay below.

// ---------------------------------------------------------------------------
// Unified verb vocabulary — execution context, do-mode, and the VerbCatalog
// single source of truth for per-verb legality + lowering.
// ---------------------------------------------------------------------------

// ExecContext is where an op runs. An op's Context list (or its VerbCatalog
// default) declares legality; the active engine supplies the running context
// and skips ops whose context set does not include it (VenueSkip).

// DoMode (the act/assert/instruct axis) lives in sdk/kit alongside the plan walk that
// dispatches on it (kit/planrun.go); these are the package-main bindings. act = perform a
// side-effect; assert = run the matchers (read-only); instruct = hand free-form text to the
// agent grader.
type DoMode = kit.DoMode

const (
	DoAct      = kit.DoAct
	DoAssert   = kit.DoAssert
	DoInstruct = kit.DoInstruct
)

// VerbSpec is the per-verb metadata in VerbCatalog. Contexts[0] is the
// canonical default context. LowersTo names the InstallPlan step kind an
// act-mode op of this verb lowers to ("" → a generic OpStep). Reversible marks
// whether act-mode reversal is automatic (an auto ReverseOp); when false an
// act-mode op needs an explicit `uninstall:` or is reversed via plan
// teardown (live verbs) — enforced in validation.
type VerbSpec struct {
	Contexts   []deploykit.ExecContext
	DefaultDo  DoMode
	Reversible bool
	// LowersTo is gone — the ONLY verbs that lowered into a typed install step
	// (package → SystemPackagesStep, service → ServicePackagedStep) are now extracted
	// plugin verbs whose TypedStepProvider owns the lowering (LowersTo() + ConstructStep
	// on the provider). No remaining VerbCatalog verb lowers into a typed step, so
	// ActsInBuildDeploy reduces to the installVerbs membership test.
}

// HasContext reports whether the verb is legal in ctx.
func (s VerbSpec) HasContext(ctx deploykit.ExecContext) bool {
	return slices.Contains(s.Contexts, ctx)
}

var (
	ctxBuildDeploy        = []deploykit.ExecContext{deploykit.CtxBuild, deploykit.CtxDeploy}
	ctxBuildDeployRuntime = []deploykit.ExecContext{deploykit.CtxBuild, deploykit.CtxDeploy, deploykit.CtxRuntime}
)

// VerbCatalog is the single source of truth for every verb's legality, default
// do-mode, reversibility, and act-mode lowering target — one table driving
// validation, dispatch, and lowering. Keys match spec.OpVerbs (gated by the
// registry bijection in registry.go).
var VerbCatalog = map[string]VerbSpec{
	// install/build — imperative; build+deploy only (no live-runtime form).
	"mkdir":    {ctxBuildDeploy, DoAct, false},
	"copy":     {ctxBuildDeploy, DoAct, true}, // build → COPY, deploy → PutFile (venue-lowered)
	"write":    {ctxBuildDeploy, DoAct, true},
	"link":     {ctxBuildDeploy, DoAct, true},
	"download": {ctxBuildDeploy, DoAct, true},
	"setcap":   {ctxBuildDeploy, DoAct, false},
	"build":    {ctxBuildDeploy, DoAct, false},

	// `command` is NOT here — it is an extracted plugin verb (plugin: command +
	// #CommandInput). It left #OpVerb/spec.OpVerbs/VerbCatalog; the check dispatches via
	// the generic `plugin:` verb and the act renders via the dedicated install-task
	// emitCmd branch (`plugin == "command"` in emitTasks/renderOpCommand/
	// opActsInBuildDeploy), preserving the full command build/deploy install path.

	// file / package / service / unix_group / user / kernel-param / mount are extracted
	// STATE-PROVISION verbs — each BOTH a check AND an act. They left #Op/spec.OpVerbs for
	// their builtin plugin units (candy/plugin-{file,package,service,unix_group,
	// user,kernel_param,mount}) and dispatch via the generic `plugin:` verb, so they have no
	// VerbCatalog entry. `package` and `service` are the TYPED-STEP verbs: each act lowers
	// into a SystemPackagesStep / ServicePackagedStep via the TypedStepProvider (its
	// LowersTo() + ConstructStep now live on the provider, NOT this catalog) so the
	// load-bearing reversals survive; file + the other four render at install emit via the
	// act-emit enabler (resolveProvisionScript — file's act is the RUNTIME touch+chmod
	// file-creation, distinct from the write/copy BUILD-time COPY directives). http /
	// interface / addr are observe-only goss verbs likewise extracted
	// (candy/plugin-{http,interface,addr}).

	// live-container — runtime only. EVERY live-container verb is now an
	// EXTERNAL-CHARLY-VERB served out-of-process; none has a VerbCatalog entry, and
	// none is a field on core #Op. Each left #OpVerb/spec.OpVerbs/VerbCatalog (no in-proc
	// CheckVerbProvider) and is authored as the generic `<word>: <input>` sugar, desugared
	// to `plugin`/`plugin_input` before #Op validates; its method enum + input schema live
	// in its own plugin's #<Word>Input def (served over Describe), NOT on core #Op (the
	// authored YAML shape is unchanged — only the schema's HOME moved to the plugin). The
	// registered external provider resolves at dispatch; each verb's context legality lives
	// on the authored `context:` + the plugin's own box-mode skip, not this table. Per-verb
	// specifics (candy that serves it; what the host pre-resolves):
	// `wl` (candy/plugin-wl) — EXEC-based (like record/dbus), driving the venue's compositor
	//   (wlrctl/grim/wtype/swaymsg) over the executor reverse channel; the screenshot PNG
	//   pulls via GetFile.
	// `dbus` (candy/plugin-dbus) — EXEC-based, driving the venue's session bus with gdbus
	//   (never godbus — a STRUCTURAL externalization, not a dep-shed) over the reverse channel.
	// `vnc` (candy/plugin-vnc) — the host pre-resolves the deployment's VNC endpoint
	//   (container port 5900 or a VM's libvirt <graphics type='vnc'> listener) to a
	//   host-reachable RFB address first.
	// `cdp` (candy/plugin-cdp) — the host pre-resolves the deployment's CDP port 9222 to a
	//   host-reachable DevTools base URL first.
	// `record` (candy/plugin-record) — EXEC-based, driving the venue over the executor
	//   reverse channel (RunCapture/GetFile).
	// `mcp` (candy/plugin-mcp) — the host pre-resolves the deployment's declared mcp_provides
	//   + the picked dial endpoint first.
	// `libvirt` (candy/plugin-vm) — the host pre-resolves any VM display endpoint host-side.
	// `kube` (candy/plugin-kube) — the host pre-resolves any --cluster profile to a
	//   kubeconfig context first.
	// `adb` (candy/plugin-adb) — the registered external provider resolves at dispatch.
	// `appium` (candy/plugin-appium) — the registered external provider resolves at dispatch.
	// `spice` (candy/plugin-spice) — the host pre-resolves the VM's live SPICE endpoint to a
	//   dialable address first.

	// meta.

	// plugin — the generic plugin-verb discriminator. Its VALUE (Op.Plugin) is the
	// reserved word served by a registered Provider (built-in or out-of-tree). The
	// handler is runOne's providerRegistry.ResolveVerb dispatch; context is
	// permissive (a plugin verb may probe at build/deploy/runtime — the plugin's
	// own check declares where it applies). DoAssert (a check), not reversible.
	"plugin": {ctxBuildDeployRuntime, DoAssert, false},
}

// installVerbs are the verbs that render directly to a generic OpStep install
// step (a Containerfile directive at build, a deploy shell command at deploy).
// The verbs that lowered into a TYPED install step (package/service) are now extracted
// plugin verbs whose TypedStepProvider owns the lowering — handled by opActsInBuildDeploy,
// not this map.
var installVerbs = map[string]bool{
	"mkdir": true, "copy": true, "write": true, "link": true,
	"download": true, "setcap": true, "build": true,
	// `command` is NOT here — it is a plugin verb now; its build/deploy install path is
	// the dedicated `plugin == "command"` emitCmd branch, accepted by opActsInBuildDeploy
	// directly (not via this map, which is keyed by the verb the Op resolves to, never
	// "command" again).
}

// opActsInBuildDeploy reports whether the PLUGIN op c's act form has a real build/deploy install path.
// The former non-plugin (install-verb) arm was removed with the validate ENGINE (task #60): the ONLY
// caller left is the validate host-fill (fillValidateWordSets), which always asks about a `plugin:` op —
// install-verb act-capability now lives in the plugin's own installVerbSet, never here. `plugin: command`
// is act-capable via the dedicated emitCmd install path (NOT a ProvisionActor); every other plugin verb
// acts when its registered provider is a ProvisionActor / TypedStepProvider / BuildEmitter, or
// (standalone, provider not connected) when the parse-time prescan saw a plugin candy declare it.
func opActsInBuildDeploy(c *spec.Op) bool {
	if c.Plugin == "command" {
		return true
	}
	// A class:STEP plugin word (F3's external step KIND) lowers to an externalStep that ACTS at DEPLOY
	// (compileActOp resolve(ClassStep) → externalStep → OpExecute). Recognized via a connected ClassStep
	// provider OR a post-scan declaration (standalone `charly box validate`, where the step plugin is not
	// connected) — the step analogue of the verb handling below.
	if _, ok := providerRegistry.ResolveStep(c.Plugin); ok {
		return true
	}
	if isDeclaredExternalStep(c.Plugin) {
		return true
	}
	prov, ok := providerRegistry.ResolveVerb(c.Plugin)
	if !ok {
		// Not connected — the standalone `charly box validate` path, where external plugins are not
		// built+connected. Trust a verb the parse-time prescan saw a plugin candy declare
		// (registerDeclaredExternalVerb): it is build-emit-capable until the BUILD (which DOES connect it
		// via the connect seam) proves otherwise at emitPluginFragment's empty-fragment guard. A BUILTIN
		// verb always resolves above, so this branch is reached only for a genuinely external,
		// not-yet-connected verb — never for a runtime-only builtin (which is correctly rejected).
		return isDeclaredExternalVerb(c.Plugin)
	}
	// A ProvisionActor renders an install shell; a TypedStepProvider (service) lowers into a typed
	// install step; a BuildEmitter (an in-proc build-emit verb) renders a Containerfile fragment via
	// Invoke(OpEmit). Each is a real build/deploy act path — the same capability whether the plugin is
	// builtin or external (placement-agnostic).
	if _, isActor := prov.(ProvisionActor); isActor {
		return true
	}
	if _, isTyped := prov.(TypedStepProvider); isTyped {
		return true
	}
	if _, isEmitter := prov.(BuildEmitter); isEmitter {
		return true
	}
	// A CONNECTED external (out-of-process) verb is build-emit-capable via Invoke(OpEmit); the host
	// cannot type-assert capability across the process boundary, so it is trusted here and gated at build
	// by emitPluginFragment's empty-fragment guard.
	_, isExternal := prov.(*grpcProvider)
	return isExternal
}

// stampStepIntentDo writes the keyword-derived do-mode onto a verb-carrying step's Op.IntentDo (via
// the shared kit.StepDoMode derivation). It is the package-main entry the label bake (bakeableSteps)
// calls so the baked ai.opencharly.description carries intent_do deterministically — formerly a SIDE
// EFFECT of the in-core validate mutating the shared structs, which died when the validate ENGINE
// moved to candy/plugin-box (K3-D+). Kept here (checkspec.go already imports kit + owns the do-mode
// logic) so description_collect.go needs no new kit import; verb-less agent-check steps stay empty.
func stampStepIntentDo(s *spec.Step) {
	if len(s.VerbsSet()) == 0 {
		return
	}
	s.IntentDo = string(kit.StepDoMode(s))
}

// EffectiveDo returns the op's resolved do-mode: the keyword-stamped intentDo
// wins (set by the enclosing Step at run/collect time), else the verb's
// VerbCatalog default, else DoAssert.
func opEffectiveDo(c *spec.Op) DoMode {
	switch DoMode(c.IntentDo) {
	case DoAct, DoAssert, DoInstruct:
		return DoMode(c.IntentDo)
	}
	verb, err := c.Kind()
	if err == nil {
		if spec, ok := VerbCatalog[verb]; ok && spec.DefaultDo != "" {
			return spec.DefaultDo
		}
	}
	return DoAssert
}

// EffectiveContexts returns the op's resolved execution contexts: an explicit
// Context wins, else the verb's VerbCatalog default, else nil.
func opEffectiveContexts(c *spec.Op) []deploykit.ExecContext {
	if len(c.Context) > 0 {
		out := make([]deploykit.ExecContext, 0, len(c.Context))
		for _, s := range c.Context {
			out = append(out, deploykit.ExecContext(s))
		}
		return out
	}
	if verb, err := c.Kind(); err == nil {
		if spec, ok := VerbCatalog[verb]; ok {
			return spec.Contexts
		}
	}
	return nil
}

// InContext reports whether the op is legal in ctx per its effective contexts.
func opInContext(c *spec.Op, ctx deploykit.ExecContext) bool {
	return slices.Contains(opEffectiveContexts(c), ctx)
}

// Inject the VerbCatalog-coupled op-context classifier into deploykit's swappable seam
// (deploykit itself holds no VerbCatalog — that vocabulary is core, reserved_registry.go).
func init() { deploykit.OpInContext = opInContext }
