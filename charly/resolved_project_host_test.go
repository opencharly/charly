package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

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
func fullResolvedBoxFixture() *ResolvedBox {
	return &ResolvedBox{
		Name:                  "demo",
		Version:               "2026.100.0001",
		EffectiveVersion:      "2026.100.0002",
		Status:                "working",
		Info:                  "a demo box",
		CheckLevel:            "noagent",
		Base:                  "fedora:43",
		From:                  "builder:pacstrap",
		BootstrapBuilderImage: "ghcr.io/opencharly/builder",
		Platforms:             []string{"linux/amd64"},
		Tag:                   "2026.100.0003",
		Registry:              "ghcr.io/opencharly",
		Pkg:                   "rpm",
		Distro:                []string{"fedora:43", "fedora"},
		BuildFormats:          []string{"rpm"},
		Tags:                  []string{"all", "fedora"},
		Candy:                 []string{"base", "charly"},
		User:                  "user",
		UID:                   1000,
		GID:                   1000,
		Home:                  "/home/user",
		UserAdopted:           true,
		Merge:                 &MergeConfig{Auto: true, MaxMB: 512, MaxTotalMB: 4096},
		Builder:               BuilderMap{"pixi": "ghcr.io/opencharly/pixi"},
		BuilderCapabilities:   []string{"pixi"},
		Auto:                  true,
		Network:               "host",
		DataImage:             true,
		IsExternalBase:        true,
		FullTag:               "ghcr.io/opencharly/demo:2026.100.0003",
		// Host-only json:"-" compute cache (must NOT leak into the wire view):
		InitSystem: "supervisord",
	}
}

// TestProjectResolvedBox_CompleteAndNoCacheLeak proves the two design invariants of the box view:
// COMPLETENESS (every field `charly box inspect` serializes — json.Marshal(*ResolvedBox) — survives
// projectResolvedBox with an equal value; a dropped/renamed field FAILS here) and NO CACHE LEAK (none
// of the 6 host-only json:"-" compute caches, InitSystem among them, appears in the wire view).
func TestProjectResolvedBox_CompleteAndNoCacheLeak(t *testing.T) {
	box := fullResolvedBoxFixture()
	view := projectResolvedBox(box)

	boxJSON, err := json.Marshal(box)
	if err != nil {
		t.Fatalf("marshal ResolvedBox: %v", err)
	}
	viewJSON, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal ResolvedBoxView: %v", err)
	}
	var boxMap, viewMap map[string]json.RawMessage
	if err := json.Unmarshal(boxJSON, &boxMap); err != nil {
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
	for k, bv := range boxMap {
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

// fixedResolvedProjectFixture assembles a deterministic spec.ResolvedProject from the box + a fully
// populated candy (via projectCandyView, exercising every #CandyView projection arm) + a deploy tree
// node — no time-dependent inputs, so its marshaling is a stable golden.
func fixedResolvedProjectFixture(t *testing.T) *spec.ResolvedProject {
	t.Helper()
	candy := &Candy{
		Name:          "charly",
		Version:       "2026.100.0004",
		Description:   "the charly toolchain",
		Status:        "working",
		Info:          "the charly toolchain",
		Remote:        true,
		RepoPath:      "github.com/opencharly/charly",
		Require:       []CandyRef{{Raw: "base"}},
		IncludedCandy: []CandyRef{{Raw: "gnupg"}},
	}
	candy.envProvides = map[string]string{"CHARLY_HOME": "/opt/charly"}
	candy.mcpProvides = []MCPServerYAML{{Name: "charly-mcp", URL: "http://localhost:9000", Transport: "http"}}
	candy.portSpecs = []PortSpec{{Port: 9000, Protocol: "tcp"}}
	candy.service = []ServiceEntry{{Name: "charly-daemon"}}

	rp := &spec.ResolvedProject{
		Version: "2026.100.0000",
		Boxes:   map[string]spec.ResolvedBoxView{"demo": projectResolvedBox(fullResolvedBoxFixture())},
		Candies: map[string]spec.CandyView{"charly": projectCandyView(candy)},
	}
	bundle := map[string]BundleNode{"demo-pod": {Target: "pod", Description: "demo deploy"}}
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

// writeResolvedProjectFixtureProject writes a minimal unified project (charly.yml + one discovered
// candy) into a temp dir — the hermetic, box-free (no ResolveBox/vocab dependency) fixture the seam
// round-trip test resolves.
func writeResolvedProjectFixtureProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "charly.yml"), []byte(
		"version: 2026.186.2323\n"+
			"discover:\n"+
			"    - path: candy\n"+
			"      recursive: true\n"), 0o644); err != nil {
		t.Fatalf("write charly.yml: %v", err)
	}
	candyDir := filepath.Join(dir, "candy", "rp-fixture")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatalf("mkdir candy: %v", err)
	}
	candy := "rp-fixture:\n" +
		"    candy:\n" +
		"        version: 2026.179.0000\n" +
		"        description: a fixture candy proving the resolved-project seam round-trips\n" +
		"        plan:\n" +
		"            - check: the true command runs\n" +
		"              id: rp-fixture-true\n" +
		"              context:\n" +
		"                  - build\n" +
		"              command: \"true\"\n"
	if err := os.WriteFile(filepath.Join(candyDir, "charly.yml"), []byte(candy), 0o644); err != nil {
		t.Fatalf("write candy charly.yml: %v", err)
	}
	return dir
}

