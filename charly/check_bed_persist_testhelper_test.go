package main

// check_bed_persist_testhelper_test.go — the shared test-local READ helper for the
// deploy-state seam-driven tests (deploy_save_test.go). testBedMarshalNode/testSaveDeployConfig
// (the WRITE-side callbacks) were deleted here (#55 final-tail split-by-assertion round,
// team-lead directive 2026-08-03): every caller that used them to write via
// deploykit.SaveBundleConfig/PersistBedDeployOverrides directly either moved to
// candy/plugin-bundle's own test suite (the writer-behavior half) or converted to drive the real
// production seam (deploy_dispatch_seam_test_helpers_test.go's testDispatchLifecycleAdd/Del).
// testLoadBundleConfig (the READ half) survives — it is charly's own LoadUnified, the genuine
// charly-loader concern these seam-driven tests verify against, now built on the local
// testProjectBundleConfig port (plugin_sidecar_test.go) instead of sdk/deploykit.ProjectBundleConfig
// — no sdk import needed.

import (
	"path/filepath"

	"github.com/opencharly/spec/spec"
)

// testLoadBundleConfig reads the per-host overlay through charly's REAL LoadUnified — the
// in-proc host read path the seam-driven deploy-state tests verify against (the temp
// XDG_CONFIG_HOME charly.yml the tests write/the seam dispatch writes).
func testLoadBundleConfig() (*spec.BundleConfig, error) {
	path, err := spec.DefaultDeployConfigPath()
	if err != nil {
		return nil, nil
	}
	uf, ok, err := LoadUnified(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if !ok || uf == nil {
		return nil, nil
	}
	return testProjectBundleConfig(uf), nil
}
