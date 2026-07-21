package box

import (
	"context"
	"reflect"
	"testing"

	"github.com/alecthomas/kong"
	pb "github.com/opencharly/sdk/proto"
)

// TestCommandParent_NestsUnderBox proves the provider declares the optional CommandParent()
// interface buildUnitInProc detects to nest every command word under `box` (`charly box generate`
// etc.), never at the CLI root.
func TestCommandParent_NestsUnderBox(t *testing.T) {
	if got := (provider{}).CommandParent(); got != "box" {
		t.Fatalf("CommandParent() = %q, want %q", got, "box")
	}
}

// TestNewMeta_DeclaresNestedCommands proves Describe advertises exactly the eleven nested command
// capabilities (generate/validate/new/pkg/pull/build/inspect/list/labels/merge/reconcile — "build"
// added FINAL/K5 unit 6a M4d, the CLI-only mirror of "pull"'s M4c move), all class "command", each
// with no InputDef (a command's args are pass-through tokens, not a structured plugin_input).
func TestNewMeta_DeclaresNestedCommands(t *testing.T) {
	caps, err := NewMeta().Describe(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	got := map[string]bool{}
	for _, c := range caps.GetProvided() {
		if c.GetClass() != "command" {
			t.Errorf("capability %s:%s is not class command", c.GetClass(), c.GetWord())
		}
		if c.GetInputDef() != "" {
			t.Errorf("command:%s must ship no InputDef, got %q", c.GetWord(), c.GetInputDef())
		}
		got[c.GetWord()] = true
	}
	for _, want := range []string{"generate", "validate", "new", "pkg", "pull", "build", "inspect", "list", "labels", "merge", "reconcile"} {
		if !got[want] {
			t.Errorf("Describe missing command:%s (got %v)", want, got)
		}
	}
	if len(caps.GetProvided()) != 11 {
		t.Errorf("want 11 command capabilities, got %d", len(caps.GetProvided()))
	}
}

// TestGenerateGrammar_Parse confirms `box generate` accepts the optional boxes positional (incl. the
// `all` sentinel) and the --tag / --include-disabled flags — the surface that formerly lived on the
// core GenerateCmd (its Kong-parse test moved here with the P15 externalization).
func TestGenerateGrammar_Parse(t *testing.T) {
	parse := func(args ...string) generateGrammar {
		var g generateGrammar
		if _, err := parseLeaf("generate", &g, args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		return g
	}
	if g := parse(); len(g.Boxes) != 0 {
		t.Errorf("bare generate: Boxes = %v, want empty", g.Boxes)
	}
	if g := parse("all"); !reflect.DeepEqual(g.Boxes, []string{"all"}) {
		t.Errorf("generate all: Boxes = %v, want [all]", g.Boxes)
	}
	if g := parse("fedora", "arch"); !reflect.DeepEqual(g.Boxes, []string{"fedora", "arch"}) {
		t.Errorf("generate fedora arch: Boxes = %v, want [fedora arch]", g.Boxes)
	}
	if g := parse("immich", "--include-disabled"); !g.IncludeDisabled {
		t.Errorf("generate immich --include-disabled: IncludeDisabled = false, want true")
	}
	if g := parse("fedora", "--tag", "v1"); g.Tag != "v1" {
		t.Errorf("generate --tag v1: Tag = %q, want v1", g.Tag)
	}
}

// TestNewGrammar_Parse confirms the `box new` subcommand tree parses candy/project/box and their
// flags (the whole group externalized to kit, so its grammar lives here).
func TestNewGrammar_Parse(t *testing.T) {
	mustParse := func(args ...string) *kong.Context {
		var g newGrammar
		parser, err := kong.New(&g, kong.Name("box new"), kong.Exit(func(int) {}))
		if err != nil {
			t.Fatalf("kong.New: %v", err)
		}
		kctx, err := parser.Parse(args)
		if err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}
		return kctx
	}
	if cmd := mustParse("candy", "my-candy").Command(); cmd != "candy <name>" {
		t.Errorf("new candy: command = %q, want %q", cmd, "candy <name>")
	}
	if cmd := mustParse("project", "somedir").Command(); cmd != "project <dir>" {
		t.Errorf("new project: command = %q, want %q", cmd, "project <dir>")
	}
	if cmd := mustParse("box", "my-box", "--base", "quay.io/fedora/fedora:43", "--candies", "a,b").Command(); cmd != "box <name>" {
		t.Errorf("new box: command = %q, want %q", cmd, "box <name>")
	}
}

// TestPkgGrammar_Parse confirms `box pkg` parses formats + --candy / --out with the expected
// defaults.
func TestPkgGrammar_Parse(t *testing.T) {
	var g pkgGrammar
	if _, err := parseLeaf("pkg", &g, []string{"rpm", "deb"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(g.Format, []string{"rpm", "deb"}) {
		t.Errorf("Format = %v, want [rpm deb]", g.Format)
	}
	if g.Candy != "charly" {
		t.Errorf("Candy default = %q, want charly", g.Candy)
	}
	if g.Out != "dist" {
		t.Errorf("Out default = %q, want dist", g.Out)
	}
}

// TestPullGrammar_Parse confirms `box pull` accepts the required box positional plus the two
// optional --tag/--platform flags — byte-identical to the former static BoxPullCmd Kong leaf
// (FINAL/K5 unit 6a M4c).
func TestPullGrammar_Parse(t *testing.T) {
	var g pullGrammar
	if _, err := parseLeaf("pull", &g, []string{"jupyter", "--tag", "2026.100.0000", "--platform", "linux/amd64"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if g.Box != "jupyter" {
		t.Errorf("Box = %q, want jupyter", g.Box)
	}
	if g.Tag != "2026.100.0000" {
		t.Errorf("Tag = %q, want 2026.100.0000", g.Tag)
	}
	if g.Platform != "linux/amd64" {
		t.Errorf("Platform = %q, want linux/amd64", g.Platform)
	}
}

// TestPullGrammar_Parse_MinimalArgs confirms Tag/Platform default to empty (unset) when omitted —
// dispatchPull relies on this to decide whether to forward them in the reentry argv.
func TestPullGrammar_Parse_MinimalArgs(t *testing.T) {
	var g pullGrammar
	if _, err := parseLeaf("pull", &g, []string{"ghcr.io/opencharly/jupyter:v1"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if g.Box != "ghcr.io/opencharly/jupyter:v1" {
		t.Errorf("Box = %q, want ghcr.io/opencharly/jupyter:v1", g.Box)
	}
	if g.Tag != "" {
		t.Errorf("Tag = %q, want empty", g.Tag)
	}
	if g.Platform != "" {
		t.Errorf("Platform = %q, want empty", g.Platform)
	}
}

// TestBuildGrammar_Parse confirms `box build` accepts the optional boxes positional plus every one
// of the nine flags — byte-identical to the former static BuildCmd Kong leaf (FINAL/K5 unit 6a
// M4d).
func TestBuildGrammar_Parse(t *testing.T) {
	var g buildGrammar
	args := []string{
		"jupyter", "arch-builder",
		"--push", "--tag", "2026.100.0000", "--platform", "linux/amd64",
		"--cache", "registry", "--no-cache",
		"--jobs", "8", "--podman-jobs", "2",
		"--include-disabled", "--dev-local-pkg",
	}
	if _, err := parseLeaf("build", &g, args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !reflect.DeepEqual(g.Boxes, []string{"jupyter", "arch-builder"}) {
		t.Errorf("Boxes = %v, want [jupyter arch-builder]", g.Boxes)
	}
	if !g.Push {
		t.Error("Push = false, want true")
	}
	if g.Tag != "2026.100.0000" {
		t.Errorf("Tag = %q, want 2026.100.0000", g.Tag)
	}
	if g.Platform != "linux/amd64" {
		t.Errorf("Platform = %q, want linux/amd64", g.Platform)
	}
	if g.Cache != "registry" {
		t.Errorf("Cache = %q, want registry", g.Cache)
	}
	if !g.NoCache {
		t.Error("NoCache = false, want true")
	}
	if g.Jobs != 8 {
		t.Errorf("Jobs = %d, want 8", g.Jobs)
	}
	if g.PodmanJobs != 2 {
		t.Errorf("PodmanJobs = %d, want 2", g.PodmanJobs)
	}
	if !g.IncludeDisabled {
		t.Error("IncludeDisabled = false, want true")
	}
	if !g.DevLocalPkg {
		t.Error("DevLocalPkg = false, want true")
	}
}

// TestBuildGrammar_Parse_MinimalArgs confirms every flag defaults to its zero value when omitted —
// dispatchBuild relies on this to decide which flags to forward in the reentry argv.
func TestBuildGrammar_Parse_MinimalArgs(t *testing.T) {
	var g buildGrammar
	if _, err := parseLeaf("build", &g, nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(g.Boxes) != 0 {
		t.Errorf("Boxes = %v, want empty", g.Boxes)
	}
	if g.Push || g.NoCache || g.IncludeDisabled || g.DevLocalPkg {
		t.Errorf("bool flags = %+v, want all false", g)
	}
	if g.Tag != "" || g.Platform != "" || g.Cache != "" {
		t.Errorf("string flags = %+v, want all empty", g)
	}
	if g.Jobs != 0 || g.PodmanJobs != 0 {
		t.Errorf("int flags = %+v, want all zero", g)
	}
}

// TestCanonicalLabelKey_ExpandsShorthand + TestSortedLabelKeys_FiltersToContractUnlessAll: moved
// from charly/box_labels_cmd_test.go (K3 reentry-class dissolution — canonicalLabelKey/
// sortedLabelKeys moved here with dispatchLabels).
func TestCanonicalLabelKey_ExpandsShorthand(t *testing.T) {
	cases := map[string]string{
		"init":                      "ai.opencharly.init",
		"version":                   "ai.opencharly.version",
		"ai.opencharly.description": "ai.opencharly.description",
		"org.opencontainers.x":      "org.opencontainers.x",
	}
	for in, want := range cases {
		if got := canonicalLabelKey(in); got != want {
			t.Errorf("canonicalLabelKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSortedLabelKeys_FiltersToContractUnlessAll(t *testing.T) {
	labels := map[string]string{
		"ai.opencharly.version": "2026.001.0001",
		"ai.opencharly.init":    "supervisord",
		"maintainer":            "someone",
	}
	if got := sortedLabelKeys(labels, false); !reflect.DeepEqual(got, []string{"ai.opencharly.init", "ai.opencharly.version"}) {
		t.Errorf("contract-only keys = %v", got)
	}
	if got := sortedLabelKeys(labels, true); !reflect.DeepEqual(got, []string{"ai.opencharly.init", "ai.opencharly.version", "maintainer"}) {
		t.Errorf("all keys = %v", got)
	}
}
