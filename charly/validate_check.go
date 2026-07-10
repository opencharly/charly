package main

import (
	"fmt"
	"regexp"
)

// lowercaseCheckVarPattern matches a ${name} token whose identifier begins with a
// lowercase letter. The check-var expander (kit.TestVarRefPattern in sdk/kit) only
// recognizes UPPERCASE names, so such a token is never substituted — it reaches
// the verb literally. Used by validateCheck to reject it in pure-identifier check
// fields (k8s/resource modifiers), catching the k3s-server `${deploy_name}` class.
var lowercaseCheckVarPattern = regexp.MustCompile(`\$\{[a-z][a-zA-Z0-9_]*\}`)

// validateTests checks every declarative test spec in the project for
// authoring errors before build time. Hooked into charly box validate.
//
// Scope:
//   - Candy-level tests (CandyYAML.Check).
//   - Box-level tests (BoxConfig.Check, BoxConfig.DeployCheck).
//   - Per-host charly.yml tests are out of scope here because that overlay is
//     loaded per-operator at runtime, not part of the project charly.yml
//     validation pass.
//
// Validation rules:
//  1. Exactly one verb discriminator is set per Check (enforced via Kind()).
//  2. Numeric port in range 1..65535 when the port verb is used.
//  3. Timeout parses as time.Duration when present.
//  4. All ${NAME[:arg]} references are legitimate: scope:"build" checks
//     may not reference runtime-only variables.
//  5. ID uniqueness within a single section (candy/box/deploy) so
//     charly.yml overrides are unambiguous.
//  6. ExitStatus / Port / UID / GID sanity (non-negative).
//  7. Matcher operator is a known one.
func validateOps(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate every Op embedded in a plan step — candy + box. Agent
	// (agent-run:/agent-check:) and include: steps carry no Op verb, so they
	// are skipped.
	validatePlanOps := func(plan []Step, who string) {
		for i := range plan {
			op := &plan[i].Op
			if len(op.VerbsSet()) == 0 {
				continue // agent / include / verb-less step
			}
			// Stamp the step keyword's do-mode onto the op so validateCheck's
			// act-form rules (e.g. a run: step whose verb has no build/deploy
			// install path) are keyword-aware — mirroring runUnit's stamping.
			op.IntentDo = string(stepDoMode(&plan[i]))
			validateCheck(op, fmt.Sprintf("%s step[%d]", who, i), errs)
		}
	}
	for name, layer := range layers {
		validatePlanOps(layer.plan, fmt.Sprintf("candy %q", name))
	}
	for name, img := range cfg.Box {
		if img.Enabled != nil && !*img.Enabled {
			continue
		}
		validatePlanOps(img.Plan, fmt.Sprintf("box %q", name))
	}
}

