package main

// deploy_add_shared.go — generic helpers shared across the per-kind
// UnifiedDeployTarget.Add methods (R3). Each one captures a step that
// was copy-pasted across the old per-kind deploy bodies; now there is
// ONE implementation, called from local/vm/pod Add.
//
// Ordering is load-bearing and preserved exactly:
//   - secrets are injected into the plans BEFORE any Emit (a candy's
//     OpStep body references the resolved token via env).
//   - artifactEnv is secretEnv first, then node.Env lines overlaid
//     (last-wins) — so a deploy entry's explicit env: overrides an
//     auto-generated secret of the same name.

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/kit"
)

// prepareCandySecrets resolves the candies backing `plans`, computes their
// secret_requires / secret_accepts env (auto-generating + persisting any
// missing required token), and injects it into every plan's TaskSteps
// BEFORE emission. Returns the resolved candy list (the caller reuses it
// for artifact retricheck) and the secret env map.
//
// Shared by the external substrate apply path AND each lifecycle hook's PrepareVenue
// (vm: before the in-guest walk; pod: before the host-side overlay build) — the paths that
// previously each ran CandyForPlan + ResolveSecretForCandy + InjectSecretsIntoPlans inline.
func prepareCandySecrets(plans []*deploykit.InstallPlan, dir string) ([]spec.CandyReader, map[string]string, error) {
	candyList, err := CandyForPlan(plans, dir, nil)
	if err != nil {
		return nil, nil, err
	}
	secretEnv := ResolveSecretForCandy(candyList)
	deploykit.InjectSecretsIntoPlans(plans, secretEnv)
	return candyList, secretEnv, nil
}

// loadDeployPlugins connects the project's OUT-OF-TREE plugin candies BEFORE a
// deploy verb resolves the target, so a deploy whose SUBSTRATE / step / verb is
// served by an external provider resolves out-of-process. It scans the WHOLE
// project (ScanAllCandyWithConfigOpts) but loads ONLY the plugin candies the
// deployment REFERENCES (perf-scoped): collectReferencedPluginWords unions the
// candy/box plans + candy external_builder selections, and deployNodePluginContext
// adds the deploy's OWN references — its substrate kind + the inline Op.Plugin words
// in its FLATTENED bed plan (members hoisted into the root node.Plan). A plugin candy
// none of whose providers is referenced is skipped (no wasted host build/connect); a
// REFERENCED one always loads (the reference set is collected COMPLETE — over-load
// safe, never under). The deployment's add_candy: candies + any caller-supplied extra
// refs are ADDED to the scan via ExtraCandyRefs (so a REMOTE composed plugin not in
// the local scan is fetched too, and its words are then collected from its plan). The
// SAME scan + loadProjectPlugins the check runner uses (resolveCheckRunnerContext) and
// the bundle-add path uses — so bundle add / bundle del / charly update all connect a
// deployment's plugins identically (R3). For an external deploy SUBSTRATE this is what
// turns the pre-scanned placeholder word into a connected grpcProvider that
// ResolveTarget can route to. Discovery and build/connect failures retain their original cause and
// abort dispatch; warning-and-continue used to mask a failed build as a downstream missing provider.
func loadDeployPlugins(dir, deployName string, extraAddCandy []string) error {
	cfg, cerr := LoadConfig(dir)
	if cerr != nil {
		return fmt.Errorf("load plugin configuration: %w", cerr)
	}
	addCandy, refWords := deployNodePluginContext(dir, deployName)
	extra := append(append([]string(nil), extraAddCandy...), addCandy...)
	candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, ResolveOpts{ExtraCandyRefs: extra})
	if scanErr != nil {
		return fmt.Errorf("scan deploy plugins: %w", scanErr)
	}
	if candyMap == nil {
		return nil
	}
	refs := collectReferencedPluginWords(candyMap, cfg.Box, refWords)
	if perr := loadProjectPlugins(context.Background(), candyMap, refs); perr != nil {
		return fmt.Errorf("load deploy plugins: %w", perr)
	}
	return nil
}

// artifactRegisterHandlers maps a candy artifact's declared `register:` hint (the
// #CandyArtifact.Register field, SDD-sourced in sdk/schema/candy.cue) to the
// post-retrieve processing it triggers. Word-keyed and data-driven (R3): a candy
// declares the hint on its OWN artifact entry (k3s-server's kubeconfig artifact
// declares `register: kubeconfig`) — adding a new registration kind means adding ONE
// map entry here, never a hardcoded candy-name special-case.
var artifactRegisterHandlers = map[string]func(artifactKey, deployName string) error{
	"kubeconfig": K3sPostProvision,
}

// retrieveArtifactsAndK3s pulls back the candies' published artifacts via the same
// executor the deploy used, then runs whichever post-retrieve registration handlers
// the retrieved candies' artifact declarations name (e.g. the k3s-server kubeconfig's
// `register: kubeconfig` — merge kubeconfig + register ClusterProfile). No-op under
// DryRun.
//
// Shared by the local deploy target.Add / the vm deploy's Add path. artifactKey is
// ENTITY-scoped (the artifact retrieve dir + the shared per-VM k3s cluster cache/context);
// deployName is the real per-deploy (domain) identity the k3s port-forward lookup keys off.
func retrieveArtifactsAndK3s(ctx context.Context, exec deploykit.DeployExecutor, candyList []spec.CandyReader, artifactKey, deployName string, artifactEnv map[string]string, opts deploykit.EmitOpts) error {
	if opts.DryRun {
		return nil
	}
	if err := deploykit.RetrieveCandyArtifacts(ctx, exec, candyList, kit.SanitizeDeployName(artifactKey), artifactEnv, opts, loadedReadiness()); err != nil {
		return err
	}
	for register := range deploykit.CandyArtifactRegisters(candyList) {
		handler, ok := artifactRegisterHandlers[register]
		if !ok {
			continue
		}
		if err := handler(artifactKey, deployName); err != nil {
			return err
		}
	}
	return nil
}

// registerEphemeralIfMarked runs the ephemeral lifecycle registration
// (systemd transient timer + parent-detection) when the dispatch-merged
// node is ephemeral. FIRST action in vm/pod/k8s Add (panic-safe TTL
// ordering). Consumes the merged node — does NOT re-read charly.yml.
// Registration failure is logged (not fatal), matching the prior run*
// behavior; the returned error is always nil today but kept for symmetry.
func registerEphemeralIfMarked(node *spec.BundleNode, name string) {
	if node == nil || !node.IsEphemeral() {
		return
	}
	if _, regErr := RegisterEphemeralLifecycle(node, name); regErr != nil {
		fmt.Fprintf(os.Stderr, "warning: ephemeral lifecycle registration: %v\n", regErr)
	}
}
