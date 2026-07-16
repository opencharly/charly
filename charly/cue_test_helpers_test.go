package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/kit"
	"gopkg.in/yaml.v3"
)

// decodeViaCUEForTest decodes a YAML body into out (a pointer) through the CUE
// loader's normalize+decode path — the replacement for the per-type shorthand
// UnmarshalYAML methods deleted in the CUE loader switch (Cutover 1). Tests that
// used to yaml.v3-decode shorthand directly route through here so they exercise
// the actual loader behavior (normalizer expanders + CUE Decode).
func decodeViaCUEForTest(t *testing.T, body string, out any) error {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return err
	}
	node := &doc
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	return decodeEntityViaCUE(node, reflect.TypeOf(out).Elem(), out, "test")
}

// candyBodyGuardErr runs the parseCandyYAML load-time guards (legacy-key +
// unknown-top-level-key typo detection) against a bare candy body. These replace
// the deleted CandyYAML.UnmarshalYAML's in-decode rejection.
func candyBodyGuardErr(body string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return err
	}
	m := kit.MappingRoot(&doc)
	if m == nil {
		return nil
	}
	if err := rejectLegacyCandyKeys("test", m); err != nil {
		return err
	}
	return rejectUnknownCandyTopLevelKeys("test", m)
}
