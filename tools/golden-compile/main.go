// Command golden-compile regenerates the golden fixture
// charly/testdata/fleet_compile_parity_golden.json that
// charly/fleet_compile_parity_test.go's TestFleetCompileParity_PluginRoundTrip compares its
// live plugin-compiled output against.
//
// WHY THIS TOOL EXISTS (#55 K3 cone 1, the golden-fixture redesign): the parity test's OLD side
// used to call deploykit.BuildDeployPlan directly, in-process, which meant charly/
// fleet_compile_parity_test.go imported github.com/opencharly/sdk/deploykit — a violation of
// charly-core's import-purity target (charly imports ONLY spec + the proto/plugin-api wire
// contract, never an sdk mechanism kit). This tool computes that SAME OLD-side ground truth
// OFFLINE, standalone (a separate module — mirrors the tools/gomod-canonical precedent: its own
// go.mod, NOT part of the repo-root go.work), and writes it to a checked-in golden file the
// charly-side test loads via plain encoding/json — no sdk import needed there anymore.
//
// DETERMINISM: BuildDeployPlan is a pure function of (candy, ResolvedBox, HostContext) — it never
// dials a plugin itself (see candy/plugin-fleet/install_build_test.go's
// TestBuildDeployPlan_BuilderPurity_NoPluginRPC). The two pieces of "external" data it needs are
// themselves pure, deterministic library functions, not live plugin RPCs:
//   - the fedora/rpm distro vocabulary: candy/plugin-distro's OpResolve is a documented PURE
//     field-copy (spec.Distro -> spec.ResolvedDistro, zero computation — see
//     candy/plugin-distro/resolve.go) of the checked-in charly/charly.yml embedded vocabulary, so
//     this tool replicates that exact field-copy locally (resolveDistro below) instead of
//     connecting the plugin, and loads the SAME charly/charly.yml the real project envelope reads.
//   - the pixi builder's deploy-time context/reverse ops: candy/plugin-builder-pixi's
//     OpCollectContext/OpReverse are thin dispatches to sdk/kit.BuilderCollectContext /
//     sdk/kit.BuilderReverse (see candy/plugin-builder-pixi/plugin.go) — PUBLIC, pure functions
//     this tool calls directly, no plugin connection needed.
//
// REGENERATE with: go run ./tools/golden-compile   (run from the repo root, or pass -repo)
// whenever the compiler (sdk/deploykit's BuildDeployPlan or its sub-compilers) or one of the three
// fixture candies (candy/debootstrap-builder, candy/dev-tools, candy/pre-commit) changes in a way that alters
// its compiled InstallPlan.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"

	"google.golang.org/grpc"
)

const goldenOutputRelPath = "charly/testdata/fleet_compile_parity_golden.json"

// fixtureCandidates mirrors the parity test's own candidate list (K4B RDD spike): a pure-package
// candy (debootstrap-builder), a package+task candy (dev-tools), and a pixi-builder candy (pre-commit) — the
// same ≥3-candies/≥2-step-classes non-vacuity requirement the test itself enforces.
var fixtureCandidates = []string{"debootstrap-builder", "dev-tools", "pre-commit"}

func main() {
	repoFlag := flag.String("repo", "", "path to the opencharly repo root (default: two directories up from this tool)")
	flag.Parse()

	repoRoot := *repoFlag
	if repoRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			fatal("getwd: %v", err)
		}
		repoRoot = wd
	}
	repoRoot, err := resolveRepoRoot(repoRoot)
	if err != nil {
		fatal("%v", err)
	}

	golden, err := computeGolden(repoRoot)
	if err != nil {
		fatal("%v", err)
	}

	out, err := json.MarshalIndent(golden, "", "  ")
	if err != nil {
		fatal("marshal golden: %v", err)
	}
	// The compiled plans carry repo-root-absolute paths (candy_dir/ctx_path). Baked literally
	// they would pin the golden to the WORKTREE that generated it and fail everywhere else, so
	// normalize them to a ${REPO_ROOT} token; the parity test substitutes its own resolved root
	// on load (the paired replace in loadCompileParityGolden).
	out = bytes.ReplaceAll(out, []byte(repoRoot), []byte("${REPO_ROOT}"))
	out = append(out, '\n')

	outPath := filepath.Join(repoRoot, goldenOutputRelPath)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fatal("mkdir %s: %v", filepath.Dir(outPath), err)
	}
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		fatal("write %s: %v", outPath, err)
	}
	fmt.Printf("wrote %s (%d candies)\n", outPath, len(golden))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "golden-compile: "+format+"\n", args...)
	os.Exit(1)
}

