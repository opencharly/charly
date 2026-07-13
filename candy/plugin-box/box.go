package box

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/alecthomas/kong"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// hostClient is the box commands' host coupling: it reaches charly's host process over the
// reverse channel — either InvokeProvider (peer plugin dispatch, for generate → build:generate) or
// the generic HostBuild("cli") reentry (for validate/pkg → the hidden __box-* core commands). The
// `new` command needs neither (it calls kit scaffolding directly).
type hostClient struct {
	ctx  context.Context
	exec *sdk.Executor
}

// cli asks the HOST to run `charly <argv>` via the generic "cli" host-builder and returns the
// CliReply (stdout when capture, the process exit code, any spawn error). Mirrors the alias
// plugin's host coupling (R3 in shape; each plugin owns its own reverse-channel calls).
func (h *hostClient) cli(capture, bestEffort bool, argv ...string) (spec.CliReply, error) {
	reqJSON, err := json.Marshal(spec.CliRequest{Argv: argv, Capture: capture, BestEffort: bestEffort})
	if err != nil {
		return spec.CliReply{}, err
	}
	resJSON, err := h.exec.HostBuild(h.ctx, "cli", reqJSON)
	if err != nil {
		return spec.CliReply{}, err
	}
	var r spec.CliReply
	if uerr := json.Unmarshal(resJSON, &r); uerr != nil {
		return spec.CliReply{}, uerr
	}
	return r, nil
}

// dispatchBoxCommand routes a box command word to its handler.
func dispatchBoxCommand(hc *hostClient, word string, args []string) error {
	switch word {
	case "generate":
		return dispatchGenerate(hc, args)
	case "validate":
		return dispatchValidate(hc, args)
	case "new":
		return dispatchNew(args)
	case "pkg":
		return dispatchPkg(hc, args)
	default:
		return fmt.Errorf("box: unknown command word %q", word)
	}
}

// parseLeaf kong-parses args into a single-command grammar struct (positional args + flags, no
// subcommands). kong.Exit is neutralised so a parse/`--help` error returns instead of exiting the
// whole charly process.
func parseLeaf(name string, target any, args []string) error {
	parser, err := kong.New(target, kong.Name("box "+name), kong.Exit(func(int) {}))
	if err != nil {
		return err
	}
	_, err = parser.Parse(args)
	return err
}

// --- box generate ---

// generateGrammar is the `charly box generate [boxes…] [--tag] [--include-disabled]` CLI surface.
type generateGrammar struct {
	Boxes           []string `arg:"" optional:"" help:"Boxes to generate (default: all enabled). The sentinel 'all' is equivalent to passing no argument."`
	Tag             string   `long:"tag" help:"Override tag (default: CalVer)"`
	IncludeDisabled bool     `long:"include-disabled" help:"Generate boxes with enabled: false in charly.yml (does not modify the file). Scoped to the named boxes when any are given."`
}

// dispatchGenerate renders the .build/ Containerfile tree by INVOKING the peer COMPILED-IN
// build:generate word (candy/plugin-build) over the InvokeProvider reverse leg — the SAME path the
// former core dispatchBoxGenerate took (invoke build:generate with OpBuild), so build:generate stays
// the single generate implementation (no duplication, no orphaned capability). The host build-resolve
// seam normalizes the `all` sentinel + scopes the selection, so the boxes ride verbatim.
func dispatchGenerate(hc *hostClient, args []string) error {
	var g generateGrammar
	if err := parseLeaf("generate", &g, args); err != nil {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	reqJSON, err := json.Marshal(spec.BuildRequest{
		Boxes:           g.Boxes,
		Tag:             g.Tag,
		Dir:             dir,
		IncludeDisabled: g.IncludeDisabled,
	})
	if err != nil {
		return err
	}
	resJSON, err := hc.exec.InvokeProvider(hc.ctx, "build", "generate", sdk.OpBuild, reqJSON, nil)
	if err != nil {
		return err
	}
	var reply spec.BuildReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return fmt.Errorf("box generate: decode reply: %w", err)
		}
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}

// --- box validate ---

// validateGrammar is the `charly box validate [--include-disabled]` CLI surface.
type validateGrammar struct {
	IncludeDisabled bool `long:"include-disabled" help:"Include boxes with enabled: false in validation (does not modify charly.yml)"`
}

// dispatchValidate reaches the hidden core `__box-validate` reentry over HostBuild("cli"): the
// validation needs the fully-resolved project the plugin cannot load pre-K1. The subprocess inherits
// charly's stderr (it renders the ValidationError text itself) and exits 0/1; a non-zero exit surfaces
// as the command error.
func dispatchValidate(hc *hostClient, args []string) error {
	var g validateGrammar
	if err := parseLeaf("validate", &g, args); err != nil {
		return err
	}
	argv := []string{"__box-validate"}
	if g.IncludeDisabled {
		argv = append(argv, "--include-disabled")
	}
	r, err := hc.cli(false, true, argv...)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("charly.yml + candies failed validation")
	}
	return nil
}

// --- box pkg ---

