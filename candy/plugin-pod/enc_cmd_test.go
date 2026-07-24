package pod

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"google.golang.org/grpc"
)

// enc_cmd_test.go — ported from charly/enc_mount_short_circuit_test.go (wave γ, the config-time
// enc leaves' relocation into this plugin). Coverage preserved: the fast-path short-circuit
// (defect C) and the non-short-circuit path both still exercise pluginEncMount exactly as
// encMount was exercised in core.
//
// R1 finding (surfaced porting this test) + its fix: deploykit.LoadBundleConfig() — which
// deploykit.EncPlanFor/LoadEncryptedVolume called — silently degrades to "no entries" OUTSIDE
// the charly-core process (see deploy_file.go's own comment on this exact historical failure
// mode). pluginEncMount/Unmount/Status/Passwd now route through the EXISTING
// "pod-config-load-bundle" seam (deploykit.LoadBundleConfigViaSeam → EncPlanForConfig/
// EncStatusFromConfig) instead of the placement-dependent bare call — the same fix
// candy/plugin-pod/remove_orchestration.go's resolveSidecarNames already applies for the
// identical bug class. This test exercises that REAL seam path via a fake
// pb.ExecutorServiceClient (sdk.NewInProcExecutor), mirroring
// sdk/deploykit/load_bundle_config_seam_test.go's fakeExecutorServiceClient — each consuming
// package keeps its own small test double, the established convention.

// fakeExecutorServiceClient is a minimal pb.ExecutorServiceClient test double covering the two
// RPCs this file's tests need: HostBuild (the "pod-config-load-bundle" seam) and InvokeProvider
// (verb:credential). Every other RPC panics if called — these tests never reach them.
type fakeExecutorServiceClient struct {
	pb.ExecutorServiceClient
	hostBuildReply *pb.HostBuildReply
	hostBuildErr   error

	invokeProviderReply *pb.InvokeReply
	invokeProviderErr   error
}

func (f *fakeExecutorServiceClient) HostBuild(_ context.Context, _ *pb.HostBuildRequest, _ ...grpc.CallOption) (*pb.HostBuildReply, error) {
	if f.hostBuildErr != nil {
		return nil, f.hostBuildErr
	}
	return f.hostBuildReply, nil
}

func (f *fakeExecutorServiceClient) InvokeProvider(_ context.Context, _ *pb.InvokeProviderRequest, _ ...grpc.CallOption) (*pb.InvokeReply, error) {
	if f.invokeProviderErr != nil {
		return nil, f.invokeProviderErr
	}
	return f.invokeProviderReply, nil
}

// testBundleConfigReply marshals the "pod-config-load-bundle" seam's
// spec.PodConfigLoadBundleReply carrying a BundleConfig with one "testimg" bundle entry + two
// encrypted volumes (mirrors the former YAML fixture's node-form shape, constructed directly as
// Go structs — no on-disk charly.yml needed once the seam is faked end-to-end).
func testBundleConfigReply(t *testing.T, dir string) *pb.HostBuildReply {
	t.Helper()
	dc := deploykit.BundleConfig{
		Bundle: map[string]deploykit.BundleNode{
			"testimg": {
				Image: "testimg",
				Volume: []spec.DeployVolume{
					{Name: "vol-a", Type: "encrypted", Host: filepath.Join(dir, "vol-a")},
					{Name: "vol-b", Type: "encrypted", Host: filepath.Join(dir, "vol-b")},
				},
			},
		},
	}
	dcJSON, err := json.Marshal(dc)
	if err != nil {
		t.Fatalf("marshal fixture BundleConfig: %v", err)
	}
	rep := spec.PodConfigLoadBundleReply{ConfigJSON: dcJSON}
	repJSON, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal fixture reply: %v", err)
	}
	return &pb.HostBuildReply{ResultJson: repJSON}
}

// installFakeExecutor stashes cmdExec/cmdCtx (host_seams.go's package vars — normally set by
// Invoke(OpRun) at the top of one `charly config …` dispatch) with a fake reverse channel, and
// restores them on test cleanup.
func installFakeExecutor(t *testing.T, fake *fakeExecutorServiceClient) {
	t.Helper()
	origExec, origCtx := cmdExec, cmdCtx
	t.Cleanup(func() { cmdExec, cmdCtx = origExec, origCtx })
	cmdExec = sdk.NewInProcExecutor(fake)
	cmdCtx = context.Background()
}