// resolveRepoRoot walks up from dir looking for the marker `candy/` directory (the repo root owns
// it; this tool's own directory tree does not) — the same disambiguator
// charly/fleet_compile_parity_test.go's compilerTestProjectDir uses.
func resolveRepoRoot(dir string) (string, error) {
	d := dir
	for range 6 {
		if info, err := os.Stat(filepath.Join(d, "candy")); err == nil && info.IsDir() {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return "", fmt.Errorf("repo root (a directory containing candy/) not found walking up from %s", dir)
}

// computeGolden builds the fedora/rpm parity image + resolves the fixture candies, computes each
// one's InstallPlan via deploykit.BuildDeployPlan, and projects each to its wire view.
func computeGolden(repoRoot string) (map[string]spec.InstallPlanView, error) {
	img, err := buildParityImage(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("build parity image: %w", err)
	}

	golden := make(map[string]spec.InstallPlanView, len(fixtureCandidates))
	for _, name := range fixtureCandidates {
		layer, err := loadRealCandy(repoRoot, name)
		if err != nil {
			return nil, fmt.Errorf("load candy %q: %w", name, err)
		}

		hostCtx := spec.HostContext{}
		needed := deploykit.DetectExternalizedBuilders([]string{name}, map[string]spec.CandyReader{name: layer}, spec.ExternalizedBuilders, img)
		if len(needed) > 0 {
			builderCtx, err := collectBuilderContext(layer, img, needed)
			if err != nil {
				return nil, fmt.Errorf("candy %q: %w", name, err)
			}
			hostCtx.BuilderContext = builderCtx
		}

		ctx, ex := stubExecutor()
		plan, err := deploykit.BuildDeployPlan(ctx, ex, layer, img, hostCtx)
		if err != nil {
			return nil, fmt.Errorf("BuildDeployPlan(%s): %w", name, err)
		}
		golden[name] = spec.WireView(plan)
	}
	return golden, nil
}

// collectBuilderContext computes the pre-resolved BuilderPreresolved map for every externalized
// builder `needed` triggers on this one candy — via the SAME PURE sdk/kit functions the real
// builder plugins dispatch to (BuilderCollectContext / BuilderReverse), never a live plugin RPC.
func collectBuilderContext(layer spec.CandyReader, img *buildkit.ResolvedBox, needed []string) (map[string]deploykit.BuilderPreresolved, error) {
	out := make(map[string]deploykit.BuilderPreresolved, len(needed))
	for _, word := range needed {
		var bDef *buildkit.BuilderDef
		if img.BuilderConfig != nil {
			bDef = img.BuilderConfig.Builder[word]
		}
		in := spec.BuilderCollectInput{Candy: layer.GetName(), Builder: word, Home: img.Home}
		if bDef != nil && bDef.DetectConfig != "" {
			if sec := layer.FormatSection(bDef.DetectConfig); sec != nil {
				in.Packages = append([]string(nil), sec.Packages...)
				if raw, ok := sec.Raw["replaces"]; ok {
					if list, ok := deploykit.StringSliceFromYAML(raw); ok {
						in.Replaces = list
					}
				}
			}
		}
		collectCtx := kit.BuilderCollectContext(word, in)
		reverse := kit.BuilderReverse(word, spec.BuilderReverseInput{Candy: layer.GetName(), Builder: word, Context: collectCtx})
		out[deploykit.BuilderCtxKey(layer.GetName(), word)] = deploykit.BuilderPreresolved{Context: collectCtx, Reverse: reverse}
	}
	return out, nil
}

// buildParityImage constructs the SAME hand-built fedora ResolvedBox
// charly/fleet_compile_parity_test.go used to build in-process (a real builder config + fedora
// distro so the pixi builder step resolves) — but with its DistroDef read from the REAL checked-in
// charly/charly.yml embedded vocabulary (via resolveDistro's pure field-copy replica below) rather
// than a synthetic literal, so its cache-mount/format data is byte-identical to what the live
// resolved-project envelope (candy/plugin-build's resolveProjectEnvelope) produces for the NEW
// side — otherwise the golden and the live comparison would diverge on data neither side actually
// computes wrong.
func buildParityImage(repoRoot string) (*buildkit.ResolvedBox, error) {
	distroCfg, builderCfg, err := loadEmbeddedBuildVocabulary(repoRoot)
	if err != nil {
		return nil, err
	}
	if distroCfg == nil {
		return nil, fmt.Errorf("no distro vocabulary found in charly/charly.yml")
	}

	img := &buildkit.ResolvedBox{
		ResolvedBox: spec.ResolvedBox{
			Name: "k4b-parity", EffectiveVersion: "2026.001.0001", Base: "quay.io/fedora/fedora:43",
			IsExternalBase: true, UID: 1000, GID: 1000, User: "user", Home: "/home/user",
			UserAdopted: true, Distro: []string{"fedora:43", "fedora"}, BuildFormats: []string{"rpm"}, Pkg: "rpm",
		},
		DistroConfig:  distroCfg,
		BuilderConfig: builderCfg,
	}
	img.DistroDef = distroCfg.ResolveDistro(img.Distro)
	if img.DistroDef == nil {
		return nil, fmt.Errorf("ResolveDistro(%v) returned nil — is charly/charly.yml's fedora distro section present?", img.Distro)
	}
	return img, nil
}

// loadEmbeddedBuildVocabulary reads charly/charly.yml (the tracked file //go:embed'd as charly's
// binary-embedded default build vocabulary) DIRECTLY as plain YAML — bypassing
// loaderkit.LoadUnified entirely. LoadUnified's WalkProject/MaterializeLoadedProject seams reach a
// registered spec.ProjectWalker/spec.Materializer — a live provider-registry dependency this
// standalone tool has no way to wire (no charly-core process, no plugin connections). That
// registry coupling is unnecessary here anyway: charly/charly.yml carries no `import:`
// (grep-confirmed) — it is a single flat file, name-first node-form, one top-level key per
// distro/builder entry (`fedora: {distro: {...}}`, `pixi: {builder: {...}}`) — so a plain
// map-of-yaml.Node decode + a per-key Distro/Builder unmarshal is byte-equivalent to what
// LoadUnified would produce for this ONE file, without needing the walk/materialize machinery a
// project with imports/discovery would require.
//
// The distro entries decode via the SAME field set candy/plugin-distro's OpResolve copies
// (resolve.go documents it as a PURE field-copy, zero computation) — reproduced inline below
// rather than connecting the plugin, since spec.Distro and spec.ResolvedDistro share an identical
// field set for this purpose. The builder entries need no such copy at all: production's own
// ProjectBuilderConfig decodes them PURELY (DecodePluginKindMap, no plugin dispatch), so a direct
// spec.Builder unmarshal here is exactly that same pure decode.
func loadEmbeddedBuildVocabulary(repoRoot string) (*spec.DistroConfig, *spec.BuilderConfig, error) {
	path := filepath.Join(repoRoot, "charly", "charly.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, err)
	}

	// A top-level entry is a plain map[string]yaml.Node first — charly.yml carries plenty of
	// non-entity top-level keys (version, providers, compiled_plugins, install_hints, the various
	// device/OVMF data tables, …) that are scalars or sequences, not {distro:}/{builder:} mapping
	// nodes; only entries whose VALUE is itself a mapping node get a distro/builder decode attempt.
	var rootNodes map[string]yaml.Node
	if err := yaml.Unmarshal(data, &rootNodes); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}

	distros := map[string]*spec.ResolvedDistro{}
	builders := map[string]*buildkit.BuilderDef{}
	for name, node := range rootNodes {
		if node.Kind != yaml.MappingNode {
			continue
		}
		var entry struct {
			Distro  *spec.Distro  `yaml:"distro"`
			Builder *spec.Builder `yaml:"builder"`
		}
		if err := node.Decode(&entry); err != nil {
			continue // not a distro/builder-shaped entity (e.g. `pacstrap:`/`debootstrap:`/format tables)
		}
		if entry.Distro != nil {
			d := entry.Distro
			distros[name] = &spec.ResolvedDistro{
				Inherits:        d.Inherits,
				InheritPackages: d.InheritPackages,
				Version:         d.Version,
				Bootstrap:       d.Bootstrap,
				Workarounds:     d.Workarounds,
				Format:          d.Format,
				BaseUser:        d.BaseUser,
				Pacstrap:        d.Pacstrap,
				Debootstrap:     d.Debootstrap,
				AlpineBootstrap: d.AlpineBootstrap,
				Bootloader:      d.Bootloader,
				Dnf:             d.Dnf,
			}
		}
		if entry.Builder != nil {
			builders[name] = entry.Builder
		}
	}

	var distroCfg *spec.DistroConfig
	if len(distros) > 0 {
		distroCfg = &spec.DistroConfig{Distro: distros}
	}
	var builderCfg *spec.BuilderConfig
	if len(builders) > 0 {
		builderCfg = &spec.BuilderConfig{Builder: builders}
	}
	return distroCfg, builderCfg, nil
}

// loadRealCandy reads a real candy/<name>/charly.yml directly (pure loaderkit.ScanInlineCandy — no
// project load, no registry, no CUE re-validation: the checked-in candy is already known-valid).
// Ported from candy/plugin-fleet/fleet_test_helpers_test.go's loadRealCandy (itself ported from
// the deleted charly/install_build_test.go's loadCompilerFixtures) — the same standalone-package
// fixture-loading pattern, reused here verbatim since this tool has the identical constraint (no
// charly-core project loader available).
func loadRealCandy(repoRoot, name string) (spec.CandyReader, error) {
	dir := filepath.Join(repoRoot, "candy", name)
	yamlPath := filepath.Join(dir, "charly.yml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", yamlPath, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", yamlPath, err)
	}
	normalizePackageShorthand(&doc)
	desugarCommandSugar(&doc)
	var root map[string]struct {
		Candy spec.CandyYAML `yaml:"candy"`
	}
	if err := doc.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode %s: %w", yamlPath, err)
	}
	entry, ok := root[name]
	if !ok {
		return nil, fmt.Errorf("%s: no top-level %q entry", yamlPath, name)
	}
	ly := entry.Candy
	m, v, _ := loaderkit.ScanInlineCandy(name, dir, &ly)
	return deploykit.NewSpecCandyModel(m, v), nil
}

