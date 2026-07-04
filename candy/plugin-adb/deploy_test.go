package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/opencharly/sdk/spec"
)

// TestInstallWithRetry covers the PackageManager-init-race remedy (moved here from
// charly's core with the F1 android-substrate deploy ORCHESTRATION): the install
// retries until it succeeds, and returns the last error if it never does.
func TestInstallWithRetry(t *testing.T) {
	// Succeeds on the 3rd attempt (PM settles).
	n := 0
	out, err := installWithRetry(2*time.Second, time.Millisecond, func() (string, error) {
		n++
		if n < 3 {
			return "", fmt.Errorf("Failed to parse APK file (attempt %d)", n)
		}
		return "Success", nil
	})
	if err != nil || out != "Success" {
		t.Fatalf("installWithRetry should succeed once op does: out=%q err=%v (attempts=%d)", out, err, n)
	}
	if n != 3 {
		t.Errorf("expected 3 attempts, got %d", n)
	}

	// Never succeeds → returns the last error after the deadline (not a panic/hang).
	m := 0
	_, err = installWithRetry(20*time.Millisecond, time.Millisecond, func() (string, error) {
		m++
		return "", fmt.Errorf("permanently broken")
	})
	if err == nil {
		t.Error("installWithRetry should return the last error when the op never succeeds")
	}
	if m < 2 {
		t.Errorf("expected multiple attempts before the deadline, got %d", m)
	}
}

// TestInstallOp verifies the install-spec → adb #Op mapping: a committed-APK entry
// becomes an `install` op carrying the host path; a package id becomes an
// `install-app` op carrying the apkeep coordinates.
func TestInstallOp(t *testing.T) {
	apk := installOp(spec.ApkPackageSpec{Apk: "/abs/MyApp.apk"})
	if string(apk.Adb) != "install" || apk.Apk != "/abs/MyApp.apk" {
		t.Errorf("committed-APK → %+v, want adb=install apk=/abs/MyApp.apk", apk)
	}
	pkg := installOp(spec.ApkPackageSpec{Package: "org.fdroid.fdroid", Source: "apk-pure", Arch: "x86_64"})
	if string(pkg.Adb) != "install-app" || pkg.AppId != "org.fdroid.fdroid" || pkg.Source != "apk-pure" || pkg.Arch != "x86_64" {
		t.Errorf("package id → %+v, want adb=install-app app_id=org.fdroid.fdroid", pkg)
	}
}

// TestAndroidUninstallReverseOp checks the teardown op builds the right host-side
// uninstall command for each venue (in-pod via engine exec; remote endpoint via
// adb -H -P), and is best-effort (`|| true`) so a torn-down pod is harmless.
func TestAndroidUninstallReverseOp(t *testing.T) {
	inPod := androidUninstallReverseOp(&adbEnv{Engine: "podman", Container: "charly-emu", Serial: "emulator-5554"}, "io.appium.android.apis")
	script := inPod.Extra[spec.ReverseOpPluginScriptKey]
	if inPod.Kind != spec.ReverseOpPluginScript || inPod.Scope != spec.ScopeSystem {
		t.Errorf("reverse op kind/scope = %v/%v", inPod.Kind, inPod.Scope)
	}
	for _, want := range []string{"podman exec charly-emu", "uninstall", "io.appium.android.apis", "|| true"} {
		if !contains(script, want) {
			t.Errorf("in-pod uninstall script %q missing %q", script, want)
		}
	}
	endpoint := androidUninstallReverseOp(&adbEnv{AdbAddr: "127.0.0.1:35002", Serial: "emulator-5554"}, "org.fdroid.fdroid")
	es := endpoint.Extra[spec.ReverseOpPluginScriptKey]
	for _, want := range []string{"adb -H 127.0.0.1 -P 35002", "uninstall", "org.fdroid.fdroid", "|| true"} {
		if !contains(es, want) {
			t.Errorf("endpoint uninstall script %q missing %q", es, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
