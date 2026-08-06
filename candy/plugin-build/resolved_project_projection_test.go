package build

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"

	"google.golang.org/grpc"
)

// Relocated from charly/resolved_project_host_test.go's TestResolvedProject_Projection +
// charly/resolved_project_namespace_test.go's TestFillNamespacedBoxes_QualifiedView (#55
// decoupling cone, Batch B, per orchestrator ruling: the test-local seam-reproduction chain these
// exercised mirrors DELETED host code — candy/plugin-build's own resolveBuildEngine now runs the
// SAME loaderkit.ProjectResolvedProject call via the PRODUCTION projectResolvedProjectLeg, so this
// is capability coverage of THIS plugin, not charly's loader). Unlike the charly-side reproduction
// (which faked distroCfg/builderCfg/vocab lookups through charly-core-only functions no plugin can
// import — resolveVocabOpts/hostBuildNamespaced/EnsureRepoDownloaded/loadProjectForResolve — these
// tests call projectResolvedProjectLeg DIRECTLY with literal fixture inputs over the SAME
// fakeExecutorServiceClient pattern candy/plugin-deploy-vm/lifecycle_test.go already uses.
//
// charly/resolved_project_namespace_test.go's OTHER two tests (TestProjectTemplates_NamespaceQualified,
// TestHostBuildDeployEntityResolve_K8sNamespaceQualified) test charly-core's OWN LoadUnified /
// "deploy-entity-resolve" HostBuild seam directly and stayed in charly untouched — they have zero
// sdk import and cannot move (that seam doesn't exist in this plugin).

// fakeNamespaceExecutorServiceClient answers the ONE generic HostBuild leg the namespace walk still
// touches — `loader-threaded`, the kind-recognition snapshot every manifest parse binds (an empty
// snapshot suffices: neither fixture declares a directory-based candy, so parseDoc is never called)
// — and PANICS on any other kind.
//
// The panic is the U3b assertion, not defensive noise: before K-wave 2 cone R1 A2 unit 3b this same
// seam round-tripped to charly's `buildengine-namespaced` leg for a host-side recursion of the
// import-namespace tree. That leg is deleted and the whole walk (scan, reachability, fetch fix-point,
// fold) now runs plugin-side, so re-introducing ANY namespace host round-trip fails this test loudly
// instead of passing on a canned reply. No other RPC is reachable either — neither fixture declares a
// `kind: resource` entity, so ResolveResources' lazy resolveResourceLeg closure is never invoked
// (spec.ResolvePluginKindViaPlugin short-circuits on an empty PluginKinds["resource"] map first).
type fakeNamespaceExecutorServiceClient struct {
	pb.ExecutorServiceClient
}

func (f *fakeNamespaceExecutorServiceClient) HostBuild(ctx context.Context, in *pb.HostBuildRequest, opts ...grpc.CallOption) (*pb.HostBuildReply, error) {
	if in.GetKind() != "loader-threaded" {
		panic("unexpected HostBuild leg in the plugin-side namespace walk: " + in.GetKind())
	}
	replyJSON, err := json.Marshal(spec.Threaded{})
	if err != nil {
		return nil, err
	}
	return &pb.HostBuildReply{ResultJson: replyJSON}, nil
}

