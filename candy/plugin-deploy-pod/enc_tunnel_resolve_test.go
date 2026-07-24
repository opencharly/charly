package deploypod

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// enc_tunnel_resolve_test.go — unit coverage for the wave γ port of
// charly/pod_lifecycle_resolve.go's resolvePodEncEnsure/resolvePodEncUnmount/resolvePodTunnel.
// dc is a plain parameter now (the caller loads it once via loadDeploy — the EXISTING
// "pod-config-load-deploy" seam), so these functions are directly unit-testable with a
// Go-constructed *deploykit.BundleConfig, no fake executor needed for the no-executor-touching
// paths (resolvePodEncUnmountPlan, resolvePodTunnelPlan). resolvePodEncEnsurePlan's
// credential-touching path is exercised only via its fast-path short-circuit here (same shape as
// candy/plugin-pod/enc_cmd_test.go's TestPluginEncMount_ShortCircuit_AllMounted) — the
// passphrase-resolution branch needs a live reverse channel, proven only by a real R10 bed run.

func testTunnelBundleConfig(dir string) *deploykit.BundleConfig {
	return &deploykit.BundleConfig{
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
}

func TestResolvePodEncUnmountPlan_NilConfigReturnsNil(t *testing.T) {
	body, err := resolvePodEncUnmountPlan(nil, "testimg", "")
	if err != nil {
		t.Fatalf("resolvePodEncUnmountPlan() error: %v", err)
	}
	if body != nil {
		t.Errorf("resolvePodEncUnmountPlan() = %v, want nil for a nil config", body)
	}
}

func TestResolvePodEncUnmountPlan_NoMatchingEntryReturnsNil(t *testing.T) {
	dc := &deploykit.BundleConfig{Bundle: map[string]deploykit.BundleNode{}}
	body, err := resolvePodEncUnmountPlan(dc, "nonexistent-box", "")
	if err != nil {
		t.Fatalf("resolvePodEncUnmountPlan() error: %v", err)
	}
	if body != nil {
		t.Errorf("resolvePodEncUnmountPlan() = %v, want nil for an unmatched deploy key", body)
	}
}

func TestResolvePodEncUnmountPlan_BuildsPlanForConfiguredVolumes(t *testing.T) {
	origMounted := deploykit.IsEncryptedMounted
	defer func() { deploykit.IsEncryptedMounted = origMounted }()
	deploykit.IsEncryptedMounted = func(string) bool { return true }

	dc := testTunnelBundleConfig(t.TempDir())
	body, err := resolvePodEncUnmountPlan(dc, "testimg", "")
	if err != nil {
		t.Fatalf("resolvePodEncUnmountPlan() error: %v", err)
	}
	if len(body) == 0 {
		t.Fatal("resolvePodEncUnmountPlan() = empty, want a non-empty EncExecInput JSON body")
	}
	var in spec.EncExecInput
	if uerr := json.Unmarshal(body, &in); uerr != nil {
		t.Fatalf("decode plan: %v", uerr)
	}
	if in.Method != spec.EncMethodUnmount {
		t.Errorf("Method = %q, want %q", in.Method, spec.EncMethodUnmount)
	}
	if len(in.Volumes) != 2 {
		t.Errorf("Volumes = %d, want 2", len(in.Volumes))
	}
}

// TestResolvePodEncEnsurePlan_ShortCircuit_AllMounted mirrors
// candy/plugin-pod/enc_cmd_test.go's TestPluginEncMount_ShortCircuit_AllMounted: when every
// requested volume is already initialized AND mounted, resolvePodEncEnsurePlan returns (nil, nil)
// WITHOUT ever reaching passphrase resolution — proven by passing a nil executor (any credential
// RPC would error) and asserting a clean nil/nil return rather than an error. Real cipher dirs
// (gocryptfs.conf present) are seeded so Initialized is also true — EncPlanForConfig's
// IsEncryptedInitialized has no test-var override (unlike IsEncryptedMounted), so this is the
// only way to drive BOTH gate flags true.
//
// CAUTION for any future edit here: deploykit.AskPassword's production path
// (systemd-ask-password --timeout=0) BLOCKS INDEFINITELY without a real TTY answer. Never
// construct a test input here that reaches passphrase resolution without either GOCRYPTFS_PASSWORD
// set (safe, returns immediately) or the short-circuit firing (safe, never resolves at all) — a
// "not ready, no env override" test case would hang this entire package's test run.
func TestResolvePodEncEnsurePlan_ShortCircuit_AllMounted(t *testing.T) {
	origMounted := deploykit.IsEncryptedMounted
	defer func() { deploykit.IsEncryptedMounted = origMounted }()
	deploykit.IsEncryptedMounted = func(string) bool { return true }

	dir := t.TempDir()
	dc := testTunnelBundleConfig(dir)
	for _, vol := range []string{"vol-a", "vol-b"} {
		cipherDir := filepath.Join(dir, vol, "cipher")
		if err := os.MkdirAll(cipherDir, 0700); err != nil {
			t.Fatalf("mkdir cipher: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cipherDir, "gocryptfs.conf"), []byte("{}"), 0600); err != nil {
			t.Fatalf("writing gocryptfs.conf: %v", err)
		}
	}

	ctx := context.Background()
	body, err := resolvePodEncEnsurePlan(ctx, nil, dc, "testimg", "")
	if err != nil {
		t.Fatalf("resolvePodEncEnsurePlan() error: %v (short-circuit should have skipped passphrase resolution entirely)", err)
	}
	if body != nil {
		t.Errorf("resolvePodEncEnsurePlan() = %v, want nil when every volume is already ready", body)
	}
}

// TestResolvePodEncEnsurePlan_NoVolumesConfiguredReturnsNil covers the "nothing declared" path —
// zero volumes means zero credential touches, regardless of executor.
func TestResolvePodEncEnsurePlan_NoVolumesConfiguredReturnsNil(t *testing.T) {
	dc := &deploykit.BundleConfig{Bundle: map[string]deploykit.BundleNode{}}
	ctx := context.Background()
	body, err := resolvePodEncEnsurePlan(ctx, nil, dc, "nonexistent-box", "")
	if err != nil {
		t.Fatalf("resolvePodEncEnsurePlan() error: %v", err)
	}
	if body != nil {
		t.Errorf("resolvePodEncEnsurePlan() = %v, want nil when no encrypted volumes are configured", body)
	}
}

func TestResolvePodTunnelPlan_NoRunningContainerReturnsNil(t *testing.T) {
	tc := resolvePodTunnelPlan(nil, "definitely-not-a-running-container", "")
	if tc != nil {
		t.Errorf("resolvePodTunnelPlan() = %+v, want nil when no container is running", tc)
	}
}
