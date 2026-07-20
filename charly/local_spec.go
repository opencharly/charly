package main

// findLocalSpec looks up a LocalSpec by name from the unified loader.
// Returns (nil, nil) when the project loads but has no `local:` entry by that
// name; returns (nil, err) when the project config FAILS to load — the caller
// surfaces that error instead of a misleading "unknown template", so a load
// failure (e.g. a transient discover EACCES from a concurrent sibling build) is
// never hidden behind a bare not-found. Used by the deploy-add dispatcher to
// resolve a deployment's `local: <template-name>` reference.
//
// STAY (Cutover B unit 5 re-audit, 2026-07-21): calls LoadUnified directly — K1-permanent
// per R-E2 (the LoadUnified/materialize keystone spike stands; no plugin will ever get
// direct loader access, so the prior framing here — "an ENABLER GAP... a FINAL/K5
// loader-access seam can close it" — assumed an enabler R-E2 rules out, and is corrected).
// This file is a FLOOR candidate (E/M/B/D: reads the permanent-core loader via the same
// path every other node in this family does); final permanence + any #118 floor-allowlist
// addition is a FLOOR-SLIM decision, not asserted here.
func findLocalSpec(dir, name string) (*ResolvedLocal, error) {
	if dir == "" || name == "" {
		return nil, nil
	}
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return nil, err
	}
	if uf == nil {
		return nil, nil
	}
	// Namespace-aware via the single resolver: a bare name hits this project's
	// `local:` map exactly as before, while a qualified `local: <ns>.<tmpl>`
	// ref descends into the imported namespace. resolveLocalRef tolerates a nil
	// Local map, so the previous explicit nil-guard is no longer needed.
	spec, _ := uf.ProjectConfig().resolveLocalRef(name)
	return spec, nil
}
