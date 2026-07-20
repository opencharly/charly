package box

// validate_rules.go — the A-PURE candy + box validation rules (task #60, Unit C). Each reads ONLY the
// resolved-project envelope (vc.models / vc.views / vc.boxes) + live filesystem re-probes against a
// candy's SourceDir; none reaches the host registry or re-loads the project. Ported verbatim from the
// former charly core validator, translated from the runtime *Candy/*Config accessors to the envelope
// field reads (validate.go documents the accessor→field mapping).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

var (
	aliasNameRe            = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	podmanSecretSlugRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	taskUserLiteralPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	taskUserUIDGIDPattern  = regexp.MustCompile(`^\d+:\d+$`)
	taskCapsPattern        = regexp.MustCompile(`^cap_[a-z_]+[=+][a-z]+(,cap_[a-z_]+[=+][a-z]+)*$`)
)

// validateCandyReferences ensures every candy a box references exists. Box candy lists on the envelope
// (ResolvedBoxView.Candy) are already BareRef-normalized, so the remote arm never fires for the box
// arm — a genuinely-missing remote candy would already be a host load diagnostic; kept for structural
// fidelity with the core rule.
func validateCandyReferences(vc *vctx, e *vErr) {
	for boxName := range vc.boxes {
		for _, candyRef := range vc.boxes[boxName].Candy {
			candyName := deploykit.BareRef(candyRef)
			if _, ok := vc.models[candyName]; ok {
				continue
			}
			if deploykit.IsRemoteCandyRef(candyRef) {
				parsed := parseRemoteRef(candyRef)
				e.Add("box %q: remote candy %q not found (candy %q doesn't exist in %s)", boxName, candyRef, parsed.Name, parsed.RepoPath)
				continue
			}
			if suggestion := findSimilarName(candyName, candyNames(vc)); suggestion != "" {
				e.Add("box %q: candy %q not found (did you mean %q?)", boxName, candyName, suggestion)
			} else {
				e.Add("box %q: candy %q not found", boxName, candyName)
			}
		}
	}
}

// validateCandyContents validates each candy has required content + the mandatory ADE plan.
func validateCandyContents(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		v := vc.views[name]
		dk := vc.dk[name]

		// At least one install file, a candy: composition, data, external_builder, localpkg, OR a
		// plugin: block — all legitimately ship no install files. HasInstallFiles is the core
		// package/manifest/task/apk predicate (NOT the adapter's broad HasContent, which counts plan
		// steps every ADE candy has), replicated from the envelope.
		if !candyHasInstallFiles(m, dk) && len(v.IncludedCandy) == 0 && !dk.HasData() &&
			!v.IsPlugin && m.ExternalBuilder == "" && len(m.LocalPkg) == 0 {
			e.Add("candy %q: must have at least one install file (candy manifest distro: packages, root.yml, pixi.toml, pyproject.toml, environment.yml, package.json, Cargo.toml, or user.yml), a candy: field, a localpkg:, an external_builder:, or a plugin: block", name)
		}

		// ADE is MANDATORY per LOCAL candy: a non-empty description: + a plan: with ≥1 deterministic
		// check: step. A fetched remote candy is its own repo's concern (v.Remote).
		if !v.Remote {
			checkSteps := 0
			for i := range m.Plan {
				if m.Plan[i].Check != "" {
					checkSteps++
				}
			}
			switch {
			case strings.TrimSpace(v.Description) == "":
				e.Add("candy %q: missing required `description:` string (ADE is mandatory). See /charly-check:check", name)
			case len(m.Plan) == 0:
				e.Add("candy %q: missing required `plan:` list (ADE is mandatory; the spec IS the test). See /charly-check:check", name)
			case checkSteps == 0:
				e.Add("candy %q: `plan:` must contain at least one `check:` step so the agentless check has something to verify. See /charly-check:check", name)
			default:
				for _, issue := range validatePlanStepsPure(v.Description, m.Plan, "candy "+name) {
					e.Add("%s", issue)
				}
			}
		}

		// SourceDir (directory:-redirected or the candy dir) must exist. The envelope carries only
		// SourceDir (no Path), so the redirect-only guard is subsumed: a non-redirected candy's
		// SourceDir is its scanned dir (always present), so a missing non-empty SourceDir is the
		// redirect-to-missing case the core rule caught. Message is byte-identical (SourceDir twice).
		if m.SourceDir != "" && !dirExists(m.SourceDir) {
			e.Add("candy %q: directory %q does not exist (resolved to %q)", name, m.SourceDir, m.SourceDir)
		}

		// validatePluginCandy (task #60 — restored via the D-data word sets, GAP CLOSED): a candy
		// declaring a `plugin:` block must declare ≥1 provider, each a well-formed <class>:<word> with a
		// known class, and every BUILTIN provider must be compiled into charly. The SUBJECT (the candy's
		// declared providers + source) rides spec.CandyView.PluginProviders/PluginSource; the TARGET (the
		// compiled-in provider set) rides ResolvedProject.ProviderCapabilities — so the check runs off the
		// envelope with no host-registry dial. Mirrors core splitCapability + validatePluginCandy exactly.
		if v.IsPlugin {
			source := v.PluginSource
			if source == "" {
				source = "builtin"
			}
			if len(v.PluginProviders) == 0 {
				e.Add("candy %q: plugin block declares no providers", name)
			}
			for _, capStr := range v.PluginProviders {
				class, word, ok := splitPluginCapability(capStr)
				if !ok {
					e.Add("candy %q: plugin capability %q is malformed (want <class>:<word>)", name, capStr)
					continue
				}
				if source == "builtin" && vc.env != nil && !slices.Contains(vc.env.ProviderCapabilities, class+":"+word) {
					e.Add("candy %q: plugin declares builtin %s:%s but no such provider is compiled into charly", name, class, word)
				}
			}
		}

		// Cargo.toml requires a src/ directory (live re-probe against SourceDir).
		if dk.GetHasCargoToml() && !dirExists(filepath.Join(m.SourceDir, "src")) {
			e.Add("candy %q: Cargo.toml requires src/ directory", name)
		}

		// depends references (already qualified to map keys at scan time; direct lookup).
		for _, depRef := range v.Require {
			dep := deploykit.BareRef(depRef)
			if _, ok := vc.models[dep]; ok {
				continue
			}
			if suggestion := findSimilarName(dep, candyNames(vc)); suggestion != "" {
				e.Add("candy %q depends: unknown candy %q (did you mean %q?)", name, dep, suggestion)
			} else {
				e.Add("candy %q depends: unknown candy %q", name, dep)
			}
		}

		validateCandyShell(name, m.Shell, e)
		validateCandyApk(name, m.Apk, e)
	}
}

