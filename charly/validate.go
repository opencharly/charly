package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"cuelang.org/go/cue"
	"gopkg.in/yaml.v3"
)

// ValidationError collects multiple validation errors
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return fmt.Sprintf("validation error: %s", e.Errors[0])
	}
	return fmt.Sprintf("%d validation errors:\n\n  %s", len(e.Errors), strings.Join(e.Errors, "\n  "))
}

// Add adds an error to the collection
func (e *ValidationError) Add(format string, args ...any) {
	e.Errors = append(e.Errors, fmt.Sprintf(format, args...))
}

// HasErrors returns true if there are any errors
func (e *ValidationError) HasErrors() bool {
	return len(e.Errors) > 0
}

// validateCandyCUESchemas validates each loaded candy's on-disk manifest against
// the candy CUE schema (via validateCandyManifestCUE — #Candy for a legacy
// kind-keyed manifest, #NodeDoc for a node-form manifest). This is the sole
// candy-schema validator; the former hand-written Go candy validators are
// deleted. Inline/synthesized candies with no manifest file on disk are skipped.
func validateCandyCUESchemas(layers map[string]*Candy, errs *ValidationError) {
	for name, c := range layers {
		if c == nil || c.Path == "" {
			continue
		}
		f := filepath.Join(c.Path, UnifiedFileName)
		data, err := os.ReadFile(f)
		if err != nil {
			continue // remote/inline candy without a local manifest — skip
		}
		if verr := validateCandyManifestCUE(f, data); verr != nil {
			errs.Add("candy %q: CUE schema: %v", name, verr)
		}
	}
}

// validateProjectCUESchemas validates the project's non-candy entities against
// the CUE schemas. Boxes are validated from the RESOLVED in-memory set
// (cfg.Box) — exactly what the Go box validators iterate, so CUE coverage
// matches Go coverage per repo (each repo validates its own boxes; submodule
// boxes are validated when `charly box validate` runs in that submodule). The
// other collection kinds are read from the root-shape files. Candies are
// handled by validateCandyCUESchemas.
func validateProjectCUESchemas(cfg *Config, dir string, opts ResolveOpts, errs *ValidationError) {
	// Boxes: BoxConfig has no Name field (the name is the cfg.Box map key), so
	// inject it into the wire form before validating against #Box. Marshal the
	// resolved struct back to YAML and run it through the same ingest path the
	// on-disk corpus uses. Skip disabled boxes exactly like the Go box
	// validators (a disabled box's invalid fields are intentionally not flagged).
	for name, box := range cfg.eachBox {
		if !box.IsEnabled() && !opts.shouldIncludeDisabled(name) {
			continue
		}
		entityYAML, err := boxEntityWireYAML(name, box)
		if err != nil {
			errs.Add("box %q: CUE wire-encode: %v", name, err)
			continue
		}
		doc, derr := cueDocFromYAML("box:"+name, entityYAML)
		if derr != nil {
			errs.Add("box %q: CUE ingest: %v", name, derr)
			continue
		}
		// Non-concrete (closedness + value-constraint conflicts, NOT
		// missing-required / disjunction-resolution): a scratch box with
		// neither base nor from is valid, but Concrete(true) can't resolve the
		// base/from mutual-exclusion disjunction when both are absent. The
		// re-wiring's purpose is to catch SET-value declarative violations
		// (version/jobs/check_level/…), which Unify().Validate() catches; the
		// only required #Box field, name, is always injected above.
		if verr := validateEntityClosedCUE("box", "box:"+name, doc.LookupPath(cue.ParsePath("box"))); verr != nil {
			errs.Add("%v", verr)
		}
	}

	collectionKinds := []string{
		"pod", "local", "android", "k8s", "sidecar", "distro", "builder",
		"init", "agent", "resource", "group", "target", "module",
		"deploy", "check", "vm",
	}
	rootFiles := []string{filepath.Join(dir, UnifiedFileName)}
	if boxRoots, _ := filepath.Glob(filepath.Join(dir, "box", "*", UnifiedFileName)); len(boxRoots) > 0 {
		rootFiles = append(rootFiles, boxRoots...)
	}
	for _, f := range rootFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// validateVocabularyCollections is the LEGACY root-shape collection
		// validator (`k8s: {entity: …}` → each entity against #K8s). A node-form
		// file whose NODE is named after a collection kind (a box named `k8s`:
		// `k8s: {box: …}`) would be misread as a `k8s`-kind collection. Node-form
		// files are validated at load (validateNodeDocCUE) and via the resolved
		// cfg.Box path above, so skip them here.
		if isNodeFormFile(data) {
			// node-form STRUCTURE is gated at load (validateNodeDocCUE); add the
			// step-typo gate here — every entity's ASSEMBLED plan steps against the
			// closed #Step/#Op (the validate entrypoint catches what load leaves lenient).
			if verr := validateNodeFormSteps(f, data); verr != nil {
				errs.Add("%v", verr)
			}
			continue
		}
		doc, derr := cueDocFromYAML(f, data)
		if derr != nil {
			errs.Add("%s: CUE ingest: %v", f, derr)
			continue
		}
		validateVocabularyCollections(doc, collectionKinds, f, errs.Add)
	}
}

// validateBoxBaseFrom surfaces the box entity-level base⊻from mutual-exclusion
// as a collected validation error. The #Box CUE schema formerly expressed this
// via a trailing `& ({from?: _|_} | {base?: _|_})` disjunction; that was dropped
// (gengotypes collapses an entity-level disjunction to an empty struct — see
// schema/box.cue) and the rule moved to Go. resolveBase already aborts on the
// conflict, but validateBoxDAG SKIPS unresolvable boxes (continue-on-error), so
// without this explicit pass a base+from box would slip past `charly box
// validate`. Both seams call the ONE predicate BoxConfig.HasBaseFromConflict (R3).
// Neither field set stays valid (a scratch box) — only BOTH is a conflict.
func validateBoxBaseFrom(cfg *Config, opts ResolveOpts, errs *ValidationError) {
	for name, img := range cfg.eachBox {
		if !img.IsEnabled() && !opts.shouldIncludeDisabled(name) {
			continue
		}
		if img.HasBaseFromConflict() {
			errs.Add("box %q: from: and base: are mutually exclusive (set one; omit both for a scratch box)", name)
		}
	}
}

// validateVocabularyCollections validates each entity of the given collection
// kinds in doc against its registered #Kind (validateEntityCUE), reporting every
// failure via report. Shared by validateProjectCUESchemas (on-disk project
// files) and the embedded-default schema-conformance gate
// (TestEmbeddedDefaults_SchemaConformance) so a project's vocabulary and the
// binary-embedded vocabulary validate through the IDENTICAL path (R3).
func validateVocabularyCollections(doc cue.Value, kinds []string, srcLabel string, report func(format string, args ...any)) {
	for _, kind := range kinds {
		m := doc.LookupPath(cue.ParsePath(kind))
		if !m.Exists() {
			continue
		}
		it, ferr := m.Fields()
		if ferr != nil {
			continue
		}
		for it.Next() {
			label := fmt.Sprintf("%s:%s.%s", srcLabel, kind, it.Selector().String())
			if verr := validateEntityCUE(kind, label, it.Value()); verr != nil {
				report("%v", verr)
			}
		}
	}
}

// isNodeFormFile reports whether any document in a YAML file is unified
// node-form (classifyDoc → docShapeNode). Used to skip the legacy root-shape
// collection validator on node-form manifests (whose entities are validated at
// load + via the resolved cfg.Box path).
func isNodeFormFile(data []byte) bool {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			break
		}
		if shape, err := classifyDoc(&node); err == nil && shape == docShapeNode {
			return true
		}
	}
	return false
}

// boxEntityWireYAML marshals a resolved BoxConfig back to the authored `box:`
// wire form (a kind-keyed document), injecting the map-key name that BoxConfig
// does not itself carry, so it can be CUE-ingested and validated against #Box.
func boxEntityWireYAML(name string, box BoxConfig) ([]byte, error) {
	raw, err := yaml.Marshal(box)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	m["name"] = name
	return yaml.Marshal(map[string]any{"box": m})
}

