package main

// Proves the candy CUE schema ENFORCES constraints (rejects invalid candies),
// not merely accepts the corpus — the constraints have teeth.
//
// Each fixture uses the CURRENT node-form shape (`<name>: {candy: {...}}`); the
// retired bare `candy:` child-node wrapper is itself rejected as a shape error
// ("no kind discriminator"), which would make every case pass vacuously. The
// `missing version` case was DELETED with the schema-versioning removal: there is
// no mandatory-CalVer rule on a candy any more, so a version-less candy is valid
// (asserted by cue_tighten_test.go's "candy missing version accepted").

import "testing"

func TestCandyCUESchema_Rejects(t *testing.T) {
	cases := []struct {
		name string
		yaml string
	}{
		{"a retired version field (closedness — the removed stamp is rejected, not silently dropped)", "x:\n  candy:\n    name: x\n    description: d\n    version: 2026.150.0000\n    plan:\n    - check: c\n      file: /x\n"},
		{"uppercase name", "x:\n  candy:\n    name: BadName\n    description: d\n    plan:\n    - check: c\n      file: /x\n"},
		{"empty description", "x:\n  candy:\n    name: x\n    description: \"\"\n    plan:\n    - check: c\n      file: /x\n"},
		{"two keywords in one step", "x:\n  candy:\n    name: x\n    description: d\n    plan:\n    - run: r\n      check: c\n      file: /x\n"},
		{"unknown top-level field (closedness — a typo'd key is rejected, not silently dropped)", "x:\n  candy:\n    name: x\n    description: d\n    bogus_typo_field: true\n    plan:\n    - check: c\n      file: /x\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := requireProjectLoader().ValidateCandyManifestCUE("test.yml", []byte(tc.yaml), loaderThreaded(), requireLoaderParser()); err == nil {
				t.Errorf("expected CUE to REJECT %q, but it passed", tc.name)
			}
		})
	}
}
