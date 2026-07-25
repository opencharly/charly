package main

// findLocalSpec looks up a LocalSpec by name from the unified loader.
// Returns (nil, nil) when the project loads but has no `local:` entry by that
// name; returns (nil, err) when the project config FAILS to load — the caller
// surfaces that error instead of a misleading "unknown template", so a load
// failure (e.g. a transient discover EACCES from a concurrent sibling build) is
// never hidden behind a bare not-found.
//
// K4 unit A (core-min wave 3): the deploy-add dispatcher's own `local: <name>`
// template lookup — this function's former sole reason to stay LoadUnified-coupled
// — moved to candy/plugin-bundle's lookupLocalTemplate (node_resolve.go), which
// reads the SAME data off the "resolved-project" envelope's Templates.Local RawBody
// map (already namespace-qualified by the host's fillNamespacedTemplates) and
// projects it via the kind:local provider's own OpResolve leg — no LoadUnified, no
// new seam. findLocalSpec itself is UNCHANGED and stays core: its one remaining
// caller, check_cmd.go's runLocalDeployScopePlan (the CLI-free check-live gather
// engine), is registry/LoadUnified-coupled by its own nature and out of this
// cutover's scope.
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