// TestResolvedProject_SeamRoundTrip proves the plugin-side path: a caller requests the resolved-project
// envelope over the registered `resolved-project` HostBuild seam (request-decode → host build →
// reply-encode, exactly what Executor.HostBuild drives over the reverse channel) and decodes
// spec.ResolvedProject faithfully — the wire contract the ~20k K5 IOU consumers depend on.
func TestResolvedProject_SeamRoundTrip(t *testing.T) {
	dir := writeResolvedProjectFixtureProject(t)

	fn, ok := hostBuilderFor(resolvedProjectBuilderKind)
	if !ok {
		t.Fatal("resolved-project host-builder not registered")
	}
	reqJSON, err := json.Marshal(spec.ResolvedProjectRequest{Dir: dir})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	replyJSON, err := fn(context.Background(), reqJSON, buildEngineContext{})
	if err != nil {
		t.Fatalf("resolved-project host-build: %v", err)
	}
	var rp spec.ResolvedProject
	if err := json.Unmarshal(replyJSON, &rp); err != nil {
		t.Fatalf("decode spec.ResolvedProject over the seam: %v", err)
	}

	cv, ok := rp.Candies["rp-fixture"]
	if !ok {
		t.Fatalf("rp-fixture candy missing from the projected ResolvedProject: %+v", rp.Candies)
	}
	if cv.Version != "2026.179.0000" || !strings.Contains(cv.Description, "resolved-project seam round-trips") {
		t.Fatalf("candy view decoded wrong over the seam: %+v", cv)
	}

	// Collection A growth (would be ABSENT pre-#54): the candy BUILD model is projected — the
	// check-projection / validate / K3-D enabler. The fixture candy declares one plan step.
	cm, ok := rp.CandyModels["rp-fixture"]
	if !ok {
		t.Fatalf("rp-fixture candy MODEL missing from CandyModels: %+v", rp.CandyModels)
	}
	if len(cm.Plan) == 0 {
		t.Fatalf("candy model Plan not projected over the seam (the check-include/validate enabler): %+v", cm)
	}
	// build VOCABULARY (the validate ENGINE consumer) is projected from the embedded charly.yml.
	if len(rp.Distro) == 0 {
		t.Fatalf("build-vocab Distro not projected into the envelope (validate needs it)")
	}
	if len(rp.Builder) == 0 {
		t.Fatalf("build-vocab Builder not projected into the envelope (validate needs it)")
	}
}
