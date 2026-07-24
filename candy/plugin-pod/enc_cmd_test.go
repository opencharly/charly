package pod

import (
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// enc_cmd_test.go — ported from charly/enc_mount_short_circuit_test.go (wave γ, the config-time
// enc leaves' relocation into this plugin). Coverage preserved: the fast-path short-circuit
// (defect C) and the non-short-circuit path both still exercise pluginEncMount exactly as
// encMount was exercised in core.
//
// R1 finding (surfaced porting this test, not a regression in the port itself):
// deploykit.LoadBundleConfig() — which deploykit.EncPlanFor/LoadEncryptedVolume calls — silently
// degrades to "no entries" OUTSIDE the charly-core process (see deploy_file.go's own comment on
// this exact failure mode: it used to drive encMount straight into a systemd-ask-password hang).
// It only works because charly-core's init() registers deploykit.DeployStateHost before any
// command dispatches. Compiled-in placement (command:pod's canonical, and ONLY supported,
// placement — CliMain refuses out-of-process) shares that SAME process, so PRODUCTION is
// unaffected; a bare `go test` of this package never runs charly's init, so this test registers
// its own DeployStateHost fake — the same seam sdk/deploykit/load_bundle_config_seam_test.go
// already establishes the pattern for (each consuming package keeps its own small test double;
// no shared exported fixture across modules).

// installFakeDeployStateHost registers a DeployStateHost whose LoadUnifiedBundleConfig returns dc
// unconditionally (configDir is ignored — no on-disk charly.yml needed), restoring the previous
// value on test cleanup.
func installFakeDeployStateHost(t *testing.T, dc *deploykit.BundleConfig) {
	t.Helper()
	orig := deploykit.DeployStateHost
	t.Cleanup(func() { deploykit.DeployStateHost = orig })
	deploykit.RegisterDeployStateHost(&deploykit.StateHostMechanisms{
		LoadUnifiedBundleConfig: func(string) (*deploykit.BundleConfig, error) { return dc, nil },
	})
}

// testEncBundleConfig builds the minimal fixture EncPlanFor needs: one "testimg" bundle entry
// with two encrypted volumes (mirrors the former YAML fixture's node-form shape, constructed
// directly as Go structs — LoadEncryptedVolume reads dc.Bundle, never the YAML on disk, once the
// DeployStateHost seam is stubbed).
func testEncBundleConfig(dir string) *deploykit.BundleConfig {
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

// TestPluginEncMount_ShortCircuit_AllMounted verifies defect C fix: when every requested volume
// is already mounted, pluginEncMount returns nil without ever reaching the credential/InvokeProvider
// path (which would need a real reverse-channel executor this unit test does not stand up — cmdExec
// stays nil here, exactly as it does whenever this package's tests run outside Invoke(OpRun)).
func TestPluginEncMount_ShortCircuit_AllMounted(t *testing.T) {
	origMounted := deploykit.IsEncryptedMounted
	defer func() { deploykit.IsEncryptedMounted = origMounted }()

	// Spy: report every plain dir as mounted.
	calls := 0
	deploykit.IsEncryptedMounted = func(plainDir string) bool {
		calls++
		return true
	}

	installFakeDeployStateHost(t, testEncBundleConfig(t.TempDir()))

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
// resolution, which needs the reverse-channel executor (cmdExec). No real host is attached in
// this unit test, so resolution fails via the same "no host reverse channel" guard pluginEncExec
// uses — the failure mode itself proves the short-circuit correctly abstained (matching the
// former core test's proof shape: no short-circuit means no early nil return).
func TestPluginEncMount_NoShortCircuit_WhenOneUnmounted(t *testing.T) {
	origMounted := deploykit.IsEncryptedMounted
	defer func() { deploykit.IsEncryptedMounted = origMounted }()

	// Spy: report first volume mounted, second not mounted.
	var seen []string
	deploykit.IsEncryptedMounted = func(plainDir string) bool {
		seen = append(seen, plainDir)
		return len(seen) == 1 // only the first check returns true
	}

	installFakeDeployStateHost(t, testEncBundleConfig(t.TempDir()))

	t.Setenv("CHARLY_SECRET_BACKEND", "config")
	t.Setenv("INVOCATION_ID", "test")
	t.Setenv("GOCRYPTFS_PASSWORD", "")

	// cmdExec/cmdCtx are package vars left at their zero value (nil) — no Invoke(OpRun) ran in
	// this test process, exactly mirroring production's "not compiled-in" guard path.
	err := pluginEncMount("testimg", "", "")
	if err == nil {
		t.Errorf("expected error from passphrase resolution path, got nil (short-circuit fired incorrectly?)")
	}
}
