package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// IsPreemptible is independent of disposable/ephemeral: a node may be both, and
// neither derives from the other.
func TestBundleNode_PreemptibleOrthogonal(t *testing.T) {
	tru := true
	both := spec.BundleNode{
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
	holderOnly := spec.BundleNode{Preemptible: &spec.PreemptibleConfig{Holds: []string{"gpu"}}}
	if holderOnly.IsDisposable() {
		t.Error("preemptible must not imply disposable")
	}

	// Disposable does NOT make a node preemptible.
	dispOnly := spec.BundleNode{Disposable: &tru}
	if dispOnly.IsPreemptible() {
		t.Error("disposable must not imply preemptible")
	}

	// Empty holds → not preemptible.
	empty := spec.BundleNode{Preemptible: &spec.PreemptibleConfig{}}
	if empty.IsPreemptible() {
		t.Error("preemptible with no holds must not count as preemptible")
	}

	// nil → not preemptible.
	if (spec.BundleNode{}).IsPreemptible() {
		t.Error("absent preemptible must not be preemptible")
	}
}

func TestPreemptibleConfig_UnmarshalYAML(t *testing.T) {
	// List shorthand → Holds, default stop/restore.
	var listForm spec.BundleNode
	if err := decodeViaCUEForTest(t, "preemptible: [gpu, tpu]\n", &listForm); err != nil {
		t.Fatalf("list-shorthand unmarshal: %v", err)
	}
	if got := listForm.PreemptionHolds(); len(got) != 2 || got[0] != "gpu" || got[1] != "tpu" {
		t.Fatalf("list shorthand holds = %v, want [gpu tpu]", got)
	}
	// The default-stop/default-restore RESOLUTION behavior (PreemptEffectiveStop/
	// PreemptEffectiveRestore) moved to sdk/deploykit/preempt_effective_test.go
	// (#55 K3 Cone 4) — those functions only read the decoded struct's fields, so a
	// literal *spec.PreemptibleConfig fixture proves them without a CUE decode. This
	// test keeps ONLY the decode-shape assertion (list shorthand → Holds, defaults
	// left unset on the decoded struct).
	if listForm.Preemptible.Stop != "" || listForm.Preemptible.Restore != "" {
		t.Errorf("list-shorthand decode should leave Stop/Restore unset (defaults resolved by PreemptEffective*, not the decoder): %+v", listForm.Preemptible)
	}

	// Block form.
	var blockForm spec.BundleNode
	blockYAML := "preemptible:\n  holds: [gpu]\n  stop: shutdown\n  restore: on-success\n"
	if err := decodeViaCUEForTest(t, blockYAML, &blockForm); err != nil {
		t.Fatalf("block unmarshal: %v", err)
	}
	if blockForm.Preemptible.Restore != "on-success" {
		t.Errorf("block decode restore = %q, want on-success (verbatim, pre-default-resolution)", blockForm.Preemptible.Restore)
	}

	// Scalar (e.g. `preemptible: true`) is rejected — a holder must name what
	// it holds. The normalizer leaves a scalar unchanged, so CUE Decode of a
	// scalar into the PreemptibleConfig struct fails.
	var scalarForm spec.BundleNode
	if err := decodeViaCUEForTest(t, "preemptible: true\n", &scalarForm); err == nil {
		t.Fatal("scalar preemptible should be rejected")
	}
}

// TestValidatePreemptibleOnNode relocated to candy/plugin-loader (#55 decoupling; Batch A
// executed this move on Batch C's behalf per the cross-batch file-ownership matrix) — it
// asserted loaderkit.ValidatePreemptibleOnNode directly, zero charly dep. NOTE: in THIS
// worktree, preemptDiagHasErr/preemptDiagText (used above by TestPreemptibleConfig_
// UnmarshalYAML's siblings — actually defined in/used by validate_preempt_test.go, Batch C's
// file, NOT this one) remain defined there and are STILL used by validate_preempt_test.go's
// own tests even after this move — so there is nothing dead to clean up on this batch's side;
// Batch C's own worktree may have already extracted them into a separate
// preempt_diag_helpers_test.go, which doesn't exist here (separate worktrees) — flagging this
// for the merge/orchestrator rather than guessing at post-merge dead code.
