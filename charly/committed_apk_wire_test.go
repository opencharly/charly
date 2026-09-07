package main

// committed_apk_wire_test.go — the E-4 unblock regression tests: the wire
// spec.CheckEnv now carries the check-run candy source-dir map
// (spec #CheckEnv.candy_dirs, spec PR #121), and BOTH consumers of that wire
// field are covered here:
//
//  1. The in-pod step carrier (plugin_dispatch_reverse.go's InvokeProvider
//     ClassVerb/ops.OpRun branch) UNWRAPS env.CandyDirs into the host
//     hostCheckCarrier, so the host-side committed-APK anchor
//     (resolveCheckApk → checkhost.ResolveCommittedApk) resolves an in-venue
//     step's relative `apk:` path against the AUTHORING candy's source tree —
//     exactly like the out-of-pod path (TestResolveCheckApk in apk_format_test.go).
//     Without the unwrap a baked-plan install step
//     (check-android-emulator-pod's adb-install-apidemos) reported
//     "0 candies scanned".
//  2. snapshotCheckEnv RE-EMITS the carrier's CandyDirs out to a nested
//     out-of-process verb dispatch, so the map survives the second hop.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencharly/spec/spec"
)

// TestResolveCheckApk_InPodWireCarrier mirrors TestResolveCheckApk's
// out-of-pod anchoring through the IN-POD wire shape: the sender (plugin-check)
// marshals spec.CheckEnv{CandyDirs:...}, the host's reverse-leg handler decodes
// it (the exact json.Unmarshal + carrier construction of the ClassVerb/ops.OpRun
// branch in plugin_dispatch_reverse.go) and the resulting carrier resolves the
// committed-APK anchor.
func TestResolveCheckApk_InPodWireCarrier(t *testing.T) {
	repo := t.TempDir()
	apk := filepath.Join(repo, "tests", "data", "x.apk") // project-root fixture
	if err := os.MkdirAll(filepath.Dir(apk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apk, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}
	authorDir := filepath.Join(repo, "candy", "android-emulator-layer")
	if err := os.MkdirAll(authorDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The SENDER's wire snapshot (plugin-check pluginSnapshotCheckEnv with the
	// 23393dd threading): the candy map folds onto the env the runner marshals
	// into every in-pod verb dispatch.
	env := spec.CheckEnv{
		Box:        "check-android-emulator-pod",
		Mode:       "live",
		Distros:    []string{"cachyos"},
		CandyDirs:  map[string]string{"pod-android-emulator-layer": authorDir},
		MCPProvide: []spec.CandyMCPProvide{},
	}
	wire, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}

	// The HOST's reverse-leg decode + carrier construction — the exact shape of
	// plugin_dispatch_reverse.go's ClassVerb/ops.OpRun branch.
	var decoded spec.CheckEnv
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	carrier := &hostCheckCarrier{
		execFn:      func() spec.DeployExecutor { return nil },
		mode:        spec.CheckModeLive,
		box:         decoded.Box,
		instance:    decoded.Instance,
		distros:     decoded.Distros,
		dialTimeout: 3 * time.Second,
		candyDirs:   decoded.CandyDirs,
	}
	hvr := &hostVerbResolver{cc: carrier}

	// In-pod committed-APK step: origin "candy:<candy-key>", apk relative —
	// anchored against the AUTHORING candy's source tree.
	got, err := hvr.resolveCheckApk("./tests/data/x.apk", "candy:pod-android-emulator-layer")
	if err != nil {
		t.Fatalf("in-pod resolveCheckApk = %v, want nil (candy dir must be unwrapped from the wire env)", err)
	}
	if got != apk {
		t.Errorf("in-pod resolveCheckApk = %q, want %q", got, apk)
	}

	// Regression guard: WITHOUT the wire CandyDirs (the pre-fix state) the same
	// step fails with the "0 candies scanned" error, so the test cannot pass
	// vacuously through a different leg.
	empty := &hostCheckCarrier{candyDirs: nil}
	if _, err := (&hostVerbResolver{cc: empty}).resolveCheckApk("./tests/data/x.apk", "candy:pod-android-emulator-layer"); err == nil {
		t.Error("empty-candyDirs carrier must error (the pre-fix 0-candies failure), got nil")
	}
}

// TestSnapshotCheckEnvCarriesCandyDirs — the wire env a nested out-of-process
// verb dispatch carries re-emits the carrier's CandyDirs, so a second hop (host
// → another out-of-process provider) keeps the committed-APK anchoring.
func TestSnapshotCheckEnvCarriesCandyDirs(t *testing.T) {
	cc := &hostCheckCarrier{
		mode: spec.CheckModeLive,
		box:  "check-android-emulator-pod",
		candyDirs: map[string]string{
			"pod-android-emulator-layer": "/srv/charly/candy/android-emulator-layer",
		},
	}
	ce := snapshotCheckEnv(cc, nil)
	if len(ce.CandyDirs) != 1 || ce.CandyDirs["pod-android-emulator-layer"] != "/srv/charly/candy/android-emulator-layer" {
		t.Fatalf("snapshotCheckEnv lost the candy_dirs map: got %+v", ce.CandyDirs)
	}
	// Round-trips through the wire JSON (the field's json tag is what crosses).
	wire, err := json.Marshal(ce)
	if err != nil {
		t.Fatal(err)
	}
	var back spec.CheckEnv
	if err := json.Unmarshal(wire, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.CandyDirs) != 1 || back.CandyDirs["pod-android-emulator-layer"] != "/srv/charly/candy/android-emulator-layer" {
		t.Errorf("candy_dirs did not survive the wire round-trip: %+v", back.CandyDirs)
	}
}
