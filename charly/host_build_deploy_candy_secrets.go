package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// host_build_deploy_candy_secrets.go — the "deploy-candy-secrets" F10 host-builder (Cone A shape
// 3): the genuine floor-M half of the former core-resident prepareCandySecrets (charly's own
// deleted deploy_add_shared.go) — scanning the project for the candies backing a compiled plan
// set (CandyForPlan → ScanAllCandyWithConfig, a K1/K4 loader-migration-inventory mechanism a
// plugin cannot run itself) and resolving their secret_requires:/secret_accepts: env against the
// credential store (ResolveSecretForCandy, itself already a core→plugin adapter to
// verb:credential). candy/plugin-bundle's handleDeployApply calls this ONCE, BEFORE the substrate
// dispatch, then injects the returned secret_env into its OWN in-proc plans via the
// already-portable deploykit.InjectSecretsIntoPlans — no plan mutation crosses the wire (only the
// resolved env does). register_hints (deploykit.CandyArtifactRegisters, computed from the SAME
// candy scan) lets the plugin decide, AFTER the substrate dispatch + artifact retrieval, whether
// to InvokeProvider verb:kube's k3s-post-provision — data-driven, never a per-candy-name special
// case.
//
// resolveCandySecrets (below) is the SHARED host-side core both this seam AND
// build_overlay.go's hostBuildOverlay call — the pod-overlay build path resolves + injects
// secrets DIRECTLY (it is already host-side code with no round trip to pay), while this seam
// hands the resolved values back across the wire for the plugin-side deploy-add path to inject
// itself (R3 — one resolver, two callers).
const deployCandySecretsBuilderKind = "deploy-candy-secrets"

func hostBuildDeployCandySecrets(_ context.Context, req spec.DeployCandySecretsRequest, _ buildEngineContext) (spec.DeployCandySecretsReply, error) {
	plans, err := decodePlanViews(req.PlansJSON)
	if err != nil {
		return spec.DeployCandySecretsReply{}, fmt.Errorf("deploy-candy-secrets: %w", err)
	}
	secretEnv, hints, err := resolveCandySecrets(plans, req.Dir)
	if err != nil {
		return spec.DeployCandySecretsReply{}, err
	}
	return spec.DeployCandySecretsReply{SecretEnv: secretEnv, RegisterHints: hints}, nil
}

// resolveCandySecrets scans dir for the candies backing plans (CandyForPlan) and resolves their
// secret_requires:/secret_accepts: env (deploykit.ResolveSecretForCandy, supplying coreCredentialAccess
// as the injected CredentialAccess — enc.go) + the distinct artifact register hints present
// (deploykit.CandyArtifactRegisters) — the shared core both hostBuildDeployCandySecrets
// and build_overlay.go's hostBuildOverlay call directly.
func resolveCandySecrets(plans []*spec.InstallPlan, dir string) (map[string]string, []string, error) {
	candyList, err := CandyForPlan(plans, dir, nil)
	if err != nil {
		return nil, nil, err
	}
	secretEnv := deploykit.ResolveSecretForCandy(candyList, coreCredentialAccess())
	registers := spec.CandyArtifactRegisters(candyList)
	hints := make([]string, 0, len(registers))
	for register := range registers {
		hints = append(hints, register)
	}
	return secretEnv, hints, nil
}

// decodePlanViews unmarshals a wire []spec.InstallPlanView and re-materializes each into a
// *spec.InstallPlan (== *spec.InstallPlan) — the shared decode both new HostBuild seams in
// this cutover need, mirroring candy/plugin-bundle/deploy_target.go's own handleDeployApply decode
// (R3, byte-identical view→plan round trip).
func decodePlanViews(plansJSON json.RawMessage) ([]*spec.InstallPlan, error) {
	var views []spec.InstallPlanView
	if len(plansJSON) > 0 {
		if err := json.Unmarshal(plansJSON, &views); err != nil {
			return nil, fmt.Errorf("decode plans: %w", err)
		}
	}
	plans := make([]*spec.InstallPlan, 0, len(views))
	for _, v := range views {
		p, err := spec.PlanFromView(v)
		if err != nil {
			return nil, fmt.Errorf("rematerialize plan: %w", err)
		}
		plans = append(plans, p)
	}
	return plans, nil
}

var _ = func() bool {
	registerHostBuilder(deployCandySecretsBuilderKind, typedHostBuilder(deployCandySecretsBuilderKind, hostBuildDeployCandySecrets))
	return true
}()