// Validate validates the configuration and candies. opts.IncludeDisabled
// extends the validation pass to images marked enabled: false (the
// validator pre-filters them out by default to keep its working-set
// aligned with the build verb).
func Validate(cfg *Config, layers map[string]*Candy, dir string, opts ResolveOpts) error {
	errs := &ValidationError{}

	// Recognize every external (out-of-tree) plugin VERB the scanned candies declare —
	// including @github-composed plugin candies fetched during the scan — so a build-context
	// `run:` plugin verb step validates as build-emit-capable even when its provider is not
	// connected (standalone `charly box validate`). Post-scan + here (not the parse-time
	// prescan) because only the scanned candy map sees a @github plugin's `verb:<word>`.
	registerExternalVerbsFromCandies(layers)

	// Load default build config for global validation. Unconditional — the
	// caller is required to pass a project dir containing charly.yml.
	// Tests that need in-memory-only validation must use testProjectDir(t).
	var defaultDistroCfg *DistroConfig
	var defaultBuilderCfg *BuilderConfig
	var defaultInitCfg *InitConfig
	{
		dc, blc, ic, err := LoadDefaultBuildConfig(dir)
		if err != nil {
			errs.Add("loading default build config: %v", err)
		} else {
			defaultDistroCfg = dc
			defaultBuilderCfg = blc
			defaultInitCfg = ic
		}
	}

	// Validate build and distro values
	if defaultDistroCfg != nil {
		validateBuildAndDistro(cfg, defaultDistroCfg, errs)
	}

	// Validate candies referenced in images
	validateCandyReferences(cfg, layers, errs)

	// Validate candy contents
	validateCandyContents(layers, errs)

	// Validate candy manifests against the CUE schema
	validateCandyCUESchemas(layers, errs)

	// Validate non-candy entities (box/deploy/check/vm/pod/...) against CUE
	validateProjectCUESchemas(cfg, dir, opts, errs)

	// Box base⊻from mutual-exclusion (the #Box entity-level XOR moved out of CUE
	// to Go — see schema/box.cue + validateBoxBaseFrom).
	validateBoxBaseFrom(cfg, opts, errs)

	// Validate tasks: field (replaces root.yml/user.yml)
	validateCandyTasks(layers, errs)

	// Validate package config (rpm/deb/pac/aur sections in the candy manifest)
	validatePkgConfig(layers, errs)

	// Validate image base references
	validateBaseReferences(cfg, errs)

	// Validate no circular dependencies in images
	validateBoxDAG(cfg, layers, dir, opts, errs)

	// Validate volumes
	validateVolume(layers, errs)

	// Validate merge config
	validateMergeConfig(cfg, errs)

	// Validate build-speed tunables (defaults.jobs / podman_jobs / cache / …)
	validateBuildTunables(cfg, errs)

	// Validate aliases
	validateAliases(cfg, layers, errs)

	// Validate builders and builds
	if defaultBuilderCfg != nil {
		validateBuilders(cfg, layers, defaultBuilderCfg, dir, opts, errs)
	}

	// Validate DNS and ACME email
	validateDNS(cfg, errs)

	// Tunnel is a deploy-time concern (charly.yml only) — not validated here.

	// Validate candy composition (candy: field)
	validateCandyIncludes(layers, errs)

	// Validate no circular dependencies in candies
	validateCandyDAG(cfg, layers, errs)

	// Validate remote candy consistency
	validateRemoteCandies(cfg, layers, errs)

	// Validate systemd service files
	validateSystemdServices(cfg, layers, errs)

	// Validate system_services entries
	validatePackagedServices(cfg, layers, errs)

	// Validate libvirt snippets
	validateLibvirt(cfg, layers, errs)

	// Validate engine declarations
	validateEngineConfig(cfg, layers, errs)

	// Validate port_relay declarations
	validatePortRelay(cfg, layers, errs)

	// Validate env_provides declarations
	validateEnvProvides(layers, errs)

	// Validate env_requires and env_accepts declarations (also enforces cross-section
	// collisions with secret_requires / secret_accepts via a unified seen map)
	validateEnvDeps(layers, errs)

	// Validate secret_requires and secret_accepts declarations (slug form, Key format,
	// collision with env_provides)
	validateSecretDeps(layers, errs)

	// Validate mcp_provides declarations
	validateMCPProvides(layers, errs)

	// Validate mcp_requires and mcp_accepts declarations
	validateMCPDeps(layers, errs)

	// Validate data candies and data images
	validateDataCandies(cfg, layers, errs)

	// Validate init system dependencies (driven by the embedded init: vocabulary)
	if defaultInitCfg != nil {
		validateInitDependencies(cfg, defaultInitCfg, layers, errs)
	}

	// Validate every Op embedded in a candy/box plan step.
	validateOps(cfg, layers, errs)

	// Validate kind:local templates and target:local deployments.
	validateLocalTemplates(dir, layers, errs)
	validateLocalDeployments(dir, errs)

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// validateLocalTemplates checks each kind:local entity for: candy
// references that resolve, an empty `candy: []` (warning, allowed for
// stub placeholders), and a missing `candy:` field entirely (error).
func validateLocalTemplates(dir string, layers map[string]*Candy, errs *ValidationError) {
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return
	}
	for name, spec := range uf.resolveLocals() {
		if spec == nil {
			continue
		}
		if spec.Candy == nil {
			errs.Add("kind:local %q: missing required field `candy:` (use `candy: []` for an explicit placeholder)", name)
			continue
		}
		for _, candyRef := range spec.Candy {
			// Normalize remote @-refs to their bare key form (mirror of the
			// image candy path in validateCandyReferences) — remote candies are
			// stored in the map keyed by bare ref.
			candyName := BareRef(candyRef)
			if _, ok := layers[candyName]; !ok {
				if IsRemoteCandyRef(candyRef) {
					parsed := ParseRemoteRef(candyRef)
					errs.Add("kind:local %q: remote candy %q not found (candy %q doesn't exist in %s)", name, candyRef, parsed.Name, parsed.RepoPath)
				} else {
					errs.Add("kind:local %q: candy %q not found", name, candyName)
				}
			}
		}
	}
}

// validateLocalDeployments checks every target:local deployment:
//   - `local: <name>` references must resolve to a kind:local template.
//   - `host:` non-`local` values must parse via ParseSSHTarget.
//   - `user:` and `ssh_args:` are only meaningful when `host:` is non-`local`.
//   - `user:` redundancy when `host: <inline-user>@<machine>` is set.
func validateLocalDeployments(dir string, errs *ValidationError) {
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return
	}
	dc := uf.ProjectBundleConfig()
	if dc == nil {
		return
	}
	for name, node := range dc.Bundle {
		if node.Target != "local" && node.Target != "" {
			continue
		}
		if node.Target == "" && !strings.HasSuffix(name, "-local") && name != "local" {
			// Default target is "pod"; only validate when explicit.
			continue
		}
		if node.From != "" {
			if uf.Local == nil || uf.Local[node.From] == nil {
				errs.Add("deployment %q: kind:local template %q not found", name, node.From)
			}
		}
		hostField := strings.TrimSpace(node.Host)
		isLocalDest := hostField == "" || hostField == "local"
		if !isLocalDest {
			if _, perr := ParseSSHTarget(hostField); perr != nil {
				errs.Add("deployment %q: invalid host %q: %v", name, hostField, perr)
			}
			if node.User != "" && strings.Contains(hostField, "@") {
				inlineUser, _, _ := strings.Cut(hostField, "@")
				if inlineUser != node.User {
					errs.Add("deployment %q: ambiguous user — host: %q has inline user %q but user: field is %q (remove one)",
						name, hostField, inlineUser, node.User)
				}
			}
		} else {
			if node.User != "" {
				errs.Add("deployment %q: user: field only meaningful when host: is non-local", name)
			}
			if len(node.SSHArgs) > 0 {
				errs.Add("deployment %q: ssh_args: field only meaningful when host: is non-local", name)
			}
		}
	}
}

// validateInitDependencies checks that images using an init system have the
// required dependency candy in their resolved dependency chain.
// For example, images with supervisord services must include the "supervisord" candy.
func validateInitDependencies(cfg *Config, initCfg *InitConfig, layers map[string]*Candy, errs *ValidationError) {
	if initCfg == nil {
		return
	}

	for imgName, img := range cfg.eachBox {
		if img.Enabled != nil && !*img.Enabled {
			continue
		}

		// Resolve all candies for this image (own + transitive deps)
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue // other validators handle resolution errors
		}

		// For each init system with a depends_candy, check if it's needed and present.
		// Candy-derived caps replace the prior img.Bootc magic flag.
		caps, _ := AggregateCandyCapabilities(layers, resolved)
		if caps == nil {
			caps = &AggregatedCandyCaps{Provided: map[string]bool{}}
		}
		isBootcFlavored := caps.PreserveUser
		for initName, def := range initCfg.Init {
			if def.DependsCandy == "" {
				continue // no dependency requirement (e.g., systemd is provided by base OS)
			}

			// Skip init systems whose RequiresCapabilities aren't met by the
			// current composition.
			if !initDefRequirementsMet(def, caps) {
				continue
			}
			// For bootc-flavored compositions with dual-init candies
			// (service: + system_services:), skip supervisord depends_candy
			// check when systemd is also triggered.
			if checkInitSystemRequirements(def, isBootcFlavored, resolved, layers) {
				continue
			}

			// Check if any candy requires this init system
			needsInit := collectInitSystemNeeds(initName, def, resolved, layers)

			if len(needsInit) == 0 {
				continue
			}

			// Check if the depends_candy is in the resolved candies
			hasDepCandy := false
			for _, candyName := range resolved {
				// candyName is the resolved-order map KEY — for a remotely
				// consumed @github candy that's the qualified path, not the bare
				// name. Compare the candy's identity (layer.Name), per the idiom
				// documented at generate.go ("for REMOTE candies candyName is the
				// full @github map key; local: candyName == layer.Name").
				if l, ok := layers[candyName]; ok && l.Name == def.DependsCandy {
					hasDepCandy = true
					break
				}
			}

			// Also check base chain — dependency may be provided by a parent image
			if !hasDepCandy {
				images, resolveErr := cfg.ResolveAllBox("unused", ".", ResolveOpts{})
				if resolveErr == nil {
					allCandies := collectAllBoxCandies(imgName, images, layers)
					if slices.Contains(allCandies, def.DependsCandy) {
						hasDepCandy = true
					}
				}
			}

			if !hasDepCandy {
				// For dual-init candies (e.g., sshd with both service: and system_services:),
				// skip the error if ALL triggering candies also support another init system.
				// The candy is designed to use whichever init system the image provides.
				if !checkDualInitFallback(initName, resolved, layers, initCfg) {
					errs.Add("box %q has candies requiring %s (%s) but missing the %q candy in its dependency chain; add %q to the box's candies or a base image",
						imgName, initName, strings.Join(needsInit, ", "), def.DependsCandy, def.DependsCandy)
				}
			}
		}
	}
}