// validateCheck runs per-check rules. effectiveScope is "build" or "deploy"
// — for candy manifest tests: the Check's own Scope field (defaulting to
// build); for charly.yml deploy_tests: always "deploy".
func validateCheck(c *Op, loc string, errs *ValidationError) {
	verb, err := c.Kind()
	if err != nil {
		errs.Add("%s: %v", loc, err)
		return
	}
	// port range (>0, <=65535), timeout (#Duration), and the context enum are enforced
	// by the closed #Op. The former "port verb needs a non-zero port" rule was removed
	// as structurally unreachable: VerbsSet (spec/charly_methods.go) classifies an op as
	// the `port` verb ONLY when c.Port != 0, so verb=="port" already implies Port!=0.
	if spec, ok := VerbCatalog[verb]; ok {
		for _, ctx := range opEffectiveContexts(c) {
			if !spec.HasContext(ctx) {
				errs.Add("%s: verb %q is not legal in context %q", loc, verb, ctx)
			}
		}
	}

	// A do:act op must have a real install path in each build/deploy context
	// it claims — otherwise the compiler would silently drop it. Runtime-only
	// act verbs (file/dbus/…) act via the check Runner's executor; in
	// build/deploy the install verbs + command + package/service are the act
	// surface (file creation = write/copy). A plugin verb (plugin: <word>) acts
	// in build/deploy when its provider is a ProvisionActor — the act-emit enabler
	// renders RenderProvisionScript at install emit (e.g. the extracted
	// plugin: user / unix_group / kernel-param / mount → useradd / groupadd /
	// sysctl / mount).
	if opEffectiveDo(c) == DoAct && !opActsInBuildDeploy(c, verb) {
		for _, ctx := range opEffectiveContexts(c) {
			if ctx == CtxBuild || ctx == CtxDeploy {
				errs.Add("%s: verb %q cannot act (do: act) in %s context — its act form is runtime-only (use context: [runtime]); create files in build/deploy with the write/copy verbs", loc, verb, ctx)
			}
		}
	}
	// uid/gid non-negativity is enforced by #Op.

	// Runtime-only variable references — illegal in a build-legal op.
	if opInContext(c, CtxBuild) {
		refs := collectCheckRefs(c)
		for _, r := range refs {
			if IsRuntimeOnlyVar(r) {
				errs.Add("%s: references runtime-only variable ${%s} but scope is build — mark as scope: deploy or use scope:deploy-only attributes", loc, r)
			}
		}
	}

	// Lowercase ${...} in a pure-identifier check field is NEVER an check variable:
	// the check-var expander (kit.TestVarRefPattern) only recognizes UPPERCASE names,
	// so a lowercase token is silently passed through literally and reaches the
	// verb as the string "${...}" (the k3s-server `cluster: "${deploy_name}"`
	// class of bug — it passed both validate and runtime, resolving to no cluster).
	// Scoped to the kube/resource-identity modifiers, which are CLI-arg identifiers
	// passed to `charly check kube` — never a shell body (cmd:) or JS (cdp expression:),
	// where a lowercase ${var} is legitimate. Did you mean an UPPERCASE check var
	// (e.g. ${DEPLOY_NAME})?
	// The identifier-like fields moved into the kube plugin's input map; scan its
	// string input values generically (a shell body / JS expression legitimately
	// carries lowercase ${...}, so the scan stays scoped to the kube word — the
	// CLI-arg identifier class this check was built for).
	if c.Plugin == "kube" {
		for k, v := range c.PluginInput {
			s, ok := v.(string)
			if !ok {
				continue
			}
			if m := lowercaseCheckVarPattern.FindString(s); m != "" {
				errs.Add("%s: %s contains %s — check variables are UPPERCASE (e.g. ${DEPLOY_NAME}); a lowercase ${...} never resolves and is passed through literally", loc, k, m)
			}
		}
	}

	// Matcher operator names (equals/contains/matches/…) are enforced by #MatchOpMap.
	//
	// The per-verb method-name allowlist, required-modifier, artifact-applicability, and
	// {element} checks now live in each live-container verb's OUT-OF-PROCESS plugin
	// (candy/plugin-*'s checkRequiredModifiers); the method-name enum is enforced by CUE on
	// core #Op. The former in-proc per-verb live-verb validation was deleted with the
	// compiled-in live-verb runtime (the live-verb externalization orphaned it).
}

// collectCheckRefs returns every ${NAME[:arg]} key referenced across every
// string-typed field of the Check.
func collectCheckRefs(c *Op) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		for _, r := range TestVarRefs(s) {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	for _, p := range c.StringFields() {
		add(*p)
	}
	// Plugin verbs author their fields in the opaque PluginInput map (NOT StringFields)
	// — e.g. `plugin: command` carries the cmd body in plugin_input.command, `plugin: http`
	// the URL in plugin_input.http. Scan them too so a build-context op referencing a
	// runtime-only var inside plugin_input is flagged uniformly with the StringFields scan
	// (R3); the read-only collectAnyStrings walk is the same one ${HOST:…} cross-member
	// collection uses.
	for _, s := range collectAnyStrings(c.PluginInput) {
		add(s)
	}
	return out
}
