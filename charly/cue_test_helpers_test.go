package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
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
	return requireProjectLoader().DecodeEntityViaCUE(node, reflect.TypeOf(out).Elem(), out, "test")
}

// candyBodyGuardErr runs the candy-manifest load-time guards (legacy-key + unknown-top-level-key
// typo detection) against a bare candy body, by driving the REAL production path: it writes the body
// into a kind-keyed manifest and calls parseCandyYAML, exactly as a scan does.
//
// It used to call the two guard functions directly, when they were charly-private. K-wave 2 cone R1
// (A2 unit 2) relocated them into the parse mechanism in sdk/loaderkit, where they are unexported —
// so rather than deleting or faking this coverage, the helper now exercises them through the seam
// the product actually uses. That is STRONGER than the pre-move form: it proves the guards are still
// reached by a real parse, not merely that the functions exist.
func candyBodyGuardErr(body string) error {
	var indented strings.Builder
	for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		indented.WriteString("  " + line + "\n")
	}
	tmp, err := os.MkdirTemp("", "candyguard")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	path := filepath.Join(tmp, spec.UnifiedFileName)
	if werr := os.WriteFile(path, []byte("candy:\n"+indented.String()), 0o644); werr != nil {
		return werr
	}
	_, perr := parseCandyYAML(path)
	return perr
}