// checkInitSystemRequirements reports whether the depends_candy check for an
// init system should be skipped: for bootc-flavored compositions with dual-init
// candies (service: + system_services:), the supervisord depends_candy check is
// skipped when systemd is also triggered by a resolved candy.
func checkInitSystemRequirements(def *ResolvedInit, isBootcFlavored bool, resolved []string, layers map[string]*Candy) bool {
	if len(def.RequiresCapability) == 0 && isBootcFlavored {
		for _, candyName := range resolved {
			if layer, ok := layers[candyName]; ok && layer.HasInit("systemd") {
				return true
			}
		}
	}
	return false
}

// collectInitSystemNeeds returns the human-readable list of resolved candies
// that require the given init system (directly via HasInit, or via port_relay
// when the init def carries a relay template).
func collectInitSystemNeeds(initName string, def *ResolvedInit, resolved []string, layers map[string]*Candy) []string {
	var needsInit []string
	for _, candyName := range resolved {
		layer, ok := layers[candyName]
		if !ok {
			continue
		}
		if layer.HasInit(initName) {
			needsInit = append(needsInit, candyName+" ("+initName+")")
		}
		// port_relay triggers init systems with relay_template
		if initHasRelayTemplate(def) && len(layer.PortRelayPorts) > 0 {
			needsInit = append(needsInit, candyName+" (port_relay)")
		}
	}
	return needsInit
}

// checkDualInitFallback reports whether ALL candies triggering the given init
// system also support an alternative init system (dual-init candies like sshd
// with both service: and system_services:). When true, a missing depends_candy
// is not an error — the candy uses whichever init system the image provides.
func checkDualInitFallback(initName string, resolved []string, layers map[string]*Candy, initCfg *InitConfig) bool {
	allDualInit := true
	for _, candyName := range resolved {
		layer, ok := layers[candyName]
		if !ok || !layer.HasInit(initName) {
			continue
		}
		hasAlternativeInit := false
		for altName := range initCfg.Init {
			if altName != initName && layer.HasInit(altName) {
				hasAlternativeInit = true
				break
			}
		}
		if !hasAlternativeInit {
			allDualInit = false
			break
		}
	}
	return allDualInit
}

// validateBuildAndDistro validates build: and distro: entries.
// build: entries are checked against the embedded distro format definitions (charly/charly.yml).
// distro: is free-form (any string, including distro:version).
func validateBuildAndDistro(cfg *Config, distroCfg *DistroConfig, errs *ValidationError) {
	validateBuild := func(context string, build BuildFormats) {
		for _, b := range build {
			if !distroCfg.ValidFormat(b) {
				errs.Add("%s: build entry %q is not valid (known formats: %s)", context, b, strings.Join(distroCfg.AllFormatNames(), ", "))
			}
		}
		// Check for duplicates
		seen := make(map[string]bool)
		for _, b := range build {
			if seen[b] {
				errs.Add("%s: duplicate build entry %q", context, b)
			}
			seen[b] = true
		}
	}

	// Validate defaults
	validateBuild("defaults", cfg.Defaults.Build)

	// Validate per-image
	for name, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}
		validateBuild(fmt.Sprintf("box %q", name), img.Build)
		// box check_level enum is enforced by #Box; the build-format set is
		// dynamic (the embedded vocabulary, not a static CUE enum) so it stays validated here.
	}
}

// validateCandyReferences ensures all candies referenced in images exist
func validateCandyReferences(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	for boxName, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}
		for _, candyRef := range img.Candy {
			candyName := BareRef(candyRef)
			if _, ok := layers[candyName]; !ok {
				if IsRemoteCandyRef(candyRef) {
					parsed := ParseRemoteRef(candyRef)
					errs.Add("box %q: remote candy %q not found (candy %q doesn't exist in %s)", boxName, candyRef, parsed.Name, parsed.RepoPath)
				} else {
					suggestion := findSimilarName(candyName, CandyNames(layers))
					if suggestion != "" {
						errs.Add("box %q: candy %q not found (did you mean %q?)", boxName, candyName, suggestion)
					} else {
						errs.Add("box %q: candy %q not found", boxName, candyName)
					}
				}
			}
		}
	}
}

// validateCandyContents validates each candy has required files
func validateCandyContents(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		// Candy must have at least one install file, a candy: field (composition),
		// data declarations, an external_builder: selection, a localpkg: source, OR a
		// plugin: block — a plugin candy's content IS its provider declaration (its Go
		// provider is compiled in or fetched+built), an external_builder: candy's content
		// is the multi-stage build artifact the selected builder bakes at image build
		// (emitExternalBuilderStages), and a localpkg: candy's content is the OS package
		// it builds + installs on a deploy target (LocalPkgInstallStep) / downloads at
		// image build — so all legitimately ship no install files.
		if !layer.HasInstallFiles() && len(layer.IncludedCandy) == 0 && !layer.HasData() && layer.Plugin == nil && layer.ExternalBuilder == "" && len(layer.LocalPkgFormats()) == 0 {
			errs.Add("candy %q: must have at least one install file (candy manifest distro: packages, root.yml, pixi.toml, pyproject.toml, environment.yml, package.json, Cargo.toml, or user.yml), a candy: field, a localpkg:, an external_builder:, or a plugin: block", name)
		}

		// version: (mandatory CalVer) and status: (working|testing|broken enum)
		// are enforced by #Candy; ADE plan-completeness below is not.

		// ADE is MANDATORY per candy: every local candy MUST ship a non-empty
		// `description:` string AND a `plan:` containing at least one
		// DETERMINISTIC `check:` step so the agentless check always has
		// something to verify (the spec IS the test). Scoped to local candies —
		// a fetched remote candy's compliance is its own repo's concern (same
		// scope as the version: check). See CLAUDE.md "Agent Driven Evaluation
		// (ADE)" + /charly-check:check.
		if !layer.Remote {
			checkSteps := 0
			for i := range layer.plan {
				if layer.plan[i].Check != "" {
					checkSteps++
				}
			}
			switch {
			case strings.TrimSpace(layer.Description) == "":
				errs.Add("candy %q: missing required `description:` string (ADE is mandatory). See /charly-check:check", name)
			case len(layer.plan) == 0:
				errs.Add("candy %q: missing required `plan:` list (ADE is mandatory; the spec IS the test). See /charly-check:check", name)
			case checkSteps == 0:
				errs.Add("candy %q: `plan:` must contain at least one `check:` step so the agentless check has something to verify. See /charly-check:check", name)
			default:
				for _, issue := range validatePlanSteps(layer.Description, layer.plan, "candy "+name) {
					errs.Add("%s", issue)
				}
			}
		}

		// If `directory:` redirected the source anchor, SourceDir must exist.
		// (For the default case SourceDir == Path, which is guaranteed to exist.)
		if layer.SourceDir != layer.Path && !dirExists(layer.SourceDir) {
			errs.Add("candy %q: directory %q does not exist (resolved to %q)", name, layer.SourceDir, layer.SourceDir)
		}

		// Plugin block (when present): capabilities well-formed; builtin providers
		// actually compiled into charly.
		for _, issue := range validatePluginCandy(name, layer.Plugin) {
			errs.Add("%s", issue)
		}

		// Cargo.toml requires src/ directory
		if layer.HasCargoToml && !layer.HasSrcDir {
			errs.Add("candy %q: Cargo.toml requires src/ directory", name)
		}

		// Validate depends references. Remote candies' deps were qualified to
		// fully-qualified map keys at scan time (qualifyRemoteSiblingDeps), so a
		// direct lookup covers both local short names and remote sibling refs.
		for _, depRef := range layer.Require {
			dep := depRef.Bare()
			if _, ok := layers[dep]; !ok {
				suggestion := findSimilarName(dep, CandyNames(layers))
				if suggestion != "" {
					errs.Add("candy %q depends: unknown candy %q (did you mean %q?)", name, dep, suggestion)
				} else {
					errs.Add("candy %q depends: unknown candy %q", name, dep)
				}
			}
		}

		// extract source/path/dest presence + path/dest absoluteness are enforced
		// by #CandyExtract.

		// Validate shell:-schema declarations (2026-05 cutover).
		validateCandyShell(name, layer.Shell(), errs)

		// Validate apk:-package-format entries (Android app installs).
		validateCandyApk(name, layer.Apk(), errs)
	}
}

// validateCandyApk enforces the `apk:` package-format invariants:
//   - exactly one of package: / apk: per entry (download by id XOR a
//     committed local APK),
//   - source: (when set) is one of the apkeep sources.
func validateCandyApk(candyName string, apks []ApkPackageSpec, errs *ValidationError) {
	// package⊕apk one-of + the source enum are enforced by #CandyApk; only the
	// "source applies only to package installs" cross-field rule stays here.
	for i, a := range apks {
		if a.Apk != "" && a.Source != "" {
			errs.Add("candy %q apk[%d]: `source:` applies only to `package:` (apkeep) installs, not committed `apk:` files", candyName, i)
		}
	}
}

