package docs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alecthomas/kong"
)

// docsCLI is the `charly docs …` grammar. Kong parses the pass-through args charly forwards
// (out-of-process: the syscall.Exec'd argv tail; compiled-in: the decoded params args).
type docsCLI struct {
	Generate generateCmd `cmd:"" help:"Regenerate the reference half of the opencharly.ai docs site"`
}

// generateCmd renders every generated page into --out. It never touches the hand-authored
// narrative pages: it owns exactly the four generated trees (vision, reference, recipes) and
// rewrites them wholesale, so a deleted source entity disappears from the site instead of
// lingering as an orphan page.
type generateCmd struct {
	Out  string `name:"out" required:"" help:"Docs content root to write into (e.g. docs/src/content/docs)"`
	Root string `name:"root" help:"Repo root holding charly.yml, candy/, box/ and plugins/ (default: cwd)"`
}

// dispatchDocsCLI is the single entry point both placements use (CliMain out-of-process,
// Invoke(OpRun) compiled-in), so the command behaves identically either way.
func dispatchDocsCLI(args []string) int {
	var cli docsCLI
	parser, err := kong.New(&cli,
		kong.Name("docs"),
		kong.Description("Generate the opencharly.ai documentation site from this repo's canonical sources"),
		kong.UsageOnError(),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly docs: build parser: %v\n", err)
		return 1
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "charly docs: %v\n", err)
		return 1
	}
	if err := ctx.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "charly docs: %v\n", err)
		return 1
	}
	return 0
}

func (c *generateCmd) Run() error {
	root := c.Root
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve cwd: %w", err)
		}
		root = cwd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve --root: %w", err)
	}
	out, err := filepath.Abs(c.Out)
	if err != nil {
		return fmt.Errorf("resolve --out: %w", err)
	}
	return generate(root, out)
}