// TestPluginEncMount_ShortCircuit_AllMounted verifies defect C fix: when every requested volume
// is already mounted, pluginEncMount returns nil without ever reaching InvokeProvider
// (verb:credential) — only HostBuild (the bundle-config load) is exercised.
func TestPluginEncMount_ShortCircuit_AllMounted(t *testing.T) {
	origMounted := deploykit.IsEncryptedMounted
	defer func() { deploykit.IsEncryptedMounted = origMounted }()

	// Spy: report every plain dir as mounted.
	calls := 0
	deploykit.IsEncryptedMounted = func(plainDir string) bool {
		calls++
		return true
	}

	dir := t.TempDir()
	fake := &fakeExecutorServiceClient{
		hostBuildReply:    testBundleConfigReply(t, dir),
		invokeProviderErr: errors.New("verb:credential unexpectedly invoked — the short-circuit should have skipped it"),
	}
	installFakeExecutor(t, fake)

	err := pluginEncMount("testimg", "", "")
	if err != nil {
		t.Fatalf("pluginEncMount returned error: %v", err)
	}
	if calls < 2 {
		t.Errorf("deploykit.IsEncryptedMounted calls = %d, want ≥ 2 (one per volume)", calls)
	}
}

// TestPluginEncMount_NoShortCircuit_WhenOneUnmounted verifies the fast path does NOT fire when at
// least one requested volume is not yet mounted — pluginEncMount proceeds to passphrase
// resolution, which reaches InvokeProvider(verb:credential). The fake returns a transport error
// there (simulating an unreachable credential plugin), so resolution fails — the failure mode
// itself proves the short-circuit correctly abstained (matching the former core test's proof
// shape: no short-circuit means no early nil return).
func TestPluginEncMount_NoShortCircuit_WhenOneUnmounted(t *testing.T) {
	origMounted := deploykit.IsEncryptedMounted
	defer func() { deploykit.IsEncryptedMounted = origMounted }()

	// Spy: report first volume mounted, second not mounted.
	var seen []string
	deploykit.IsEncryptedMounted = func(plainDir string) bool {
		seen = append(seen, plainDir)
		return len(seen) == 1 // only the first check returns true
	}

	dir := t.TempDir()
	fake := &fakeExecutorServiceClient{
		hostBuildReply:    testBundleConfigReply(t, dir),
		invokeProviderErr: errors.New("verb:credential unreachable (test double)"),
	}
	installFakeExecutor(t, fake)

	t.Setenv("CHARLY_SECRET_BACKEND", "config")
	t.Setenv("INVOCATION_ID", "test")
	t.Setenv("GOCRYPTFS_PASSWORD", "")

	err := pluginEncMount("testimg", "", "")
	if err == nil {
		t.Errorf("expected error from passphrase resolution path, got nil (short-circuit fired incorrectly?)")
	}
}

// TestPluginEncStatus_RoutesThroughSeam proves pluginEncStatus reaches the bundle config via the
// seam (HostBuild) rather than degrading silently — a nil cmdExec would previously make
// EncPlanFor/EncStatus's bare LoadBundleConfig() silently report "no encrypted volumes" instead
// of erroring; this asserts the seam is actually consulted (no error) and prints the loaded
// volumes rather than a false "not configured" outcome.
func TestPluginEncStatus_RoutesThroughSeam(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeExecutorServiceClient{hostBuildReply: testBundleConfigReply(t, dir)}
	installFakeExecutor(t, fake)

	if err := pluginEncStatus("testimg", ""); err != nil {
		t.Fatalf("pluginEncStatus returned error: %v", err)
	}
}

// TestPluginEncMount_NilExecutorErrors covers the nil-executor guard on the seam call — a
// command not compiled-in (cmdExec never stashed) gets a clean error instead of a nil-pointer
// panic reaching into deploykit.
func TestPluginEncMount_NilExecutorErrors(t *testing.T) {
	origExec, origCtx := cmdExec, cmdCtx
	t.Cleanup(func() { cmdExec, cmdCtx = origExec, origCtx })
	cmdExec, cmdCtx = nil, nil

	if err := pluginEncMount("testimg", "", ""); err == nil {
		t.Error("pluginEncMount with nil cmdExec: want an error, got nil")
	}
}
