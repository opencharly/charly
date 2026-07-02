package main

// Proves the candy CUE schema ENFORCES constraints (rejects invalid candies),
// not merely accepts the corpus — the constraints have teeth.

import "testing"

func TestCandyCUESchema_Rejects(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"non-calver version", "candy:\n  version: 4\n  name: x\n  description: d\n  plan:\n  - check: c\n    file: /x\n"},
		{"uppercase name", "candy:\n  version: 2026.144.1443\n  name: BadName\n  description: d\n  plan:\n  - check: c\n    file: /x\n"},
		{"empty description", "candy:\n  version: 2026.144.1443\n  name: x\n  description: \"\"\n  plan:\n  - check: c\n    file: /x\n"},
		{"two keywords in one step", "candy:\n  version: 2026.144.1443\n  name: x\n  description: d\n  plan:\n  - run: r\n    check: c\n    file: /x\n"},
		{"missing version", "candy:\n  name: x\n  description: d\n  plan:\n  - check: c\n    file: /x\n"},
		{"unknown top-level field (closedness — a typo'd key is rejected, not silently dropped)", "candy:\n  version: 2026.144.1443\n  name: x\n  description: d\n  bogus_typo_field: true\n  plan:\n  - check: c\n    file: /x\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateCandyManifestCUE("test.yml", []byte(tc.yaml)); err == nil {
				t.Errorf("expected CUE to REJECT %q, but it passed", tc.name)
			}
		})
	}
}
