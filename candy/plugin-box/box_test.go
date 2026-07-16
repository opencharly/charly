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

// TestNewMeta_DeclaresNestedCommands proves Describe advertises exactly the seven nested command
// capabilities (generate/validate/new/pkg/inspect/list/labels), all class "command", each with no
// InputDef (a command's args are pass-through tokens, not a structured plugin_input).
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
	for _, want := range []string{"generate", "validate", "new", "pkg", "inspect", "list", "labels"} {
		if !got[want] {
			t.Errorf("Describe missing command:%s (got %v)", want, got)
		}
	}
	if len(caps.GetProvided()) != 7 {
		t.Errorf("want 7 command capabilities, got %d", len(caps.GetProvided()))
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
