package main

// layer_secrets.go — resolver for the candy manifest `secret_requires:` and
// `secret_accepts:` when a candy is being applied via host / vm / ssh deploy
// targets (the non-container install-plan flow).
//
// Container targets have their own path (ProvisionPodmanSecrets, see
// secrets.go) that mounts secrets as podman secrets / env at container-run
// time. That path runs AFTER build and does not inject env into the candy's
// build-time tasks. For install-plan-based targets, the candy's tasks run
// directly on the deploy target, so the credential-store value must be
// resolved on the operator side and passed through as env on the step.
//
// Resolution policy (post 2026-05-06 cutover): `secret_requires:` entries
// auto-generate a 32-byte hex token via DefaultCredentialStore.Set when
// missing everywhere (env + store). `secret_accepts:` entries fall back to
// dep.Default when missing, never auto-generate. The auto-generation is
// race-free across multiple candies declaring the same secret because
// DefaultCredentialStore is cached via sync.Once and the first caller's
// Set is visible to the second caller's ResolveCredential.
//
// P13-KERNEL fold-in: InjectSecretsIntoPlans (the ONE genuinely pure function
// in this file — no credential-store or project-loader dependency) relocated
// to sdk/deploykit/secret_declare.go. Every other function here routes through
// DefaultCredentialStore/ResolveCredential (provider-registry-coupled) or
// ScanAllCandyWithConfig (loader-coupled) and stays charly-core, registered
// FINAL/K5 credential-family inventory (see ensureCandySecret's own header).

import (
	"maps"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// ensureCandySecret resolves a secret_requires/secret_accepts EnvDependency
// against the credential store. For required deps that miss everywhere
// (env, store), generates a 32-byte hex token, persists via
// DefaultCredentialStore, and returns the new value. For optional deps
// that miss, returns "" with source classification from ResolveCredential
// so the caller can fall back to dep.Default if set.
//
// The Key field on an EnvDependency follows the format "<service>/<key>"
// and must start with "charly/" (enforced by validate.go). When Key is empty,
// the default lookup is service="charly/secret", key=Name.
//
// Race-free across multiple candies declaring the same secret: the first
// caller's store.Set lands in the active backend (keyring/config
// fallback per credential_store.go DefaultCredentialStore); the second
// caller's ResolveCredential reads the persisted value. All callers in
// one process share the cached singleton.
//
// TRACKED FINAL/K5 EXIT (DEPLOY-wave W2 audit, 2026-07-20): DefaultCredentialStore/
// ResolveCredential route through the core provider registry (same verb-dispatch
// coupling as enc.go/secrets.go — see their headers); CandyForPlan (below) additionally
// takes *Config, the core-only project type. Registered FINAL/K5 alongside enc.go/
// secrets.go's InvokeProvider rewrite, not this wave.
func ensureCandySecret(dep spec.EnvDependency, required bool) (val, source string) {
	service, key := "charly/secret", dep.Name
	if dep.Key != "" {
		if idx := strings.LastIndex(dep.Key, "/"); idx > 0 {
			service = dep.Key[:idx]
			key = dep.Key[idx+1:]
		}
	}
	// Pass dep.Name as envVar so an operator can override the persisted
	// value via `export K3S_CLUSTER_TOKEN=…` before invoking deploy
	// (matches the ResolveCredential pattern used elsewhere).
	val, source = ResolveCredential(dep.Name, service, key, "")
	if val != "" {
		return val, source
	}
	if !required {
		return "", source
	}
	return generateAndStoreSecret(service, key)
}

// ResolveCandySecret walks the candy's secret_requires + secret_accepts
// and resolves each via the credential store. Required entries that miss
// everywhere auto-generate a 32-byte hex token (see ensureCandySecret).
// Optional `secret_accepts:` entries that miss fall back to dep.Default.
//
// Returns the env map; never returns an error. The auto-generate policy
// guarantees every `secret_requires:` resolves to a non-empty value.
// Takes spec.CandyReader (the read-only interface every scanned candy is wrapped
// into, W9) rather than a concrete type — this function needs only the
// SecretRequire/SecretAccept accessors.
func ResolveCandySecret(layer spec.CandyReader) map[string]string {
	env := map[string]string{}
	if layer == nil {
		return env
	}

	for _, dep := range layer.SecretRequire() {
		val, _ := ensureCandySecret(dep, true)
		env[dep.Name] = val
	}

	for _, dep := range layer.SecretAccept() {
		val, _ := ensureCandySecret(dep, false)
		if val == "" && dep.Default != "" {
			env[dep.Name] = dep.Default
			continue
		}
		if val != "" {
			env[dep.Name] = val
		}
	}

	return env
}

// ResolveSecretForCandy is the batch variant used when multiple candies in
// a single deploy share secret_requires — their resolution results merge
// into one env map, with candy-order precedence (later candies win on
// duplicate names, matching the existing generate.go `secretRequiresMap`
// semantics in the label-emission path).
func ResolveSecretForCandy(layers []spec.CandyReader) map[string]string {
	env := map[string]string{}
	for _, l := range layers {
		maps.Copy(env, ResolveCandySecret(l))
	}
	return env
}

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
