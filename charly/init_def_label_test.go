package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// TestInitDefLabel_RoundTrip proves the init system is TRUE single-source: the
// build-resolved init contract is read from the embedded init: vocabulary,
// baked (via the deploykit WriteLabels formatter — the #67 render-DRIVE emitter)
// into the ai.opencharly.init_def label, parsed back by ExtractMetadata, and the
// deploy read this file still covers — resolveInitDefFromMeta (the `charly service`
// management-surface resolver, still charly-core) — returns the VOCAB values, not a
// hardcoded duplicate. The sibling entrypoint-resolution read (resolveEntrypointFromMeta)
// moved to candy/plugin-deploy-pod (Cutover B unit 2) and is covered there.
func TestInitDefLabel_RoundTrip(t *testing.T) {
	uf, err := embeddedDefaults()
	if err != nil {
		t.Fatalf("embeddedDefaults: %v", err)
	}
	ic := uf.ProjectInitConfig()
	if ic == nil || ic.Init["supervisord"] == nil {
		t.Fatal("embedded vocabulary missing supervisord init def")
	}
	def := ic.Init["supervisord"]

	// Build the runtime-relevant subset exactly as the host render-prep bake seam does
	// (buildBakedMetadata → spec.BakedLabelSet.InitDef).
	capDef := spec.CapabilityInitDef{
		Entrypoint:         def.Entrypoint,
		FallbackEntrypoint: def.FallbackEntrypoint,
		ManagementTool:     def.ManagementTool,
		ManagementCommands: def.ManagementCommands,
	}

	// Sanity: the vocab carries non-trivial values (else the round-trip would
	// trivially "pass" on empties).
	if len(def.Entrypoint) == 0 || def.ManagementTool == "" || len(def.ManagementCommands) == 0 {
		t.Fatalf("embedded supervisord vocab unexpectedly sparse: %+v", capDef)
	}

	payload, err := json.Marshal(capDef)
	if err != nil {
		t.Fatalf("marshal CapabilityInitDef: %v", err)
	}

	// Exercise the actual bake seam: deploykit WriteLabels must emit the
	// ai.opencharly.init_def label carrying exactly this JSON payload (podman's
	// Containerfile parser consumes the shell-quoting, so the stored OCI label
	// value is the raw JSON).
	bakedMeta := &spec.BakedLabelSet{
		Version:      "2026.001.0000",
		Box:          "round-trip",
		Init:         "supervisord",
		InitDef:      &capDef,
		InitLabelKey: def.LabelKey,
	}
	var b strings.Builder
	deploykit.NewRenderGenerator().WriteLabels(&b, bakedMeta, "round-trip")
	emitted := b.String()
	if !strings.Contains(emitted, spec.LabelInitDef) || !strings.Contains(emitted, string(payload)) {
		t.Fatalf("bake seam did not emit %s with payload %s; got: %q", spec.LabelInitDef, payload, emitted)
	}

	// Parse path: deploykit.ExtractMetadata reads the label value podman returns (raw JSON).
	orig := deploykit.InspectLabels
	defer func() { deploykit.InspectLabels = orig }()
	deploykit.InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			spec.LabelVersion: "2026.001.0000",
			spec.LabelBox:     "round-trip",
			spec.LabelInit:    "supervisord",
			spec.LabelInitDef: string(payload),
		}, nil
	}
	meta, err := deploykit.ExtractMetadata("podman", "round-trip")
	if err != nil {
		t.Fatalf("ExtractMetadata: %v", err)
	}
	if meta.InitDef == nil {
		t.Fatal("meta.InitDef nil after parse; expected the baked init_def")
	}
	if !reflect.DeepEqual(*meta.InitDef, capDef) {
		t.Errorf("parsed init_def = %+v, want %+v", *meta.InitDef, capDef)
	}

	// Deploy read: resolveInitDefFromMeta itself moved to candy/plugin-pod/service_resolve.go
	// (Cutover B unit 2 service-verb completion — the whole argv-building chain is now
	// plugin-side); its label-first round-trip coverage + the legacy-fallback and
	// custom-init-at-runtime cases live in candy/plugin-pod/service_resolve_test.go now. This
	// test still proves the CORE half — bake (WriteLabels) → parse (ExtractMetadata) →
	// meta.InitDef round-trips byte-for-byte (asserted above) — which is what stays charly-core.
}
