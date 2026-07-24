package deploypod

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"google.golang.org/grpc"
)

// fakeVolumeExecutorServiceClient is a minimal pb.ExecutorServiceClient test double for
// resolveDeployVolumes — the deploy-volume-persistence regression fix (RCA'd 2026-07-24: a
// project-declared deploy-level `volume:` override, e.g. a disposable check bed's
// `volume: [{name: enc-data, type: encrypted}]`, never reached the per-host overlay charly config
// reads from, because Setup's deployVolumes resolution consulted only CLI flags, the
// CHARLY_VOLUMES_<BOX> env var, and the overlay itself — never the project). It answers HostBuild
// for the two seams the project-declared-volume fallback touches ("pod-config-project-volume" the
// read, "pod-config-save-bundle" the persist) and records every call in order, so a test can
// assert both WHAT was resolved/persisted and WHETHER the project seam was ever consulted (a
// higher-priority source — CLI/env/overlay — must short-circuit BEFORE calling it at all). Every
// other RPC panics if called (mirrors candy/plugin-deploy-vm/lifecycle_test.go's
// fakeExecutorServiceClient — the established plugin-side test-double shape, R3).
type fakeVolumeExecutorServiceClient struct {
	pb.ExecutorServiceClient
	calls              []string
	projectVolumeReply *pb.HostBuildReply
	projectVolumeErr   error
	savedConfigJSON    []byte
}

func (f *fakeVolumeExecutorServiceClient) HostBuild(_ context.Context, in *pb.HostBuildRequest, _ ...grpc.CallOption) (*pb.HostBuildReply, error) {
	f.calls = append(f.calls, in.GetKind())
	switch in.GetKind() {
	case podConfigProjectVolumeKind:
		if f.projectVolumeErr != nil {
			return nil, f.projectVolumeErr
		}
		return f.projectVolumeReply, nil
	case podConfigSaveBundleKind:
		var req spec.PodConfigSaveBundleRequest
		if err := json.Unmarshal(in.GetSpecJson(), &req); err != nil {
			return nil, err
		}
		f.savedConfigJSON = req.ConfigJSON
		return &pb.HostBuildReply{}, nil
	}
	panic("fakeVolumeExecutorServiceClient: unexpected HostBuild kind " + in.GetKind())
}

func marshalHostBuildReply(t *testing.T, v any) *pb.HostBuildReply {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture reply: %v", err)
	}
	return &pb.HostBuildReply{ResultJson: b}
}

func containsKind(calls []string, kind string) bool {
	for _, k := range calls {
		if k == kind {
			return true
		}
	}
	return false
}

// TestResolveDeployVolumes_ProjectDeclaredFallback is the regression test: with NO CLI flag, NO
// CHARLY_VOLUMES_<BOX> env var, and NO existing per-host overlay entry, a project-declared
// `volume:` override must (a) be resolved as this run's deployVolumes and (b) be PERSISTED into
// the (previously-nil) overlay via the pod-config-save-bundle seam — exactly as a --volume flag
// would. This FAILS on the pre-fix shape: the old code never called anything past the overlay
// check, so with a project-only fixture (no overlay, no CLI, no env) deployVolumes stayed empty
// and check-enc-pod's encrypted bind mount was silently never established.
func TestResolveDeployVolumes_ProjectDeclaredFallback(t *testing.T) {
	wantVolumes := []spec.DeployVolume{{Name: "enc-data", Type: "encrypted"}}
	volJSON, err := json.Marshal(wantVolumes)
	if err != nil {
		t.Fatalf("marshal fixture volumes: %v", err)
	}
	fake := &fakeVolumeExecutorServiceClient{
		projectVolumeReply: marshalHostBuildReply(t, spec.PodConfigProjectVolumeReply{VolumeJSON: volJSON}),
	}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "check-enc-pod"}
	var dc *deploykit.BundleConfig

	got, err := resolveDeployVolumes(context.Background(), ex, c, &dc)
	if err != nil {
		t.Fatalf("resolveDeployVolumes() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "enc-data" || got[0].Type != "encrypted" {
		t.Fatalf("resolveDeployVolumes() = %+v, want the project-declared volume", got)
	}

	if dc == nil {
		t.Fatal("resolveDeployVolumes() left dc nil — a project-declared hit must seed the overlay (persistDeployVolumes)")
	}
	entry, ok := dc.Bundle[deploykit.DeployKey("check-enc-pod", "")]
	if !ok || len(entry.Volume) != 1 || entry.Volume[0].Name != "enc-data" {
		t.Fatalf("overlay entry.Volume = %+v, want the persisted project-declared volume", entry.Volume)
	}
	if !containsKind(fake.calls, podConfigSaveBundleKind) {
		t.Errorf("resolveDeployVolumes() calls = %v, want a pod-config-save-bundle call — the fallback hit must actually be persisted, not just held in memory", fake.calls)
	}
	if len(fake.savedConfigJSON) == 0 {
		t.Error("pod-config-save-bundle was called with an empty ConfigJSON")
	}
}