// validPluginClasses is the closed provider-class set (mirrors core providerClasses, provider.go) —
// the classes a candy's `plugin.providers:` capability may name. Copied across the module boundary
// (a small static set, like the other pure helpers); the plugin cannot import charly core.
var validPluginClasses = map[string]bool{
	"kind": true, "verb": true, "deploy": true, "step": true,
	"builder": true, "command": true, "build": true, "loader": true, "refs": true,
	"agent-runtime": true, "terminal": true,
}

// splitPluginCapability splits a `<class>:<word>` capability, mirroring core splitCapability
// (provider.go): the class must be non-empty, the word non-empty, and the class a known one.
func splitPluginCapability(s string) (class, word string, ok bool) {
	i := strings.IndexByte(s, ':')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	c := s[:i]
	if !validPluginClasses[c] {
		return "", "", false
	}
	return c, s[i+1:], true
}

// validateCandyApk enforces the apk: cross-field rule CUE cannot express (source: applies only to
// apkeep package installs, never a committed apk: file).
func validateCandyApk(candyName string, apks []spec.ApkPackageSpec, e *vErr) {
	for i, a := range apks {
		if a.Apk != "" && a.Source != "" {
			e.Add("candy %q apk[%d]: `source:` applies only to `package:` (apkeep) installs, not committed `apk:` files", candyName, i)
		}
	}
}

// validateCandyShell enforces the shell:-schema init/path semantics.
func validateCandyShell(candyName string, cfg *spec.Shell, e *vErr) {
	if cfg == nil {
		return
	}
	checkSpec := func(label string, s *spec.ShellSpec) {
		if s == nil {
			return
		}
		if s.Init != "" && strings.TrimSpace(s.Init) == "" {
			e.Add("candy %q: shell.%s.init must not be whitespace-only", candyName, label)
		}
		if s.Path != "" {
			validateShellPath(candyName, fmt.Sprintf("shell.%s.path", label), s.Path, e)
		}
		for _, p := range s.PathAppend {
			validateShellPath(candyName, fmt.Sprintf("shell.%s.path_append", label), p, e)
		}
	}
	if cfg.Init != "" && strings.TrimSpace(cfg.Init) == "" {
		e.Add("candy %q: shell.init must not be whitespace-only", candyName)
	}
	if cfg.Path != "" {
		validateShellPath(candyName, "shell.path", cfg.Path, e)
	}
	for _, p := range cfg.PathAppend {
		validateShellPath(candyName, "shell.path_append", p, e)
	}
	for shell, s := range cfg.ByShell() {
		checkSpec(shell, s)
	}
}

