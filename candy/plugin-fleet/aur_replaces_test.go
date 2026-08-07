package fleet

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// aur_replaces_test.go — relocated from charly/aur_replaces_test.go (#55 decoupling, Batch A):
// both tests assert deploykit.StringSliceFromYAML / ExtractStringSlice directly, zero charly
// coupling.

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
		got, ok := deploykit.StringSliceFromYAML(c.in)
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
	repls, ok := deploykit.StringSliceFromYAML([]any{"code", "code-features"})
	if !ok {
		t.Fatal("stringSliceFromYAML rejected expected shape")
	}
	ctx := map[string]any{
		"layer":    "vscode",
		"builder":  "aur",
		"packages": []string{"visual-studio-code-bin"},
		"replaces": repls,
	}
	got := deploykit.ExtractStringSlice(ctx, "replaces")
	want := []string{"code", "code-features"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractStringSlice replaces: got %v, want %v", got, want)
	}
	// Absent key returns empty.
	if got := deploykit.ExtractStringSlice(ctx, "absent-key"); len(got) != 0 {
		t.Errorf("absent key: got %v, want []", got)
	}
}

// TestExtractStringSliceHandlesBothShapes — relocated from charly/install_plan_test.go (#55
// decoupling, Batch A; R3 consolidation: near-duplicate deploykit.ExtractStringSlice coverage
// of TestExtractStringSlice_AurReplacesShape above, kept in the SAME file rather than
// scattered across two — this test additionally exercises the direct []string input shape,
// the nil-map case, and a missing key against a []string-backed map, none of which the
// AUR-shaped test above covers).
func TestExtractStringSliceHandlesBothShapes(t *testing.T) {
	// []string
	m1 := map[string]any{"k": []string{"a", "b"}}
	if got := deploykit.ExtractStringSlice(m1, "k"); len(got) != 2 || got[0] != "a" {
		t.Errorf("extractStringSlice([]string) = %v, want [a b]", got)
	}
	// []interface{} (as produced by yaml.v3 when unmarshaling into map[string]interface{})
	m2 := map[string]any{"k": []any{"a", "b"}}
	if got := deploykit.ExtractStringSlice(m2, "k"); len(got) != 2 || got[0] != "a" {
		t.Errorf("extractStringSlice([]interface{}) = %v, want [a b]", got)
	}
	// Missing key → nil
	if got := deploykit.ExtractStringSlice(m1, "missing"); got != nil {
		t.Errorf("missing key returned %v, want nil", got)
	}
	// Nil map → nil
	if got := deploykit.ExtractStringSlice(nil, "k"); got != nil {
		t.Errorf("nil map returned %v, want nil", got)
	}
}
