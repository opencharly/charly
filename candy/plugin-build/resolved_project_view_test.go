package build

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"

	"github.com/opencharly/spec/spec"
)

// Relocated from charly/resolved_project_host_test.go (#55 decoupling cone, Batch B) — both tests
// asserted deploykit.ProjectResolvedBox / a literal spec.ResolvedProject marshal directly, with
// ZERO dependency on charly's own loader or the deleted host projectResolvedProjectWithBoxes shape
// (TestResolvedProject_Projection, which DOES exercise that deleted-code path via a test-local seam
// reproduction, stayed in charly pending an orchestrator ruling — it's also needed by
// charly/resolved_project_namespace_test.go's namespace-qualification coverage).

var updateResolvedProjectGolden = flag.Bool("update-resolved-project-golden", false,
	"regenerate the resolved-project golden testdata")

// canonKey folds a JSON key to its case/underscore-insensitive form so a snake_case #ResolvedBoxView
// key (base / build_formats / bootstrap_builder_image) maps 1:1 to the corresponding json.Marshal
// key of buildkit.ResolvedBox (Base / BuildFormats / BootstrapBuilderImage). This is why the
// completeness assertion below can compare the two field sets without a per-field name table.
func canonKey(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }

// fullResolvedBoxFixture returns a ResolvedBox with EVERY non-json:"-" field set to a distinct
// non-zero value, plus the InitSystem json:"-" cache set — so the completeness test proves (a) every
// field `charly box inspect` serializes survives the projection and (b) the host-only compute caches
// are DROPPED (InitSystem is the flagged judgment call: it is json:"-", so inspect never emits it).
func fullResolvedBoxFixture() *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "demo", Version: "2026.100.0001", EffectiveVersion: "2026.100.0002", Status: "working", Info: "a demo box", CheckLevel: "noagent", Base: "fedora:43", From: "builder:pacstrap", BootstrapBuilderImage: "ghcr.io/opencharly/builder", Platforms: []string{"linux/amd64"}, Tag: "2026.100.0003", Registry: "ghcr.io/opencharly", Pkg: "rpm", Distro: []string{"fedora:43", "fedora"}, BuildFormats: []string{"rpm"}, Tags: []string{"all", "fedora"}, Candy: []string{"base", "charly"}, User: "user", UID: 1000, GID: 1000, Home: "/home/user", UserAdopted: true, Merge: &vmshared.MergeConfig{Auto: true, MaxMB: 512, MaxTotalMB: 4096}, Builder: spec.BuilderMap{"pixi": "ghcr.io/opencharly/pixi"}, BuilderCapabilities: []string{"pixi"}, Auto: true, Network: "host", DataImage: true, IsExternalBase: true, FullTag: "ghcr.io/opencharly/demo:2026.100.0003"}, // Host-only json:"-" compute cache (must NOT leak into the wire view):
		InitSystem: "supervisord"}
}

// TestProjectResolvedBox_CompleteAndNoCacheLeak proves the two design invariants of the box view:
// COMPLETENESS (every field `charly box inspect` serializes — json.Marshal(*ResolvedBox) — survives
// projectResolvedBox with an equal value; a dropped/renamed field FAILS here) and NO CACHE LEAK (none
// of the 6 host-only json:"-" compute caches, InitSystem among them, appears in the wire view).
func TestProjectResolvedBox_CompleteAndNoCacheLeak(t *testing.T) {
	box := fullResolvedBoxFixture()
	view := deploykit.ProjectResolvedBox(box)

	boxJSON, err := json.Marshal(box)
	if err != nil {
		t.Fatalf("marshal ResolvedBox: %v", err)
	}
	viewJSON, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal ResolvedBoxView: %v", err)
	}
	var boxMapVar, viewMap map[string]json.RawMessage
	if err := json.Unmarshal(boxJSON, &boxMapVar); err != nil {
		t.Fatalf("unmarshal ResolvedBox: %v", err)
	}
	if err := json.Unmarshal(viewJSON, &viewMap); err != nil {
		t.Fatalf("unmarshal ResolvedBoxView: %v", err)
	}

	viewCanon := make(map[string]json.RawMessage, len(viewMap))
	for k, v := range viewMap {
		viewCanon[canonKey(k)] = v
	}

	// Completeness: box inspect's serialized field set ⊆ the projection, value-for-value.
	for k, bv := range boxMapVar {
		ck := canonKey(k)
		vv, ok := viewCanon[ck]
		if !ok {
			t.Fatalf("ResolvedBox field %q (canon %q) is DROPPED by projectResolvedBox — inspect exposes it", k, ck)
		}
		if !bytes.Equal(bv, vv) {
			t.Fatalf("field %q value differs: inspect=%s view=%s", k, bv, vv)
		}
	}

	// No host-only compute cache leaks into the wire view. The 3 RESOLVE-time vocab pointers
	// (DistroConfig/DistroDef/BuilderConfig) STAY host-only — the plugin render re-attaches them
	// from the project vocab (NewSpecResolvedBox), so they must never cross the wire. The
	// build-RENDER caches (BakedMetadata/Caps/RenderCandyOrder/InitSystem/InitDef/ActiveInits)
	// ARE wire data now (#67 render-DRIVE move — the plugin render reads them from the envelope
	// WITHOUT the live *Candy graph), so they are asserted in the positive set below.
	for _, cache := range []string{"distroconfig", "distrodef", "builderconfig"} {
		if _, leaked := viewCanon[cache]; leaked {
			t.Fatalf("host-only vocab pointer %q leaked into ResolvedBoxView (must stay json:%q, never wire data)", cache, "-")
		}
	}
}

