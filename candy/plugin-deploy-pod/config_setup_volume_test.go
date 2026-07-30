package deploypod

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
	"google.golang.org/grpc"
)

// fakeVolumeExecutorServiceClient is a minimal pb.ExecutorServiceClient test double for
// resolveDeployVolumes — the deploy-volume-persistence regression fix (RCA'd 2026-07-24: a
// project-declared deploy-level `volume:` override, e.g. a disposable check bed's
// `volume: [{name: enc-data, type: encrypted}]`, never reached the per-host overlay charly config
// reads from, because Setup's deployVolumes resolution consulted only CLI flags, the
// CHARLY_VOLUMES_<BOX> env var, and the overlay itself — never the project). It answers HostBuild
// for the persist seam ("pod-config-save-bundle") and records every call in order. The project-read
// leg is stubbed via stubProjectVolume (the loadProjectVolume package var) rather than a HostBuild
// case: since the #55 Cone A Unit 3a seam-collapse, loadProjectVolume self-resolves the merged tree
// over the reverse channel (deploy-plugins-connect + loaderkit.ResolveMergedTreeViaExecutor) — a
// multi-leg loader path a single HostBuild-kind stub cannot canned-reply. Every other RPC panics if
// called (mirrors candy/plugin-deploy-vm/lifecycle_test.go's fakeExecutorServiceClient, R3).
type fakeVolumeExecutorServiceClient struct {
	pb.ExecutorServiceClient
	calls           []string
	savedConfigJSON []byte
}

func (f *fakeVolumeExecutorServiceClient) HostBuild(_ context.Context, in *pb.HostBuildRequest, _ ...grpc.CallOption) (*pb.HostBuildReply, error) {
	f.calls = append(f.calls, in.GetKind())
	switch in.GetKind() {
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

// projectVolumeStub replaces the loadProjectVolume package var for a test — the project-consult leg
// of resolveDeployVolumes' precedence chain. `called` records whether the fallback fired at all, so
// a test asserts both WHAT the project declared and WHETHER a higher-priority source (CLI/env/
// overlay) short-circuited BEFORE the project was ever consulted.
type projectVolumeStub struct {
	vols   []spec.DeployVolume
	err    error
	called bool
}

func (s *projectVolumeStub) install(t *testing.T) {
	t.Helper()
	orig := loadProjectVolume
	loadProjectVolume = func(_ context.Context, _ *sdk.Executor, _, _ string) ([]spec.DeployVolume, error) {
		s.called = true
		return s.vols, s.err
	}
	t.Cleanup(func() { loadProjectVolume = orig })
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
	stub := &projectVolumeStub{vols: wantVolumes}
	stub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
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
	if !stub.called {
		t.Error("resolveDeployVolumes() did not consult the project — the project-declared fallback must fire with no CLI/env/overlay source")
	}

	if dc == nil {
		t.Fatal("resolveDeployVolumes() left dc nil — a project-declared hit must seed the overlay (persistDeployVolumes)")
	}
	entry, ok := dc.Bundle[spec.DeployKey("check-enc-pod", "")]
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
// volume entry, ALREADY marked checked, wins over the project declaration, and the project seam
// is NEVER consulted (the fake has no project-volume reply configured — a call would panic).
func TestResolveDeployVolumes_OverlayWinsOverProject(t *testing.T) {
	stub := &projectVolumeStub{}
	stub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "check-enc-pod"}
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		spec.DeployKey("check-enc-pod", ""): {
			Volume:               []spec.DeployVolume{{Name: "already-set", Type: "bind"}},
			VolumeProjectChecked: true,
		},
	}}

	got, err := resolveDeployVolumes(context.Background(), ex, c, &dc)
	if err != nil {
		t.Fatalf("resolveDeployVolumes() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "already-set" {
		t.Fatalf("resolveDeployVolumes() = %+v, want the overlay's existing volume unchanged", got)
	}
	if stub.called {
		t.Error("resolveDeployVolumes() consulted the project — the fallback must never fire once VolumeProjectChecked is set")
	}
	if len(fake.calls) != 0 {
		t.Errorf("resolveDeployVolumes() HostBuild calls = %v, want none", fake.calls)
	}
}

// TestResolveDeployVolumes_AlreadyCheckedVolumeLessSkipsProjectLookup is the round-2 conditional-
// lookup regression test (RCA'd 2026-07-24, val-volfix): an ALREADY-CHECKED deploy — the project
// was consulted once before, VolumeProjectChecked is true, and it declared no volume — must take
// the ORIGINAL zero-lookup path on every subsequent `charly config`/`charly update` re-config: the
// project seam must NEVER fire again, regardless of what else lives on the overlay entry (here,
// a resolved port from an unrelated earlier Setup stage). The fake has no project-volume reply
// configured, so a call would panic.
func TestResolveDeployVolumes_AlreadyCheckedVolumeLessSkipsProjectLookup(t *testing.T) {
	stub := &projectVolumeStub{}
	stub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "preempt-vm-taker", Instance: ""}
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		// Already checked once (the project genuinely declares nothing for this key) AND
		// carries an unrelated ResolvedPort from an earlier Setup stage — VolumeProjectChecked
		// is the ONLY signal that must gate the fallback, not the port field's presence.
		spec.DeployKey("preempt-vm-taker", ""): {ResolvedPort: []string{"8080:8080"}, VolumeProjectChecked: true},
	}}

	got, err := resolveDeployVolumes(context.Background(), ex, c, &dc)
	if err != nil {
		t.Fatalf("resolveDeployVolumes() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("resolveDeployVolumes() = %+v, want empty — this member's overlay declares no volume", got)
	}
	if stub.called {
		t.Error("resolveDeployVolumes() consulted the project — a deploy with VolumeProjectChecked=true must never re-consult it")
	}
	if len(fake.calls) != 0 {
		t.Errorf("resolveDeployVolumes() HostBuild calls = %v, want none", fake.calls)
	}
}

