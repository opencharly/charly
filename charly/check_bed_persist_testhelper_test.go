package main

// check_bed_persist_testhelper_test.go — the shared test-local marshalNode callback for the
// bed-persist tests, after the host-side persistBedDeployOverrides wrapper moved PLUGIN-SIDE to
// candy/plugin-check (#55 coneC-dsh β1). The bed-persist tests (check_bed_run_test.go,
// deploy_f3_test.go, node_bundle_venue_test.go) now call deploykit.PersistBedDeployOverrides
// directly with this marshalNode (deploykit.MarshalBundleNode, nil primaries — the test beds carry
// no plugin-verb sugar) + a nil reader (the DeployStateHost-backed fallback), verifying the SAME
// seed behavior the plugin-side bed persist relies on. NOT a production alias (test-only).

import (
	"github.com/opencharly/sdk/deploykit"
	"gopkg.in/yaml.v3"
)

// testBedMarshalNode is the test-local marshalNode callback for deploykit.PersistBedDeployOverrides.
func testBedMarshalNode(_ string, node *deploykit.BundleNode) (*yaml.Node, error) {
	return deploykit.MarshalBundleNode(node, nil)
}