// validateShellPath rejects traversal + non-absolute/non-~ paths.
func validateShellPath(candyName, field, p string, e *vErr) {
	if p == "" {
		return
	}
	if strings.Contains(p, "..") {
		e.Add("candy %q: %s contains traversal sequence (got %q)", candyName, field, p)
		return
	}
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/") && p != "~" {
		e.Add("candy %q: %s must be absolute or ~/-prefixed (got %q)", candyName, field, p)
	}
}

// validateCandyIncludes validates candy composition (candy: field) references + overlap + cycles.
func validateCandyIncludes(vc *vctx, e *vErr) {
	for name := range vc.views {
		v := vc.views[name]
		if len(v.IncludedCandy) == 0 {
			continue
		}
		depSet := make(map[string]bool)
		for _, d := range v.Require {
			depSet[deploykit.BareRef(d)] = true
		}
		for _, includedRef := range v.IncludedCandy {
			ref := deploykit.BareRef(includedRef)
			if ref == name {
				e.Add("candy %q candy: cannot include itself", name)
				continue
			}
			if _, ok := vc.models[ref]; !ok {
				if suggestion := findSimilarName(ref, candyNames(vc)); suggestion != "" {
					e.Add("candy %q candy: unknown candy %q (did you mean %q?)", name, ref, suggestion)
				} else {
					e.Add("candy %q candy: unknown candy %q", name, ref)
				}
			}
			if depSet[ref] {
				e.Add("candy %q: %q appears in both 'candy' and 'depends'", name, ref)
			}
		}
	}
	for name := range vc.views {
		if len(vc.views[name].IncludedCandy) == 0 {
			continue
		}
		if err := checkIncludeCycle(name, vc, nil); err != nil {
			e.Add("candy %q: %v", name, err)
		}
	}
}

// checkIncludeCycle detects circular candy composition over the envelope views.
func checkIncludeCycle(name string, vc *vctx, visited map[string]bool) error {
	if visited == nil {
		visited = make(map[string]bool)
	}
	if visited[name] {
		return fmt.Errorf("circular candy composition involving %q", name)
	}
	v, ok := vc.views[name]
	if !ok || len(v.IncludedCandy) == 0 {
		return nil
	}
	visited[name] = true
	for _, includedRef := range v.IncludedCandy {
		if err := checkIncludeCycle(deploykit.BareRef(includedRef), vc, visited); err != nil {
			return err
		}
	}
	delete(visited, name)
	return nil
}

// validatePkgConfig validates per-distro/format repo/copr/module package requirements.
func validatePkgConfig(vc *vctx, e *vErr) {
	validateRaw := func(name, label string, raw map[string]any, candyHasPkgs bool) {
		if raw == nil {
			return
		}
		if repos := buildkit.ToMapSlice(raw["repo"]); len(repos) > 0 && !candyHasPkgs {
			e.Add("candy %q candy manifest: %s.repo requires packages (none declared anywhere in the candy)", name, label)
		}
		if copr := buildkit.ToStringSlice(raw["copr"]); len(copr) > 0 && !candyHasPkgs {
			e.Add("candy %q candy manifest: %s.copr requires packages", name, label)
		}
		// The authored key is singular `module:` (#Candy `module` → TagSection Raw key
		// "module" via derivePackageSectionsFromCalamares; sdk spec `Module yaml:"module"`).
		// Checking Raw["modules"] (plural) made this rule UNREACHABLE on real config — it
		// only ever matched a hand-built plural key (#71). repo/copr above are already
		// singular authored keys checked singular; module was the sole mismatch.
		if modules := buildkit.ToStringSlice(raw["module"]); len(modules) > 0 && !candyHasPkgs {
			e.Add("candy %q candy manifest: %s.module requires packages", name, label)
		}
	}
	for name := range vc.models {
		m := vc.models[name]
		hasPkgs := candyHasAnyPackages(m)
		for tag, cfg := range m.TagSections {
			validateRaw(name, "distro."+tag, cfg.Raw, hasPkgs)
		}
		for formatName, section := range m.FormatSections {
			validateRaw(name, formatName, section.Raw, hasPkgs)
		}
	}
}