// normalizePackageShorthand / desugarCommandSugar / isRunStepWithCommandSugar /
// desugarOneCommandStep are ported verbatim from candy/plugin-fleet/fleet_test_helpers_test.go
// (the same fixture-loading constraint: no CUE validation pipeline available standalone).

func normalizePackageShorthand(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			normalizePackageShorthand(c)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Value == "package" && val.Kind == yaml.SequenceNode {
				for j, item := range val.Content {
					if item.Kind == yaml.ScalarNode {
						val.Content[j] = &yaml.Node{
							Kind: yaml.MappingNode,
							Content: []*yaml.Node{
								{Kind: yaml.ScalarNode, Value: "name", Tag: "!!str"},
								{Kind: yaml.ScalarNode, Value: item.Value, Tag: item.Tag},
							},
						}
					}
				}
				continue
			}
			normalizePackageShorthand(val)
		}
	}
}

func desugarCommandSugar(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			desugarCommandSugar(c)
		}
	case yaml.MappingNode:
		if isRunStepWithCommandSugar(n) {
			desugarOneCommandStep(n)
			return
		}
		for i := 1; i < len(n.Content); i += 2 {
			desugarCommandSugar(n.Content[i])
		}
	}
}

func isRunStepWithCommandSugar(step *yaml.Node) bool {
	hasRun, hasCommand, hasPlugin := false, false, false
	for i := 0; i+1 < len(step.Content); i += 2 {
		switch step.Content[i].Value {
		case "run":
			hasRun = true
		case "command":
			hasCommand = true
		case "plugin", "plugin_input":
			hasPlugin = true
		}
	}
	return hasRun && hasCommand && !hasPlugin
}

