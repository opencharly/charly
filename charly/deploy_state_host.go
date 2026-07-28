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
	// The plan-resugar primaries table is now DATA (spec.ResolvedProject.Primaries): the host
	// sources it from loaderThreaded().Primaries — the SAME registry-derived projection that fills
	// the resolved-project envelope's Primaries field — instead of marshalBundleNode reaching the
	// package-private pluginPrimaries global. A plugin-side deploy-state writer passes the envelope's
	// Primaries to the identical (relocated) marshal, the K4 keystone this decoupling establishes.
	return marshalBundleNode(node, loaderThreaded().Primaries)
}

// saveBundleConfigNodeForm persists a BundleConfig through the kind-blind
// deploykit.SaveBundleConfig shell, supplying the deploy-kind-specific marshal
// (marshalDeployNode) as the per-entry callback. This is the ONE charly/ call site for
// the deploy-state writer (R3): every charly/ deploy-lifecycle path that persists the
// per-host overlay calls this helper instead of deploykit.SaveBundleConfig directly.
//
// Tracked K4-exit inventory — the NAMED exit (K4 unit C RDD spike, core-min wave 3):
// deploykit.SaveBundleConfig itself is already callable by any compiled-in plugin (the
// process-global DeployStateHost registration is shared); this helper + marshalDeployNode
// stay core ONLY because marshalBundleNode's resugarPlan needs pluginPrimaryFor — a
// charly-package-private, registry-derived lookup with no sdk exposure today. Deferred
// until a K4 slice's plugin-side code actually needs to call SaveBundleConfig itself (see
// deploy_nodeform.go's header for the proven HOW — extending spec.ResolvedProject with a
// Primaries map[string]string field).
func saveBundleConfigNodeForm(dc *deploykit.BundleConfig) error {
	return deploykit.SaveBundleConfig(dc, marshalDeployNode)
}