// validateCandyShell enforces the shell:-schema invariants:
//   - Per-shell sub-block keys are restricted to the ShellAllowlist
//     (bash/zsh/fish/sh). Anything else is a hard error.
//   - When `init:` is declared (intrinsic OR per-shell), it must be
//     non-empty.
//   - `path:` overrides must not contain `..` traversal sequences and,
//     when present, must be either absolute or `~/`-prefixed.
//   - `path_append` entries follow the same path-traversal guard.
//   - Empty config (no init, no path_append, no per-shell subs, no
//     intrinsic path) is allowed and produces no warning — the parser
//     would have rejected anything genuinely malformed.
func validateCandyShell(candyName string, cfg *ShellConfig, errs *ValidationError) {
	if cfg == nil {
		return
	}
	checkSpec := func(label string, spec *ShellSpec) {
		if spec == nil {
			return
		}
		if spec.Init != "" && strings.TrimSpace(spec.Init) == "" {
			errs.Add("candy %q: shell.%s.init must not be whitespace-only", candyName, label)
		}
		if spec.Path != "" {
			validateShellPath(candyName, fmt.Sprintf("shell.%s.path", label), spec.Path, errs)
		}
		for _, p := range spec.PathAppend {
			validateShellPath(candyName, fmt.Sprintf("shell.%s.path_append", label), p, errs)
		}
	}
	// Intrinsic body — same checks as a per-shell spec.
	if cfg.Init != "" && strings.TrimSpace(cfg.Init) == "" {
		errs.Add("candy %q: shell.init must not be whitespace-only", candyName)
	}
	if cfg.Path != "" {
		validateShellPath(candyName, "shell.path", cfg.Path, errs)
	}
	for _, p := range cfg.PathAppend {
		validateShellPath(candyName, "shell.path_append", p, errs)
	}
	// Per-shell sub-block keys (bash/zsh/fish/sh) are enforced by the closed
	// #Shell def; here we validate each sub-block's init/path semantics.
	for shell, spec := range cfg.ByShell() {
		checkSpec(shell, spec)
	}
}

// validateShellPath rejects path-traversal sequences and accepts only
// absolute paths or `~/`-prefixed paths (which ExpandPath resolves at
// install time).
func validateShellPath(candyName, field, p string, errs *ValidationError) {
	if p == "" {
		return
	}
	if strings.Contains(p, "..") {
		errs.Add("candy %q: %s contains traversal sequence (got %q)", candyName, field, p)
		return
	}
	if !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "~/") && p != "~" {
		errs.Add("candy %q: %s must be absolute or ~/-prefixed (got %q)", candyName, field, p)
	}
}

// validateCandyIncludes validates candy composition (candy: field in the candy manifest)
func validateCandyIncludes(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.IncludedCandy) == 0 {
			continue
		}

		depSet := make(map[string]bool)
		for _, d := range layer.Require {
			depSet[d.Bare()] = true
		}

		for _, includedRef := range layer.IncludedCandy {
			ref := includedRef.Bare()
			// Self-inclusion
			if ref == name {
				errs.Add("candy %q candy: cannot include itself", name)
				continue
			}

			// Check ref exists. Deps are pre-qualified at scan time, so a direct
			// lookup covers both local short names and remote sibling refs.
			if _, ok := layers[ref]; !ok {
				suggestion := findSimilarName(ref, CandyNames(layers))
				if suggestion != "" {
					errs.Add("candy %q candy: unknown candy %q (did you mean %q?)", name, ref, suggestion)
				} else {
					errs.Add("candy %q candy: unknown candy %q", name, ref)
				}
			}

			// Overlap with depends
			if depSet[ref] {
				errs.Add("candy %q: %q appears in both 'candy' and 'depends'", name, ref)
			}
		}
	}

	// Check for circular composition
	for name, layer := range layers {
		if len(layer.IncludedCandy) == 0 {
			continue
		}
		if err := checkIncludeCycle(name, layers, nil); err != nil {
			errs.Add("candy %q: %v", name, err)
		}
	}
}

// checkIncludeCycle detects circular candy composition
func checkIncludeCycle(name string, layers map[string]*Candy, visited map[string]bool) error {
	if visited == nil {
		visited = make(map[string]bool)
	}
	if visited[name] {
		return fmt.Errorf("circular candy composition involving %q", name)
	}
	layer, ok := layers[name]
	if !ok || len(layer.IncludedCandy) == 0 {
		return nil
	}
	visited[name] = true
	for _, includedRef := range layer.IncludedCandy {
		if err := checkIncludeCycle(includedRef.Bare(), layers, visited); err != nil {
			return err
		}
	}
	delete(visited, name)
	return nil
}

// validatePkgConfig validates per-distro/format package config in the candy
// manifest. Packages live in per-distro tag sections (debian, ubuntu, debian:13,
// …) plus the always-included top-level base; the only format section is `aur`.
// repo/copr/module entries are meaningful only when the candy installs packages,
// but a repo may sit on one distro level while the package it serves sits at the
// top-level base (the nodesource pattern) — so the "requires packages" gate is
// the WHOLE-CANDY union (HasAnyPackages), not the single section. Repo entries
// must carry a name. The canonical repo key is `repo` (singular) — what
// derivePackageSectionsFromCalamares writes and DistroPackages.Repo unmarshals.
func validatePkgConfig(layers map[string]*Candy, errs *ValidationError) {
	validateRaw := func(name, label string, raw map[string]any, candyHasPkgs bool) {
		if raw == nil {
			return
		}
		// repo-entry name presence is enforced by #RepoBlock; only the
		// cross-section "repo/copr/modules require packages" union gate stays here.
		if repos := toMapSlice(raw["repo"]); len(repos) > 0 && !candyHasPkgs {
			errs.Add("candy %q candy manifest: %s.repo requires packages (none declared anywhere in the candy)", name, label)
		}
		if copr := toStringSlice(raw["copr"]); len(copr) > 0 && !candyHasPkgs {
			errs.Add("candy %q candy manifest: %s.copr requires packages", name, label)
		}
		if modules := toStringSlice(raw["modules"]); len(modules) > 0 && !candyHasPkgs {
			errs.Add("candy %q candy manifest: %s.modules requires packages", name, label)
		}
	}
	for name, layer := range layers {
		hasPkgs := layer.HasAnyPackages()
		for tag, cfg := range layer.tagSections {
			validateRaw(name, "distro."+tag, cfg.Raw, hasPkgs)
		}
		for formatName, section := range layer.formatSections {
			validateRaw(name, formatName, section.Raw, hasPkgs)
		}
	}
}

// validateBaseReferences ensures base references resolve
func validateBaseReferences(cfg *Config, errs *ValidationError) {
	// Base references can be:
	// 1. External OCI images (always valid)
	// 2. Names of other images in charly.yml (validated by image DAG check)
	// No additional validation needed here
}

// validateBoxDAG checks for circular image dependencies
func validateBoxDAG(cfg *Config, layers map[string]*Candy, dir string, opts ResolveOpts, errs *ValidationError) {
	calverTag := "test"
	// Try to resolve images — some fields may be missing during basic validation
	images := make(map[string]*ResolvedBox)
	for name, img := range cfg.eachBox {
		if !img.IsEnabled() && !opts.shouldIncludeDisabled(name) {
			continue
		}
		ri, err := cfg.ResolveBox(name, calverTag, dir, opts)
		if err != nil {
			// Skip images that can't resolve (e.g., missing build: field)
			continue
		}
		images[name] = ri
	}
	if len(images) == 0 {
		return
	}
	// Pull in namespace-qualified base images (e.g. `versa: base: cachyos.cachyos`)
	// so the DAG's base edges don't dangle — same enrichment ResolveAllImages does.
	// SURFACE a namespace-resolution error: an enabled image references a
	// namespaced base — or that base's builder / bootstrap builder — that does
	// not resolve. This is the automatic guard that catches namespace-ref leaks
	// (e.g. a pulled base's `builder: charly.arch-builder` not re-qualified to
	// `selkies.charly.arch-builder`) at `charly box validate` time, before a build hits
	// it. The DAG check below can't run with dangling bases, so report + return.
	if err := cfg.resolveNamespacedBases(images, calverTag, dir, opts); err != nil {
		errs.Add("namespaced base resolution: %v", err)
		return
	}

	_, orderErr := ResolveBoxOrder(images, layers)
	if orderErr != nil {
		if cycleErr, ok := errors.AsType[*CycleError](orderErr); ok {
			errs.Add("box dependency cycle: %s", strings.Join(cycleErr.Cycle, " -> "))
		} else {
			errs.Add("box DAG error: %v", orderErr)
		}
		// A cyclic / broken image DAG was already reported; the global candy
		// order over it is meaningless, so stop here.
		return
	}

	// Resolve the GLOBAL candy order over the enriched image set (locals PLUS the
	// namespace-pulled bases/builders above) — the SAME computation that
	// `charly box generate`/`build` run (ComputeIntermediates → GlobalCandyOrder).
	// validateCandyDAG only iterates cfg.Box, so a candy missing from a PULLED
	// builder (e.g. an imported charly.fedora-builder's rpmfusion) slipped past
	// validate yet failed generate; running it here restores validate↔generate
	// agreement so the gap is caught at validate time, not only at build time.
	// Reached only on an acyclic DAG (the cycle guard above returned otherwise).
	if _, glErr := GlobalCandyOrder(images, layers); glErr != nil {
		errs.Add("global candy order: %v", glErr)
	}
}

// validateCandyDAG checks for circular candy dependencies
func validateCandyDAG(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Check each image's candies for cycles
	for boxName, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}
		// Convert raw refs to bare refs for candy map lookup
		bareCandies := make([]string, len(img.Candy))
		for i, ref := range img.Candy {
			bareCandies[i] = BareRef(ref)
		}
		_, err := ResolveCandyOrder(bareCandies, layers, nil)
		if err != nil {
			if cycleErr, ok := errors.AsType[*CycleError](err); ok {
				errs.Add("box %q: candy dependency cycle: %s", boxName, strings.Join(cycleErr.Cycle, " -> "))
			} else {
				errs.Add("box %q: candy resolution error: %v", boxName, err)
			}
		}
	}
}

