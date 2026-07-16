package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// IsPreemptible is independent of disposable/ephemeral: a node may be both, and
// neither derives from the other.
func TestBundleNode_PreemptibleOrthogonal(t *testing.T) {
	tru := true
	both := BundleNode{
		Disposable:  &tru,
		Preemptible: &spec.PreemptibleConfig{Holds: []string{"gpu"}},
	}
	if !both.IsDisposable() {
		t.Error("explicit disposable: true should be disposable")
	}
	if !both.IsPreemptible() {
		t.Error("preemptible with holds should be preemptible")
	}

	// Preemptible does NOT make a node disposable.
	holderOnly := BundleNode{Preemptible: &spec.PreemptibleConfig{Holds: []string{"gpu"}}}
	if holderOnly.IsDisposable() {
		t.Error("preemptible must not imply disposable")
	}

	// Disposable does NOT make a node preemptible.
	dispOnly := BundleNode{Disposable: &tru}
	if dispOnly.IsPreemptible() {
		t.Error("disposable must not imply preemptible")
	}

	// Empty holds → not preemptible.
	empty := BundleNode{Preemptible: &spec.PreemptibleConfig{}}
	if empty.IsPreemptible() {
		t.Error("preemptible with no holds must not count as preemptible")
	}

	// nil → not preemptible.
	if (BundleNode{}).IsPreemptible() {
		t.Error("absent preemptible must not be preemptible")
	}
}

func TestPreemptibleConfig_UnmarshalYAML(t *testing.T) {
	// List shorthand → Holds, default stop/restore.
	var listForm BundleNode
	if err := decodeViaCUEForTest(t, "preemptible: [gpu, tpu]\n", &listForm); err != nil {
		t.Fatalf("list-shorthand unmarshal: %v", err)
	}
	if got := listForm.PreemptionHolds(); len(got) != 2 || got[0] != "gpu" || got[1] != "tpu" {
		t.Fatalf("list shorthand holds = %v, want [gpu tpu]", got)
	}
	if deploykit.PreemptEffectiveStop(listForm.Preemptible) != spec.PreemptStopShutdown {
		t.Errorf("default stop = %q, want shutdown", deploykit.PreemptEffectiveStop(listForm.Preemptible))
	}
	if preemptEffectiveRestore(listForm.Preemptible) != spec.PreemptRestoreAlways {
		t.Errorf("default restore = %q, want always", preemptEffectiveRestore(listForm.Preemptible))
	}

	// Block form.
	var blockForm BundleNode
	blockYAML := "preemptible:\n  holds: [gpu]\n  stop: shutdown\n  restore: on-success\n"
	if err := decodeViaCUEForTest(t, blockYAML, &blockForm); err != nil {
		t.Fatalf("block unmarshal: %v", err)
	}
	if preemptEffectiveRestore(blockForm.Preemptible) != spec.PreemptRestoreSuccess {
		t.Errorf("block restore = %q, want on-success", preemptEffectiveRestore(blockForm.Preemptible))
	}

	// Scalar (e.g. `preemptible: true`) is rejected — a holder must name what
	// it holds. The normalizer leaves a scalar unchanged, so CUE Decode of a
	// scalar into the PreemptibleConfig struct fails.
	var scalarForm BundleNode
	if err := decodeViaCUEForTest(t, "preemptible: true\n", &scalarForm); err == nil {
		t.Fatal("scalar preemptible should be rejected")
	}
}

func TestValidatePreemptibleOnNode(t *testing.T) {
	cases := []struct {
		name     string
		node     BundleNode
		wantErr  bool
		contains string
	}{
		{
			name: "valid holder",
			node: BundleNode{Preemptible: &spec.PreemptibleConfig{Holds: []string{"gpu"}}},
		},
		{
			name: "valid claimant",
			node: BundleNode{RequiresExclusive: []string{"gpu"}},
		},
		{
			name:     "empty holds",
			node:     BundleNode{Preemptible: &spec.PreemptibleConfig{}},
			wantErr:  true,
			contains: "must list at least one",
		},
		{
			name:     "bad stop",
			node:     BundleNode{Preemptible: &spec.PreemptibleConfig{Holds: []string{"gpu"}, Stop: "pause"}},
			wantErr:  true,
			contains: "not supported",
		},
		{
			name:     "bad restore",
			node:     BundleNode{Preemptible: &spec.PreemptibleConfig{Holds: []string{"gpu"}, Restore: "maybe"}},
			wantErr:  true,
			contains: "is invalid",
		},
		{
			name:     "empty requires token",
			node:     BundleNode{RequiresExclusive: []string{""}},
			wantErr:  true,
			contains: "empty token",
		},
		{
			name: "self-contention",
			node: BundleNode{
				Preemptible:       &spec.PreemptibleConfig{Holds: []string{"gpu"}},
				RequiresExclusive: []string{"gpu"},
			},
			wantErr:  true,
			contains: "cannot both hold and require",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := &ValidationError{}
			node := tc.node
			ValidatePreemptibleOnNode(tc.name, &node, errs)
			if errs.HasErrors() != tc.wantErr {
				t.Fatalf("HasErrors=%v want %v (errs=%s)", errs.HasErrors(), tc.wantErr, errs.Error())
			}
			if tc.contains != "" && !strings.Contains(errs.Error(), tc.contains) {
				t.Fatalf("error %q does not contain %q", errs.Error(), tc.contains)
			}
		})
	}
}
