package file

import (
	"reflect"
	"testing"
)

// TestDecodeContainsList covers the file verb's contains-default codec — relocated with
// the verb into this candy. A BARE scalar element defaults to Op="contains" (substring
// match), while an explicit single-operator map keeps its authored operator. The input is
// the gengotypes-degraded `any` shape a `plugin_input.contains` decodes to.
func TestDecodeContainsList(t *testing.T) {
	tests := []struct {
		name      string
		in        any
		wantOps   []string
		wantValue []any
	}{
		{"bare scalar promotes to contains", "foo", []string{"contains"}, []any{"foo"}},
		{"bare sequence promotes each element to contains", []any{"foo", "bar"}, []string{"contains", "contains"}, []any{"foo", "bar"}},
		{"explicit equals map keeps Op=equals", map[string]any{"equals": "foo"}, []string{"equals"}, []any{"foo"}},
		{"explicit matches map keeps Op=matches", map[string]any{"matches": "^prefix"}, []string{"matches"}, []any{"^prefix"}},
		{"explicit not_contains map keeps Op=not_contains", []any{map[string]any{"not_contains": "nope"}}, []string{"not_contains"}, []any{"nope"}},
		{"mixed sequence: explicit equals + bare scalar", []any{map[string]any{"equals": "foo"}, "bar"}, []string{"equals", "contains"}, []any{"foo", "bar"}},
		{"real-world marker list defaults to contains", []any{"charly-fixture-web-content-marker"}, []string{"contains"}, []any{"charly-fixture-web-content-marker"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeContainsList(tc.in)
			if len(got) != len(tc.wantOps) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(tc.wantOps), got)
			}
			for i := range got {
				if got[i].Op != tc.wantOps[i] {
					t.Errorf("[%d].Op = %q, want %q", i, got[i].Op, tc.wantOps[i])
				}
				if !reflect.DeepEqual(got[i].Value, tc.wantValue[i]) {
					t.Errorf("[%d].Value = %v (%T), want %v (%T)", i, got[i].Value, got[i].Value, tc.wantValue[i], tc.wantValue[i])
				}
			}
		})
	}
}

// TestDecodeContainsList_Nil ensures an absent contains decodes to a nil list.
func TestDecodeContainsList_Nil(t *testing.T) {
	if got := decodeContainsList(nil); got != nil {
		t.Errorf("decodeContainsList(nil) = %v, want nil", got)
	}
}
