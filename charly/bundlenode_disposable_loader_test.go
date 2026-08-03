package main

import (
	"github.com/opencharly/spec/spec"
	"os"
	"path/filepath"
	"testing"
)

// bundlenode_disposable_loader_test.go — the STAY (charly-loader) half of
// charly/deploy_save_test.go's former TestBundleNode_DisposableFalseRoundTrip (#55 final-tail
// split-by-assertion round, team-lead directive 2026-08-03): does LoadUnified correctly parse
// the *bool Disposable field — nil for an absent `disposable:` key, &false for an explicit
// `disposable: false`, &true for an explicit `disposable: true`. The fixture below is a literal,
// hand-authored YAML string — the on-disk node-form shape is a spec contract charly's loader
// must parse regardless of who produced the bytes; no sdk writer runs here, so this file needs
// no sdk import. The WRITE-side companion (SaveBundleConfig correctly RE-EMITS an explicit
// false/true rather than dropping it as indistinguishable-from-omitempty) moved to
// candy/plugin-bundle/deploy_state_writer_test.go's TestBundleNode_DisposableFalseRoundTrip_Writer.
func TestBundleNode_DisposableFalseRoundTrip_Loader(t *testing.T) {
	dir := t.TempDir()
	src := `version: 2026.204.1223
locked-pod:
    pod:
        image: foo
        disposable: false
open-pod:
    pod:
        image: bar
        disposable: true
bare-pod:
    pod:
        image: baz
`
	if err := os.WriteFile(filepath.Join(dir, spec.UnifiedFileName), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	uf, ok, err := LoadUnified(dir)
	if err != nil {
		t.Fatalf("LoadUnified: %v", err)
	}
	if !ok || uf == nil {
		t.Fatal("LoadUnified returned no project")
	}
	dc := testProjectBundleConfig(uf)
	if dc == nil {
		t.Fatal("testProjectBundleConfig returned nil")
	}

	locked := dc.Bundle["locked-pod"]
	if locked.Disposable == nil {
		t.Fatal("locked-pod: explicit `disposable: false` parsed as nil; should be &false")
	}
	if *locked.Disposable {
		t.Errorf("locked-pod: disposable = %v, want false", *locked.Disposable)
	}
	if locked.IsDisposable() {
		t.Error("locked-pod.IsDisposable() returned true despite explicit disposable: false")
	}

	open := dc.Bundle["open-pod"]
	if open.Disposable == nil || !*open.Disposable {
		t.Errorf("open-pod: disposable = %v, want &true", open.Disposable)
	}
	if !open.IsDisposable() {
		t.Error("open-pod.IsDisposable() returned false despite explicit disposable: true")
	}

	bare := dc.Bundle["bare-pod"]
	if bare.Disposable != nil {
		t.Errorf("bare-pod: disposable = %v, want nil (field absent in source)", bare.Disposable)
	}
	if bare.IsDisposable() {
		t.Error("bare-pod.IsDisposable() returned true for absent disposable field")
	}
}
