package main

// Unified node-form document validation — the load-time "validate-before-execute"
// gate (CLAUDE.md / the bundle cutover's mandated guarantee #2). A whole charly.yml
// document in unified node-form is validated against #NodeDoc (schema/node.cue)
// BEFORE any reshape / normalize / build / deploy runs, so a typo'd discriminator,
// an unknown field in a kind-value, a wrong-kind child, or a leaf-with-children is
// a hard load error — never silently executed. This is the Go counterpart to the
// offline `cue vet` proof (RDD-1).

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
)

// validateNodeDocCUE validates a unified node-form document (raw YAML bytes) by
// unifying EACH top-level entity node against #Node (the reserved document
// directives are skipped — the loader decodes those). It runs the CLOSEDNESS check
// (no cue.Concrete): it catches a typo'd discriminator, an unknown field in a
// kind-value, a wrong-kind child, and a child under a childless kind — but does NOT
// require every entity's required fields (that stays the concrete `charly box
// validate` gate). label identifies the document in errors.
//
// Validation is PER ENTITY, not whole-document: unifying the whole document against
// a closed #NodeDoc forced CUE to resolve the per-child kind-disjunction across
// every entity at once — an O(entities × kinds × children) blow-up (a full-graph
// validate took ~30 CPU-minutes). One small entity at a time keeps each unification
// bounded by that entity's own size while preserving identical strictness.
func validateNodeDocCUE(label string, data []byte) error {
	doc, err := cueDocFromYAML(label, data)
	if err != nil {
		return err
	}
	docDef := sharedCueSchema.LookupPath(cue.ParsePath("#NodeDoc"))
	if docDef.Err() != nil {
		return fmt.Errorf("%s: #NodeDoc schema not found: %w", label, docDef.Err())
	}
	iter, ierr := doc.Fields()
	if ierr != nil {
		return fmt.Errorf("%s: %w", label, ierr)
	}
	for iter.Next() {
		name := iter.Selector().Unquoted()
		if docDirectiveSet[name] {
			continue // version/repo/import/discover/defaults/provides — not entities
		}
		// Validate ONE entity through #NodeDoc's pattern constraint (`{[!~dir]:
		// #Node}`) via FillPath: this is the SAME lazy, closedness-only evaluation
		// the whole-document Unify used (so the #DataChild|#StepChild disjunction
		// stays lazy — no spurious "incomplete value" on an env/var child whose key
		// also exists on a step), but bounded to this single entity for speed.
		filled := docDef.FillPath(cue.MakePath(cue.Str(name)), iter.Value())
		if err := filled.Validate(); err != nil {
			return fmt.Errorf("%s: node %q: %s", label, name, errors.Details(err, nil))
		}
	}
	return nil
}
