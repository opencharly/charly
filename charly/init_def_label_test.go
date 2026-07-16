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
// deploy reads — resolveEntrypointFromMeta + resolveInitDefFromMeta — return the
// VOCAB values, not a hardcoded duplicate.
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
	if !strings.Contains(emitted, LabelInitDef) || !strings.Contains(emitted, string(payload)) {
		t.Fatalf("bake seam did not emit %s with payload %s; got: %q", LabelInitDef, payload, emitted)
	}

	// Parse path: ExtractMetadata reads the label value podman returns (raw JSON).
	orig := InspectLabels
	defer func() { InspectLabels = orig }()
	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion: "2026.001.0000",
			LabelBox:     "round-trip",
			LabelInit:    "supervisord",
			LabelInitDef: string(payload),
		}, nil
	}
	meta, err := ExtractMetadata("podman", "round-trip")
	if err != nil {
		t.Fatalf("ExtractMetadata: %v", err)
	}
	if meta.InitDef == nil {
		t.Fatal("meta.InitDef nil after parse; expected the baked init_def")
	}
	if !reflect.DeepEqual(*meta.InitDef, capDef) {
		t.Errorf("parsed init_def = %+v, want %+v", *meta.InitDef, capDef)
	}

	// Deploy read 1: entrypoint comes from the label (the vocab entrypoint).
	gotEntry := resolveEntrypointFromMeta(meta)
	if !reflect.DeepEqual(gotEntry, def.Entrypoint) {
		t.Errorf("resolveEntrypointFromMeta = %v, want vocab entrypoint %v", gotEntry, def.Entrypoint)
	}

	// Deploy read 2: management surface comes from the label.
	gotDef, err := resolveInitDefFromMeta(meta)
	if err != nil {
		t.Fatalf("resolveInitDefFromMeta: %v", err)
	}
	if gotDef.ManagementTool != def.ManagementTool {
		t.Errorf("management tool = %q, want %q", gotDef.ManagementTool, def.ManagementTool)
	}
	if !reflect.DeepEqual(gotDef.ManagementCommands, def.ManagementCommands) {
		t.Errorf("management commands = %v, want %v", gotDef.ManagementCommands, def.ManagementCommands)
	}
}

// TestResolveEntrypointFromMeta_LegacyLabelAbsent proves the wellKnownInitDefs
// fallback still drives entrypoint resolution for pre-init_def-label images
// (meta.InitDef nil): supervisord gets its entrypoint, systemd gets none
// (boots via the image's own init).
func TestResolveEntrypointFromMeta_LegacyLabelAbsent(t *testing.T) {
	cases := []struct {
		init string
		want []string
	}{
		{"supervisord", []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"}},
		{"systemd", nil},
		{"", []string{"sleep", "infinity"}},
		{"unknown-legacy-init", []string{"sleep", "infinity"}},
	}
	for _, tc := range cases {
		meta := &BoxMetadata{Init: tc.init} // InitDef intentionally nil
		got := resolveEntrypointFromMeta(meta)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("resolveEntrypointFromMeta(Init=%q, no label) = %v, want %v", tc.init, got, tc.want)
		}
	}
}

// TestResolveInitDefFromMeta_LegacyLabelAbsent proves the management-surface
// fallback still resolves supervisord + systemd from wellKnownInitDefs when
// the init_def label is absent, and errors on a truly unknown legacy init.
func TestResolveInitDefFromMeta_LegacyLabelAbsent(t *testing.T) {
	for _, tc := range []struct{ init, tool string }{
		{"supervisord", "supervisorctl"},
		{"systemd", "systemctl"},
	} {
		meta := &BoxMetadata{Init: tc.init} // InitDef nil → legacy fallback
		def, err := resolveInitDefFromMeta(meta)
		if err != nil {
			t.Fatalf("resolveInitDefFromMeta(%q): %v", tc.init, err)
		}
		if def.ManagementTool != tc.tool {
			t.Errorf("init %q: management tool = %q, want %q", tc.init, def.ManagementTool, tc.tool)
		}
	}

	if _, err := resolveInitDefFromMeta(&BoxMetadata{Init: "vocab-only-custom"}); err == nil {
		t.Error("resolveInitDefFromMeta with unknown init + no label should error")
	}
}

// TestInitDefLabel_CustomInitAtRuntime proves the capability win: an init
// system declared ONLY in the vocabulary (so absent from wellKnownInitDefs)
// now resolves at RUNTIME via the baked label — the prior build-only
// limitation is gone. Both the entrypoint and the management surface come
// from meta.InitDef even though "myinit" has no registry entry.
func TestInitDefLabel_CustomInitAtRuntime(t *testing.T) {
	if _, ok := wellKnownInitDefs["myinit"]; ok {
		t.Fatal("precondition: myinit must NOT be a well-known init")
	}
	meta := &BoxMetadata{
		Init: "myinit",
		InitDef: &CapabilityInitDef{
			Entrypoint:         []string{"myinit", "--run", "/etc/myinit.conf"},
			ManagementTool:     "myctl",
			ManagementCommands: map[string]string{"status": "status", "restart": "restart {{.Service}}"},
		},
	}

	gotEntry := resolveEntrypointFromMeta(meta)
	if !reflect.DeepEqual(gotEntry, meta.InitDef.Entrypoint) {
		t.Errorf("custom init entrypoint = %v, want %v (label-first, no registry entry)", gotEntry, meta.InitDef.Entrypoint)
	}

	gotDef, err := resolveInitDefFromMeta(meta)
	if err != nil {
		t.Fatalf("resolveInitDefFromMeta(custom): %v", err)
	}
	if gotDef.ManagementTool != "myctl" {
		t.Errorf("custom init management tool = %q, want myctl", gotDef.ManagementTool)
	}

	// Render a management command end-to-end to prove the baked commands are usable.
	rendered, err := initRenderManagementCommand(gotDef, "restart", "web")
	if err != nil {
		t.Fatalf("initRenderManagementCommand: %v", err)
	}
	if rendered != "restart web" {
		t.Errorf("rendered restart command = %q, want %q", rendered, "restart web")
	}
}