// validateVolume checks the cross-entry duplicate-name invariant CUE cannot express.
func validateVolume(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		if len(m.Volumes) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, vol := range m.Volumes {
			if seen[vol.Name] {
				e.Add("candy %q candy manifest volumes: duplicate volume name %q", name, vol.Name)
			}
			seen[vol.Name] = true
		}
	}
}

// validateAliases validates candy + box alias name char-set + cross-entry dedup.
func validateAliases(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		if len(m.Aliases) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, a := range m.Aliases {
			if a.Name != "" && !aliasNameRe.MatchString(a.Name) {
				e.Add("candy %q candy manifest aliases: name %q must match [a-zA-Z0-9][a-zA-Z0-9._-]*", name, a.Name)
			}
			if seen[a.Name] {
				e.Add("candy %q candy manifest aliases: duplicate alias name %q", name, a.Name)
			}
			seen[a.Name] = true
		}
	}
	for boxName := range vc.boxes {
		aliases := vc.boxes[boxName].AuthoredAliases
		if len(aliases) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, a := range aliases {
			switch {
			case a.Name == "":
				e.Add("box %q aliases: missing required \"name\" field", boxName)
			case !aliasNameRe.MatchString(a.Name):
				e.Add("box %q aliases: name %q must match [a-zA-Z0-9][a-zA-Z0-9._-]*", boxName, a.Name)
			case seen[a.Name]:
				e.Add("box %q aliases: duplicate alias name %q", boxName, a.Name)
			default:
				seen[a.Name] = true
			}
		}
	}
}

// validateSystemdServices re-stats each candy's globbed systemd .service files (live host probe).
func validateSystemdServices(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		for _, svcPath := range m.ServiceFiles {
			info, err := os.Stat(svcPath)
			if err != nil {
				e.Add("candy %q: systemd service file %q not readable: %v", name, filepath.Base(svcPath), err)
				continue
			}
			if info.Size() == 0 {
				e.Add("candy %q: systemd service file %q is empty", name, filepath.Base(svcPath))
			}
		}
	}
}

// validateLibvirt validates candy-level libvirt XML snippets (image-level libvirt: was removed).
func validateLibvirt(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		for i, snippet := range m.Libvirt {
			if err := vmshared.ValidateLibvirtSnippet(snippet); err != nil {
				e.Add("candy %q libvirt[%d]: %v", name, i, err)
			}
		}
	}
}

// validateEnvProvides checks env_provides template-variable well-formedness.
func validateEnvProvides(vc *vctx, e *vErr) {
	for name := range vc.views {
		for key, tmpl := range vc.views[name].EnvProvides {
			if key == "" {
				e.Add("candy %s: env_provides has empty key", name)
				continue
			}
			if !validateProvidesTemplate(tmpl) {
				e.Add("candy %s: env_provides[%s] contains unknown or malformed template variable (allowed: {{.ContainerName}}, {{.HostPort N}}, {{.ContainerPort N}}): %s", name, key, tmpl)
			}
		}
	}
}

// validateEnvDeps enforces the single-membership rule across env/secret dependency sections.
func validateEnvDeps(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		seen := make(map[string]string)
		validateDepEntries(name, "env_requires", m.EnvRequire, seen, e)
		validateDepEntries(name, "env_accepts", m.EnvAccept, seen, e)
		validateDepEntries(name, "secret_requires", m.SecretRequire, seen, e)
		validateDepEntries(name, "secret_accepts", m.SecretAccept, seen, e)
	}
}

func validateDepEntries(candyName, section string, entries []spec.EnvDependency, seen map[string]string, e *vErr) {
	for _, dep := range entries {
		if dep.Name == "" {
			continue
		}
		if prev, ok := seen[dep.Name]; ok && prev != section {
			e.Add("candy %s: env var %s appears in both %s and %s — an env var belongs to exactly one section", candyName, dep.Name, prev, section)
		}
		seen[dep.Name] = section
	}
}