// TestResolveDeployVolumes_PortedDeployProjectVolumeAppliedOnFirstConfig is the round-3 BLOCKING
// regression test (RCA'd 2026-07-24, team-lead): config_setup.go's port-resolution block runs
// BEFORE resolveDeployVolumes and writes dc.Bundle[key] (ResolvedPort) the moment a deploy has
// ANY container port — on the FIRST config of a PORTED deploy this creates an overlay entry with
// VolumeProjectChecked still false (never explicitly set). The round-2 fix's `!ok` discriminator
// treated that entry as "already configured" and skipped the project fallback, silently DROPPING
// a genuine project-declared volume on exactly the scenario #183 exists to fix. This test FAILS
// on that shape (302fcbe/392fcbe's `!ok` gate) and PASSES once the gate is VolumeProjectChecked
// instead: the project fallback must still fire, find the volume, apply it, mark
// VolumeProjectChecked true, and leave the pre-existing ResolvedPort untouched.
func TestResolveDeployVolumes_PortedDeployProjectVolumeAppliedOnFirstConfig(t *testing.T) {
	stub := &projectVolumeStub{vols: []spec.DeployVolume{{Name: "enc-data", Type: "encrypted"}}}
	stub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "check-enc-pod"}
	key := spec.DeployKey("check-enc-pod", "")
	dc := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{
		// Simulates the port-resolution block's write, which runs BEFORE resolveDeployVolumes
		// in the real config_setup.go sequence: the key exists, VolumeProjectChecked is the
		// zero value (never set), Volume is empty.
		key: {ResolvedPort: []string{"8080:8080"}},
	}}

	got, err := resolveDeployVolumes(context.Background(), ex, c, &dc)
	if err != nil {
		t.Fatalf("resolveDeployVolumes() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "enc-data" {
		t.Fatalf("resolveDeployVolumes() = %+v, want the project-declared volume even though the port block already created the overlay key", got)
	}
	if !stub.called {
		t.Error("resolveDeployVolumes() did not consult the project — a ported deploy's first config must still consult it")
	}
	entry, ok := dc.Bundle[key]
	if !ok {
		t.Fatal("overlay entry vanished")
	}
	if !entry.VolumeProjectChecked {
		t.Error("VolumeProjectChecked not set after the project consult")
	}
	if len(entry.ResolvedPort) != 1 || entry.ResolvedPort[0] != "8080:8080" {
		t.Errorf("entry.ResolvedPort = %v, want the pre-existing resolved port left untouched", entry.ResolvedPort)
	}
}

