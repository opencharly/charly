package main

// findLocalSpec looks up a LocalSpec by name from the unified loader.
// Returns (nil, nil) when the project loads but has no `local:` entry by that
// name; returns (nil, err) when the project config FAILS to load — the caller
// surfaces that error instead of a misleading "unknown template", so a load
// failure (e.g. a transient discover EACCES from a concurrent sibling build) is
// never hidden behind a bare not-found. Used by the deploy-add dispatcher to
// resolve a deployment's `local: <template-name>` reference.
//
// K1-unblock wave 2 (R1 correction, superseding the 2026-07-21 "STAY... K1-permanent...
// no plugin will ever get direct loader access... FLOOR candidate" note below): that framing
// conflated "a plugin can't call LoadUnified" (true) with "this consumer must therefore stay
// core forever" (false, and the same reasoning pattern the operator already overruled on the
// Axis-B deploy-dispatch cone — a "stays core" verdict is suspect until independently
// re-verified against the boundary law's E/M/B/D test, never accepted from a prior claim).
// This file calls LoadUnified directly TODAY only because its ONE caller
// (bundle_add_cmd.go's deploy dispatcher) is itself still core-resident — it moves alongside
// that caller, into candy/plugin-bundle, in the K1-unblock wave that relocates the
// bundle_add_cmd.go dispatch kernel (the operator-confirmed real K4 cutover; see
// deploy-resolution-67-gated-cone "Axis B"). Nothing here is architecturally permanent; the
// mechanism when that wave lands is the SAME resolved-project-envelope pattern the sibling
// consumers in this family (deploy_ref.go, k8s_config.go) already use or are moving to —
// findLocalSpec ALREADY supports a namespace-qualified `local: <ns>.<tmpl>` ref (via
// resolveLocalRefFor below), so unlike k8s_config.go's findK8sSpec there is no functional gap
// to close here — only the caller's own relocation is pending.
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
	spec, _ := resolveLocalRefFor(uf.ProjectConfig(), name)
	return spec, nil
}