// validateSecretDeps enforces the credential-store slug + env_provides collision rules.
func validateSecretDeps(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		if len(m.SecretAccept) == 0 && len(m.SecretRequire) == 0 {
			continue
		}
		v := vc.views[name]
		envProvidesKeys := map[string]bool{}
		for key := range v.EnvProvides {
			envProvidesKeys[key] = true
		}
		checkOne := func(section string, entries []spec.EnvDependency) {
			for _, dep := range entries {
				if dep.Name == "" {
					continue
				}
				if envProvidesKeys[dep.Name] {
					e.Add("candy %s: %s[%s] also appears in env_provides — credential-backed secrets and plaintext env_provides are mutually exclusive for the same variable", name, section, dep.Name)
				}
				slug := envVarNameToPodmanSecretSlug(dep.Name)
				if !podmanSecretSlugRe.MatchString(slug) {
					e.Add("candy %s: %s[%s] would produce invalid podman secret slug %q (must match %s)", name, section, dep.Name, slug, podmanSecretSlugRe.String())
				}
			}
		}
		checkOne("secret_requires", m.SecretRequire)
		checkOne("secret_accepts", m.SecretAccept)
	}
}

// validateMCPProvides checks mcp_provides duplicate names + URL template grammar.
func validateMCPProvides(vc *vctx, e *vErr) {
	for name := range vc.views {
		v := vc.views[name]
		if len(v.MCPProvide) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, mcp := range v.MCPProvide {
			if mcp.Name != "" {
				if seen[mcp.Name] {
					e.Add("candy %s: mcp_provides has duplicate name %q", name, mcp.Name)
				}
				seen[mcp.Name] = true
			}
			if mcp.URL != "" && !validateProvidesTemplate(mcp.URL) {
				e.Add("candy %s: mcp_provides[%s] url contains unknown or malformed template variable (allowed: {{.ContainerName}}, {{.HostPort N}}, {{.ContainerPort N}}): %s", name, mcp.Name, mcp.URL)
			}
		}
	}
}

// validateMCPDeps enforces the single-membership rule across mcp_requires/mcp_accepts.
func validateMCPDeps(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		seen := make(map[string]string)
		check := func(entries []spec.EnvDependency, section string) {
			for _, dep := range entries {
				if dep.Name == "" {
					continue
				}
				if prev, ok := seen[dep.Name]; ok && prev != section {
					e.Add("candy %s: MCP server %s appears in both mcp_%s and mcp_%s", name, dep.Name, prev, section)
				}
				seen[dep.Name] = section
			}
		}
		check(m.MCPRequire, "requires")
		check(m.MCPAccept, "accepts")
	}
}

// validateLocalTemplates checks each kind:local template's candy references. The opaque Local body
// rides the envelope as spec.Templates.Local RawBody; the plugin decodes it into spec.Local (a plugin
// MAY know a kind the kernel may not) and reads the authored candy stack.
func validateLocalTemplates(vc *vctx, e *vErr) {
	if vc.env == nil || vc.env.Templates == nil {
		return
	}
	for name, raw := range vc.env.Templates.Local {
		if len(raw) == 0 {
			continue
		}
		var lt spec.Local
		if err := json.Unmarshal(raw, &lt); err != nil {
			continue // a malformed template is a host/load concern, not a validate finding
		}
		if lt.Candy == nil {
			e.Add("kind:local %q: missing required field `candy:` (use `candy: []` for an explicit placeholder)", name)
			continue
		}
		for _, candyRef := range lt.Candy {
			candyName := deploykit.BareRef(candyRef)
			if _, ok := vc.models[candyName]; ok {
				continue
			}
			if deploykit.IsRemoteCandyRef(candyRef) {
				parsed := parseRemoteRef(candyRef)
				e.Add("kind:local %q: remote candy %q not found (candy %q doesn't exist in %s)", name, candyRef, parsed.Name, parsed.RepoPath)
			} else {
				e.Add("kind:local %q: candy %q not found", name, candyName)
			}
		}
	}
}

