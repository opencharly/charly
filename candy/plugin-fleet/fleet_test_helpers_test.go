package fleet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/grpc"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// spec.OpInContext is a package-level DI hook deploykit.CompileOpSteps calls: in production it is
// wired by charly core's own init() (charly/layers.go: spec.OpInContext = opInContext) — safe there
// because a COMPILED-IN plugin-fleet always shares that process. This package's OWN test binary
// links no charly core, so the hook is left nil (a nil-func-call panic) unless wired here. The
// implementation itself is pure (spec.VerbCatalog is static data + the op's own declared Context —
// no registry consult), so it is ported verbatim from charly/planrun_adapter.go's opInContext/
// opEffectiveContexts rather than stubbed.
func init() {
	spec.OpInContext = testOpInContext
}

func testOpEffectiveContexts(c *spec.Op) []spec.ExecContext {
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

func testOpInContext(c *spec.Op, ctx spec.ExecContext) bool {
	return slices.Contains(testOpEffectiveContexts(c), ctx)
}

// fleet_test_helpers_test.go — the shared spec.CandyReader test-fixture constructors relocated
// here from charly/candy_test_helpers_test.go (#55 decoupling, Batch A). testCandy/pixiCandy build
// a spec.CandyReader from a literal (spec.CandyModel, spec.CandyView) pair, matching production's
// deploykit.NewSpecCandyModel(m, v) call exactly.

func testCandy(name string, m spec.CandyModel, v spec.CandyView) spec.CandyReader {
	m.Name = name
	v.Name = name
	return deploykit.NewSpecCandyModel(m, v)
}

func pixiCandy(t *testing.T, name string) spec.CandyReader {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pixi.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("write pixi.toml: %v", err)
	}
	return testCandy(name, spec.CandyModel{SourceDir: dir}, spec.CandyView{})
}

// nopSeamExecutorClient is a minimal pb.ExecutorServiceClient stub — NOT the real in-proc
// reverse channel charly's original testConstructStepExecutor built (that one reached the
// compiled-in provider registry via inprocExecutorClient+executorReverseServer, both
// charly-core-internal types this out-of-module plugin package cannot construct). Every test
// moved here that drives a `plugin:` verb step only needs the "construct-step" HostBuild seam
// to answer "no special typed step" (an empty ConstructStepReply — reply.Step == nil), which
// falls constructOpStep back to the pure buildGenericOpStep path (deploykit's own documented
// fallback semantics) — the correct answer for every verb (package/service excepted, and no
// fixture here compiles one via CompileOpSteps) since no builtin TypedStepProvider is reachable
// standalone anyway. Every other method is unreachable by the tests that use this stub and
// errors loudly if ever called.
type nopSeamExecutorClient struct{}

func (nopSeamExecutorClient) Venue(context.Context, *pb.Empty, ...grpc.CallOption) (*pb.VenueReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: Venue not implemented")
}
func (nopSeamExecutorClient) RunSystem(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: RunSystem not implemented")
}
func (nopSeamExecutorClient) RunUser(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: RunUser not implemented")
}
func (nopSeamExecutorClient) PutFile(context.Context, *pb.PutFileRequest, ...grpc.CallOption) (*pb.PutFileReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: PutFile not implemented")
}
func (nopSeamExecutorClient) RunCapture(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.CaptureReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: RunCapture not implemented")
}
func (nopSeamExecutorClient) RunInteractive(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: RunInteractive not implemented")
}
func (nopSeamExecutorClient) RunStream(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: RunStream not implemented")
}
func (nopSeamExecutorClient) GetFile(context.Context, *pb.GetFileRequest, ...grpc.CallOption) (*pb.GetFileReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: GetFile not implemented")
}
func (nopSeamExecutorClient) RunHostStep(context.Context, *pb.HostStepRequest, ...grpc.CallOption) (*pb.HostStepReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: RunHostStep not implemented")
}
func (nopSeamExecutorClient) InvokeProvider(context.Context, *pb.InvokeProviderRequest, ...grpc.CallOption) (*pb.InvokeReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: InvokeProvider not implemented")
}
func (nopSeamExecutorClient) HostBuild(_ context.Context, in *pb.HostBuildRequest, _ ...grpc.CallOption) (*pb.HostBuildReply, error) {
	if in.GetKind() == "construct-step" {
		return &pb.HostBuildReply{ResultJson: []byte("{}")}, nil
	}
	return nil, fmt.Errorf("nopSeamExecutorClient: HostBuild(%q) not implemented", in.GetKind())
}
func (nopSeamExecutorClient) DescribeProvider(context.Context, *pb.DescribeProviderRequest, ...grpc.CallOption) (*pb.DescribeProviderReply, error) {
	return nil, fmt.Errorf("nopSeamExecutorClient: DescribeProvider not implemented")
}

