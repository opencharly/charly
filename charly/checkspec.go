package main

import (
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The valid verb-discriminator vocabulary is the CUE-derived spec.OpVerbs
// (#OpVerb → vocab_gen.go); the package-main hand copy was deleted in the
// CUE-single-source cutover. VerbCatalog (below) is gated against it.

// LabelDescriptionSet (labelset.go) is the three-section label set carrying an
// image's baked plan steps; the LabelSet aggregate there wraps it.

// ---------------------------------------------------------------------------
// Variable expansion (extended grammar shared with tasks)
//
// The existing taskVarRefPattern in charly/tasks.go matches ${NAME}. Tests need
// parameterized refs like ${HOST_PORT:6379} and ${VOLUME_PATH:workspace} to
// address deploy-time values. testVarRefPattern is the extended grammar;
// it is a superset of the task pattern so task refs continue to work here.
// ---------------------------------------------------------------------------

// testVarRefPattern matches ${NAME} and ${NAME:arg} references. Group 1 is
// the variable name; group 2 is the optional argument (empty when absent).
//
// Backward-compatible widening of taskVarRefPattern at charly/tasks.go.
var testVarRefPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)(?::([^}]+))?\}`)

// ExpandTestVars substitutes ${NAME} and ${NAME:arg} references using the
// supplied environment map.
//
// Keys in env for plain refs use just the name: env["HOME"] = "/home/user".
// Keys for parameterized refs combine name and argument with a colon:
// env["HOST_PORT:6379"] = "16379", env["VOLUME_PATH:workspace"] = "/var/lib/…".
//
// Returns the substituted string and a list of unresolved refs (in encounter
// order, deduplicated). The caller decides whether unresolved refs are an
// error (build-time validation) or a skip reason (runtime).
func ExpandTestVars(s string, env map[string]string) (string, []string) {
	seen := map[string]bool{}
	var missing []string
	out := testVarRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := testVarRefPattern.FindStringSubmatch(match)
		name, arg := sub[1], sub[2]
		key := name
		if arg != "" {
			key = name + ":" + arg
		}
		if v, ok := env[key]; ok {
			return v
		}
		if !seen[key] {
			seen[key] = true
			missing = append(missing, key)
		}
		return match // leave unresolved refs visible in output
	})
	return out, missing
}

// TestVarRefs returns the set of ${NAME[:arg]} references in s, as their
// fully-qualified keys (matching the env-map format used by ExpandTestVars).
// Used by the validator to catch typos at config time.
func TestVarRefs(s string) []string {
	matches := testVarRefPattern.FindAllStringSubmatch(s, -1)
	var out []string
	seen := map[string]bool{}
	for _, m := range matches {
		key := m[1]
		if m[2] != "" {
			key = m[1] + ":" + m[2]
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}

// runtimeOnlyVarPrefixes lists variable name prefixes that are only resolvable
// against a running container. scope:"build" checks must not reference these.
var runtimeOnlyVarPrefixes = []string{
	"HOST_PORT",
	"VOLUME_PATH",
	"VOLUME_CONTAINER_PATH",
	"CONTAINER_IP",
	"CONTAINER_NAME",
	"INSTANCE",
	"ENV_",
	// The step id is populated only at plan-run execution time, so it is
	// effectively runtime-only.
	"STEP_ID",
	// VM live-check intent: how many <hostdev> the VM's spec declares. Resolved
	// only against a live VM deployment (check_cmd.go VM path), so a build-scope
	// check must not reference it.
	"VM_HOSTDEV_COUNT",
	// The sanitized deploy name of the deployment under check — the same value
	// K3sPostProvision uses for the kubeconfig context + ClusterProfile name, so
	// a deploy-scope k8s check can address its own cluster generically via
	// cluster: "${DEPLOY_NAME}". Resolved only against a live deployment.
	"DEPLOY_NAME",
	// Cross-member address var (check_members.go): the unified ${HOST:<member>}
	// (+ optional :port) lets a driven probe (a check with `on:`, or a sibling
	// bundle member) reach a SEPARATE member. Resolved only against running
	// deployments, so a build-scope check must not reference it.
	"HOST",
}

// IsRuntimeOnlyVar reports whether the given variable key (as returned by
// TestVarRefs) refers to a runtime-only value. The check matches on name
// prefix because parameterized vars share a common prefix with their arg.
func IsRuntimeOnlyVar(key string) bool {
	name := key
	if before, _, ok := strings.Cut(key, ":"); ok {
		name = before
	}
	for _, p := range runtimeOnlyVarPrefixes {
		if name == p || strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Field-walking helpers
// ---------------------------------------------------------------------------

// ExpandVars rewrites every ${...} reference on this Check in place using
// the supplied environment map. Returns the combined list of unresolved refs
// encountered across all string fields.
func opExpandVars(c *Op, env map[string]string) []string {
	seen := map[string]bool{}
	var missing []string
	record := func(unresolved []string) {
		for _, k := range unresolved {
			if !seen[k] {
				seen[k] = true
				missing = append(missing, k)
			}
		}
	}
	for _, p := range c.StringFields() {
		if *p == "" {
			continue
		}
		replaced, unresolved := ExpandTestVars(*p, env)
		*p = replaced
		record(unresolved)
	}
	// A plugin verb (http/interface/addr/process/port/dns/…) carries its authored fields
	// in the opaque PluginInput map, NOT in StringFields. Expand ${VAR} references in
	// every string within it so an http URL's / addr's ${HOST_PORT:N} (and any other
	// runtime var) resolves at runtime exactly as it did before the verb left #Op. The
	// map analogue of the StringFields walk; ONE generic path for every plugin verb (R3).
	if len(c.PluginInput) > 0 {
		_, unresolved := expandAnyVars(c.PluginInput, env)
		record(unresolved)
	}
	sort.Strings(missing)
	return missing
}

// collectAnyStrings returns every string within a plugin_input value (scalar string /
// nested map / list), depth-first. The READ-ONLY analogue of expandAnyVars: it lets the
// ${HOST:…} cross-member scan (collectHostRefs) reach a plugin verb's authored fields,
// which live in the opaque PluginInput map rather than StringFields.
func collectAnyStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case map[string]any:
		var out []string
		for _, e := range x {
			out = append(out, collectAnyStrings(e)...)
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, collectAnyStrings(e)...)
		}
		return out
	default:
		return nil
	}
}

// expandAnyVars expands ${VAR} references in every string within a plugin_input value
// (scalar string / nested map / list), mutating maps and slices in place, and returns
// the (possibly rewritten) value plus the unresolved var names. Non-string scalars pass
// through untouched.
func expandAnyVars(v any, env map[string]string) (any, []string) {
	switch x := v.(type) {
	case string:
		return ExpandTestVars(x, env)
	case map[string]any:
		var missing []string
		for k, e := range x {
			ne, un := expandAnyVars(e, env)
			x[k] = ne
			missing = append(missing, un...)
		}
		return x, missing
	case []any:
		var missing []string
		for i, e := range x {
			ne, un := expandAnyVars(e, env)
			x[i] = ne
			missing = append(missing, un...)
		}
		return x, missing
	default:
		return v, nil
	}
}

// ---------------------------------------------------------------------------
// Unified verb vocabulary — execution context, do-mode, and the VerbCatalog
// single source of truth for per-verb legality + lowering.
// ---------------------------------------------------------------------------

// ExecContext is where an op runs. An op's Context list (or its VerbCatalog
// default) declares legality; the active engine supplies the running context
// and skips ops whose context set does not include it (VenueSkip).

// DoMode is the act/assert/instruct axis. act = perform a side-effect;
// assert = run the matchers (read-only); instruct = hand free-form text to the
// agent grader.
type DoMode string

const (
	DoAct      DoMode = "act"
	DoAssert   DoMode = "assert"
	DoInstruct DoMode = "instruct"
)

// VerbSpec is the per-verb metadata in VerbCatalog. Contexts[0] is the
// canonical default context. LowersTo names the InstallPlan step kind an
// act-mode op of this verb lowers to ("" → a generic OpStep). Reversible marks
// whether act-mode reversal is automatic (an auto ReverseOp); when false an
// act-mode op needs an explicit `uninstall:` or is reversed via plan
// teardown (live verbs) — enforced in validation.
type VerbSpec struct {
	Contexts   []ExecContext
	DefaultDo  DoMode
	Reversible bool
	// LowersTo is gone — the ONLY verbs that lowered into a typed install step
	// (package → SystemPackagesStep, service → ServicePackagedStep) are now extracted
	// plugin verbs whose TypedStepProvider owns the lowering (LowersTo() + ConstructStep
	// on the provider). No remaining VerbCatalog verb lowers into a typed step, so
	// ActsInBuildDeploy reduces to the installVerbs membership test.
}

// HasContext reports whether the verb is legal in ctx.
func (s VerbSpec) HasContext(ctx ExecContext) bool {
	return slices.Contains(s.Contexts, ctx)
}

var (
	ctxBuildDeploy        = []ExecContext{CtxBuild, CtxDeploy}
	ctxBuildDeployRuntime = []ExecContext{CtxBuild, CtxDeploy, CtxRuntime}
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

// ActsInBuildDeploy reports whether a do:act op with this NON-plugin verb has a real
// build/deploy install path — a generic OpStep (the install verbs). The verbs that lowered
// into a typed install step (package/service) are extracted plugin verbs now, handled by
// opActsInBuildDeploy's TypedStepProvider branch — so no remaining VerbCatalog verb lowers,
// and this reduces to the installVerbs membership test. Every other verb's act form runs
// only at runtime (the check Runner's executor), so a build/deploy do:act op of such a verb
// would be silently dropped by the compiler — the validator rejects it instead (file
// creation in build/deploy is the write/copy verbs).
func ActsInBuildDeploy(verb string) bool {
	return installVerbs[verb]
}

// opActsInBuildDeploy is the Op-level act-capability test, threading the generic
// `plugin:` verb: `plugin: command` is the ONE install-task plugin verb — act-capable
// via the dedicated emitCmd branch in emitTasks/renderOpCommand (preserving the full
// command build/deploy install path), NOT a ProvisionActor — so it is accepted directly.
// Every other plugin verb acts in build/deploy only when its registered provider is a
// ProvisionActor (the act-emit enabler renders RenderProvisionScript at install emit).
// Every non-plugin verb defers to the verb-keyed ActsInBuildDeploy. verb is the caller's
// already-computed c.Kind() (avoids recomputation).
func opActsInBuildDeploy(c *Op, verb string) bool {
	if verb == "plugin" {
		if c.Plugin == "command" {
			return true
		}
		// A class:STEP plugin word (F3's external step KIND) lowers to an externalStep that ACTS
		// at DEPLOY (compileActOp resolve(ClassStep) → externalStep → OpExecute). Recognized via a
		// connected ClassStep provider OR a post-scan declaration (standalone `charly box validate`,
		// where the step plugin is not connected) — the step analogue of the verb handling below.
		if _, ok := providerRegistry.ResolveStep(c.Plugin); ok {
			return true
		}
		if isDeclaredExternalStep(c.Plugin) {
			return true
		}
		prov, ok := providerRegistry.ResolveVerb(c.Plugin)
		if !ok {
			// Not connected — the standalone `charly box validate` path, where external
			// plugins are not built+connected. Trust a verb the parse-time prescan saw a
			// plugin candy declare (registerDeclaredExternalVerb): it is build-emit-capable
			// until the BUILD (which DOES connect it via the connect seam) proves otherwise
			// at emitPluginFragment's empty-fragment guard. A BUILTIN verb always resolves
			// above, so this branch is reached only for a genuinely external, not-yet-
			// connected verb — never for a runtime-only builtin (which is correctly rejected).
			return isDeclaredExternalVerb(c.Plugin)
		}
		// A ProvisionActor renders an install shell; a TypedStepProvider (service) lowers
		// into a typed install step; a BuildEmitter (an in-proc build-emit verb) renders a
		// Containerfile fragment via Invoke(OpEmit). Each is a real build/deploy act path —
		// the same capability whether the plugin is builtin or external (placement-agnostic).
		if _, isActor := prov.(ProvisionActor); isActor {
			return true
		}
		if _, isTyped := prov.(TypedStepProvider); isTyped {
			return true
		}
		if _, isEmitter := prov.(BuildEmitter); isEmitter {
			return true
		}
		// A CONNECTED external (out-of-process) verb is build-emit-capable via Invoke(OpEmit);
		// the host cannot type-assert capability across the process boundary, so it is trusted
		// here and gated at build by emitPluginFragment's empty-fragment guard.
		_, isExternal := prov.(*grpcProvider)
		return isExternal
	}
	return ActsInBuildDeploy(verb)
}

// EffectiveDo returns the op's resolved do-mode: the keyword-stamped intentDo
// wins (set by the enclosing Step at run/collect time), else the verb's
// VerbCatalog default, else DoAssert.
func opEffectiveDo(c *Op) DoMode {
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
func opEffectiveContexts(c *Op) []ExecContext {
	if len(c.Context) > 0 {
		out := make([]ExecContext, 0, len(c.Context))
		for _, s := range c.Context {
			out = append(out, ExecContext(s))
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
func opInContext(c *Op, ctx ExecContext) bool {
	return slices.Contains(opEffectiveContexts(c), ctx)
}
