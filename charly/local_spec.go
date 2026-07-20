package main

// findLocalSpec looks up a LocalSpec by name from the unified loader.
// Returns (nil, nil) when the project loads but has no `local:` entry by that
// name; returns (nil, err) when the project config FAILS to load — the caller
// surfaces that error instead of a misleading "unknown template", so a load
// failure (e.g. a transient discover EACCES from a concurrent sibling build) is
// never hidden behind a bare not-found. Used by the deploy-add dispatcher to
// resolve a deployment's `local: <template-name>` reference.
//
// TRACKED FINAL/K5 EXIT (DEPLOY-wave W2 audit, 2026-07-20): calls LoadUnified directly —
// no plugin has loader access today, an ENABLER GAP (not a permanence claim, same class as
// the arbiter IOU) that only a FINAL/K5 loader-access seam can close. 26 LOC, trivial once
// that seam exists.
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
