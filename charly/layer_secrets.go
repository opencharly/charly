package main

// layer_secrets.go — the deploy-scoped candy scanner backing the "deploy-candy-secrets" seam
// (host_build_deploy_candy_secrets.go). The secret_requires:/secret_accepts: RESOLUTION logic
// this file used to carry (ensureCandySecret/ResolveCandySecret/ResolveSecretForCandy) had no
// core-only dependency beyond the credential-store access GenerateAndStoreSecret already took as
// an INJECTED CredentialAccess — relocated to sdk/deploykit/secret_candy_resolve.go (#118
// coneB-p8bremainder), taking coreCredentialAccess() (enc.go) as that injected value at its call
// site (host_build_deploy_candy_secrets.go's resolveCandySecrets). The ONE thing genuinely left —
// CandyForPlan, below — needs ScanAllCandyWithConfig + *Config, both core-only (the loader).
//
// P13-KERNEL fold-in: InjectSecretsIntoPlans (the pure plan-injection half) relocated to
// sdk/deploykit/secret_declare.go earlier.

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// CandyForPlan reloads the candy map and returns the ordered spec.CandyReader
// slice covered by the given plans (both CandiesIncluded for image-level
// plans and per-plan Candy for candy-only plans). Used by deploy-add to
// call ResolveSecretForCandy + RetrieveCandyArtifacts.
func CandyForPlan(plans []*deploykit.InstallPlan, dir string, cfg *Config) ([]spec.CandyReader, error) {
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ordered []spec.CandyReader
	pick := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		if l, ok := layers[name]; ok {
			ordered = append(ordered, l)
		}
	}
	for _, p := range plans {
		for _, name := range p.CandiesIncluded {
			pick(name)
		}
		pick(p.Candy)
	}
	return ordered, nil
}