// validatePort validates port declarations in candies and images

// validateRoutes validates route file declarations in candies

// validateMergeConfig validates merge configuration
func validateMergeConfig(cfg *Config, errs *ValidationError) {
	// box-entity merge.max_mb >= 0 is enforced by #BoxMerge; the `defaults:`
	// block is NOT validated against #Box, so its check stays here.
	if m := cfg.Defaults.Merge; m != nil && m.MaxMB < 0 {
		errs.Add("defaults: merge max_mb must be > 0, got %d", m.MaxMB)
	}
}

// validBuildCacheModes is the allow-list for defaults.cache / image.cache.
// Empty string means "auto" (resolved at build time in cacheArgs).
var validBuildCacheModes = map[string]bool{
	"": true, "image": true, "registry": true, "gha": true, "none": true,
}

// validateBuildTunables validates the build-speed knobs on defaults: and any
// image entry: jobs >= 1, podman_jobs >= 0, podman_jobs_cap >= 1, cache in
// the allow-list, and no empty context_ignore entries. These are project-wide
// defaults; values are validated wherever they appear so a typo surfaces at
// `charly box validate` rather than silently mis-driving a build.
func validateBuildTunables(cfg *Config, errs *ValidationError) {
	check := func(name string, ic BoxConfig) {
		if ic.Jobs != nil && *ic.Jobs < 1 {
			errs.Add("%s: jobs must be >= 1, got %d", name, *ic.Jobs)
		}
		if ic.PodmanJobs != nil && *ic.PodmanJobs < 0 {
			errs.Add("%s: podman_jobs must be >= 0, got %d", name, *ic.PodmanJobs)
		}
		if ic.PodmanJobsCap != nil && *ic.PodmanJobsCap < 1 {
			errs.Add("%s: podman_jobs_cap must be >= 1, got %d", name, *ic.PodmanJobsCap)
		}
		if !validBuildCacheModes[ic.Cache] {
			errs.Add("%s: cache must be one of image|registry|gha|none, got %q", name, ic.Cache)
		}
		for i, p := range ic.ContextIgnore {
			if strings.TrimSpace(p) == "" {
				errs.Add("%s: context_ignore[%d] must not be empty", name, i)
			}
		}
		if ic.KeepImages != nil && *ic.KeepImages < 0 {
			errs.Add("%s: keep_images must be >= 0 (0 = disabled), got %d", name, *ic.KeepImages)
		}
		if ic.KeepCheckRuns != nil && *ic.KeepCheckRuns < 0 {
			errs.Add("%s: keep_check_runs must be >= 0 (0 = disabled), got %d", name, *ic.KeepCheckRuns)
		}
	}

	check("defaults", cfg.Defaults)
	for name, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}
		check(fmt.Sprintf("box %q", name), img)
	}
}

// validateVolume checks the cross-entry duplicate-name invariant CUE cannot
// express; volume name char-set + name/path presence are enforced by #CandyVolume.
func validateVolume(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasVolumes() {
			continue
		}
		seen := make(map[string]bool)
		for _, vol := range layer.Volume() {
			if seen[vol.Name] {
				errs.Add("candy %q candy manifest volumes: duplicate volume name %q", name, vol.Name)
			}
			seen[vol.Name] = true
		}
	}
}

// validateAliases validates alias declarations in candies and images
func validateAliases(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate candy aliases
	for name, layer := range layers {
		if !layer.HasAliases() {
			continue
		}
		// name + command presence are enforced by #CandyAlias; the name char-set
		// and cross-entry dedup are not.
		seen := make(map[string]bool)
		for _, a := range layer.Alias() {
			if a.Name != "" && !aliasNameRe.MatchString(a.Name) {
				errs.Add("candy %q candy manifest aliases: name %q must match [a-zA-Z0-9][a-zA-Z0-9._-]*", name, a.Name)
			}
			if seen[a.Name] {
				errs.Add("candy %q candy manifest aliases: duplicate alias name %q", name, a.Name)
			}
			seen[a.Name] = true
		}
	}

	// Validate image-level aliases
	for boxName, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}
		if len(img.Alias) == 0 {
			continue
		}
		seen := make(map[string]bool)
		for _, a := range img.Alias {
			switch {
			case a.Name == "":
				errs.Add("box %q aliases: missing required \"name\" field", boxName)
			case !aliasNameRe.MatchString(a.Name):
				errs.Add("box %q aliases: name %q must match [a-zA-Z0-9][a-zA-Z0-9._-]*", boxName, a.Name)
			case seen[a.Name]:
				errs.Add("box %q aliases: duplicate alias name %q", boxName, a.Name)
			default:
				seen[a.Name] = true
			}
		}
	}
}

// validateBuilders validates builder and builds configuration
//
//nolint:gocyclo // builder validation: peer checks on defaults/produces/per-image builders/resolved formats; extraction fragments the validator
func validateBuilders(cfg *Config, layers map[string]*Candy, builderCfg *BuilderConfig, dir string, opts ResolveOpts, errs *ValidationError) {
	// Validate defaults.builder entries
	for typ, builder := range cfg.Defaults.Builder {
		if !builderCfg.ValidBuilderType(typ) {
			errs.Add("defaults.builder: build type %q is not valid (known builders: %s)", typ, strings.Join(builderCfg.BuilderNames(), ", "))
		}
		if builder != "" {
			// Namespace-aware: a defaults builder ref may be qualified (e.g.
			// `charly.fedora-builder`), resolving through an import namespace.
			builderImg, _, exists := cfg.resolveBoxRef(builder)
			if !exists {
				errs.Add("defaults.builder.%s: box %q not found", typ, builder)
			} else if !builderImg.IsEnabled() {
				errs.Add("defaults.builder.%s: box %q is disabled", typ, builder)
			}
		}
	}

	// Validate each enabled image
	for boxName, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}

		// Validate builds: entries (capability declarations on builder images)
		for _, b := range img.Produce {
			if !builderCfg.ValidBuilderType(b) {
				errs.Add("box %q: builds entry %q is not valid (known builders: %s)", boxName, b, strings.Join(builderCfg.BuilderNames(), ", "))
			}
		}

		// Validate builder: entries (per-type builder assignments)
		for typ, builder := range img.Builder {
			if !builderCfg.ValidBuilderType(typ) {
				errs.Add("box %q: builder.%s is not a valid build type (known builders: %s)", boxName, typ, strings.Join(builderCfg.BuilderNames(), ", "))
			}
			if builder == boxName {
				errs.Add("box %q: builder.%s cannot reference self", boxName, typ)
				continue
			}
			if builder != "" {
				// Namespace-aware: a builder ref may be qualified (e.g.
				// `charly.arch-builder`), resolving through an import namespace.
				builderImg, _, exists := cfg.resolveBoxRef(builder)
				if !exists {
					errs.Add("box %q: builder.%s references %q which is not found", boxName, typ, builder)
					continue
				}
				if !builderImg.IsEnabled() {
					errs.Add("box %q: builder.%s references %q which is disabled", boxName, typ, builder)
					continue
				}
				// Check builder declares this capability
				hasCapability := slices.Contains(builderImg.Produce, typ)
				if len(builderImg.Produce) > 0 && !hasCapability {
					errs.Add("box %q: builder.%s references %q which does not declare builds: [%s]", boxName, typ, builder, typ)
				}
			}
		}

		// Resolve the image through the SINGLE canonical path (ResolveBox) —
		// the SAME resolution `charly box build` / `generate` / `inspect` use — so
		// the effective builder map and build formats checked here are exactly
		// what the build will see. This block previously RE-IMPLEMENTED builder +
		// build-format resolution inline, which silently diverged from
		// ResolveBox (it missed the distro-keyed builder default, so a cachyos
		// image's auto-resolved arch-builder — including aur — was invisible to
		// validation and produced a false "no builder.aur configured" error).
		// One code path, no drift.
		ri, rerr := cfg.ResolveBox(boxName, "test", dir, opts)
		if rerr != nil {
			// Can't resolve (e.g. a namespaced base unavailable during basic
			// validation) — validateBoxDAG surfaces resolution errors; skip
			// the builder-needed check rather than false-positive on a
			// half-resolved map.
			continue
		}
		resolved := ri.Builder
		buildFmtSet := make(map[string]bool, len(ri.BuildFormats))
		for _, f := range ri.BuildFormats {
			buildFmtSet[f] = true
		}

		// Check if candies need builders that aren't configured.
		// Detection is fully config-driven from the embedded builder: vocabulary:
		//   detect_files: candy has any of these files
		//   detect_config: candy has this format section with packages
		candyOrder, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue
		}
		for _, candyName := range candyOrder {
			layer, ok := layers[candyName]
			if !ok {
				continue
			}
			for builderName, builderDef := range builderCfg.Builder {
				fileMatched := false
				for _, f := range builderDef.DetectFiles {
					if layer.HasFile(f) {
						fileMatched = true
						break
					}
				}
				configMatched := false
				if builderDef.DetectConfig != "" && candyHasFormatConfig(layer, builderDef.DetectConfig) {
					configMatched = true
				}
				if !fileMatched && !configMatched {
					continue
				}
				// Distro-aware gate: when detection fired ONLY via a
				// format-section (DetectConfig, e.g. aur:) and not via files,
				// skip the check if the image's resolved BuildFormats doesn't
				// include that format. The IR compiler
				// (install_build.go:236-249) iterates only img.BuildFormats, so
				// the section is unreachable for this image — the builder is
				// not actually needed even though the candy has the section.
				// Multi-distro candies can carry rpm: + pac: + aur:
				// simultaneously without forcing every Fedora consumer to
				// declare an arch-builder.
				//
				// detect_files-based detection is NOT gated: pixi/npm/cargo
				// artifacts are produced once at build time and copied into
				// the final stage regardless of distro, so the builder is
				// required for any image consuming the candy.
				if !fileMatched && configMatched && !buildFmtSet[builderDef.DetectConfig] {
					continue
				}
				if !resolved.HasBuilder(builderName) {
					errs.Add("box %q: candy %q needs builder %q but no builder.%s configured", boxName, candyName, builderName, builderName)
				}
			}
		}
	}
}