// testConstructStepExecutor returns a background context + an *sdk.Executor wrapping the
// nopSeamExecutorClient stub above.
func testConstructStepExecutor() (context.Context, *sdk.Executor) {
	return context.Background(), sdk.NewInProcExecutor(nopSeamExecutorClient{})
}

// testCompileServiceSteps drives deploykit.CompileServiceSteps with the
// nopSeamExecutorClient-backed executor (needed by any moved test whose fixture carries a
// `plugin:` verb step, e.g. a `command:` sugar task — see testConstructStepExecutor).
// t.Fatalf's on error so callers don't have to.
func testCompileServiceSteps(t *testing.T, layer spec.CandyReader, img *buildkit.ResolvedBox, hostCtx deploykit.HostContext) []spec.InstallStep {
	t.Helper()
	ctx, ex := testConstructStepExecutor()
	steps, err := deploykit.CompileServiceSteps(ctx, ex, layer, img, hostCtx)
	if err != nil {
		t.Fatalf("CompileServiceSteps: %v", err)
	}
	return steps
}

// builderTestImg builds a ResolvedBox carrying the four externalized builders in its
// BuilderConfig (pixi/npm/cargo by detect-file, aur by detect-config) with the given build
// formats. Relocated from charly/builder_preresolve_test.go — shared by the builder-detection
// tests moved here.
func builderTestImg(buildFormats ...string) *buildkit.ResolvedBox {
	return &buildkit.ResolvedBox{ResolvedBox: spec.ResolvedBox{Name: "t", Home: "/home/u", BuildFormats: buildFormats}, BuilderConfig: &spec.BuilderConfig{Builder: map[string]*buildkit.BuilderDef{
		"pixi":  {DetectFiles: []string{"pixi.toml"}},
		"npm":   {DetectFiles: []string{"package.json"}},
		"cargo": {DetectFiles: []string{"Cargo.toml"}},
		"aur":   {DetectConfig: "aur"},
	}}}
}

// aurCandy builds a spec.CandyReader fixture carrying an "aur" FormatSection with the given
// packages — the DetectConfig-builder trigger CandyNeedsBuilderStep reads via FormatSection.
func aurCandy(name string, pkgs ...string) spec.CandyReader {
	return testCandy(name,
		spec.CandyModel{FormatSections: map[string]spec.PackageSection{"aur": {FormatName: "aur", Packages: pkgs}}},
		spec.CandyView{},
	)
}

// loadRealCandy reads a REAL candy directory's own charly.yml (this repo's actual candy/<name>,
// reached relative to this test file's package dir) and builds a spec.CandyReader from it via the
// pure loaderkit.ScanInlineCandy — no project load, no registry, no CUE re-validation (the
// checked-in candy is already known-valid). Ported from charly/install_build_test.go's
// loadCompilerFixtures, which drove the full LoadConfig/ScanAllCandyWithConfig/resolveBoxTest
// project-loader stack — unavailable to an out-of-module plugin package. Skips (not fails) when
// the candy directory or its top-level entry is missing, matching the original's graceful-skip
// intent for an environment where the fixture candy isn't present.
func loadRealCandy(t *testing.T, name string) spec.CandyReader {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "candy", name))
	if err != nil {
		t.Fatalf("abs candy dir: %v", err)
	}
	yamlPath := filepath.Join(dir, "charly.yml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Skipf("%s: %v (fixture candy missing?)", yamlPath, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", yamlPath, err)
	}
	normalizePackageShorthand(&doc)
	desugarCommandSugar(&doc)
	var root map[string]struct {
		Candy spec.CandyYAML `yaml:"candy"`
	}
	if err := doc.Decode(&root); err != nil {
		t.Fatalf("decode %s: %v", yamlPath, err)
	}
	entry, ok := root[name]
	if !ok {
		t.Skipf("%s: no top-level %q entry", yamlPath, name)
	}
	ly := entry.Candy
	m, v, _ := loaderkit.ScanInlineCandy(name, dir, &ly)
	return deploykit.NewSpecCandyModel(m, v)
}

// normalizePackageShorthand rewrites every "package: [...]" sequence's bare-scalar entries into
// {name: <scalar>} mapping nodes — the bare-scalar-XOR-object canonicalization CUE's
// #PackageItem disjunction performs at real load time (spec.PackageItem's own doc comment: "the
// bare-scalar shorthand is canonicalized to {name} by the loader normalizer"). loadRealCandy
// cannot invoke the full CUE validation pipeline standalone (this package imports no CUE
// machinery), so this narrowly replicates just that one canonicalization on the raw yaml.Node
// tree before decoding into spec.CandyYAML.
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

