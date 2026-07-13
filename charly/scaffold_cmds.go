package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// scaffold_cmds.go — Kong command structs for the authoring + remote-repo
// surface. Each command auto-becomes an MCP tool via the `__cli-model` seam's
// kong reflection (buildCLIModel), which the out-of-process MCP bridge
// (candy/plugin-mcp) reads, so adding one here adds it to both the CLI and the
// MCP server in lockstep.

// The `charly box new project/box/candy` scaffolding commands moved to the COMPILED-IN
// candy/plugin-box (command:new, nested under box); they call kit.ScaffoldProject / kit.AddBox /
// kit.ScaffoldCandy directly, so no core handler remains here.

// ---------------------------------------------------------------------------
// `charly box set <dotpath> <value>`

type BoxSetCmd struct {
	Path  string `arg:"" help:"Dot-path into charly.yml (e.g. defaults.tag, <name>.candy.candy — the entity node, then its kind body)"`
	Value string `arg:"" help:"Value (parsed as YAML; use [a,b] for lists, {x: y} for maps)"`
}

func (c *BoxSetCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(dir, UnifiedFileName)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("charly.yml not found in %s; run `charly box new project .` to scaffold one", dir)
	}
	return kit.SetByDotPath(target, c.Path, c.Value)
}

// ---------------------------------------------------------------------------
// `charly box add-candy <box> <candy>`

type BoxAddCandyCmd struct {
	Box   string `arg:"" help:"Name of the box in charly.yml"`
	Candy string `arg:"" help:"Name of the candy to append"`
}

func (c *BoxAddCandyCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return AddCandyToBox(dir, c.Box, c.Candy)
}

// ---------------------------------------------------------------------------
// `charly box rm-candy <box> <candy>`

type BoxRmCandyCmd struct {
	Box   string `arg:"" help:"Name of the box in charly.yml"`
	Candy string `arg:"" help:"Name of the candy to remove"`
}

func (c *BoxRmCandyCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return RemoveCandyFromBox(dir, c.Box, c.Candy)
}

// ---------------------------------------------------------------------------
// `charly box fetch [<spec>]` and `charly box refresh [<spec>]`

type BoxFetchCmd struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → opencharly/charly)"`
}

func (c *BoxFetchCmd) Run() error {
	spec := c.Spec
	if spec == "" {
		spec = "default"
	}
	path, err := ResolveProjectRepo(spec)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

type BoxRefreshCmd struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → opencharly/charly)"`
}

func (c *BoxRefreshCmd) Run() error {
	spec := c.Spec
	if spec == "" {
		spec = "default"
	}
	repoPath, version := normalizeRepoSpec(spec)
	if version == "" {
		branch, err := GitDefaultBranch(RepoGitURL(repoPath))
		if err != nil {
			return fmt.Errorf("resolving default branch for %s: %w", repoPath, err)
		}
		version = branch
	}
	cachePath, err := RepoCachePath(repoPath, version)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(cachePath); err != nil {
		return fmt.Errorf("removing cache %s: %w", cachePath, err)
	}
	path, err := ResolveProjectRepo(spec)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// ---------------------------------------------------------------------------
// `charly box write <rel-path>` and `charly box cat <rel-path>`

type BoxWriteCmd struct {
	Path    string `arg:"" help:"Path under the project root (relative; .. is rejected)"`
	Content string `long:"content" help:"File content (mutually exclusive with --from-stdin)"`
	FromIn  bool   `long:"from-stdin" help:"Read file content from stdin"`
}

func (c *BoxWriteCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	abs, err := resolveProjectFile(dir, c.Path)
	if err != nil {
		return err
	}
	var data []byte
	switch {
	case c.FromIn && c.Content != "":
		return fmt.Errorf("--content and --from-stdin are mutually exclusive")
	case c.FromIn:
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
	default:
		data = []byte(c.Content)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("creating parent dir: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", abs, err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %d bytes to %s\n", len(data), abs)
	return nil
}

type BoxCatCmd struct {
	Path string `arg:"" help:"Path under the project root (relative; .. is rejected)"`
}

func (c *BoxCatCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	abs, err := resolveProjectFile(dir, c.Path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// resolveProjectFile turns a user-supplied relative path into an absolute
// path under projectDir, rejecting absolute paths and any traversal that
// would escape the project root. This is the one safety boundary for the
// `charly box write` / `charly box cat` escape hatch — every path passes
// through here.
func resolveProjectFile(projectDir, relPath string) (string, error) {
	if relPath == "" {
		return "", fmt.Errorf("path must be specified")
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative to project root, got absolute %q", relPath)
	}
	abs := filepath.Clean(filepath.Join(projectDir, relPath))
	rel, err := filepath.Rel(projectDir, abs)
	if err != nil {
		return "", fmt.Errorf("computing relative path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the project root", relPath)
	}
	return abs, nil
}