// validateDNS is a no-op in schema v4. DNS and AcmeEmail moved off
// BoxConfig to BundleNode (they're deployment choices). Deploy-side
// validation of these fields is handled by validateDeployConfig.
func validateDNS(cfg *Config, errs *ValidationError) {
	// intentionally empty — schema v4 removed image-level dns/acme_email
}

// validateRemoteCandies checks remote candy consistency
func validateRemoteCandies(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Check version conflicts (same repo referenced with different versions)
	_, err := CollectRemoteRefs(cfg, layers)
	if err != nil {
		errs.Add("%v", err)
	}

	// Check for naming conflicts between remote candies from different repos
	for _, layer := range layers {
		if !layer.Remote {
			continue
		}
		for _, other := range layers {
			if !other.Remote || other == layer {
				continue
			}
			if other.Name == layer.Name && other.RepoPath != layer.RepoPath {
				errs.Add("remote candy name conflict: %q provided by both %s and %s", layer.Name, layer.RepoPath, other.RepoPath)
			}
		}
	}
}

// validateSystemdServices validates systemd .service files in candies
func validateSystemdServices(_ *Config, layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.ServiceFiles()) == 0 {
			continue
		}
		for _, svcPath := range layer.ServiceFiles() {
			info, err := os.Stat(svcPath)
			if err != nil {
				errs.Add("candy %q: systemd service file %q not readable: %v", name, filepath.Base(svcPath), err)
				continue
			}
			if info.Size() == 0 {
				errs.Add("candy %q: systemd service file %q is empty", name, filepath.Base(svcPath))
			}
		}
	}
}

// validatePackagedServices validates use_packaged: entries in each candy's
// service: list. Use_packaged names the distro-shipped systemd unit that the
// entry reuses (e.g. "sshd.service", "postgresql.service"). Rules:
//   - use_packaged name cannot contain paths or spaces (must be a unit name).
//   - The candy must declare packages that provide those units.
//   - Packaged entries only work on bootc / systemd images; warn otherwise.
func validatePackagedServices(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	candyHasPackaged := func(l *Candy) bool {
		for i := range l.Service() {
			if l.Service()[i].IsPackaged() {
				return true
			}
		}
		return false
	}
	for name, layer := range layers {
		for i := range layer.Service() {
			entry := &layer.Service()[i]
			if !entry.IsPackaged() {
				continue
			}
			unit := entry.UsePackaged
			if unit == "" {
				errs.Add("candy %q: service[%d] use_packaged cannot be empty", name, i)
			}
			if strings.Contains(unit, "/") || strings.Contains(unit, " ") {
				errs.Add("candy %q: service[%d] use_packaged %q must be a unit name (no paths or spaces)", name, i, unit)
			}
		}
		if candyHasPackaged(layer) && !layer.HasAnyPackages() {
			errs.Add("candy %q: use_packaged entries require candy packages (distro tag sections or top-level package:) that provide those units", name)
		}
	}

	// Warn if use_packaged is used on a composition that doesn't preserve
	// the candy USER (i.e. non-bootc-flavored images). Only systemd-based
	// compositions consume packaged units; supervisord, the container
	// default init, can't.
	for boxName, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}
		// Resolve candy order to aggregate caps for this image.
		var imgCandies []string
		for _, ref := range img.Candy {
			imgCandies = append(imgCandies, BareRef(ref))
		}
		caps, _ := AggregateCandyCapabilities(layers, imgCandies)
		if caps != nil && caps.PreserveUser {
			continue
		}
		for _, candyRef := range img.Candy {
			bare := BareRef(candyRef)
			layer, ok := layers[bare]
			if !ok || !candyHasOrphanPackaged(layer) {
				continue
			}
			fmt.Fprintf(os.Stderr, "Warning: box %q includes candy %q with a packaged-only service (no custom-exec sibling), but composition does not preserve_user (its systemd unit will be ignored)\n", boxName, bare)
		}
	}
}

// candyHasOrphanPackaged reports whether the candy has a use_packaged service
// entry with NO custom-exec sibling of the same name. Only such "orphan"
// packaged units are genuinely dropped under supervisord (the container default
// init can't consume packaged systemd units), so only they warrant the
// preserve_user warning. When a same-name exec sibling exists — the canonical
// mixed-form init polymorphism pattern (e.g. sshd: use_packaged sshd.service +
// exec sshd-wrapper) — supervisord renders the exec form, nothing is lost, and
// warning would be a false positive.
func candyHasOrphanPackaged(l *Candy) bool {
	if l == nil {
		return false
	}
	svcs := l.Service()
	customNames := make(map[string]bool)
	for i := range svcs {
		if !svcs[i].IsPackaged() && svcs[i].Exec != "" && svcs[i].Name != "" {
			customNames[svcs[i].Name] = true
		}
	}
	for i := range svcs {
		if svcs[i].IsPackaged() && !customNames[svcs[i].Name] {
			return true
		}
	}
	return false
}

// findSimilarName finds a similar name for typo suggestions
func findSimilarName(target string, candidates []string) string {
	// Simple Levenshtein-like check for close matches
	for _, candidate := range candidates {
		if levenshteinDistance(target, candidate) <= 2 {
			return candidate
		}
	}
	return ""
}

// levenshteinDistance calculates the edit distance between two strings
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Create matrix
	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
		matrix[i][0] = i
	}
	for j := range matrix[0] {
		matrix[0][j] = j
	}

	// Fill matrix
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			matrix[i][j] = min(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}

	return matrix[len(a)][len(b)]
}

// validateLibvirt validates libvirt XML snippets in candies and images
func validateLibvirt(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate candy-level snippets
	for name, layer := range layers {
		if !layer.HasLibvirt() {
			continue
		}
		for i, snippet := range layer.Libvirt() {
			if err := ValidateLibvirtSnippet(snippet); err != nil {
				errs.Add("candy %q libvirt[%d]: %v", name, i, err)
			}
		}
	}

	// Image-level `libvirt:` field was removed in the VM hard-cutover.
	// Raw XML snippets live on candy `libvirt:` fields (validated above)
	// and on `kind: vm` entity `spec.libvirt.snippets:` lists. The latter
	// are modeled typed-open (`[...string]`) by #Vm in schema/vm.cue; their
	// XML well-formedness is not checked at config time (it surfaces at
	// libvirt-define time) — ValidateLibvirtSnippet remains the candy/image
	// check above.
	_ = cfg
	_ = layers
}

// validateEngineConfig validates engine declarations in candies and images
func validateEngineConfig(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// The candy engine enum (docker|podman) is enforced by #Candy.engine; only
	// cross-candy engine-conflict detection within an image stays here.

	for boxName, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}

		// Check for conflicting candy engine requirements within the image
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue
		}

		engineSources := make(map[string]string) // engine value -> first candy declaring it
		for _, candyName := range resolved {
			layer, ok := layers[candyName]
			if !ok {
				continue
			}
			if e := layer.Engine(); e != "" {
				if _, exists := engineSources[e]; !exists {
					engineSources[e] = candyName
				}
			}
		}
		if len(engineSources) > 1 {
			conflicts := make([]string, 0, len(engineSources))
			for e, l := range engineSources {
				conflicts = append(conflicts, fmt.Sprintf("%s (from candy %s)", e, l))
			}
			sort.Strings(conflicts)
			errs.Add("box %q: conflicting engine requirements: %s", boxName, strings.Join(conflicts, ", "))
		}
	}
}

// validatePortRelay validates port_relay declarations in candies
func validatePortRelay(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if len(layer.PortRelayPorts) == 0 {
			continue
		}
		// Port range (1-65535) is enforced by #Candy.port_relay; dedup +
		// relay-port-declared-in-ports + the socat-presence gate stay here.
		portSet := make(map[int]bool)
		for _, port := range layer.PortRelayPorts {
			if portSet[port] {
				errs.Add("candy %q port_relay: duplicate port %d", name, port)
			}
			portSet[port] = true
		}

		// Warn if relay port isn't declared in the candy's ports
		if layer.HasPorts() {
			candyPorts := make(map[int]bool)
			for _, ps := range layer.PortSpecs() {
				candyPorts[ps.Port] = true
			}
			for _, port := range layer.PortRelayPorts {
				if !candyPorts[port] {
					errs.Add("candy %q port_relay: port %d is not declared in the candy's ports", name, port)
				}
			}
		} else {
			errs.Add("candy %q port_relay: candy has no ports declared", name)
		}
	}

	// Validate that images with port_relay candies include the socat candy
	for boxName, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue
		}
		hasRelay := false
		hasSocat := false
		for _, candyName := range resolved {
			layer, ok := layers[candyName]
			if !ok {
				continue
			}
			if len(layer.PortRelayPorts) > 0 {
				hasRelay = true
			}
			if layer.Name == "socat" {
				hasSocat = true
			}
		}
		if hasRelay && !hasSocat {
			errs.Add("box %q: has port_relay candies but missing \"socat\" candy (add it to the box candies or as a dependency)", boxName)
		}
	}
}