func desugarOneCommandStep(step *yaml.Node) {
	for i := 0; i+1 < len(step.Content); i += 2 {
		if step.Content[i].Value == "command" {
			cmdVal := step.Content[i+1]
			step.Content[i] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "plugin"}
			step.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "command"}
			step.Content = append(step.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "plugin_input"},
				&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
					{Kind: yaml.ScalarNode, Tag: "!!str", Value: "command"},
					cmdVal,
				}},
			)
			return
		}
	}
}

// spec.OpInContext is a package-level DI hook deploykit.CompileOpSteps calls; this standalone
// binary links no charly core (which normally wires it at init), so it's wired here. Pure —
// spec.VerbCatalog is static data + the op's own declared Context, no registry consult. Ported
// verbatim from charly/planrun_adapter.go's opInContext/opEffectiveContexts (the same port
// candy/plugin-fleet's own test binary carries — each standalone binary needs its own copy).
func init() {
	spec.OpInContext = opInContext
}

func opEffectiveContexts(c *spec.Op) []spec.ExecContext {
	if len(c.Context) > 0 {
		out := make([]spec.ExecContext, 0, len(c.Context))
		for _, s := range c.Context {
			out = append(out, spec.ExecContext(s))
		}
		return out
	}
	if verb, err := c.Kind(); err == nil {
		if vs, ok := spec.VerbCatalog[verb]; ok {
			return vs.Contexts
		}
	}
	return nil
}

