package authoring

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// dispatchAuthoringCommand routes a box authoring command word to its handler. The pure verbs
// (set/add-candy/rm-candy/write/cat) run entirely on sdk/kit + stdlib (authoring_edit.go); fetch
// and refresh reach the host-coupled repo resolver over the reverse channel (hc.fetchResolve,
// HostBuild("box-fetch-resolve")).
func dispatchAuthoringCommand(hc *hostClient, word string, args []string) error {
	switch word {
	case "set":
		return dispatchSet(args)
	case "add-candy":
		return dispatchAddCandy(args)
	case "rm-candy":
		return dispatchRmCandy(args)
	case "write":
		return dispatchWrite(args)
	case "cat":
		return dispatchCat(args)
	case "fetch":
		return dispatchFetch(hc, args)
	case "refresh":
		return dispatchRefresh(hc, args)
	default:
		return fmt.Errorf("authoring: unknown command word %q", word)
	}
}

// parseLeaf kong-parses args into a single-command grammar struct (positional args + flags, no
// subcommands) via the shared sdk helper, which neutralises kong's process-exit and handles
// `--help`/`--version` cleanly. done=true means kong printed help/version — the caller MUST return
// nil without running the leaf's action (otherwise `charly box <leaf> --help` would run the leaf).
func parseLeaf(name string, target any, args []string) (done bool, err error) {
	return sdk.ParseInProcCLI("box "+name, target, args)
}

// --- box set <dotpath> <value> ---

// setGrammar is the `charly box set <path> <value>` CLI surface. The value is parsed as YAML by
// kit.SetByDotPath (use [a,b] for lists, {x: y} for maps).
type setGrammar struct {
	Path  string `arg:"" help:"Dot-path into charly.yml (e.g. defaults.tag, <name>.candy.candy — the entity node, then its kind body)"`
	Value string `arg:"" help:"Value (parsed as YAML; use [a,b] for lists, {x: y} for maps)"`
}

func dispatchSet(args []string) error {
	var g setGrammar
	if done, err := parseLeaf("set", &g, args); err != nil || done {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	target := filepath.Join(dir, spec.UnifiedFileName)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("charly.yml not found in %s; run `charly box new project .` to scaffold one", dir)
	}
	return kit.SetByDotPath(target, g.Path, g.Value)
}

// --- box add-candy <box> <candy> ---

// addCandyGrammar is the `charly box add-candy <box> <candy>` CLI surface.
type addCandyGrammar struct {
	Box   string `arg:"" help:"Name of the box in charly.yml"`
	Candy string `arg:"" help:"Name of the candy to append"`
}

func dispatchAddCandy(args []string) error {
	var g addCandyGrammar
	if done, err := parseLeaf("add-candy", &g, args); err != nil || done {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return addCandyToBox(dir, g.Box, g.Candy)
}

// --- box rm-candy <box> <candy> ---

// rmCandyGrammar is the `charly box rm-candy <box> <candy>` CLI surface.
type rmCandyGrammar struct {
	Box   string `arg:"" help:"Name of the box in charly.yml"`
	Candy string `arg:"" help:"Name of the candy to remove"`
}

func dispatchRmCandy(args []string) error {
	var g rmCandyGrammar
	if done, err := parseLeaf("rm-candy", &g, args); err != nil || done {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return removeCandyFromBox(dir, g.Box, g.Candy)
}

// --- box write <rel-path> and box cat <rel-path> ---

// writeGrammar is the `charly box write <path> [--content X | --from-stdin]` CLI surface.
type writeGrammar struct {
	Path    string `arg:"" help:"Path under the project root (relative; .. is rejected)"`
	Content string `name:"content" help:"File content (mutually exclusive with --from-stdin)"`
	FromIn  bool   `name:"from-stdin" help:"Read file content from stdin"`
}

func dispatchWrite(args []string) error {
	var g writeGrammar
	if done, err := parseLeaf("write", &g, args); err != nil || done {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	abs, err := resolveProjectFile(dir, g.Path)
	if err != nil {
		return err
	}
	var data []byte
	switch {
	case g.FromIn && g.Content != "":
		return fmt.Errorf("--content and --from-stdin are mutually exclusive")
	case g.FromIn:
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
	default:
		data = []byte(g.Content)
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

// catGrammar is the `charly box cat <path>` CLI surface.
type catGrammar struct {
	Path string `arg:"" help:"Path under the project root (relative; .. is rejected)"`
}

func dispatchCat(args []string) error {
	var g catGrammar
	if done, err := parseLeaf("cat", &g, args); err != nil || done {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	abs, err := resolveProjectFile(dir, g.Path)
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

// --- box fetch [<spec>] and box refresh [<spec>] ---

// fetchGrammar is the `charly box fetch [<spec>]` CLI surface (default spec: 'default' →
// opencharly/charly). The repo resolver is host-coupled (CHARLY_REPO_OVERRIDE + the registered
// refs-backend download + the command:migrate auto-migration), so the plugin reaches the generic
// "box-fetch-resolve" HostBuild seam (charly/host_build_box_fetch_resolve.go, K3 build-tail tail,
// coneB-buildremnant — the former hidden core `__box-fetch` reentry is DELETED) and prints the
// returned cache path itself.
type fetchGrammar struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → opencharly/charly)"`
}

func dispatchFetch(hc *hostClient, args []string) error {
	var g fetchGrammar
	if done, err := parseLeaf("fetch", &g, args); err != nil || done {
		return err
	}
	path, err := hc.fetchResolve(g.Spec, false)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// refreshGrammar is the `charly box refresh [<spec>]` CLI surface — force re-clone of a remote
// project repo. Reaches the same "box-fetch-resolve" HostBuild seam with Refresh=true (see
// dispatchFetch), which removes the spec's cache entry before resolving — the former hidden core
// `__box-refresh` reentry is DELETED.
type refreshGrammar struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → opencharly/charly)"`
}

func dispatchRefresh(hc *hostClient, args []string) error {
	var g refreshGrammar
	if done, err := parseLeaf("refresh", &g, args); err != nil || done {
		return err
	}
	path, err := hc.fetchResolve(g.Spec, true)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
