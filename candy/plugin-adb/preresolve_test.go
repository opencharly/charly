package adb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// preresolve_test.go — relocated from charly/android_deploy_preresolve_test.go +
// android_hostport_test.go (FINAL/K5 unit 6a) alongside the deploy:android preresolve body they
// cover. TestAndroidDeploySubstrate_Prescan (the loader parse-time coverage, unaffected by this
// move) stays in charly/android_prescan_test.go.

// TestCollectAndroidInstalls proves the install-spec collection: it walks the deploy's compiled
// plans for ApkInstallStep entries, rewrites a committed-APK relative ref to its ABSOLUTE host
// path (this plugin reads the file on the host), and passes package entries through unchanged.
func TestCollectAndroidInstalls(t *testing.T) {
	repo := t.TempDir()
	candyDir := filepath.Join(repo, "candy", "android-apidemos")
	if err := os.MkdirAll(candyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	apk := filepath.Join(repo, "tests", "data", "ApiDemos.apk")
	if err := os.MkdirAll(filepath.Dir(apk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(apk, []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}

	plans := []*deploykit.InstallPlan{{
		Steps: []spec.InstallStep{&deploykit.ApkInstallStep{
			CandyName: "android-apidemos",
			CandyDir:  candyDir,
			Packages: []spec.ApkPackageSpec{
				{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"},
				{Apk: "tests/data/ApiDemos.apk"}, // project-root-relative → absolute
			},
		}},
	}}

	installs, err := collectAndroidInstalls(plans)
	if err != nil {
		t.Fatalf("collectAndroidInstalls: %v", err)
	}
	if len(installs) != 2 {
		t.Fatalf("installs len = %d, want 2", len(installs))
	}
	if installs[0].Package != "org.fdroid.fdroid" || installs[0].Source != "apk-pure" {
		t.Errorf("package entry not passed through: %+v", installs[0])
	}
	if installs[1].Apk != apk {
		t.Errorf("committed-APK Apk = %q, want absolute %q", installs[1].Apk, apk)
	}

	// A relative committed-APK that cannot be anchored is a HARD ERROR (no silent pass).
	bad := []*deploykit.InstallPlan{{Steps: []spec.InstallStep{&deploykit.ApkInstallStep{
		CandyName: "x", CandyDir: "", Packages: []spec.ApkPackageSpec{{Apk: "rel/missing.apk"}},
	}}}}
	if _, err := collectAndroidInstalls(bad); err == nil {
		t.Error("unanchored relative committed-APK must error, got nil")
	}
}

// TestAndroidDeployVenue_WireRoundTrip proves the spec.AndroidDeployVenue payload round-trips
// through DeployVenue.Substrate (the opaque substrate carrier) — the exact path this plugin's
// invokeAndroidPreresolve marshals into ResultJson and the host stores in DeployVenue.Substrate.
func TestAndroidDeployVenue_WireRoundTrip(t *testing.T) {
	av := spec.AndroidDeployVenue{
		AdbAddr:  "127.0.0.1:35002",
		Serial:   "emulator-5554",
		Installs: []spec.ApkPackageSpec{{Package: "org.fdroid.fdroid"}, {Apk: "/abs/x.apk"}},
	}
	payload, err := json.Marshal(av)
	if err != nil {
		t.Fatal(err)
	}
	venue := spec.DeployVenue{DeployName: "check-android-device.device", Substrate: payload}
	wire, err := json.Marshal(venue)
	if err != nil {
		t.Fatal(err)
	}
	var got spec.DeployVenue
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatal(err)
	}
	var gotAV spec.AndroidDeployVenue
	if err := json.Unmarshal(got.Substrate, &gotAV); err != nil {
		t.Fatalf("decode substrate: %v", err)
	}
	if gotAV.AdbAddr != av.AdbAddr || len(gotAV.Installs) != 2 || gotAV.Installs[1].Apk != "/abs/x.apk" {
		t.Errorf("android venue did not round-trip: %+v", gotAV)
	}
}

// TestResolveAndroidHostPortRef covers the parse paths of the nested-endpoint ${HOST_PORT:N}
// resolver. NO android-emulator R10 bed exists in the current roster to exercise the LIVE
// resolution (inspecting a running parent pod) — verified against the live `check-` bed
// inventory, FINAL/K5 unit 6a. This move's live proof runs through the k8s/vm preresolve
// bodies' beds (check-k8s-deploy / check-charly-vm), which exercise the SAME
// wireDeployPreresolver + "deploy-entity-resolve" seam mechanism this file also uses; the
// android-specific runtime path stays prereq-limited on this host until an android bed exists.
func TestResolveAndroidHostPortRef(t *testing.T) {
	// A literal host:port (no ${HOST_PORT}) passes through unchanged.
	if got, err := resolveAndroidHostPortRef("192.168.1.50:5555", "stack.device-net", nil); err != nil || got != "192.168.1.50:5555" {
		t.Fatalf("literal: got (%q, %v), want (192.168.1.50:5555, nil)", got, err)
	}
	// ${HOST_PORT:N} on a non-nested device (no parent in the deploy path) errors.
	if _, err := resolveAndroidHostPortRef("127.0.0.1:${HOST_PORT:5037}", "toplevel", nil); err == nil || !strings.Contains(err.Error(), "not nested") {
		t.Fatalf("no-parent: expected a 'not nested' error, got %v", err)
	}
	// Non-numeric container port errors.
	if _, err := resolveAndroidHostPortRef("127.0.0.1:${HOST_PORT:abc}", "stack.device-net", nil); err == nil || !strings.Contains(err.Error(), "positive container port") {
		t.Fatalf("malformed: expected a 'positive container port' error, got %v", err)
	}
	// Missing closing brace errors.
	if _, err := resolveAndroidHostPortRef("127.0.0.1:${HOST_PORT:5037", "stack.device-net", nil); err == nil || !strings.Contains(err.Error(), "closing brace") {
		t.Fatalf("no-brace: expected a 'closing brace' error, got %v", err)
	}
}
