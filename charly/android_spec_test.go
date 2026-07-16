package main

import (
	"github.com/opencharly/sdk/vmshared"
	"testing"
)

func TestAndroidSpec_IsEndpoint(t *testing.T) {
	img := &AndroidSpec{Box: "android-emulator"}
	if img.IsEndpoint() {
		t.Error("image-source device should not be an endpoint")
	}
	ep := &AndroidSpec{Adb: &vmshared.AndroidAdbEndpoint{Host: "127.0.0.1:5037"}}
	if !ep.IsEndpoint() {
		t.Error("adb-source device should be an endpoint")
	}
	// Adb present but empty host = not an endpoint.
	if (&AndroidSpec{Adb: &vmshared.AndroidAdbEndpoint{}}).IsEndpoint() {
		t.Error("empty adb host should not count as an endpoint")
	}
}

func TestAndroidSpec_EffectiveSerial(t *testing.T) {
	if got := (&AndroidSpec{}).EffectiveSerial(); got != "emulator-5554" {
		t.Errorf("default serial = %q, want emulator-5554", got)
	}
	if got := (&AndroidSpec{Serial: "emulator-5556"}).EffectiveSerial(); got != "emulator-5556" {
		t.Errorf("serial override = %q, want emulator-5556", got)
	}
}

func TestApkPackageSpec_Defaults(t *testing.T) {
	s := ApkPackageSpec{Package: "org.fdroid.fdroid"}
	if s.EffectiveSource() != "apk-pure" {
		t.Errorf("default source = %q, want apk-pure", s.EffectiveSource())
	}
	if s.EffectiveArch() != "x86_64" {
		t.Errorf("default arch = %q, want x86_64", s.EffectiveArch())
	}
	s2 := ApkPackageSpec{Package: "x", Source: "f-droid", Arch: "arm64-v8a"}
	if s2.EffectiveSource() != "f-droid" || s2.EffectiveArch() != "arm64-v8a" {
		t.Errorf("overrides not honored: %+v", s2)
	}
}

// TestValidateCandyApk — the apk⊕source cross-field rule moved with the validate engine to
// candy/plugin-box (task #60); its coverage now lives in candy/plugin-box/validate_pure_test.go
// (TestValidateCandyApk, an envelope-unit — a 1:1 helper port over spec.ApkPackageSpec).

// The adb-address parsing (splitAdbAddr) + the per-venue adb-prefix selection
// (adbScriptPrefix) moved out of core with the goadb-backed install path in the adb →
// external-plugin dep-shed; their unit coverage now lives in candy/plugin-adb.