// TestResolvedProject_Projection proves the SHARED projection path (projectResolvedProjectLeg →
// loaderkit.ProjectResolvedProject) decodes candy/candy-model/vocab data faithfully — the wire
// contract this plugin's `build:project` word and its ~8 consumers depend on. The full
// load-a-real-project-from-disk round trip (LoadUnified → scan → vocab) is proven live by the R10
// exploratory run against a real project (box inspect / status / check-project / fleet resolve);
// this test's job is the SHARED projection logic itself, fed literal fixture inputs.
func TestResolvedProject_Projection(t *testing.T) {
	ex := sdk.NewInProcExecutor(&fakeNamespaceExecutorServiceClient{})
	uf := &spec.UnifiedFile{} // no namespaces — the FillNamespacedBoxes seam's walk is a no-op
	layers := map[string]spec.CandyReader{
		"rp-fixture": candyReaderFixture("rp-fixture",
			spec.CandyModel{
				Version: "2026.179.0000",
				Plan:    []spec.Step{{Run: "install", Op: spec.Op{Command: "true"}}},
			},
			spec.CandyView{
				Version:     "2026.179.0000",
				Description: "a fixture candy proving the resolved-project seam round-trips",
			},
		),
	}
	distroCfg := &spec.DistroConfig{Distro: map[string]*spec.ResolvedDistro{"fedora": {}}}
	builderCfg := &spec.BuilderConfig{Builder: map[string]*spec.Builder{"pixi-builder": {}}}

	rp, err := projectResolvedProjectLeg(context.Background(), ex, &spec.Config{}, layers, uf,
		distroCfg, builderCfg, &buildkit.InitConfig{}, t.TempDir(), "2026.100.0000", "2026.100.0000",
		false, nil, nil)
	if err != nil {
		t.Fatalf("projectResolvedProjectLeg: %v", err)
	}

	cv, ok := rp.Candies["rp-fixture"]
	if !ok {
		t.Fatalf("rp-fixture candy missing from the projected ResolvedProject: %+v", rp.Candies)
	}
	if cv.Version != "2026.179.0000" || cv.Description != "a fixture candy proving the resolved-project seam round-trips" {
		t.Fatalf("candy view decoded wrong over the seam: %+v", cv)
	}

	// Collection A growth (would be ABSENT pre-#54): the candy BUILD model is projected — the
	// check-projection / validate / K3-D enabler. The fixture candy declares one plan step.
	cm, ok := rp.CandyModels["rp-fixture"]
	if !ok {
		t.Fatalf("rp-fixture candy MODEL missing from CandyModels: %+v", rp.CandyModels)
	}
	if len(cm.Plan) == 0 {
		t.Fatalf("candy model Plan not projected over the seam (the check-include/validate enabler): %+v", cm)
	}
	// build VOCABULARY (the validate ENGINE consumer) is projected from distroCfg/builderCfg.
	if len(rp.Distro) == 0 {
		t.Fatalf("build-vocab Distro not projected into the envelope (validate needs it)")
	}
	if len(rp.Builder) == 0 {
		t.Fatalf("build-vocab Builder not projected into the envelope (validate needs it)")
	}
}

// TestFillNamespacedBoxes_QualifiedView proves the resolved-project envelope's rp.Boxes carries a
// namespace-qualified spec.ResolvedBoxView ("fedora.jupyter") for a box reachable only through an
// import namespace, in addition to (additive, never replacing) the root-scoped boxes. Drives the
// namespace walk (fillNamespacedBoxes, plugin-side production code) off the ROOT uf's own
// Namespaces map — the plugin-side successor to BOTH the deleted host namespaced-box fill and the
// deleted `buildengine-namespaced` leg it later became.
func TestFillNamespacedBoxes_QualifiedView(t *testing.T) {
	subUF := &spec.UnifiedFile{
		Box: boxMapOfFixture(map[string]spec.BoxConfig{
			"jupyter": {Base: "quay.io/fedora/fedora:43", Build: []string{"rpm"}, Distro: []string{"fedora"}, Candy: []string{}},
		}),
	}
	rootUF := &spec.UnifiedFile{Namespaces: map[string]*spec.UnifiedFile{"fedora": subUF}}
	ex := sdk.NewInProcExecutor(&fakeNamespaceExecutorServiceClient{})

	distroCfg := &spec.DistroConfig{}
	builderCfg := &spec.BuilderConfig{}
	rp, err := projectResolvedProjectLeg(context.Background(), ex, &spec.Config{}, nil, rootUF,
		distroCfg, builderCfg, &buildkit.InitConfig{}, t.TempDir(), "2026.100.0000", "2026.100.0000",
		false, nil, nil)
	if err != nil {
		t.Fatalf("projectResolvedProjectLeg: %v", err)
	}

	view, ok := rp.Boxes["fedora.jupyter"]
	if !ok {
		keys := make([]string, 0, len(rp.Boxes))
		for k := range rp.Boxes {
			keys = append(keys, k)
		}
		t.Fatalf("fedora.jupyter missing from the namespace-flattened rp.Boxes: keys=%v", keys)
	}
	if view.Base != "quay.io/fedora/fedora:43" {
		t.Errorf("fedora.jupyter Base = %q, want quay.io/fedora/fedora:43", view.Base)
	}
	if _, ok := rp.Boxes["jupyter"]; ok {
		t.Error("jupyter should NOT be visible at root scope (it's namespaced under 'fedora')")
	}
}