// candyReaderFixture builds a spec.CandyReader test double the same way charly's own
// candy_test_helpers_test.go testCandy does — a thin deploykit.NewSpecCandyModel data adapter.
func candyReaderFixture(name string, m spec.CandyModel, v spec.CandyView) spec.CandyReader {
	m.Name = name
	v.Name = name
	return deploykit.NewSpecCandyModel(m, v)
}

// fixedResolvedProjectFixture assembles a deterministic spec.ResolvedProject from the box + a fully
// populated candy (via candyReaderFixture, exercising every #CandyView projection arm) + a deploy tree
// node — no time-dependent inputs, so its marshaling is a stable golden.
func fixedResolvedProjectFixture(t *testing.T) *spec.ResolvedProject {
	t.Helper()
	candy := candyReaderFixture("charly",
		spec.CandyModel{Version: "2026.100.0004"},
		spec.CandyView{
			Version:       "2026.100.0004",
			Description:   "the charly toolchain",
			Status:        "working",
			Info:          "the charly toolchain",
			Remote:        true,
			RepoPath:      "github.com/opencharly/charly",
			Require:       []string{"base"},
			IncludedCandy: []string{"gnupg"},
			EnvProvides:   map[string]string{"CHARLY_HOME": "/opt/charly"},
			MCPProvide:    []spec.MCPServerYAML{{Name: "charly-mcp", URL: "http://localhost:9000", Transport: "http"}},
			Ports:         []int64{9000},
			ServiceNames:  []string{"charly-daemon"},
		},
	)
	_, candyView, ok := deploykit.RawCandyPair(candy)
	if !ok {
		t.Fatal("rawCandyPair: candy fixture does not expose RawCandy()")
	}

	rp := &spec.ResolvedProject{
		Version: "2026.100.0000",
		Boxes:   map[string]spec.ResolvedBoxView{"demo": deploykit.ProjectResolvedBox(fullResolvedBoxFixture())},
		Candies: map[string]spec.CandyView{"charly": candyView},
	}
	bundle := map[string]spec.BundleNode{"demo-pod": {Target: "pod", Description: "demo deploy"}}
	for k, v := range bundle {
		node := v
		if rp.Deploy == nil {
			rp.Deploy = make(map[string]*spec.Deploy, len(bundle))
		}
		rp.Deploy[k] = &node
	}
	return rp
}

// TestResolvedProject_ByteStableGolden proves the assembled spec.ResolvedProject is deterministic
// (two marshals identical) and byte-stable against the committed golden. A dropped field, a reordered
// struct, or a changed projection all FAIL here. Regenerate with -update-resolved-project-golden.
func TestResolvedProject_ByteStableGolden(t *testing.T) {
	rp := fixedResolvedProjectFixture(t)

	got, err := json.MarshalIndent(rp, "", "  ")
	if err != nil {
		t.Fatalf("marshal ResolvedProject: %v", err)
	}
	got2, err := json.MarshalIndent(rp, "", "  ")
	if err != nil {
		t.Fatalf("marshal ResolvedProject (2nd): %v", err)
	}
	if !bytes.Equal(got, got2) {
		t.Fatalf("ResolvedProject marshaling is not deterministic:\n1st: %s\n2nd: %s", got, got2)
	}

	golden := filepath.Join("testdata", "resolved_project_golden.json")
	if *updateResolvedProjectGolden {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with -update-resolved-project-golden to create it): %v", err)
	}
	if !bytes.Equal(bytes.TrimRight(want, "\n"), got) {
		t.Fatalf("golden mismatch (run -update-resolved-project-golden if intended):\n got:\n%s\nwant:\n%s", got, want)
	}
}