// TestResolveDeployVolumes_OverlayWinsOverProject asserts precedence: an existing per-host overlay
// volume entry wins over the project declaration, and the project seam is NEVER consulted (the
// fake has no project-volume reply configured — a call would panic).
func TestResolveDeployVolumes_OverlayWinsOverProject(t *testing.T) {
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "check-enc-pod"}
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		deploykit.DeployKey("check-enc-pod", ""): {Volume: []spec.DeployVolume{{Name: "already-set", Type: "bind"}}},
	}}

	got, err := resolveDeployVolumes(context.Background(), ex, c, &dc)
	if err != nil {
		t.Fatalf("resolveDeployVolumes() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "already-set" {
		t.Fatalf("resolveDeployVolumes() = %+v, want the overlay's existing volume unchanged", got)
	}
	if len(fake.calls) != 0 {
		t.Errorf("resolveDeployVolumes() HostBuild calls = %v, want none — the project fallback must never fire once the overlay already has a volume", fake.calls)
	}
}

// TestResolveDeployVolumes_CLIFlagWinsOverProject asserts precedence: a CLI --volume flag wins
// over the project declaration, and the project seam is NEVER consulted.
func TestResolveDeployVolumes_CLIFlagWinsOverProject(t *testing.T) {
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "check-enc-pod", VolumeFlag: []string{"data:bind:/tmp/x"}}
	var dc *deploykit.BundleConfig

	got, err := resolveDeployVolumes(context.Background(), ex, c, &dc)
	if err != nil {
		t.Fatalf("resolveDeployVolumes() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "data" {
		t.Fatalf("resolveDeployVolumes() = %+v, want the CLI-flag volume", got)
	}
	if len(fake.calls) != 0 {
		t.Errorf("resolveDeployVolumes() HostBuild calls = %v, want none — a CLI flag must short-circuit before the project fallback", fake.calls)
	}
}

// TestResolveDeployVolumes_NoProjectDeclaration covers the common no-op case: the project declares
// no volume for this deploy either, so the result stays empty and nothing is persisted.
func TestResolveDeployVolumes_NoProjectDeclaration(t *testing.T) {
	fake := &fakeVolumeExecutorServiceClient{
		projectVolumeReply: marshalHostBuildReply(t, spec.PodConfigProjectVolumeReply{}),
	}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "no-volumes-here"}
	var dc *deploykit.BundleConfig

	got, err := resolveDeployVolumes(context.Background(), ex, c, &dc)
	if err != nil {
		t.Fatalf("resolveDeployVolumes() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("resolveDeployVolumes() = %+v, want empty when the project declares no volume", got)
	}
	if dc != nil {
		t.Error("resolveDeployVolumes() must not seed the overlay when there is nothing to persist")
	}
	if containsKind(fake.calls, podConfigSaveBundleKind) {
		t.Error("resolveDeployVolumes() called pod-config-save-bundle with nothing to persist")
	}
}

// TestResolveDeployVolumes_ProjectHostBuildErrorPropagates covers the failure path: a HostBuild
// transport error (e.g. no reverse channel) on the project-volume read must surface as an error,
// never a silent empty result.
func TestResolveDeployVolumes_ProjectHostBuildErrorPropagates(t *testing.T) {
	fake := &fakeVolumeExecutorServiceClient{projectVolumeErr: errors.New("no host reverse channel")}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "check-enc-pod"}
	var dc *deploykit.BundleConfig

	if _, err := resolveDeployVolumes(context.Background(), ex, c, &dc); err == nil {
		t.Fatal("resolveDeployVolumes() with a HostBuild transport error on the project-volume seam: want an error, got nil")
	}
}
