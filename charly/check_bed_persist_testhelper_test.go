package main

// check_bed_persist_testhelper_test.go — the shared test-local helpers for the bed-persist +
// deploy-state tests, after the host-side persistBedDeployOverrides wrapper moved PLUGIN-SIDE to
// candy/plugin-check (#55 coneC-dsh β1) and the charly deploy_state_host.go init()/DeployStateHost
// filler was deleted (#55 coneC-dsh δ — the DeployStateHost-backed nil-reader fallback is gone, so
// the in-proc tests inject their OWN reader). The bed-persist tests (check_bed_run_test.go,
// deploy_f3_test.go, node_bundle_venue_test.go) call deploykit.PersistBedDeployOverrides directly
// with testBedMarshalNode (deploykit.MarshalBundleNode, nil primaries — the test beds carry no
// plugin-verb sugar) + testLoadBundleConfig (the reader replicating the deleted init()'s
// LoadUnifiedBundleConfig). The deploy-state tests (deploy_preserve_test.go, deploy_save_test.go)
// call deploykit.SaveVmDeployState/RemoveVmDeployEntry with testSaveDeployConfig + testLoadBundleConfig.
// NOT a production alias (test-only).

import (
	"path/filepath"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"gopkg.in/yaml.v3"
)

// testBedMarshalNode is the test-local marshalNode callback for deploykit.PersistBedDeployOverrides
// (and the save callback deploykit.SaveBundleConfig takes via testSaveDeployConfig).
func testBedMarshalNode(_ string, node *deploykit.BundleNode) (*yaml.Node, error) {
	return deploykit.MarshalBundleNode(node, nil)
}

// testLoadBundleConfig is the test-local reader replicating the deleted init()'s
// LoadUnifiedBundleConfig (LoadUnified(configDir) + deploykit.ProjectBundleConfig(uf)) — the
// in-proc host read path the bed-persist + deploy-state tests exercise. #55 coneC-dsh δ: with
// init() deleted (DeployStateHost nil), deploykit.LoadBundleConfig() returns (nil, nil) — so these
// tests inject this reader directly, reading the per-host overlay (the temp XDG_CONFIG_HOME
// charly.yml the tests write) through the SAME LoadUnified path the deleted seam used.
func testLoadBundleConfig() (*deploykit.BundleConfig, error) {
	path, err := kit.DefaultDeployConfigPath()
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
	return deploykit.ProjectBundleConfig(uf), nil
}

// testSaveDeployConfig is the test-local save callback (the save param deploykit.SaveVmDeployState /
// RemoveVmDeployEntry take) — wraps deploykit.SaveBundleConfig with testBedMarshalNode + the
// test-local reader. Mirrors the deleted host save callback, minus the Primaries
// (test fixtures carry no plugin-verb sugar).
func testSaveDeployConfig(dc *deploykit.BundleConfig) error {
	return deploykit.SaveBundleConfig(dc, testBedMarshalNode, testLoadBundleConfig)
}