// TestResolveDeployVolumes_CLIFlagWinsOverProject asserts precedence: a CLI --volume flag wins
// over the project declaration, and the project seam is NEVER consulted.
func TestResolveDeployVolumes_CLIFlagWinsOverProject(t *testing.T) {
	stub := &projectVolumeStub{}
	stub.install(t)
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
	if stub.called {
		t.Error("resolveDeployVolumes() consulted the project — a CLI flag must short-circuit before the project fallback")
	}
	if len(fake.calls) != 0 {
		t.Errorf("resolveDeployVolumes() HostBuild calls = %v, want none", fake.calls)
	}
}

// TestResolveDeployVolumes_NoProjectDeclaration covers the common no-op case: the project declares
// no volume for this deploy either. The result stays empty, but — unlike the pre-round-3 shape —
// the checked-marker IS now persisted (VolumeProjectChecked: true, Volume: nil): recording "the
// project was consulted and found nothing" is exactly what lets every SUBSEQUENT call skip the
// project seam (see TestResolveDeployVolumes_AlreadyCheckedVolumeLessSkipsProjectLookup); never
// persisting anything here is what caused the round-2 regression (a project re-query on every
// single re-config of every volume-less deploy).
func TestResolveDeployVolumes_NoProjectDeclaration(t *testing.T) {
	stub := &projectVolumeStub{}
	stub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
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
	if dc == nil {
		t.Fatal("resolveDeployVolumes() left dc nil — the checked-marker must be persisted even when the project declares nothing")
	}
	entry, ok := dc.Bundle[spec.DeployKey("no-volumes-here", "")]
	if !ok || !entry.VolumeProjectChecked {
		t.Fatalf("overlay entry = %+v (ok=%v), want VolumeProjectChecked=true persisted", entry, ok)
	}
	if len(entry.Volume) != 0 {
		t.Errorf("entry.Volume = %+v, want empty", entry.Volume)
	}
	if !containsKind(fake.calls, podConfigSaveBundleKind) {
		t.Error("resolveDeployVolumes() did not call pod-config-save-bundle — the checked-marker was never persisted")
	}
}

// TestResolveDeployVolumes_ProjectHostBuildErrorPropagates covers the failure path: a HostBuild
// transport error (e.g. no reverse channel) on the project-volume read must surface as an error,
// never a silent empty result.
func TestResolveDeployVolumes_ProjectHostBuildErrorPropagates(t *testing.T) {
	stub := &projectVolumeStub{err: errors.New("no host reverse channel")}
	stub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "check-enc-pod"}
	var dc *deploykit.BundleConfig

	if _, err := resolveDeployVolumes(context.Background(), ex, c, &dc); err == nil {
		t.Fatal("resolveDeployVolumes() with a project-resolution transport error: want an error, got nil")
	}
}