func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// candyHasFile checks if a candy has a specific file (used for builder detection).

// candyHasFormatConfig checks if a candy has a non-empty config section for a format.
// Fully generic — uses the FormatSection accessor which checks both typed and dynamic sections.
func candyHasFormatConfig(layer *Candy, formatName string) bool {
	section := layer.FormatSection(formatName)
	return section != nil && len(section.Packages) > 0
}

// validateDataCandies checks data candy declarations and data image constraints.
func validateDataCandies(cfg *Config, layers map[string]*Candy, errs *ValidationError) {
	// Validate data src directories exist
	for name, layer := range layers {
		if !layer.HasData() {
			continue
		}
		for _, d := range layer.Data() {
			srcPath := filepath.Join(layer.SourceDir, d.Src)
			if !dirExists(srcPath) {
				errs.Add("candy %s: data src %q does not exist or is not a directory", name, d.Src)
			}
		}
	}

	// Validate per-image constraints
	for imgName, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}

		// Resolve candies for this image
		resolved, err := ResolveCandyOrder(img.Candy, layers, nil)
		if err != nil {
			continue // candy resolution errors are caught elsewhere
		}

		// Collect all volume names declared in this image's candy chain
		volumeNames := make(map[string]bool)
		for _, candyName := range resolved {
			layer, ok := layers[candyName]
			if !ok {
				continue
			}
			for _, v := range layer.Volume() {
				volumeNames[v.Name] = true
			}
		}

		// Check that data volume references resolve
		hasData := false
		for _, candyName := range resolved {
			layer, ok := layers[candyName]
			if !ok || !layer.HasData() {
				continue
			}
			hasData = true
			for _, d := range layer.Data() {
				if !volumeNames[d.Volume] {
					errs.Add("box %s: candy %s data references volume %q which is not declared by any candy in the box", imgName, candyName, d.Volume)
				}
			}
		}

		// Data image specific validations
		if img.DataImage {
			if img.Base != "" {
				errs.Add("box %s: data_image cannot specify base (always FROM scratch)", imgName)
			}
			if !hasData {
				errs.Add("box %s: data_image has no candies with data declarations", imgName)
			}
			// Check for incompatible features
			for _, candyName := range resolved {
				layer, ok := layers[candyName]
				if !ok {
					continue
				}
				if layer.HasService() {
					errs.Add("box %s: data_image includes candy %s which has service: declarations", imgName, candyName)
				}
				if layer.HasPorts() {
					errs.Add("box %s: data_image includes candy %s which has port declarations", imgName, candyName)
				}
				if len(layer.PortRelayPorts) > 0 {
					errs.Add("box %s: data_image includes candy %s which has port_relay declarations", imgName, candyName)
				}
			}
		}
	}
}

// validateEnvProvides checks env_provides declarations in candies.
func validateEnvProvides(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasEnvProvides() {
			continue
		}
		for key, tmpl := range layer.EnvProvides() {
			if key == "" {
				errs.Add("candy %s: env_provides has empty key", name)
				continue
			}

			// Check for valid template variables. Allowed: {{.ContainerName}},
			// {{.HostPort <N>}}, {{.ContainerPort <N>}} (where N is a numeric
			// container port). Routes through validateProvidesTemplate so this
			// validator and the runtime resolver agree on the allowlist.
			if !validateProvidesTemplate(tmpl) {
				errs.Add("candy %s: env_provides[%s] contains unknown or malformed template variable (allowed: {{.ContainerName}}, {{.HostPort N}}, {{.ContainerPort N}}): %s", name, key, tmpl)
			}

			// Note: env_provides key may intentionally overlap with env key in the same candy.
			// env is baked into the service's own image (e.g., OLLAMA_HOST="0.0.0.0" for binding).
			// env_provides is injected into OTHER containers (e.g., OLLAMA_HOST="http://charly-ollama:11434").
		}
	}
}

// validateEnvDeps checks env_requires, env_accepts, secret_requires, and
// secret_accepts declarations in candies. A single `seen` map enforces the rule
// that an env var name cannot appear in more than one of these four sections
// within the same candy — each entry is either a plaintext-safe accept/require
// or a credential-backed accept/require, never both.
func validateEnvDeps(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		seen := make(map[string]string) // name -> originating section label

		validateDepEntries(name, "env_requires", layer.EnvRequire(), seen, errs)
		validateDepEntries(name, "env_accepts", layer.EnvAccept(), seen, errs)
		validateDepEntries(name, "secret_requires", layer.SecretRequire(), seen, errs)
		validateDepEntries(name, "secret_accepts", layer.SecretAccept(), seen, errs)
	}
}

// validateDepEntries validates a single env/secret dependency list against the
// shared `seen` map, reporting collisions with whichever section claimed the
// name first. Used by validateEnvDeps for all four env/secret sections.
func validateDepEntries(candyName, section string, entries []EnvDependency, seen map[string]string, errs *ValidationError) {
	// name presence + env-var-name format + description presence are enforced by
	// #CandyEnvDep / #CandySecretDep; only the cross-section single-membership
	// rule (an env var belongs to exactly one section) stays here.
	for _, dep := range entries {
		if dep.Name == "" {
			continue
		}
		if prev, ok := seen[dep.Name]; ok && prev != section {
			errs.Add("candy %s: env var %s appears in both %s and %s — an env var belongs to exactly one section", candyName, dep.Name, prev, section)
		}
		seen[dep.Name] = section
	}
}

// podmanSecretSlugPattern matches the lowercase-kebab slug form used for
// per-image podman secret names (charly-<image>-<slug>). Plan §4.4 rule 4.
var podmanSecretSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// envVarNameToPodmanSecretSlug converts an env var name to the slug used in
// the podman secret name. Mirrors what CollectCandySecretAccepts will do at
// `charly config` time (to be added in Step 4). Lowercase + underscores → hyphens.
func envVarNameToPodmanSecretSlug(envVarName string) string {
	return strings.ReplaceAll(strings.ToLower(envVarName), "_", "-")
}

// validateSecretDeps enforces the secret-specific rules that validateEnvDeps
// cannot express: credential store `key:` format, podman secret slug validity,
// and the prohibition on credential-backed entries overlapping with the
// plaintext `env_provides` map. Plan §4.4.
func validateSecretDeps(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasSecretAccepts() && !layer.HasSecretRequires() {
			continue
		}

		// Build a set of env_provides keys for the collision check (rule 2).
		// env_provides values use {{.ContainerName}} templating and land in
		// charly.yml plaintext — a credential-backed env var with the same
		// name would be ambiguous and defeat the point of the split.
		envProvidesKeys := map[string]bool{}
		for key := range layer.EnvProvides() {
			envProvidesKeys[key] = true
		}

		checkOne := func(section string, entries []EnvDependency) {
			for _, dep := range entries {
				if dep.Name == "" {
					continue // already reported by validateEnvDeps
				}
				// Rule 2: cannot collide with env_provides.
				if envProvidesKeys[dep.Name] {
					errs.Add("candy %s: %s[%s] also appears in env_provides — credential-backed secrets and plaintext env_provides are mutually exclusive for the same variable", name, section, dep.Name)
				}
				// Rule 4: the podman secret slug must be valid.
				slug := envVarNameToPodmanSecretSlug(dep.Name)
				if !podmanSecretSlugPattern.MatchString(slug) {
					errs.Add("candy %s: %s[%s] would produce invalid podman secret slug %q (must match %s)", name, section, dep.Name, slug, podmanSecretSlugPattern.String())
				}
				// The credential-store key: format is enforced by #CandySecretDep.key.
			}
		}

		checkOne("secret_requires", layer.SecretRequire())
		checkOne("secret_accepts", layer.SecretAccept())
	}
}

// validateMCPProvides checks mcp_provides declarations in candies.
func validateMCPProvides(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasMCPProvides() {
			continue
		}
		// name/url presence + the transport enum are enforced by #CandyMCPProvide;
		// duplicate-name detection + the {{...}} URL template grammar stay here.
		seen := make(map[string]bool)
		for _, mcp := range layer.MCPProvide() {
			if mcp.Name != "" {
				if seen[mcp.Name] {
					errs.Add("candy %s: mcp_provides has duplicate name %q", name, mcp.Name)
				}
				seen[mcp.Name] = true
			}
			if mcp.URL != "" && !validateProvidesTemplate(mcp.URL) {
				errs.Add("candy %s: mcp_provides[%s] url contains unknown or malformed template variable (allowed: {{.ContainerName}}, {{.HostPort N}}, {{.ContainerPort N}}): %s", name, mcp.Name, mcp.URL)
			}
		}
	}
}

// validateMCPDeps checks mcp_requires and mcp_accepts declarations in candies.
func validateMCPDeps(layers map[string]*Candy, errs *ValidationError) {
	// name presence + description presence are enforced by #CandyMCPDep; only the
	// cross-section single-membership rule (a server in exactly one of
	// mcp_requires/mcp_accepts) stays here.
	for name, layer := range layers {
		seen := make(map[string]string) // name -> "requires" or "accepts"
		check := func(entries []EnvDependency, section string) {
			for _, dep := range entries {
				if dep.Name == "" {
					continue
				}
				if prev, ok := seen[dep.Name]; ok && prev != section {
					errs.Add("candy %s: MCP server %s appears in both mcp_%s and mcp_%s", name, dep.Name, prev, section)
				}
				seen[dep.Name] = section
			}
		}
		check(layer.MCPRequire(), "requires")
		check(layer.MCPAccept(), "accepts")
	}
}

