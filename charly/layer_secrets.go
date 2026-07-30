package main

// layer_secrets.go — the deploy-scoped candy scanner + candy-secret RESOLVER used HOST-SIDE by the
// pod-overlay build (build_overlay.go's hostBuildOverlay). CandyForPlan needs ScanAllCandyWithConfig
// + *Config (both core-only, the loader), so it stays host-resident; the pure plan→candy SELECTION
// it applies is the shared deploykit.SelectCandiesForPlans (R3, the SAME pick candy/plugin-bundle
// runs plugin-side over the resolved-project envelope's candy set — #55 K4). The secret_requires:/
// secret_accepts: RESOLUTION (ResolveSecretForCandy) lives in sdk/deploykit; resolveCandySecrets
// below is the thin host wrapper feeding it CandyForPlan's scan + coreCredentialAccess (enc.go).
//
// #55 K4 (this cone): command:bundle's deploy-add path resolves candy secrets + retrieves artifacts
// PLUGIN-SIDE (candy/plugin-bundle/secrets_artifacts.go — envelope candy set + verb:credential
// CredentialAccess + deploykit.RetrieveCandyArtifacts over its live venue), so the former
// "deploy-candy-secrets" / "deploy-artifacts-retrieve" host seams are DELETED. resolveCandySecrets
// stays only for build_overlay.go's pod-overlay build (already host-side code, no round trip to pay).

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// CandyForPlan reloads the candy map (ScanAllCandyWithConfig — core-only loader) and returns the
// ordered spec.CandyReader slice covered by the given plans, via the shared
// deploykit.SelectCandiesForPlans pick (CandiesIncluded topo order + per-plan Candy).
func CandyForPlan(plans []*spec.InstallPlan, dir string, cfg *Config) ([]spec.CandyReader, error) {
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return nil, err
	}
	return deploykit.SelectCandiesForPlans(plans, layers), nil
}

// resolveCandySecrets scans dir for the candies backing plans (CandyForPlan) and resolves their
// secret_requires:/secret_accepts: env (deploykit.ResolveSecretForCandy, supplying coreCredentialAccess
// as the injected CredentialAccess — enc.go) + the distinct artifact register hints present
// (spec.CandyArtifactRegisters). Host-side; the pod-overlay build (build_overlay.go's hostBuildOverlay)
// is its sole caller — the deploy-add path resolves these PLUGIN-SIDE now (#55 K4).
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
