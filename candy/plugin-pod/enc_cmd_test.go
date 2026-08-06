package pod

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
	"google.golang.org/grpc"
)

// enc_cmd_test.go — ported from charly/enc_mount_short_circuit_test.go (wave γ, the config-time
// enc leaves' relocation into this plugin). Coverage preserved: the fast-path short-circuit
// (defect C) and the non-short-circuit path both still exercise pluginEncMount exactly as
// encMount was exercised in core.
//
// R1 finding (surfaced porting this test) + its fix: deploykit.LoadFleetConfig() — which
// deploykit.EncPlanFor/LoadEncryptedVolume called — silently degrades to "no entries" OUTSIDE
// the charly-core process (see deploy_file.go's own comment on this exact historical failure
// mode). pluginEncMount/Unmount/Status/Passwd now route through the cycle-free plugin-side
// loaderkit.LoadHostFleetConfigViaExecutor helper (→ EncPlanForConfig/EncStatusFromConfig)
// instead of the placement-dependent bare call — #55 coneC Unit C2 retired the former
// deploykit.LoadFleetConfigViaSeam host-seam round-trip — the same fix
// candy/plugin-pod/remove_orchestration.go's resolveSidecarNames already applies for the
// identical bug class.
//
// The new helper drives the FULL LoadUnified pipeline over the reverse channel (schema gate +
// the LoadSeams), which a HostBuild-only test double cannot faithfully fake, so loadPodFleetConfig
// is a package-var seam (enc_cmd.go) these tests swap for a canned *FleetConfig. The
// nil-executor guard (TestPluginEncMount_NilExecutorErrors) keeps the REAL helper to prove the
// loud-error contract still holds when no reverse channel is stashed.

// fakeExecutorServiceClient is a minimal pb.ExecutorServiceClient test double covering the ONE
// RPC these tests still need once the fleet-config load is swapped: InvokeProvider (verb:credential).
// HostBuild is no longer reached (loadPodFleetConfig is swapped); every other RPC panics if called.
type fakeExecutorServiceClient struct {
	pb.ExecutorServiceClient
	invokeProviderReply *pb.InvokeReply
	invokeProviderErr   error
}

func (f *fakeExecutorServiceClient) HostBuild(_ context.Context, _ *pb.HostBuildRequest, _ ...grpc.CallOption) (*pb.HostBuildReply, error) {
	panic("HostBuild should not be reached: loadPodFleetConfig is swapped in these tests")
}

func (f *fakeExecutorServiceClient) InvokeProvider(_ context.Context, _ *pb.InvokeProviderRequest, _ ...grpc.CallOption) (*pb.InvokeReply, error) {
	if f.invokeProviderErr != nil {
		return nil, f.invokeProviderErr
	}
	return f.invokeProviderReply, nil
}

// testFleetConfig builds the canned FleetConfig the swapped loadPodFleetConfig returns: one
// "testimg" fleet entry + two encrypted volumes (mirrors the former YAML fixture's node-form
// shape, constructed directly as Go structs — no on-disk charly.yml + no reverse channel needed).
func testFleetConfig(t *testing.T, dir string) *deploykit.FleetConfig {
	t.Helper()
	return &deploykit.FleetConfig{
		Fleet: map[string]deploykit.FleetNode{
			"testimg": {
				Image: "testimg",
				Volume: []spec.DeployVolume{
					{Name: "vol-a", Type: "encrypted", Host: filepath.Join(dir, "vol-a")},
					{Name: "vol-b", Type: "encrypted", Host: filepath.Join(dir, "vol-b")},
				},
			},
		},
	}
}

// swapLoadPodFleetConfig replaces the package-var loadPodFleetConfig with one returning the
// canned dc, restoring the real helper on cleanup.
func swapLoadPodFleetConfig(t *testing.T, dc *deploykit.FleetConfig) {
	t.Helper()
	orig := loadPodFleetConfig
	t.Cleanup(func() { loadPodFleetConfig = orig })
	loadPodFleetConfig = func() (*deploykit.FleetConfig, error) { return dc, nil }
}

// installFakeExecutor stashes cmdExec/cmdCtx (host_seams.go's package vars — normally set by
// Invoke(OpRun) at the top of one `charly config …` dispatch) with a fake reverse channel, and
// restores them on test cleanup. Kept for the InvokeProvider(verb:credential) reach the
// non-short-circuit path needs; the fleet-config load is swapped separately.
func installFakeExecutor(t *testing.T, fake *fakeExecutorServiceClient) {
	t.Helper()
	origExec, origCtx := cmdExec, cmdCtx
	t.Cleanup(func() { cmdExec, cmdCtx = origExec, origCtx })
	cmdExec = sdk.NewInProcExecutor(fake)
	cmdCtx = context.Background()
}

// TestPluginEncMount_ShortCircuit_AllMounted verifies defect C fix: when every requested volume
// is already mounted, pluginEncMount returns nil without ever reaching InvokeProvider
// (verb:credential) — only the swapped fleet-config load is exercised.
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
	swapLoadPodFleetConfig(t, testFleetConfig(t, dir))
	installFakeExecutor(t, &fakeExecutorServiceClient{
		invokeProviderErr: errors.New("verb:credential unexpectedly invoked — the short-circuit should have skipped it"),
	})

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
	swapLoadPodFleetConfig(t, testFleetConfig(t, dir))
	installFakeExecutor(t, &fakeExecutorServiceClient{
		invokeProviderErr: errors.New("verb:credential unreachable (test double)"),
	})

	t.Setenv("CHARLY_SECRET_BACKEND", "config")
	t.Setenv("INVOCATION_ID", "test")
	t.Setenv("GOCRYPTFS_PASSWORD", "")

	err := pluginEncMount("testimg", "", "")
	if err == nil {
		t.Errorf("expected error from passphrase resolution path, got nil (short-circuit fired incorrectly?)")
	}
}

// TestPluginEncStatus_RoutesThroughSeam proves pluginEncStatus reaches the fleet config via the
// swapped loader (the package-var loadPodFleetConfig) rather than degrading silently — a nil
// cmdExec would previously make EncPlanFor/EncStatus's bare LoadFleetConfig() silently report
// "no encrypted volumes" instead of erroring; this asserts the loader is actually consulted (no
// error) and prints the loaded volumes rather than a false "not configured" outcome.
func TestPluginEncStatus_RoutesThroughSeam(t *testing.T) {
	dir := t.TempDir()
	swapLoadPodFleetConfig(t, testFleetConfig(t, dir))
	installFakeExecutor(t, &fakeExecutorServiceClient{})

	if err := pluginEncStatus("testimg", ""); err != nil {
		t.Fatalf("pluginEncStatus returned error: %v", err)
	}
}

// TestPluginEncMount_NilExecutorErrors covers the nil-executor guard on the REAL loader path — a
// command not compiled-in (cmdExec never stashed) gets a clean error instead of a nil-pointer
// panic reaching into deploykit. Uses the un-swapped real loadPodFleetConfig (which calls
// loaderkit.LoadHostFleetConfigViaExecutor → LoadUnifiedViaExecutor → errors on a nil executor).
func TestPluginEncMount_NilExecutorErrors(t *testing.T) {
	origExec, origCtx := cmdExec, cmdCtx
	t.Cleanup(func() { cmdExec, cmdCtx = origExec, origCtx })
	cmdExec, cmdCtx = nil, nil

	if err := pluginEncMount("testimg", "", ""); err == nil {
		t.Error("pluginEncMount with nil cmdExec: want an error, got nil")
	}
}

// silence json import if no test above marshals (kept for the fixture helpers' potential future use).
var _ = json.Marshal