// validateLocalDeployments checks every target:local deployment's template ref + host/user/ssh_args
// semantics, reading the folded deploy tree (env.Deploy) + the local-template presence set.
func validateLocalDeployments(vc *vctx, e *vErr) {
	if vc.env == nil {
		return
	}
	var localTemplates map[string]spec.RawBody
	if vc.env.Templates != nil {
		localTemplates = vc.env.Templates.Local
	}
	for name, node := range vc.env.Deploy {
		if node == nil {
			continue
		}
		if node.Target != "local" && node.Target != "" {
			continue
		}
		if node.Target == "" && !strings.HasSuffix(name, "-local") && name != "local" {
			continue // default target is pod; only validate explicit local deployments
		}
		from := string(node.From)
		if from != "" {
			if _, ok := localTemplates[from]; !ok {
				e.Add("deployment %q: kind:local template %q not found", name, from)
			}
		}
		hostField := strings.TrimSpace(node.Host)
		isLocalDest := hostField == "" || hostField == "local"
		if !isLocalDest {
			if _, perr := vmshared.ParseSSHTarget(hostField); perr != nil {
				e.Add("deployment %q: invalid host %q: %v", name, hostField, perr)
			}
			if node.User != "" && strings.Contains(hostField, "@") {
				inlineUser, _, _ := strings.Cut(hostField, "@")
				if inlineUser != node.User {
					e.Add("deployment %q: ambiguous user — host: %q has inline user %q but user: field is %q (remove one)",
						name, hostField, inlineUser, node.User)
				}
			}
		} else {
			if node.User != "" {
				e.Add("deployment %q: user: field only meaningful when host: is non-local", name)
			}
			if len(node.SSHArgs) > 0 {
				e.Add("deployment %q: ssh_args: field only meaningful when host: is non-local", name)
			}
		}
	}
}

// --- Task validation (plan run: ops) ---

// validateCandyTasks validates each candy's vars: keys + lowered run ops.
func validateCandyTasks(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		for k := range m.Vars {
			if deploykit.TaskAutoExports[k] {
				e.Add("candy %q: vars: key %q collides with a reserved auto-export (USER, UID, GID, HOME, ARCH, BUILD_ARCH)", name, k)
			}
			if m.Env != nil {
				if _, exists := m.Env.Vars[k]; exists {
					e.Add("candy %q: vars: key %q also declared in env: — pick one", name, k)
				}
			}
		}
		if len(m.RunOps) == 0 {
			continue
		}
		known := deploykit.TaskKnownNames(m.Vars)
		for i := range m.RunOps {
			t := m.RunOps[i]
			verb, err := t.Kind()
			if err != nil {
				e.Add("candy %q: plan run[%d]: %v", name, i, err)
				continue
			}
			validateSingleTask(name, i, verb, &t, known, e)
		}
	}
}

// validateSingleTask runs per-verb modifier + ${VAR}-reference validation for one lowered run op.
func validateSingleTask(candyName string, idx int, verb string, t *spec.Op, known map[string]bool, e *vErr) {
	if t.RunAs != "" && !isValidTaskUser(t.RunAs) {
		e.Add("candy %q: tasks[%d]: user: %q is not valid (expected root, ${USER}, a name matching ^[a-z_][a-z0-9_-]*$, or <uid>:<gid>)", candyName, idx, t.RunAs)
	}

	if len(t.Cache) > 0 {
		if verb != "download" && (verb != "plugin" || t.Plugin != "command") {
			e.Add("candy %q: tasks[%d]: cache: is only valid on download: or plugin: command tasks (got %s)", candyName, idx, verb)
		}
		for _, p := range t.Cache {
			if !isAbsOrHomePath(p) {
				e.Add("candy %q: tasks[%d]: cache: %q must be an absolute path (or start with ~/ / ${HOME})", candyName, idx, p)
			}
		}
	}

	switch verb {
	case "mkdir":
		validateMkdirTask(candyName, idx, t, e)
	case "copy":
		validateCopyTask(candyName, idx, t, e)
	case "write":
		validateWriteTask(candyName, idx, t, e)
	case "link":
		validateLinkTask(candyName, idx, t, e)
	case "download":
		validateDownloadTask(candyName, idx, t, e)
	case "setcap":
		validateSetcapTask(candyName, idx, t, e)
	case "build":
		validateBuildTask(candyName, idx, t, e)
	}

	nonShellFields := map[string]string{
		"mkdir":    t.Mkdir,
		"copy":     t.Copy,
		"write":    t.Write,
		"link":     t.Link,
		"target":   t.Target,
		"to":       t.To,
		"download": t.Download,
		"setcap":   t.Setcap,
	}
	for field, val := range nonShellFields {
		if val == "" {
			continue
		}
		if unresolved := deploykit.TaskUnresolvedRefs(val, known); len(unresolved) > 0 {
			e.Add("candy %q: tasks[%d]: %s references unknown ${VAR}: %s (declare in vars: or use an auto-export)", candyName, idx, field, strings.Join(unresolved, ", "))
		}
	}
}

