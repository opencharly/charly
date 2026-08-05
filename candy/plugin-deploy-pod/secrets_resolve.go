package deploypod

import (
	"context"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// secrets_resolve.go — pod-config secret provisioning + hook-secret-env, relocated from
// charly/host_build_pod_config_seams.go's hostBuildPodConfigProvisionSecrets / HookSecretEnv
// (seam-death, this cone). The plugin drives the deploykit secret primitives with the SHARED
// deploykit.CredentialAccessViaExecutor (verb:credential = candy/plugin-secrets — the SAME credential
// drive enc_tunnel_resolve.go / sidecar_resolve.go use) and resolves the secret backend itself via
// kit.LoadRuntimeConfig (sdk/kit, plugin-importable). The former pod-config-provision-secrets /
// pod-config-hook-secret-env HostBuild seams + core secrets.go's ProvisionPodmanSecrets /
// CollectCandySecretAccepts / resolveHookSecretEnv shims are RETIRED.

// resolvePodProvisionSecrets collects candy-owned + credential-backed secrets, provisions them as
// podman secrets, and reports the resolutions + isKeyring flag — the plugin-side port of the former
// hostBuildPodConfigProvisionSecrets seam.
func resolvePodProvisionSecrets(ctx context.Context, ex *sdk.Executor, meta *spec.BoxMetadata, box, instance, runEngine string, autoGen bool, refreshSecret []string) (provisioned []deploykit.CollectedSecret, fallbackEnv []string, resolutions []secretResolution, isKeyring bool, err error) {
	cred := deploykit.CredentialAccessViaExecutor(ctx, ex)
	candyOwned := deploykit.CollectSecretsFromLabels(box, meta.Secret)
	credBacked, dkResolutions := deploykit.CollectCandySecretAccepts(box, instance, meta, credServiceVNC, cred)
	collected := append(append([]deploykit.CollectedSecret{}, candyOwned...), credBacked...)
	collected, _ = deploykit.ApplySecretRefresh(collected, refreshSecret)
	provisioned, fallbackEnv, err = deploykit.ProvisionPodmanSecrets(runEngine, box, instance, collected, autoGen, credServiceVNC, cred)
	if err != nil {
		return nil, nil, nil, false, err
	}
	resolutions = make([]secretResolution, len(dkResolutions))
	for i, r := range dkResolutions {
		resolutions[i] = secretResolution{Name: r.Name, Source: r.Source, Resolved: r.Resolved, Required: r.Required}
	}
	return provisioned, fallbackEnv, resolutions, secretBackendIsKeyring(), nil
}

// secretBackendIsKeyring reports whether the secret backend is keyring-class (the isKeyring flag the
// quadlet KeyringBackend needs) — the plugin-side port of charly/credential_plugin.go's
// resolveSecretBackend, via kit.LoadRuntimeConfig (sdk/kit host-config, plugin-importable).
//
// TRIPWIRE — this predicate LOOKS interchangeable with sdk/deploykit/enc_passphrase.go's
// `usesWaitingBackend`, and it is not. Three people collapsed the two into one and rebuilt this
// gate on the strength of it; the rebuild was reverted.
//
//	usesWaitingBackend  answers "will the mount resolver WAIT rather than fail fast?"  — retry semantics
//	this function       answers "may systemd enable this unit at boot?"                — autostart policy
//
// They compute the same SET (keyring|auto|"") and so agree on those three values, which is what
// makes the confusion so easy. They diverge on exactly one: `config`, where the passphrase may be
// perfectly obtainable — so a capability-shaped gate would grant autostart — but there is nothing
// to wait for, so the resolver correctly fails fast. This gate is right by near-miss, not by
// identity.
//
// Do NOT "fix" that divergence into a source-based capability check. Doing so grants unattended
// boot-time mounting to a deploy whose key sits in cleartext in ~/.config/charly/config.yml, which
// is the configuration the operator asked to WARN about, not to automate further.
//
// The shared SET across the sdk/candy boundary is a genuine R3 duplicate and is filed as such —
// dedupe the set if you like, but keep the two QUESTIONS distinct.
func secretBackendIsKeyring() bool {
	backend := os.Getenv("CHARLY_SECRET_BACKEND")
	if backend == "" {
		if cfg, cerr := kit.LoadRuntimeConfig(); cerr == nil && cfg.SecretBackend != "" {
			backend = cfg.SecretBackend
		} else {
			backend = "auto"
		}
	}
	return backend == "keyring" || backend == "auto" || backend == ""
}

// resolvePodHookSecretEnv resolves the post_enable hook's secret env — the plugin-side port of the
// former hostBuildPodConfigHookSecretEnv seam.
func resolvePodHookSecretEnv(ctx context.Context, ex *sdk.Executor, meta *spec.BoxMetadata, box, instance string) []string {
	return deploykit.ResolveHookSecretEnv(box, instance, meta, credServiceVNC, deploykit.CredentialAccessViaExecutor(ctx, ex))
}
