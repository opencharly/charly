package deploypod

import (
	"context"
	"encoding/json"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// deploy_save_state.go — the #55 K4 config-write seam-collapse for plugin-deploy-pod: the deploy-state
// WRITE runs PLUGIN-SIDE via deploykit.SaveDeployState directly, instead of over the deleted
// "deploy-config-save-state" host seam. Mirrors candy/plugin-bundle's plugin-side write: Primaries come
// from the GENERIC "loader-threaded" HostBuild leg (the SAME leg plugin-build/-bundle/-vm already call —
// no new seam), and the current-state re-read is the candy's own loadDeploy (the "pod-config-load-bundle"
// seam), so SaveDeployState no longer needs charly's DeployStateHost registration.

// fetchLoaderPrimaries returns the loader-threaded Primaries DATA snapshot (plugin-verb WORD →
// scalar-sugar primary field) via the generic "loader-threaded" host builder — the SAME map
// candy/plugin-build's resolve fills spec.ResolvedProject.Primaries from. The node-form marshal
// (deploykit.MarshalBundleNode) resugars each plan step with it. A HostBuild failure degrades to
// an empty map (a plan with no plugin-verb sugar marshals identically).
func fetchLoaderPrimaries(ctx context.Context, ex *sdk.Executor) map[string]string {
	out, err := ex.HostBuild(ctx, "loader-threaded", nil)
	if err != nil {
		return nil
	}
	var t spec.Threaded
	if err := json.Unmarshal(out, &t); err != nil {
		return nil
	}
	return t.Primaries
}

// deploySaveState persists a SaveDeployStateInput to the per-host deploy overlay PLUGIN-SIDE. The
// marshal callback resugars via loader-threaded Primaries; the reader (the candy's loadDeploy) supplies
// the current-state re-read SaveDeployState performs, so the write needs no charly-init DeployStateHost.
// SaveDeployState is best-effort (stderr warnings, no error return) — matching the host-side write it
// replaces (the deleted seam returned nil on success too).
func deploySaveState(ctx context.Context, ex *sdk.Executor, box, instance string, input spec.SaveDeployStateInput) {
	primaries := fetchLoaderPrimaries(ctx, ex)
	marshalNode := func(_ string, node *deploykit.BundleNode) (*yaml.Node, error) {
		return deploykit.MarshalBundleNode(node, primaries)
	}
	reader := func() (*deploykit.BundleConfig, error) { return loadDeploy(ctx, ex, "deploy-save-state") }
	deploykit.SaveDeployState(box, instance, input, marshalNode, reader)
}
