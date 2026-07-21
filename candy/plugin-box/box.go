package box

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// hostClient is the box commands' host coupling: it reaches charly's host process over the
// reverse channel — InvokeProvider (peer plugin dispatch, for generate → build:generate), the
// HostBuild("resolved-project") envelope fetch (inspect/list), the HostBuild("validate-project")
// envelope fetch (validate runs the rule ENGINE in-plugin over the reply), or the generic
// HostBuild("cli") reentry (pkg → the hidden __box-pkg core command, pull → the hidden __box-pull
// core command, build → the hidden __box-build core command, and inspect/list's overlay/store
// residue → __box-inspect-overlay / __box-list-tags). The `new` command needs neither (kit
// scaffolding directly).
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
	case "pull":
		return dispatchPull(hc, args)
	case "build":
		return dispatchBuild(hc, args)
	case "inspect":
		return dispatchInspect(hc, args)
	case "list":
		return dispatchList(hc, args)
	case "labels":
		return dispatchLabels(args)
	case "merge":
		return dispatchMerge(hc, args)
	case "reconcile":
		return dispatchReconcile(args)
	default:
		return fmt.Errorf("box: unknown command word %q", word)
	}
}

// parseLeaf kong-parses args into a single-command grammar struct (positional args + flags, no
// subcommands) via the shared sdk helper, which neutralises kong's process-exit and handles
// `--help`/`--version` cleanly. done=true means kong printed help/version — the caller MUST return
// nil without running the leaf's action (otherwise `charly box <leaf> --help` would run the leaf).
func parseLeaf(name string, target any, args []string) (done bool, err error) {
	return sdk.ParseInProcCLI("box "+name, target, args)
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
	if done, err := parseLeaf("generate", &g, args); err != nil || done {
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
	resJSON, err := hc.exec.InvokeProvider(hc.ctx, "build", "generate", sdk.OpBuild, reqJSON, nil, sdk.InvokeProviderOpts{})
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

// validateGrammar is the `charly box validate [--include-disabled]` CLI surface. The validate ENGINE
// itself lives in validate.go (it reads the resolved-project envelope + re-runs the deploykit graph
// checks); dispatchValidate is defined there.
type validateGrammar struct {
	IncludeDisabled bool `long:"include-disabled" help:"Include boxes with enabled: false in validation (does not modify charly.yml)"`
}

// --- box pkg ---

// pkgGrammar is the `charly box pkg [formats…] [--candy] [--out]` CLI surface.
type pkgGrammar struct {
	Format []string `arg:"" optional:"" help:"Package formats to build (pac/rpm/deb). Default: every format the candy declares a localpkg source for."`
	Candy  string   `long:"candy" default:"charly" help:"Candy whose localpkg sources to build."`
	Out    string   `long:"out" default:"dist" help:"Output directory for the built package files."`
}

// dispatchPkg reaches the hidden core `__box-pkg` reentry over HostBuild("cli"): the localpkg build
// engine (deploykit.BuildLocalPkgOnHost, W3) still needs core's builder-image resolve closures pre-K1. The
// subprocess inherits charly's stdio (it prints the built file paths + status) and exits 0/1.
func dispatchPkg(hc *hostClient, args []string) error {
	var g pkgGrammar
	if done, err := parseLeaf("pkg", &g, args); err != nil || done {
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

// --- box pull ---

// pullGrammar is the `charly box pull <box> [--tag] [--platform]` CLI surface — byte-identical to
// the former static BoxPullCmd Kong leaf (FINAL/K5 unit 6a M4c): same positional, same two flags,
// same help text, so `charly box pull --help` renders unchanged.
type pullGrammar struct {
	Box      string `arg:"" help:"Box name (short, resolved via charly.yml), fully-qualified ref, or @github.com/org/repo/box[:version]"`
	Tag      string `long:"tag" help:"Image CalVer tag when resolving a short name (empty = resolve from charly.yml metadata or error with explicit guidance)"`
	Platform string `long:"platform" help:"Target platform (default: host)"`
}

// dispatchPull reaches the hidden core `__box-pull` reentry over HostBuild("cli"): EnsureImagePresent
// (BoxPullCmd's Run body, UNCHANGED) still needs the full box-build engine + charly.yml resolution
// pre the ensure_image.go + build.go + remote_image.go batch. Tag/Platform are omitted from argv when
// empty (Kong's own zero-value default for an absent flag) rather than passed as an empty string —
// avoids any flag-parsing divergence between "flag absent" and "flag present with empty value" on the
// reentered leaf, keeping behavior identical to the un-dispersed command. The subprocess inherits
// charly's stdio (same "ensure-image: ..." progress lines) and exits 0/1.
func dispatchPull(hc *hostClient, args []string) error {
	var g pullGrammar
	if done, err := parseLeaf("pull", &g, args); err != nil || done {
		return err
	}
	argv := []string{"__box-pull", g.Box}
	if g.Tag != "" {
		argv = append(argv, "--tag", g.Tag)
	}
	if g.Platform != "" {
		argv = append(argv, "--platform", g.Platform)
	}
	r, err := hc.cli(false, true, argv...)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("box pull failed (exit %d)", r.ExitCode)
	}
	return nil
}

// --- box build ---

// buildGrammar is the `charly box build [boxes…] [flags]` CLI surface — byte-identical to the
// former static BuildCmd Kong leaf (FINAL/K5 unit 6a M4d): same positional, same nine flags
// (including the three env-var-backed tunables), same help text, so `charly box build --help`
// renders unchanged and CHARLY_BUILD_CACHE/CHARLY_BUILD_JOBS/CHARLY_PODMAN_JOBS keep working —
// Kong resolves them in-plugin at parse time and the resolved values flow through argv, so the
// reentered `__box-build` subprocess needs neither the env vars nor to re-parse them itself.
type buildGrammar struct {
	Boxes           []string `arg:"" optional:"" help:"Boxes to build (default: all enabled; the sentinel 'all' is equivalent). Supports remote refs (github.com/org/repo/box[@version])"`
	Push            bool     `long:"push" help:"Push to registry after building"`
	Tag             string   `long:"tag" help:"Override tag (default: CalVer)"`
	Platform        string   `long:"platform" help:"Target platform (default: host platform)"`
	Cache           string   `long:"cache" help:"Build cache type: registry, image, gha, none (default: auto)" env:"CHARLY_BUILD_CACHE"`
	NoCache         bool     `long:"no-cache" help:"Disable build cache entirely"`
	Jobs            int      `long:"jobs" help:"Max concurrent image builds per DAG level (0=auto: defaults.jobs, else 4)" env:"CHARLY_BUILD_JOBS"`
	PodmanJobs      int      `long:"podman-jobs" help:"Stages per podman build (0=auto: min(NCPU, defaults.podman_jobs_cap))" env:"CHARLY_PODMAN_JOBS"`
	IncludeDisabled bool     `long:"include-disabled" help:"Build boxes with enabled: false in charly.yml (does not modify the file). Use for one-off operational rebuilds without flipping authored config."`
	DevLocalPkg     bool     `long:"dev-local-pkg" help:"Build localpkg candies (the charly toolchain) from LOCAL in-development source instead of downloading the published release. Set automatically for disposable check-bed image builds so a bed tests in-development code; never on a production box build."`
}

// dispatchBuild reaches the hidden core `__box-build` reentry over HostBuild("cli"): BuildCmd's Run
// body (UNCHANGED — the bootstrap-builder subsystem, remote-ref resolve, retention pruning) is
// K1/K3-ENGINE family, pre the loader/build-engine waves that will eventually move it. The
// subprocess inherits charly's stdio (the full build progress output) and exits 0/1.
func dispatchBuild(hc *hostClient, args []string) error {
	var g buildGrammar
	if done, err := parseLeaf("build", &g, args); err != nil || done {
		return err
	}
	argv := append([]string{"__box-build"}, g.Boxes...)
	if g.Push {
		argv = append(argv, "--push")
	}
	if g.Tag != "" {
		argv = append(argv, "--tag", g.Tag)
	}
	if g.Platform != "" {
		argv = append(argv, "--platform", g.Platform)
	}
	if g.Cache != "" {
		argv = append(argv, "--cache", g.Cache)
	}
	if g.NoCache {
		argv = append(argv, "--no-cache")
	}
	if g.Jobs != 0 {
		argv = append(argv, "--jobs", strconv.Itoa(g.Jobs))
	}
	if g.PodmanJobs != 0 {
		argv = append(argv, "--podman-jobs", strconv.Itoa(g.PodmanJobs))
	}
	if g.IncludeDisabled {
		argv = append(argv, "--include-disabled")
	}
	if g.DevLocalPkg {
		argv = append(argv, "--dev-local-pkg")
	}
	r, err := hc.cli(false, true, argv...)
	if err != nil {
		return err
	}
	if r.ExitCode != 0 {
		return fmt.Errorf("box build failed (exit %d)", r.ExitCode)
	}
	return nil
}

// --- box labels ---

// labelsGrammar is the `charly box labels <image> [--format] [--all]` CLI surface.
type labelsGrammar struct {
	Image  string `arg:"" help:"Image reference (full ref or short name resolved against local container storage; never reads charly.yml)"`
	Format string `name:"format" help:"Print only this label's raw value — a full key, or the ai.opencharly.<key> shorthand (e.g. 'init'); exits non-zero when the label is absent"`
	All    bool   `name:"all" help:"Print every label, not just the ai.opencharly.* contract"`
}

// dispatchLabels prints a built image's OCI labels straight from local container storage — pure
// container-storage probes (kit.ResolveRuntime/ResolveLocalImageRef/InspectImageLabels), zero
// loader coupling, zero host reentry (K3 reentry-class dissolution — this word no longer needs
// hc at all, matching the `new` group's zero-reentry pattern).
func dispatchLabels(args []string) error {
	var g labelsGrammar
	if done, err := parseLeaf("labels", &g, args); err != nil || done {
		return err
	}
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return err
	}
	imageRef, err := kit.ResolveLocalImageRef(rt.RunEngine, g.Image)
	if err != nil {
		return err
	}
	labels, err := kit.InspectImageLabels(rt.RunEngine, imageRef)
	if err != nil {
		if !kit.LocalImageExists(rt.RunEngine, imageRef) {
			return fmt.Errorf("%w: %s", kit.ErrImageNotLocal, imageRef)
		}
		return err
	}
	if g.Format != "" {
		key := canonicalLabelKey(g.Format)
		v, ok := labels[key]
		if !ok {
			return fmt.Errorf("label %q not present on %s — an empty or missing capability label is a failure (CLAUDE.md R8)", key, imageRef)
		}
		fmt.Println(v)
		return nil
	}
	keys := sortedLabelKeys(labels, g.All)
	if len(keys) == 0 {
		return fmt.Errorf("no %s labels on %s — not an opencharly image (use --all for every label)", "ai.opencharly.*", imageRef)
	}
	for _, k := range keys {
		fmt.Printf("%s=%s\n", k, labels[k])
	}
	return nil
}

// canonicalLabelKey expands the ai.opencharly.<key> shorthand: a bare token without dots refers
// to the capability-contract namespace. Moved from charly/box_labels_cmd.go (K3 reentry-class
// dissolution).
func canonicalLabelKey(k string) string {
	if strings.Contains(k, ".") {
		return k
	}
	return "ai.opencharly." + k
}

// sortedLabelKeys returns the label keys to print, sorted; without --all only the
// ai.opencharly.* contract participates. Moved from charly/box_labels_cmd.go (K3 reentry-class
// dissolution).
func sortedLabelKeys(labels map[string]string, all bool) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		if !all && !strings.HasPrefix(k, "ai.opencharly.") {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
	return sdk.RunInProcCLI("box new", &g, args)
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