// TestConfigFlow_PortResolutionThenResolveDeployVolumes_ProjectVolumeStillApplied is the
// INTEGRATION-level regression test team-lead required (R7: unit tests never substitute for
// runtime verification) — it drives the ACTUAL two-stage sequence config_setup.go's runConfig
// executes for a ported deploy, in the SAME order: the port-resolution block (config_setup.go,
// via kit.ResolveDeployPorts) runs FIRST against a fresh dc, exactly as it does for any deploy
// declaring container ports, THEN resolveDeployVolumes runs against the SAME dc pointer. Every
// existing TestResolveDeployVolumes_* test calls resolveDeployVolumes in ISOLATION and so could
// never observe this ordering hazard (the round-3 bug team-lead found): the port block silently
// creates the overlay key before the volume fallback ever inspects it. This test FAILS against
// the round-2 `!ok`-gated shape (392fcbe) and PASSES against the VolumeProjectChecked-gated fix.
func TestConfigFlow_PortResolutionThenResolveDeployVolumes_ProjectVolumeStillApplied(t *testing.T) {
	stub := &projectVolumeStub{vols: []spec.DeployVolume{{Name: "enc-data", Type: "encrypted"}}}
	stub.install(t)
	fake := &fakeVolumeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)
	c := &spec.PodConfigSetupRequest{Box: "check-enc-pod"}
	key := spec.DeployKey(c.Box, c.Instance)
	var dc *deploykit.BundleConfig

	// --- Stage 1: replicate config_setup.go's port-resolution block (runConfig, the lines
	// immediately preceding the resolveDeployVolumes call) VERBATIM, against a brand-new
	// (nil) dc — the exact state a deploy's FIRST-EVER `charly config` starts from. ---
	containerPorts := kit.ContainerPortsFromMappings([]string{"8080:8080"})
	if dc == nil {
		dc = &deploykit.BundleConfig{Bundle: make(map[string]spec.BundleNode)}
	}
	overlay := dc.Bundle[key]
	if len(containerPorts) > 0 || len(overlay.Port) > 0 {
		resolved, rErr := kit.ResolveDeployPorts(containerPorts, overlay.Port, overlay.ResolvedPort, dc.OccupiedHostPorts(key))
		if rErr != nil {
			t.Fatalf("kit.ResolveDeployPorts() error = %v", rErr)
		}
		if !kit.SameStringSlice(overlay.ResolvedPort, resolved) {
			overlay.ResolvedPort = resolved
			dc.Bundle[key] = overlay
		}
	}
	// Confirm stage 1 alone already reproduces the hazard's precondition: the overlay key now
	// exists, with an empty Volume and VolumeProjectChecked unset.
	preVolumeEntry, ok := dc.Bundle[key]
	if !ok {
		t.Fatal("port-resolution block did not create the overlay entry — test setup is wrong")
	}
	if len(preVolumeEntry.ResolvedPort) == 0 {
		t.Fatal("port-resolution block did not resolve a port — test setup is wrong")
	}
	if preVolumeEntry.VolumeProjectChecked {
		t.Fatal("VolumeProjectChecked already true before resolveDeployVolumes ran — test setup is wrong")
	}

	// --- Stage 2: resolveDeployVolumes runs exactly as runConfig calls it next. ---
	got, err := resolveDeployVolumes(context.Background(), ex, c, &dc)
	if err != nil {
		t.Fatalf("resolveDeployVolumes() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "enc-data" {
		t.Fatalf("resolveDeployVolumes() = %+v, want the project-declared volume — the port block's prior write must not suppress the project consult", got)
	}
	if !stub.called {
		t.Error("resolveDeployVolumes() did not consult the project after the port block ran")
	}
	postEntry, ok := dc.Bundle[key]
	if !ok {
		t.Fatal("overlay entry vanished after resolveDeployVolumes")
	}
	if !postEntry.VolumeProjectChecked {
		t.Error("VolumeProjectChecked not set after resolveDeployVolumes")
	}
	if len(postEntry.ResolvedPort) != 1 || postEntry.ResolvedPort[0] != preVolumeEntry.ResolvedPort[0] {
		t.Errorf("entry.ResolvedPort = %v, want stage 1's resolved port (%v) preserved", postEntry.ResolvedPort, preVolumeEntry.ResolvedPort)
	}
}