func opInContext(c *spec.Op, ctx spec.ExecContext) bool {
	for _, e := range opEffectiveContexts(c) {
		if e == ctx {
			return true
		}
	}
	return false
}

// stubExecutorClient is the minimal pb.ExecutorServiceClient double this tool needs: every fixture
// candy's `run:` steps are either non-plugin install verbs (no wire call at all) or the `plugin:
// command` sugar (dev-tools' cmd: task), whose ONLY host-reaching leg is the "construct-step"
// HostBuild seam — answering an empty reply (no special typed step) falls the compiler back to its
// pure buildGenericOpStep path, the correct answer here (no builtin TypedStepProvider is
// reachable standalone anyway). Ported from candy/plugin-fleet/fleet_test_helpers_test.go's
// nopSeamExecutorClient (same constraint, same fixed answer).
type stubExecutorClient struct{ pb.ExecutorServiceClient }

func (stubExecutorClient) HostBuild(_ context.Context, in *pb.HostBuildRequest, _ ...grpc.CallOption) (*pb.HostBuildReply, error) {
	if in.GetKind() == "construct-step" {
		return &pb.HostBuildReply{ResultJson: []byte("{}")}, nil
	}
	return nil, fmt.Errorf("stubExecutorClient: HostBuild(%q) not implemented", in.GetKind())
}

func stubExecutor() (context.Context, *sdk.Executor) {
	return context.Background(), sdk.NewInProcExecutor(stubExecutorClient{})
}
