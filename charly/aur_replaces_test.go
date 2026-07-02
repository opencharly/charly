package main

import (
	"reflect"
	"testing"
)

// TestStringSliceFromYAML covers the three input shapes the helper
// must tolerate: pre-stringified slice, []interface{} (the YAML
// decoder's default), and unsupported types (return ok=false).
//
// Backs the AUR `replaces:` list extraction in collectBuilderContext —
// the decoder produces []interface{} for sequences, but pre-processed
// callers may pass []string directly.
func TestStringSliceFromYAML(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
		ok   bool
	}{
		{"pre-stringified", []string{"code", "vscode"}, []string{"code", "vscode"}, true},
		{"yaml-decoded", []any{"code", "vscode"}, []string{"code", "vscode"}, true},
		{"empty-decoded", []any{}, []string{}, true},
		{"non-string-elements", []any{"code", 42, "vscode"}, []string{"code", "vscode"}, true},
		{"nil", nil, nil, false},
		{"string", "code", nil, false},
		{"map", map[string]any{"x": 1}, nil, false},
	}
	for _, c := range cases {
		got, ok := stringSliceFromYAML(c.in)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// TestExtractStringSlice_AurReplacesShape exercises the helper used
// by execBuilder to read `replaces:` out of the BuilderStep's
// RawStageContext map. End-to-end shape: yaml-decoded list →
// stringSliceFromYAML → ctx["replaces"] → extractStringSlice.
func TestExtractStringSlice_AurReplacesShape(t *testing.T) {
	repls, ok := stringSliceFromYAML([]any{"code", "code-features"})
	if !ok {
		t.Fatal("stringSliceFromYAML rejected expected shape")
	}
	ctx := map[string]any{
		"layer":    "vscode",
		"builder":  "aur",
		"packages": []string{"visual-studio-code-bin"},
		"replaces": repls,
	}
	got := extractStringSlice(ctx, "replaces")
	want := []string{"code", "code-features"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractStringSlice replaces: got %v, want %v", got, want)
	}
	// Absent key returns empty.
	if got := extractStringSlice(ctx, "absent-key"); len(got) != 0 {
		t.Errorf("absent key: got %v, want []", got)
	}
}
