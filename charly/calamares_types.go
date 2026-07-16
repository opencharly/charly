package main

import (
	"github.com/opencharly/sdk/spec"
)

// PackageItem shorthand (bare scalar `nginx`) is canonicalized to {name: nginx}
// by the CUE loader's normalizer (cue_normalize.go, expandPackageItemNode); the
// custom (Un)MarshalYAML were deleted in the CUE loader switch (Cutover 1).

// PackageNames returns just the names from a PackageItem list, in order.
// Convenience for places that only need the install-target list.
func PackageNames(items []spec.PackageItem) []string {
	out := make([]string, 0, len(items))
	for _, p := range items {
		if p.Name != "" {
			out = append(out, p.Name)
		}
	}
	return out
}

// PackageItemsFromStrings constructs a PackageItem slice from bare names.
// Used by the migrator when collapsing legacy format sections that only
// carried `packages: [name1, name2]`.
func PackageItemsFromStrings(names []string) []spec.PackageItem {
	out := make([]spec.PackageItem, 0, len(names))
	for _, n := range names {
		if n != "" {
			out = append(out, spec.PackageItem{Name: n})
		}
	}
	return out
}
