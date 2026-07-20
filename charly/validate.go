package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/spec"

	"cuelang.org/go/cue"
	"github.com/opencharly/sdk/kit"
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

	// Every ROOT-file entity is validated at LOAD (the #NodeDoc gate): a legacy kind-keyed (non-node-form)
	// root file is HARD-REJECTED there with a `charly migrate` hint and never reaches validation. So the
	// former root-shape collection validator — a HARDCODED per-kind `collectionKinds` word list driving
	// validateVocabularyCollections over non-node-form files — was DELETED as an unreachable dead legacy
	// arm (task #60 CONDITION-1: the kernel carries no compiled-in concrete-kind word list; the load gate
	// owns the rejection). What LOAD leaves lenient is each entity's ASSEMBLED plan STEPS, so the node-form
	// step-typo gate (validateNodeFormSteps against the closed #Step/#Op) stays here.
	rootFiles := []string{filepath.Join(dir, UnifiedFileName)}
	if boxRoots, _ := filepath.Glob(filepath.Join(dir, "box", "*", UnifiedFileName)); len(boxRoots) > 0 {
		rootFiles = append(rootFiles, boxRoots...)
	}
	for _, f := range rootFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if !isNodeFormFile(data) {
			continue // a legacy root-shape file is load-rejected (charly migrate) — nothing to validate here
		}
		if verr := validateNodeFormSteps(f, data); verr != nil {
			errs.Add("%v", verr)
		}
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

// envVarNameToPodmanSecretSlug converts an env var name to the slug used in the podman secret name
// (lowercase + underscores → hyphens). A CORE secrets helper (secrets.go), kept host-side when the
// validate ENGINE moved to plugin-box (the plugin's validateSecretDeps carries its own copy across the
// module boundary). Not validate-specific — it just historically lived here.
func envVarNameToPodmanSecretSlug(envVarName string) string {
	return strings.ReplaceAll(strings.ToLower(envVarName), "_", "-")
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
// node-form (kit.ClassifyDoc → kit.DocShapeNode). Used to skip the legacy
// root-shape collection validator on node-form manifests (whose entities are
// validated at load + via the resolved cfg.Box path).
func isNodeFormFile(data []byte) bool {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			break
		}
		if shape, err := kit.ClassifyDoc(&node); err == nil && shape == kit.DocShapeNode {
			return true
		}
	}
	return false
}

// boxEntityWireYAML marshals a resolved BoxConfig back to the authored `box:`
// wire form (a kind-keyed document), injecting the map-key name that BoxConfig
// does not itself carry, so it can be CUE-ingested and validated against #Box.
func boxEntityWireYAML(name string, box spec.BoxConfig) ([]byte, error) {
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

// validateBuildAndDistro validates build: and distro: entries.
// build: entries are checked against the embedded distro format definitions (charly/charly.yml).
// distro: is free-form (any string, including distro:version).
func validateBuildAndDistro(cfg *Config, distroCfg *buildkit.DistroConfig, errs *ValidationError) {
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
// Empty string means "auto" (resolved at build time by the build drive's cache-arg logic).
var validBuildCacheModes = map[string]bool{
	"": true, "image": true, "registry": true, "gha": true, "none": true,
}

// validateBuildTunables validates the build-speed knobs on defaults: and any
// image entry: jobs >= 1, podman_jobs >= 0, podman_jobs_cap >= 1, cache in
// the allow-list, and no empty context_ignore entries. These are project-wide
// defaults; values are validated wherever they appear so a typo surfaces at
// `charly box validate` rather than silently mis-driving a build.
func validateBuildTunables(cfg *Config, errs *ValidationError) {
	check := func(name string, ic spec.BoxConfig) {
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

// validateBuilderRefs is the HOST-NATURAL half of the former validateBuilders (task #60): the authored
// builder-REFERENCE checks that read RAW authored config a projection cannot carry — cfg.Defaults.Builder,
// each box's builds:/builder: refs against the DYNAMIC builder vocab (builderCfg), and the
// namespace-aware resolveBoxRef existence/capability checks. The candy-needs-builder DETECTION half
// (over the resolved builder map + ResolveCandyOrder) moved to the validate plugin (envelope-portable);
// this reference-validation half stays host (like validateBuildAndDistro) and rides reply.Diagnostics.
// Kind-blind: builder/build TYPE words are checked against the runtime builder vocab, no kind switch.
func validateBuilderRefs(cfg *Config, builderCfg *buildkit.BuilderConfig, errs *ValidationError) {
	// Validate defaults.builder entries.
	for typ, builder := range cfg.Defaults.Builder {
		if !builderCfg.ValidBuilderType(typ) {
			errs.Add("defaults.builder: build type %q is not valid (known builders: %s)", typ, strings.Join(builderCfg.BuilderNames(), ", "))
		}
		if builder != "" {
			// Namespace-aware: a defaults builder ref may be qualified (e.g. `charly.fedora-builder`).
			builderImg, _, exists := cfg.resolveBoxRef(builder)
			if !exists {
				errs.Add("defaults.builder.%s: box %q not found", typ, builder)
			} else if !builderImg.IsEnabled() {
				errs.Add("defaults.builder.%s: box %q is disabled", typ, builder)
			}
		}
	}
	// Validate each enabled image's builds:/builder: authored refs.
	for boxName, img := range cfg.eachBox {
		if !img.IsEnabled() {
			continue
		}
		for _, b := range img.Produce {
			if !builderCfg.ValidBuilderType(b) {
				errs.Add("box %q: builds entry %q is not valid (known builders: %s)", boxName, b, strings.Join(builderCfg.BuilderNames(), ", "))
			}
		}
		for typ, builder := range img.Builder {
			if !builderCfg.ValidBuilderType(typ) {
				errs.Add("box %q: builder.%s is not a valid build type (known builders: %s)", boxName, typ, strings.Join(builderCfg.BuilderNames(), ", "))
			}
			if builder == boxName {
				errs.Add("box %q: builder.%s cannot reference self", boxName, typ)
				continue
			}
			if builder != "" {
				builderImg, _, exists := cfg.resolveBoxRef(builder)
				if !exists {
					errs.Add("box %q: builder.%s references %q which is not found", boxName, typ, builder)
					continue
				}
				if !builderImg.IsEnabled() {
					errs.Add("box %q: builder.%s references %q which is disabled", boxName, typ, builder)
					continue
				}
				if len(builderImg.Produce) > 0 && !slices.Contains(builderImg.Produce, typ) {
					errs.Add("box %q: builder.%s references %q which does not declare builds: [%s]", boxName, typ, builder, typ)
				}
			}
		}
	}
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

// candyHasFile checks if a candy has a specific file (used for builder detection).

// --- Task validation (replaces root.yml / user.yml) ---
