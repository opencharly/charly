package spec

// verb_context.go — DoMode + ExecContext, the two check/plan vocabulary enums shared by
// every consumer that needs to classify an Op/Step (the plan walk in sdk/kit, the deploy-plan
// compiler in sdk/deploykit, charly core's VerbCatalog + registry-coupled semantics, and
// candy/plugin-box's independent validate-engine re-derivation) — moved here (K3, task #39)
// so none of them need to import a MECHANISM kit just for these types. Hand-written (not
// CUE-sourced): simple internal vocabulary enums, not an authored charly.yml surface.
//
// VerbCatalog (below, FLOOR-SLIM Unit 4) is the DATA half of the same vocabulary — moved
// from charly/checkspec.go so charly core's registry-coupled consumers (reserved_registry.go's
// verb-bijection gate, provider_verb.go's installVerbs membership test) reference this
// package-level table directly instead of a package-main literal. The FUNCTIONS that consult
// it against the live provider registry (opActsInBuildDeploy in charly/checkspec.go;
// opEffectiveContexts / opInContext in charly/planrun_adapter.go, K-wave 2 cone R4; the former
// opEffectiveDo is gone) stay in core — they dispatch through providerRegistry.ResolveVerb/
// ResolveStep, a package-main-private mechanism a separate module cannot reach; only the static
// per-verb metadata table is portable.

// DoMode is the act/assert/instruct axis. act = perform a side-effect; assert = run the
// matchers (read-only); instruct = hand free-form text to the agent grader.
type DoMode string

const (
	DoAct      DoMode = "act"
	DoAssert   DoMode = "assert"
	DoInstruct DoMode = "instruct"
)

// StepDoMode maps the step keyword to the act/assert/instruct dispatch enum.
func StepDoMode(s *Step) DoMode {
	switch {
	case s.Run != "":
		return DoAct
	case s.Check != "":
		return DoAssert
	case s.AgentRun != "", s.AgentCheck != "":
		return DoInstruct
	}
	return DoAssert
}

// ExecContext is where an op runs. An op's Context list (or its VerbCatalog default)
// declares legality; the active engine supplies the running context and skips ops whose
// context set does not include it.
type ExecContext string

const (
	CtxBuild   ExecContext = "build"
	CtxDeploy  ExecContext = "deploy"
	CtxRuntime ExecContext = "runtime"
)

// VerbSpec is the per-verb metadata in VerbCatalog. Contexts[0] is the canonical default
// context.
type VerbSpec struct {
	Contexts   []ExecContext
	DefaultDo  DoMode
	Reversible bool
}

// HasContext reports whether the verb is legal in ctx.
func (s VerbSpec) HasContext(ctx ExecContext) bool {
	for _, c := range s.Contexts {
		if c == ctx {
			return true
		}
	}
	return false
}

var (
	ctxBuildDeploy        = []ExecContext{CtxBuild, CtxDeploy}
	ctxBuildDeployRuntime = []ExecContext{CtxBuild, CtxDeploy, CtxRuntime}
)

// VerbCatalog is the single source of truth for every core install-verb's legality, default
// do-mode, and reversibility — one table driving validation, dispatch, and lowering. Keys
// match spec.OpVerbs (gated by the registry bijection in charly/reserved_registry.go).
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

// InstallVerbs are the verbs that render directly to a generic OpStep install step (a
// Containerfile directive at build, a deploy shell command at deploy). The verbs that
// lowered into a TYPED install step (package/service) are now extracted plugin verbs whose
// TypedStepProvider owns the lowering — handled by charly core's opActsInBuildDeploy, not
// this map.
var InstallVerbs = map[string]bool{
	"mkdir": true, "copy": true, "write": true, "link": true,
	"download": true, "setcap": true, "build": true,
	// `command` is NOT here — it is a plugin verb now; its build/deploy install path is
	// the dedicated `plugin == "command"` emitCmd branch, accepted by opActsInBuildDeploy
	// directly (not via this map, which is keyed by the verb the Op resolves to, never
	// "command" again).
}
