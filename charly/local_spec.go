package main

import (
	"os"
)

// findLocalSpec looks up a LocalSpec by name from the unified loader.
// Returns nil when the project has no charly.yml, no `local:` map,
// or no entry by that name. Used by the deploy-add dispatcher to
// resolve a deployment's `local: <template-name>` reference.
func findLocalSpec(dir, name string) *LocalSpec {
	if dir == "" || name == "" {
		return nil
	}
	uf, _, err := LoadUnified(dir)
	if err != nil || uf == nil {
		return nil
	}
	// Namespace-aware via the single resolver: a bare name hits this project's
	// `local:` map exactly as before, while a qualified `local: <ns>.<tmpl>`
	// ref descends into the imported namespace. resolveLocalRef tolerates a nil
	// Local map, so the previous explicit nil-guard is no longer needed.
	spec, _ := uf.ProjectConfig().resolveLocalRef(name)
	return spec
}

// Force os import use — findLocalSpec doesn't reach for it but the
// import is kept stable for the package layout.
var _ = os.Getwd
