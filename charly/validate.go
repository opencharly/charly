package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/spec/spec"

	"cuelang.org/go/cue"
	"gopkg.in/yaml.v3"
)

// spec.ValidationError (the loader validation accumulator) lives in the dedicated spec module
// (#55 Phase B) — charly core reaches it as spec.ValidationError.

// validateCandyCUESchemas validates each loaded candy's on-disk manifest against
// the candy CUE schema (via the loader seam's ValidateCandyManifestCUE — #Candy for a legacy
// kind-keyed manifest, #NodeDoc for a node-form manifest). This is the sole
// candy-schema validator; the former hand-written Go candy validators are
// deleted. Inline/synthesized candies with no manifest file on disk are skipped.
func validateCandyCUESchemas(layers map[string]spec.CandyReader, errs *spec.ValidationError) {
	for name, c := range layers {
		if c == nil || c.GetSourceDir() == "" {
			continue
		}
		f := filepath.Join(c.GetSourceDir(), spec.UnifiedFileName)
		data, err := os.ReadFile(f)
		if err != nil {
			continue // remote/inline candy without a local manifest — skip
		}
		if verr := requireProjectLoader().ValidateCandyManifestCUE(f, data, loaderThreaded(), requireLoaderParser()); verr != nil {
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
func validateProjectCUESchemas(cfg *spec.Config, dir string, opts spec.ResolveOpts, errs *spec.ValidationError) {
	// Boxes: BoxConfig has no Name field (the name is the cfg.Box map key), so
	// inject it into the wire form before validating against #Box. Marshal the
	// resolved struct back to YAML and run it through the same ingest path the
	// on-disk corpus uses. Skip disabled boxes exactly like the Go box
	// validators (a disabled box's invalid fields are intentionally not flagged).
	for name, box := range cfg.EachBox {
		if !box.IsEnabled() && !opts.ShouldIncludeDisabled(name) {
			continue
		}
		entityYAML, err := boxEntityWireYAML(name, box)
		if err != nil {
			errs.Add("box %q: CUE wire-encode: %v", name, err)
			continue
		}
		doc, derr := requireProjectLoader().CueDocFromYAML("box:"+name, entityYAML)
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
		if verr := requireProjectLoader().ValidateEntityClosedCUE("box", "box:"+name, doc.LookupPath(cue.ParsePath("box"))); verr != nil {
			errs.Add("%v", verr)
		}
	}

	// Every ROOT-file entity is validated at LOAD (the #NodeDoc gate): a legacy kind-keyed (non-node-form)
	// root file is HARD-REJECTED there with a `charly migrate` hint and never reaches validation. So the
	// former root-shape collection validator — a HARDCODED per-kind `collectionKinds` word list driving
	// validateVocabularyCollections over non-node-form files — was DELETED as an unreachable dead legacy
	// arm (task #60 CONDITION-1: the kernel carries no compiled-in concrete-kind word list; the load gate
	// owns the rejection). validateVocabularyCollections itself (and its sibling validateEntityCUE) were
	// FULLY deleted in the dead-code-radical-removal batch — RDD-verified live that the modern per-kind
	// LOAD-time plugin gate (`plugin kind:<X>: plugin_input fails #<X>Input`) is the actual production
	// entity-schema enforcement for every non-box collection kind today. What LOAD leaves lenient is each
	// entity's ASSEMBLED plan STEPS, so the node-form step-typo gate (ValidateNodeFormSteps against the
	// closed #Step/#Op) stays here.
	rootFiles := []string{filepath.Join(dir, spec.UnifiedFileName)}
	if boxRoots, _ := filepath.Glob(filepath.Join(dir, "box", "*", spec.UnifiedFileName)); len(boxRoots) > 0 {
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
		if verr := requireProjectLoader().ValidateNodeFormSteps(f, data, loaderThreaded(), requireLoaderParser()); verr != nil {
			errs.Add("%v", verr)
		}
	}
}

// isNodeFormFile reports whether any document in a YAML file is unified
// node-form (spec.ClassifyDoc → spec.DocShapeNode). Used to skip the legacy
// root-shape collection validator on node-form manifests (whose entities are
// validated at load + via the resolved cfg.Box path).
func isNodeFormFile(data []byte) bool {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var node yaml.Node
		if err := dec.Decode(&node); err != nil {
			break
		}
		if shape, err := spec.ClassifyDoc(&node); err == nil && shape == spec.DocShapeNode {
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

// validateBuildAndDistro / validateBoxBaseFrom / validateMergeConfig / validateBuildTunables /
// validateBuilderRefs relocated to candy/plugin-box/validate_config_rules.go (K3-W2, task #13):
// pure over *spec.Config/BoxConfig/DistroConfig/BuilderConfig, no CUE, no registry — the plugin
// self-loads the raw config itself via the hoisted sdk/loaderkit.LoadUnifiedViaExecutor witness,
// no host round-trip needed. What genuinely remains host-only in THIS file (validateCandyCUESchemas
// / validateProjectCUESchemas below) needs the host's spliced cross-plugin CUE schema — a live,
// non-marshalable cue.Value graph.

// validateRemoteCandies checks remote candy consistency. STAYS host (unlike its former siblings
// above): it calls CollectRemoteRefs (refs.go), which needs spec.RefsCollectSeams
// (Downloader/MigrateCache/ResolveLocal — registry-coupled host callbacks with no existing
// executor-backed bridge, unlike LoadSeams) — an IOU, not this unit's scope (see
// charly/KERNEL_MANIFEST.md).
func validateRemoteCandies(cfg *spec.Config, layers map[string]spec.CandyReader, errs *spec.ValidationError) {
	// Check version conflicts (same repo referenced with different versions)
	_, err := requireProjectLoader().CollectRemoteRefsOpts(hostInProcCtx(), cfg, layers, spec.ResolveOpts{})
	if err != nil {
		errs.Add("%v", err)
	}

	// Check for naming conflicts between remote candies from different repos
	for _, layer := range layers {
		if !layer.GetRemote() {
			continue
		}
		for _, other := range layers {
			if !other.GetRemote() || other == layer {
				continue
			}
			if other.GetName() == layer.GetName() && other.GetRepoPath() != layer.GetRepoPath() {
				errs.Add("remote candy name conflict: %q provided by both %s and %s", layer.GetName(), layer.GetRepoPath(), other.GetRepoPath())
			}
		}
	}
}

// candyHasFile checks if a candy has a specific file (used for builder detection).

// --- Task validation (replaces root.yml / user.yml) ---
