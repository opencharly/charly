package main

import "sort"

// bundleKeys returns the sorted deploy names of a BundleConfig — a shared test
// helper used across the deploy-save / substrate / venue tests. It previously
// co-lived in a migration test file that the migration-baseline reset removed;
// re-homed here since it is a generic BundleConfig helper, not migration-specific.
func bundleKeys(dc *BundleConfig) []string {
	if dc == nil {
		return nil
	}
	out := make([]string, 0, len(dc.Bundle))
	for k := range dc.Bundle {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
