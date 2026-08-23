package marketplace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
)

// marketplaceCLI is the `charly marketplace …` grammar. Kong parses the pass-through args charly
// forwards (out-of-process: the syscall.Exec'd argv tail; compiled-in: the decoded params args).
type marketplaceCLI struct {
	Generate generateCmd `cmd:"" help:"Regenerate the charly-plugins marketplace + harness surface from candy config"`
	Drift    driftCmd    `cmd:"" help:"Fail-closed no-op check: regenerate in memory and compare with the artifacts on disk (git is never consulted)"`
}

// generateCmd renders every generated artifact: the marketplace corpus into --out (the
// standalone opencharly/marketplace repo root — skills, agents, the per-family manifests, the
// Claude/Codex/kimi/pi catalogs, profiles.json, the setup launcher) and the charly-local harness
// surface into --root (.claude/ hooks + settings, the dispatcher section). It rewrites the
// generated trees wholesale after pruning by the DO-NOT-EDIT header, so a deleted source entity
// disappears instead of lingering.
type generateCmd struct {
	Root string `name:"root" help:"Repo root holding charly.yml, candy/, box/, .claude/ (default: cwd)"`
	Out  string `name:"out" required:"" help:"Marketplace corpus root to write into (the opencharly/marketplace checkout)"`
}

// driftCmd is a fail-closed no-op check: the same pipeline in memory, compared byte-for-byte with
// the on-disk generated artifacts (the corpus under --out, the harness surface under --root).
// Any difference is a hard failure (exit 1) with a diff summary. It runs in no CI workflow — it
// is red only for whoever runs it, via `task skills:drift` or by hand.
type driftCmd struct {
	Root string `name:"root" help:"Repo root (default: cwd)"`
	Out  string `name:"out" required:"" help:"Marketplace corpus root to compare against (the opencharly/marketplace checkout)"`
}

// dispatchMarketplaceCLI is the single entry point both placements use (CliMain out-of-process,
// Invoke(OpRun) compiled-in), so the command behaves identically either way.
func dispatchMarketplaceCLI(args []string) int {
	var cli marketplaceCLI
	parser, err := kong.New(&cli,
		kong.Name("marketplace"),
		kong.Description("Regenerate the charly-plugins marketplace + harness surface from candy config"),
		kong.UsageOnError(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly marketplace: build parser: %v\n", err)
		return 1
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly marketplace: %v\n", err)
		return 1
	}
	if err := ctx.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "charly marketplace: %v\n", err)
		return 1
	}
	return 0
}

func resolveRoot(flag string) (string, error) {
	root := flag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		root = cwd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve --root: %w", err)
	}
	return root, nil
}

// resolveOut resolves --out. The marketplace corpus is standalone since the de-submodule
// cutover (the opencharly/marketplace repo), so the corpus destination is always explicit.
func resolveOut(flag string) (string, error) {
	out, err := filepath.Abs(flag)
	if err != nil {
		return "", fmt.Errorf("resolve --out: %w", err)
	}
	return out, nil
}

func (c *generateCmd) Run() error {
	root, err := resolveRoot(c.Root)
	if err != nil {
		return err
	}
	out, err := resolveOut(c.Out)
	if err != nil {
		return err
	}
	return generate(root, out)
}

func (c *driftCmd) Run() error {
	root, err := resolveRoot(c.Root)
	if err != nil {
		return err
	}
	out, err := resolveOut(c.Out)
	if err != nil {
		return err
	}
	return drift(root, out)
}
