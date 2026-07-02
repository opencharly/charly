package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/charly/charly/spec"
)

// TestAndroidDeploySubstrate_Prescan proves the PARSE-time half of F1 routing: a
// candy declaring `deploy:android` makes `target: android` an EXTERNAL deploy
// substrate. android has NO in-proc builtin (it was externalized), so before any
// plugin is recognized it is NOT external; once the byte-gated prescan reads a
// plugin manifest declaring deploy:android, isExternalDeploySubstrate("android")
// flips true — which is what routes target:android to externalDeployTarget.
func TestAndroidDeploySubstrate_Prescan(t *testing.T) {
	// Save+restore the process-global declaration so the test is order-independent.
	declaredDeployMu.Lock()
	prior := declaredDeploySubstrate["android"]
	delete(declaredDeploySubstrate, "android")
	declaredDeployMu.Unlock()
	t.Cleanup(func() {
		declaredDeployMu.Lock()
		if prior {
			declaredDeploySubstrate["android"] = true
		} else {
			delete(declaredDeploySubstrate, "android")
		}
		declaredDeployMu.Unlock()
	})

	// android has no in-proc deploy provider (externalized) and is not yet declared.
	if _, ok := providerRegistry.resolve(ClassDeployTarget, "android"); ok {
		t.Fatal("android must have NO in-proc DeployTargetProvider (externalized, F1)")
	}
	if isExternalDeploySubstrate("android") {
		t.Fatal("android must not be external before a deploy:android plugin is recognized")
	}

	// Simulate the loader's parse-time prescan over candy/plugin-adb's manifest shape.
	dir := t.TempDir()
	manifest := filepath.Join(dir, UnifiedFileName)
	if err := os.WriteFile(manifest, []byte(`plugin-adb:
  plugin-adb-decl:
    plugin:
      providers:
        - verb:adb
        - deploy:android
      source: github.com/opencharly/charly/candy/plugin-adb
`), 0o644); err != nil {
		t.Fatal(err)
	}
	prescanPluginManifest(manifest)

	if !recognizedDeploySubstrate("android") {
		t.Fatal("prescan did not register deploy:android from the plugin manifest")
	}
	if !isExternalDeploySubstrate("android") {
		t.Fatal("android must be an EXTERNAL substrate once deploy:android is recognized (F1) — this is what routes target:android to externalDeployTarget")
	}
}

// TestCollectAndroidInstalls proves the host-side preresolver collection: it walks
// the deploy's compiled plans for ApkInstallStep entries, rewrites a committed-APK
// relative ref to its ABSOLUTE host path (the plugin reads the file on the host), and
// passes package entries through unchanged.
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

	plans := []*InstallPlan{{
		Steps: []InstallStep{&ApkInstallStep{
			CandyName: "android-apidemos",
			CandyDir:  candyDir,
			Packages: []ApkPackageSpec{
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
	bad := []*InstallPlan{{Steps: []InstallStep{&ApkInstallStep{
		CandyName: "x", CandyDir: "", Packages: []ApkPackageSpec{{Apk: "rel/missing.apk"}},
	}}}}
	if _, err := collectAndroidInstalls(bad); err == nil {
		t.Error("unanchored relative committed-APK must error, got nil")
	}
}

// TestAndroidDeployVenue_WireRoundTrip proves the spec.AndroidDeployVenue payload
// round-trips through DeployVenue.Substrate (the opaque substrate carrier) — the
// exact path the host marshals and the plugin decodes.
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
	venue := spec.DeployVenue{DeployName: "check-android-emulator-pod.device", Substrate: payload}
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
