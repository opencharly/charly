package main

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestInitDefLabel_EmbeddedVocabNonSparse proves the embedded build vocabulary's
// supervisord init def carries non-trivial values — a sanity guard against a
// vocab edit accidentally emptying Entrypoint/ManagementTool/ManagementCommands
// (K3 cone2 test closure narrowed this file to charly's OWN embedded-config
// concern; the bake(WriteLabels)→parse(ExtractMetadata) round-trip proof this
// file used to also carry — a pure sdk/deploykit + spec/container concern with
// no charly dependency — moved to sdk/deploykit/init_def_label_test.go with a
// literal *spec.CapabilityInitDef fixture in place of this vocab). Calls
// spec.ProjectInitConfig directly (the canonical home, matching loader_threaded.go's
// own production call) rather than the sdk/loaderkit re-export the original test
// used — dropping the sdk/loaderkit import charly core doesn't otherwise need.
func TestInitDefLabel_EmbeddedVocabNonSparse(t *testing.T) {
	uf, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	ic := spec.ProjectInitConfig(uf, resolveInitConfigViaPlugin)
	if ic == nil || ic.Init["supervisord"] == nil {
		t.Fatal("embedded vocabulary missing supervisord init def")
	}
	def := ic.Init["supervisord"]

	if len(def.Entrypoint) == 0 || def.ManagementTool == "" || len(def.ManagementCommands) == 0 {
		t.Fatalf("embedded supervisord vocab unexpectedly sparse: %+v", def)
	}
}
