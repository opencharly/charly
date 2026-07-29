package main

import (
	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
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
// The deploy-kind-specific marshal (the struct-body → node-form transform) is NOT a seam op — it is
// a callback the kind-blind SaveBundleConfig shell takes per entry. The kit stays kind-blind; the
// marshal LOGIC now lives in deploykit.MarshalBundleNode (plugin-reachable, primaries threaded as
// DATA — the deploy_nodeform convergence), and marshalDeployNode below is the thin host wrapper.
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

// marshalDeployNode is the per-entry callback for deploykit.SaveBundleConfig: it serializes one
// BundleNode to the compact node-form the per-host overlay loader accepts. The marshal LOGIC itself
// now lives in deploykit.MarshalBundleNode (the deploy_nodeform convergence — a pure, plugin-reachable
// yaml transform); this thin host wrapper only supplies the plan-resugar primaries as DATA
// (loaderThreaded().Primaries — the SAME registry-derived projection that fills the resolved-project
// envelope's spec.ResolvedProject.Primaries field). A plugin-side deploy-state writer passes the
// envelope's Primaries to the IDENTICAL deploykit.MarshalBundleNode, so no marshal knowledge stays core.
func marshalDeployNode(name string, node *spec.Deploy) (*yaml.Node, error) {
	return deploykit.MarshalBundleNode(node, loaderThreaded().Primaries)
}

// saveBundleConfigNodeForm persists a BundleConfig through the kind-blind
// deploykit.SaveBundleConfig shell, supplying the deploy-kind-specific marshal
// (marshalDeployNode) as the per-entry callback. This is the ONE charly/ call site for
// the deploy-state writer (R3): every charly/ deploy-lifecycle path that persists the
// per-host overlay calls this helper instead of deploykit.SaveBundleConfig directly.
//
// K4-exit DONE (the deploy_nodeform convergence): the marshal LOGIC moved to
// deploykit.MarshalBundleNode (pure yaml transform, primaries threaded as DATA), so
// deploykit.SaveBundleConfig + the whole node-form marshal are now plugin-reachable. This helper
// stays as the ONE charly/ host call site (R3) that feeds loaderThreaded().Primaries — the residual
// host-only step is the registry-derived primaries projection, not the marshal itself.
func saveBundleConfigNodeForm(dc *deploykit.BundleConfig) error {
	return deploykit.SaveBundleConfig(dc, marshalDeployNode)
}
