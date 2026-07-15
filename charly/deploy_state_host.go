package main

import (
	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// deploy_state_host.go — the charly-side filler for the sdk/deploykit DeployStateHost
// seam (K5-Unit-1 Option A) + the deploy-kind-specific marshal callback the kind-blind
// SaveBundleConfig shell invokes. The deploy STATE-MODEL body in sdk/deploykit reaches
// the ONE core Mechanism it needs (the unified LOADER) through the package-level
// DeployStateHost seam — filled HERE at package-var init, before any command runs. This
// is the DeployConfigPath = kit.DefaultDeployConfigPath precedent generalized: the SDK
// side holds the kind-blind state-file mechanism; charly hands it the host-coupled
// LoadUnified op via the seam (IMPORT-PURITY: no new charly/*_aliases.go; charly/ calls
// deploykit.RegisterDeployStateHost directly).
//
// The deploy-kind-specific marshal (marshalDeployNode, the struct-body → node-form
// transform) is NOT a seam op — it is a callback the kind-blind SaveBundleConfig shell
// takes per entry. The kit stays kind-blind; the marshal lives in charly/deploy_nodeform.go
// (tracked K4-exit inventory: it moves to its plugin home when K4 moves the consumers).
//
// Nil-safe by design: a plugin/SDK consumer that never writes the per-host ledger leaves
// DeployStateHost nil and the write paths no-op (the read-only validate/inspect paths).

func init() {
	deploykit.RegisterDeployStateHost(&deploykit.StateHostMechanisms{
		// LoadUnifiedBundleConfig — load the per-host charly.yml at configDir through the
		// unified loader (the SAME path every project charly.yml takes) and return its
		// ProjectBundleConfig. Returns (nil, nil) for an absent/unselected file. This is the
		// ONE kind-blind K1-gated op the seam threads; it dies when K1 relocates LoadUnified
		// to sdk/loaderkit (task #31) and plugin-bundle calls loaderkit.LoadUnified directly.
		LoadUnifiedBundleConfig: func(configDir string) (*deploykit.BundleConfig, error) {
			uf, ok, err := LoadUnified(configDir)
			if err != nil {
				return nil, err
			}
			if !ok || uf == nil {
				return nil, nil
			}
			return uf.ProjectBundleConfig(), nil
		},
	})
}

// marshalDeployNode is the per-entry callback for deploykit.SaveBundleConfig: it serializes
// one BundleNode to the compact node-form the per-host overlay loader accepts (the
// deploy-kind-specific marshal the kind-blind kit shell invokes per entry). The kit cannot
// import it — it is a core/plugin concern, tracked K4-exit inventory. spec.Deploy IS
// deploykit.BundleNode (a type alias), so this satisfies SaveBundleConfig's callback
// signature without a deploykit type reference in deploy_nodeform.go.
func marshalDeployNode(name string, node *spec.Deploy) (*yaml.Node, error) {
	return marshalBundleNode(node)
}

// saveBundleConfigNodeForm persists a BundleConfig through the kind-blind
// deploykit.SaveBundleConfig shell, supplying the deploy-kind-specific marshal
// (marshalDeployNode) as the per-entry callback. This is the ONE charly/ call site for
// the deploy-state writer (R3): every charly/ deploy-lifecycle path that persists the
// per-host overlay calls this helper instead of deploykit.SaveBundleConfig directly.
// Tracked K4-exit inventory: the marshal + this helper live in charly/ core until K4
// moves the deploy-lifecycle consumers to their plugin homes (plugin-bundle /
// plugin-deploy-pod / plugin-check).
func saveBundleConfigNodeForm(dc *deploykit.BundleConfig) error {
	return deploykit.SaveBundleConfig(dc, marshalDeployNode)
}