package main

import (
	"os"
	"path/filepath"
	"testing"
)

// android_prescan_test.go — the CORE-side loader-mechanism coverage for deploy:android (FINAL/K5
// unit 6a). Relocated from the deleted android_deploy_preresolve_test.go — the device-resolution
// + install-collection tests moved to candy/plugin-adb/preresolve_test.go alongside the body they
// now cover; this ONE test stays because it exercises the byte-gated PARSE-time prescan
// (prescanPluginManifest / declaredDeploySubstrate / isExternalDeploySubstrate), which is
// unaffected by the F6 body move.

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