// --- Task validation (replaces root.yml / user.yml) ---

var (
	taskUserLiteralPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	taskUserUIDGIDPattern  = regexp.MustCompile(`^\d+:\d+$`)
	taskCapsPattern        = regexp.MustCompile(`^cap_[a-z_]+[=+][a-z]+(,cap_[a-z_]+[=+][a-z]+)*$`)
)

// validateCandyTasks enforces the tasks: schema:
//   - exactly-one-verb per task
//   - per-verb required modifier presence
//   - path / mode / caps format checks
//   - vars: key rules (pattern, no auto-export collision, no env: collision)
//   - ${VAR} references resolve against vars ∪ auto-exports
//   - dual-config: tasks: AND root.yml/user.yml in same candy → error
//   - build: value restricted to "all" in initial implementation
func validateCandyTasks(layers map[string]*Candy, errs *ValidationError) {
	for name, layer := range layers {
		// vars: key shape (^[A-Z_][A-Z0-9_]*$) is enforced by #Candy.var; the
		// reserved-auto-export + env: cross-section collisions stay here.
		for k := range layer.vars {
			if taskAutoExports[k] {
				errs.Add("candy %q: vars: key %q collides with a reserved auto-export (USER, UID, GID, HOME, ARCH, BUILD_ARCH)", name, k)
			}
			if layer.envConfig != nil {
				if _, exists := layer.envConfig.Vars[k]; exists {
					errs.Add("candy %q: vars: key %q also declared in env: — pick one", name, k)
				}
			}
		}

		if !layer.HasTasks() {
			continue
		}

		known := taskKnownNames(layer.vars)
		for i, t := range layer.runOps() {
			// Exactly-one-verb
			verb, err := t.Kind()
			if err != nil {
				errs.Add("candy %q: plan run[%d]: %v", name, i, err)
				continue
			}

			validateSingleTask(name, i, verb, &t, known, errs)
		}
	}
}

// validateSingleTask runs per-verb modifier and field validation for a single
// task. Errors accumulate in errs. known is the set of ${VAR} names that
// resolve (auto-exports ∪ candy.Vars keys).
func validateSingleTask(candyName string, idx int, verb string, t *Op, known map[string]bool, errs *ValidationError) {
	// user: format check
	if t.RunAs != "" {
		u := t.RunAs
		if !isValidTaskUser(u) {
			errs.Add("candy %q: tasks[%d]: user: %q is not valid (expected root, ${USER}, a name matching ^[a-z_][a-z0-9_-]*$, or <uid>:<gid>)", candyName, idx, u)
		}
	}

	// mode: octal format (^0[0-7]{3,4}$) is enforced by #Op.mode.

	// cache: additional buildkit cache mounts — only meaningful on the
	// RUN-emitting verbs (download + the `plugin: command` install-task verb, which
	// renders a RUN via emitCmd); each path must be absolute (or ~/ / ${HOME}, which
	// resolve to absolute mount points). `command` is a plugin verb now, so its cache:
	// rides a `plugin: command` step (verb == "plugin").
	if len(t.Cache) > 0 {
		if verb != "download" && (verb != "plugin" || t.Plugin != "command") {
			errs.Add("candy %q: tasks[%d]: cache: is only valid on download: or plugin: command tasks (got %s)", candyName, idx, verb)
		}
		for _, p := range t.Cache {
			if !isAbsOrHomePath(p) {
				errs.Add("candy %q: tasks[%d]: cache: %q must be an absolute path (or start with ~/ / ${HOME})", candyName, idx, p)
			}
		}
	}

	// Per-verb required modifiers. `command` is NOT here — it is a plugin verb now
	// (`plugin: command`); its only modifier check (command non-empty) is enforced by
	// the #CommandInput CUE schema (`command: string & !=""`) at plugin_input validation.
	switch verb {
	case "mkdir":
		validateMkdirTask(candyName, idx, t, errs)
	case "copy":
		validateCopyTask(candyName, idx, t, errs)
	case "write":
		validateWriteTask(candyName, idx, t, errs)
	case "link":
		validateLinkTask(candyName, idx, t, errs)
	case "download":
		validateDownloadTask(candyName, idx, t, errs)
	case "setcap":
		validateSetcapTask(candyName, idx, t, errs)
	case "build":
		validateBuildTask(candyName, idx, t, errs)
	}

	// ${VAR} reference validation in all non-shell fields.
	// cmd: and write: content are passed verbatim to shell / filesystem,
	// so unresolved ${BAR} is legal there (shell handles, or literal bytes).
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
		if unresolved := taskUnresolvedRefs(val, known); len(unresolved) > 0 {
			errs.Add("candy %q: tasks[%d]: %s references unknown ${VAR}: %s (declare in vars: or use an auto-export)", candyName, idx, field, strings.Join(unresolved, ", "))
		}
	}
}

// validateMkdirTask checks the required modifiers for a mkdir: task.
func validateMkdirTask(candyName string, idx int, t *Op, errs *ValidationError) {
	if !isAbsOrHomePath(t.Mkdir) {
		errs.Add("candy %q: tasks[%d]: mkdir: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.Mkdir)
	}
}

// validateCopyTask checks the required modifiers for a copy: task.
func validateCopyTask(candyName string, idx int, t *Op, errs *ValidationError) {
	switch {
	case t.Copy == "":
		errs.Add("candy %q: tasks[%d]: copy: requires a non-empty source", candyName, idx)
	case strings.HasPrefix(t.Copy, "/"):
		errs.Add("candy %q: tasks[%d]: copy: %q must be a relative path (candy-dir file)", candyName, idx, t.Copy)
	case strings.Contains(t.Copy, ".."):
		errs.Add("candy %q: tasks[%d]: copy: %q may not contain .. (no traversal)", candyName, idx, t.Copy)
	}
	if t.To == "" {
		errs.Add("candy %q: tasks[%d]: copy: requires to: destination", candyName, idx)
	} else if !isAbsOrHomePath(t.To) {
		errs.Add("candy %q: tasks[%d]: copy to: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.To)
	}
}

// validateWriteTask checks the required modifiers for a write: task.
func validateWriteTask(candyName string, idx int, t *Op, errs *ValidationError) {
	if !isAbsOrHomePath(t.Write) {
		errs.Add("candy %q: tasks[%d]: write: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.Write)
	}
	if t.Content == "" {
		errs.Add("candy %q: tasks[%d]: write: requires non-empty content:", candyName, idx)
	}
}

// validateLinkTask checks the required modifiers for a link: task.
func validateLinkTask(candyName string, idx int, t *Op, errs *ValidationError) {
	if !isAbsOrHomePath(t.Link) {
		errs.Add("candy %q: tasks[%d]: link: %q must be an absolute path or start with ~/ / ${HOME}", candyName, idx, t.Link)
	}
	if t.Target == "" {
		errs.Add("candy %q: tasks[%d]: link: requires target: (what the symlink points to)", candyName, idx)
	}
}

// validateDownloadTask checks the required modifiers for a download: task.
func validateDownloadTask(candyName string, idx int, t *Op, errs *ValidationError) {
	if t.Download == "" {
		errs.Add("candy %q: tasks[%d]: download: requires a URL", candyName, idx)
	}
	// The extract enum (tar.gz/tar.xz/tar.zst/zip/none/sh/"") is enforced by the closed
	// #Op via the CUE step gate (validateNodeFormSteps). to: required unless extract=sh
	// (the script typically decides its own install path).
	if t.Extract != "sh" && t.To == "" {
		errs.Add("candy %q: tasks[%d]: download requires to: destination (unless extract: sh)", candyName, idx)
	}
}

// validateSetcapTask checks the required modifiers for a setcap: task.
func validateSetcapTask(candyName string, idx int, t *Op, errs *ValidationError) {
	if !strings.HasPrefix(t.Setcap, "/") {
		errs.Add("candy %q: tasks[%d]: setcap: %q must be an absolute path", candyName, idx, t.Setcap)
	}
	if t.Caps != "" && !taskCapsPattern.MatchString(t.Caps) {
		errs.Add("candy %q: tasks[%d]: setcap caps: %q not valid (expected cap_name=flags[,cap_name=flags])", candyName, idx, t.Caps)
	}
}

// validateBuildTask checks the required modifiers for a build: task.
func validateBuildTask(candyName string, idx int, t *Op, errs *ValidationError) {
	if t.Build != "all" {
		errs.Add("candy %q: tasks[%d]: build: %q not supported (initial implementation accepts only \"all\")", candyName, idx, t.Build)
	}
}

// isValidTaskUser returns true for accepted user: values: "root", "${USER}",
// a name matching lowercase-alphanum-hyphen, a numeric "<uid>:<gid>", or a
// string containing ${VAR} references (which resolve at generate time).
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
	// Allow bare numeric uid
	if _, err := strconv.Atoi(u); err == nil {
		return true
	}
	return false
}

// isAbsOrHomePath returns true for absolute paths or paths starting with
// ~/ or ${HOME}/. Empty paths return false (required-field check).
func isAbsOrHomePath(p string) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return true
	}
	if strings.HasPrefix(p, "~/") {
		return true
	}
	if strings.HasPrefix(p, "${HOME}") {
		return true
	}
	return false
}