// pkgGrammar is the `charly box pkg [formats…] [--candy] [--out]` CLI surface.
type pkgGrammar struct {
	Format []string `arg:"" optional:"" help:"Package formats to build (pac/rpm/deb). Default: every format the candy declares a localpkg source for."`
	Candy  string   `long:"candy" default:"charly" help:"Candy whose localpkg sources to build."`
	Out    string   `long:"out" default:"dist" help:"Output directory for the built package files."`
}

// dispatchPkg reaches the hidden core `__box-pkg` reentry over HostBuild("cli"): the localpkg build
// engine (buildLocalPkgOnHost) needs the host build context the plugin cannot compute pre-K1. The
// subprocess inherits charly's stdio (it prints the built file paths + status) and exits 0/1.
func dispatchPkg(hc *hostClient, args []string) error {
	var g pkgGrammar
	if err := parseLeaf("pkg", &g, args); err != nil {
		return err
	}
	argv := append([]string{"__box-pkg"}, g.Format...)
	argv = append(argv, "--candy", g.Candy, "--out", g.Out)
	r, err := hc.cli(false, true, argv...)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("box pkg failed (exit %d)", r.ExitCode)
	}
	return nil
}

// --- box new (candy/project/box) ---

// newGrammar is the `charly box new candy/project/box …` subcommand group. Each leaf's Run calls the
// sdk/kit scaffold ENGINE directly (kit.ScaffoldCandy / kit.ScaffoldProject / kit.AddBox), so the
// whole `new` group externalizes with ZERO core reentry — the scaffolders already live in sdk/kit.
type newGrammar struct {
	Candy   newCandyGrammar   `cmd:"" name:"candy" help:"Scaffold a candy directory"`
	Project newProjectGrammar `cmd:"" name:"project" help:"Scaffold a fresh charly project (charly.yml + candy/)"`
	Box     newBoxGrammar     `cmd:"" name:"box" help:"Add a new box entry to charly.yml"`
}

// dispatchNew kong-parses the `new` subcommand tree and runs the selected leaf (kctx.Run dispatches
// to that leaf's Run — none needs the host reverse channel).
func dispatchNew(args []string) error {
	var g newGrammar
	parser, err := kong.New(&g, kong.Name("box new"), kong.Exit(func(int) {}))
	if err != nil {
		return err
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		return err
	}
	return kctx.Run()
}

// nowCalVer computes the current wall-clock CalVer via the existing kit.CalVer type (no duplicate
// format literal): the candy-identity stamp ScaffoldCandy writes into the new candy's charly.yml.
func nowCalVer() string {
	now := time.Now().UTC()
	return kit.CalVer{Year: now.Year(), Day: now.YearDay(), HHMM: now.Hour()*100 + now.Minute()}.String()
}

type newCandyGrammar struct {
	Name string `arg:"" help:"Candy name"`
}

func (c *newCandyGrammar) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := kit.ScaffoldCandy(dir, c.Name, nowCalVer()); err != nil {
		return err
	}
	fmt.Printf("Created candy at %s\n", filepath.Join(dir, kit.DefaultCandyDir, c.Name))
	fmt.Println("Files created:")
	fmt.Println("  charly.yml - Candy config (distro packages, require, env, port, route, service)")
	fmt.Println()
	fmt.Println("Optional files you can add:")
	fmt.Println("  root.yml        - Custom root install task")
	fmt.Println("  pixi.toml       - Python/conda packages")
	fmt.Println("  package.json    - npm packages")
	fmt.Println("  Cargo.toml      - Rust crate (requires src/)")
	fmt.Println("  user.yml        - Custom user install task")
	return nil
}

type newProjectGrammar struct {
	Dir string `arg:"" help:"Directory to scaffold the project in (created if missing)"`
}

func (c *newProjectGrammar) Run() error {
	if err := kit.ScaffoldProject(c.Dir); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Scaffolded project at %s\n", c.Dir)
	fmt.Fprintln(os.Stderr, "Next steps:")
	fmt.Fprintln(os.Stderr, "  # The distro/builder/init build vocabulary is embedded — declare distro:/builder:/init: only to override it.")
	fmt.Fprintln(os.Stderr, "  # Add a candy, populate it, wire it into a box, then build:")
	fmt.Fprintln(os.Stderr, "  charly -C "+c.Dir+" box new candy my-candy")
	fmt.Fprintln(os.Stderr, "  charly -C "+c.Dir+" candy add-rpm my-candy curl jq")
	fmt.Fprintln(os.Stderr, "  charly -C "+c.Dir+" box new box my-box --base quay.io/fedora/fedora:43 --candies my-candy")
	fmt.Fprintln(os.Stderr, "  charly -C "+c.Dir+" box build my-box")
	return nil
}

type newBoxGrammar struct {
	Name    string   `arg:"" help:"Name for the new box entry"`
	Base    string   `long:"base" required:"" help:"Base image (URL like quay.io/... or another box name)"`
	Candies []string `long:"candy" sep:"," help:"Comma-separated list of candy names to include"`
}

func (c *newBoxGrammar) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := kit.AddBox(dir, c.Name, c.Base, c.Candies); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Added box %s to charly.yml\n", c.Name)
	return nil
}