func validateMkdirTask(candyName string, idx int, t *spec.Op, e *vErr) {
	if !isAbsOrHomePath(t.Mkdir) {
		e.Add("candy %q: tasks[%d]: mkdir: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.Mkdir)
	}
}

func validateCopyTask(candyName string, idx int, t *spec.Op, e *vErr) {
	switch {
	case t.Copy == "":
		e.Add("candy %q: tasks[%d]: copy: requires a non-empty source", candyName, idx)
	case strings.HasPrefix(t.Copy, "/"):
		e.Add("candy %q: tasks[%d]: copy: %q must be a relative path (candy-dir file)", candyName, idx, t.Copy)
	case strings.Contains(t.Copy, ".."):
		e.Add("candy %q: tasks[%d]: copy: %q may not contain .. (no traversal)", candyName, idx, t.Copy)
	}
	if t.To == "" {
		e.Add("candy %q: tasks[%d]: copy: requires to: destination", candyName, idx)
	} else if !isAbsOrHomePath(t.To) {
		e.Add("candy %q: tasks[%d]: copy to: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.To)
	}
}

func validateWriteTask(candyName string, idx int, t *spec.Op, e *vErr) {
	if !isAbsOrHomePath(t.Write) {
		e.Add("candy %q: tasks[%d]: write: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.Write)
	}
	if t.Content == "" {
		e.Add("candy %q: tasks[%d]: write: requires non-empty content:", candyName, idx)
	}
}

func validateLinkTask(candyName string, idx int, t *spec.Op, e *vErr) {
	if !isAbsOrHomePath(t.Link) {
		e.Add("candy %q: tasks[%d]: link: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.Link)
	}
	if t.Target == "" {
		e.Add("candy %q: tasks[%d]: link: requires target: (what the symlink points to)", candyName, idx)
	}
}

func validateDownloadTask(candyName string, idx int, t *spec.Op, e *vErr) {
	if t.Download == "" {
		e.Add("candy %q: tasks[%d]: download: requires a URL", candyName, idx)
	}
	if t.Extract != "sh" && t.To == "" {
		e.Add("candy %q: tasks[%d]: download requires to: destination (unless extract: sh)", candyName, idx)
	}
}

func validateSetcapTask(candyName string, idx int, t *spec.Op, e *vErr) {
	if !strings.HasPrefix(t.Setcap, "/") {
		e.Add("candy %q: tasks[%d]: setcap: %q must be an absolute path", candyName, idx, t.Setcap)
	}
	if t.Caps != "" && !taskCapsPattern.MatchString(t.Caps) {
		e.Add("candy %q: tasks[%d]: setcap caps: %q not valid (expected cap_name=flags[,cap_name=flags])", candyName, idx, t.Caps)
	}
}

func validateBuildTask(candyName string, idx int, t *spec.Op, e *vErr) {
	if t.Build != "all" {
		e.Add("candy %q: tasks[%d]: build: %q not supported (initial implementation accepts only \"all\")", candyName, idx, t.Build)
	}
}

// --- pure helpers (copied verbatim across the core/plugin module boundary) ---

// validatePlanStepsPure is the shared static plan-block validator (former charly plan_validate.go):
// description non-empty; each step exactly one keyword; run/check carry one Op verb, agent-* carry none.
func validatePlanStepsPure(desc string, plan []spec.Step, eid string) []string {
	var errs []string
	if strings.TrimSpace(desc) == "" {
		errs = append(errs, fmt.Sprintf("%s: description is empty", eid))
	}
	for i := range plan {
		step := plan[i]
		kw, err := step.StepKind()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: step %d: %v", eid, i, err))
			continue
		}
		switch kw {
		case kit.KwRun, kit.KwCheck:
			if _, verbErr := step.Kind(); verbErr != nil {
				errs = append(errs, fmt.Sprintf("%s: step %d (%s): %v", eid, i, kw, verbErr))
			}
		case kit.KwAgentRun, kit.KwAgentCheck:
			if _, verbErr := step.Kind(); verbErr == nil {
				errs = append(errs, fmt.Sprintf("%s: step %d (%s): agent steps must not carry an Op verb", eid, i, kw))
			}
		}
	}
	return errs
}

// candyHasInstallFiles replicates the core Candy.HasInstallFiles predicate from the envelope (package
// sections / manifests / tasks / apk) — NOT the adapter's broad HasContent (which counts plan steps
// every ADE candy has, so it would never flag a content-less candy).
func candyHasInstallFiles(m spec.CandyModel, dk deploykit.CandyModel) bool {
	return candyHasFormatPackages(m) || candyHasTagPackages(m) || len(m.TopPackages) > 0 ||
		dk.PixiManifest() != "" || dk.GetHasPackageJson() || dk.GetHasCargoToml() ||
		len(m.RunOps) > 0 || len(m.Apk) > 0
}

// candyHasAnyPackages is the whole-candy package-presence union (tag ∪ top ∪ format sections).
func candyHasAnyPackages(m spec.CandyModel) bool {
	return candyHasTagPackages(m) || len(m.TopPackages) > 0 || candyHasFormatPackages(m)
}

func candyHasFormatPackages(m spec.CandyModel) bool {
	for _, s := range m.FormatSections {
		if len(s.Packages) > 0 {
			return true
		}
	}
	return false
}

func candyHasTagPackages(m spec.CandyModel) bool {
	for _, s := range m.TagSections {
		if len(s.Package) > 0 {
			return true
		}
	}
	return false
}

// isValidTaskUser accepts root / ${USER} / ${UID}:${GID} / a literal name / a numeric uid:gid / uid.
func isValidTaskUser(u string) bool {
	if u == "root" || u == "${USER}" || u == "${UID}:${GID}" {
		return true
	}
	if taskUserUIDGIDPattern.MatchString(u) {
		return true
	}
	if taskUserLiteralPattern.MatchString(u) {
		return true
	}
	if _, err := strconv.Atoi(u); err == nil {
		return true
	}
	return false
}

// isAbsOrHomePath returns true for absolute or ~/ / ${HOME}-prefixed paths.
func isAbsOrHomePath(p string) bool {
	if p == "" {
		return false
	}
	return strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "${HOME}")
}

// envVarNameToPodmanSecretSlug lowercases + hyphenates an env var name into its podman-secret slug.
func envVarNameToPodmanSecretSlug(envVarName string) string {
	return strings.ReplaceAll(strings.ToLower(envVarName), "_", "-")
}

// validateProvidesTemplate reports whether a provides template uses only the allowed
// {{.ContainerName}} / {{.ContainerPort N}} / {{.HostPort N}} placeholders (former charly provides.go).
func validateProvidesTemplate(tmpl string) bool {
	stripped := strings.ReplaceAll(tmpl, "{{.ContainerName}}", "")
	stripped = stripPortTemplate(stripped, "{{.ContainerPort ", "}}")
	stripped = stripPortTemplate(stripped, "{{.HostPort ", "}}")
	return !strings.Contains(stripped, "{{") && !strings.Contains(stripped, "}}")
}

func stripPortTemplate(s, prefix, suffix string) string {
	var out strings.Builder
	for {
		i := strings.Index(s, prefix)
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		rest := s[i+len(prefix):]
		before, after, ok := strings.Cut(rest, suffix)
		if !ok {
			out.WriteString(prefix)
			s = rest
			continue
		}
		arg := strings.TrimSpace(before)
		if _, err := strconv.Atoi(arg); err != nil {
			out.WriteString(prefix)
			out.WriteString(before)
			out.WriteString(suffix)
		}
		s = after
	}
}

// parsedRef + parseRemoteRef mirror the pure charly refs.go remote-ref parser (repo/sub-path/name/
// version split) used only for the "remote candy not found" diagnostic wording.
type parsedRef struct {
	Raw      string
	RepoPath string
	SubPath  string
	Name     string
	Version  string
}

func parseRemoteRef(ref string) *parsedRef {
	raw := ref
	ref = strings.TrimPrefix(ref, "@")
	version := ""
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		version = ref[idx+1:]
		ref = ref[:idx]
	}
	repoPath, subPath, name := splitRepoAndSubPath(ref)
	return &parsedRef{Raw: raw, RepoPath: repoPath, SubPath: subPath, Name: name, Version: version}
}

func splitRepoAndSubPath(ref string) (repoPath, subPath, name string) {
	parts := strings.SplitN(ref, "/", 4)
	if len(parts) < 4 {
		name = parts[len(parts)-1]
		if len(parts) <= 1 {
			return "", "", name
		}
		return strings.Join(parts, "/"), "", name
	}
	repoPath = strings.Join(parts[:3], "/")
	subPath = parts[3]
	if idx := strings.LastIndex(subPath, "/"); idx != -1 {
		name = subPath[idx+1:]
	} else {
		name = subPath
	}
	return repoPath, subPath, name
}
