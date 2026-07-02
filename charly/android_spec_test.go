package main

import "testing"

func TestAndroidSpec_IsEndpoint(t *testing.T) {
	img := &AndroidSpec{Box: "android-emulator"}
	if img.IsEndpoint() {
		t.Error("image-source device should not be an endpoint")
	}
	ep := &AndroidSpec{Adb: &AndroidAdbEndpoint{Host: "127.0.0.1:5037"}}
	if !ep.IsEndpoint() {
		t.Error("adb-source device should be an endpoint")
	}
	// Adb present but empty host = not an endpoint.
	if (&AndroidSpec{Adb: &AndroidAdbEndpoint{}}).IsEndpoint() {
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

func TestValidateCandyApk(t *testing.T) {
	cases := []struct {
		name    string
		apks    []ApkPackageSpec
		wantErr bool
	}{
		{"valid-package", []ApkPackageSpec{{Package: "org.fdroid.fdroid", Source: "apk-pure"}}, false},
		{"valid-committed", []ApkPackageSpec{{Apk: "tests/data/x.apk"}}, false},
		// package⊕apk one-of + the source enum are now enforced by #CandyApk
		// (cue_tighten_test.go); only the source⊕apk cross-field rule stays in Go.
		{"source-on-committed", []ApkPackageSpec{{Apk: "y.apk", Source: "apk-pure"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := &ValidationError{}
			validateCandyApk("test-layer", tc.apks, errs)
			if errs.HasErrors() != tc.wantErr {
				t.Errorf("validateCandyApk(%+v): HasErrors=%v want %v (%v)", tc.apks, errs.HasErrors(), tc.wantErr, errs.Errors)
			}
		})
	}
}

// The adb-address parsing (splitAdbAddr) + the per-venue adb-prefix selection
// (adbScriptPrefix) moved out of core with the goadb-backed install path in the adb →
// external-plugin dep-shed; their unit coverage now lives in candy/plugin-adb.