// desugarCommandSugar performs the ONE plan-step plugin-verb sugar desugar loadRealCandy's real
// candy fixtures (dev-tools, pre-commit) need: a `run:` plan step carrying a `command: <script>`
// sugar key — the only plugin-verb sugar used by any `run:` step in the fixtures this package
// loads — is rewritten to the internal `plugin: command, plugin_input: {command: <script>}` pair,
// the transform sdk/loaderkit's (unexported) desugarStep performs at real parse time
// (charly.yml's `command:` sugar authors an install/deploy task; `plugin_input` is the CUE-hidden
// internal shape `deploykit.CompileOpSteps` actually dispatches on). A narrowly scoped
// re-implementation, not the general N-verb desugar — no other plugin-verb sugar appears on a
// `run:` step in any candy this package's tests load.
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

// fedoraCoderLikeImg is a synthetic stand-in for the real project's "fedora-coder" box
// resolution — rpm format + the four externalized detection builders — used by the real-candy
// fixture tests moved here (they need A resolved fedora-rpm image to compile against, not the
// EXACT fedora-coder inheritance chain, which is charly-project-loader territory this package
// cannot reach standalone).
func fedoraCoderLikeImg() *buildkit.ResolvedBox {
	img := builderTestImg("rpm")
	img.Distro = []string{"fedora:43", "fedora"}
	img.Tags = []string{"fedora:43", "fedora", "all", "rpm"}
	img.Pkg = "rpm"
	img.Home = "/home/user"
	img.DistroDef = &buildkit.DistroDef{Format: map[string]*spec.Format{
		"rpm": {Phases: &spec.PhaseSet{Install: &spec.PhaseTemplates{Container: "dnf install -y {{.Packages}}"}}},
	}}
	return img
}

// boxMapOf folds typed spec.BoxConfig test literals into the generic opaque image map
// (spec.BoxMap) the *spec.Config a capability like deploykit.CollectSecurity/CollectBoxPorts/
// CollectBoxVolume consumes actually stores — the test-construction analog of the loader's
// encodeBox. Relocated from charly/node_loader_test.go (a file outside this decoupling batch);
// several moved files in this package need it, so it lives here once (R3) rather than
// per-file-duplicated.
func boxMapOf(m map[string]spec.BoxConfig) spec.BoxMap {
	out := make(spec.BoxMap, len(m))
	for k, v := range m {
		out[k] = spec.EncodeBox(v)
	}
	return out
}

// cmdOp returns an already-desugared `plugin: command` Op — the fixture shape a `run:` plan
// step's `command:` sugar key desugars to at real parse time (see desugarCommandSugar above for
// the loadRealCandy path; a hand-built spec.Step literal, as most tests in this package use,
// authors the desugared form directly and needs no desugar pass). Relocated from
// charly/check_helpers_test.go (a file outside this decoupling batch); several moved files need
// it (R3).
func cmdOp(command string) spec.Op {
	return spec.Op{Plugin: "command", PluginInput: map[string]any{"command": command}}
}

// testSubstrateTraits mirrors candy/plugin-substrate/plugin.go's substrateTraits DATA table
// (Appendix B) — the compiled-in registry deployTraitsFor resolves in production. That resolve
// (providerRegistry.ResolveKind) is charly-core internal, so tests in this package that build
// spec.FleetNode fixtures directly (bypassing the loader) stamp descents against this literal
// copy of the same canonical values instead.
var testSubstrateTraits = map[string]*spec.DeployTraits{
	"pod":        {Venue: "container", ImageBacked: true, ImageContext: true, BracketedLifecycle: true, BedTarget: true},
	"vm":         {Venue: "ssh", MachineVenue: true, ExclusiveVenue: true, BedTarget: true, SupportsEphemeral: true, SupportsFromSnapshot: true},
	"local":      {Venue: "shell", MachineVenue: true, BedTarget: true},
	"kubernetes": {Venue: "shell", ImageContext: true, LeafOnly: true},
	"android":    {Venue: "parent", BedTarget: true},
}

func testDeployTraitsFor(word string) *spec.DeployTraits { return testSubstrateTraits[word] }

// stampTestDescents mirrors the substrate loader: it stamps the descent descriptor from the
// substrate's DECLARED #DeployTraits, recursing the nested/peer subtree — so chain unit tests,
// which build FleetNode literals directly bypassing the loader, run against
// realistically-stamped nodes instead of tripping the nil-descent guard. Relocated from
// charly/deploy_chain_test.go (#55 decoupling, Batch A), using testDeployTraitsFor above in
// place of charly's registry-backed deployTraitsFor.
func stampTestDescents(roots map[string]spec.FleetNode) map[string]spec.FleetNode {
	out := make(map[string]spec.FleetNode, len(roots))
	for k, v := range roots {
		n := v
		kit.StampDescent(&n, testDeployTraitsFor)
		out[k] = n
	}
	return out
}
