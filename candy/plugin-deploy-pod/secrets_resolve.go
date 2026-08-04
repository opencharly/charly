package deploypod

import (
	"context"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// secrets_resolve.go — pod-config secret provisioning + hook-secret-env, relocated from
// charly/host_build_pod_config_seams.go's hostBuildPodConfigProvisionSecrets / HookSecretEnv
// (seam-death, this cone). The plugin drives the deploykit secret primitives with the SHARED
// deploykit.CredentialAccessViaExecutor (verb:credential = candy/plugin-secrets — the SAME credential
// drive enc_tunnel_resolve.go / sidecar_resolve.go use). The former pod-config-provision-secrets /
// pod-config-hook-secret-env HostBuild seams + core secrets.go's ProvisionPodmanSecrets /
// CollectCandySecretAccepts / resolveHookSecretEnv shims are RETIRED.
//
// This file is also the "secret layer" the quadlet autostart decision is computed in. It reports
// a CAPABILITY — can this deploy's passphrase be obtained with no human present — never a
// backend name, so sdk/deploykit's emitters stay kind-blind and a backend added tomorrow earns
// autostart on merit rather than on what it is called.

// resolvePodProvisionSecrets collects candy-owned + credential-backed secrets, provisions them as
// podman secrets, and reports the resolutions — the plugin-side port of the former
// hostBuildPodConfigProvisionSecrets seam.
func resolvePodProvisionSecrets(ctx context.Context, ex *sdk.Executor, meta *spec.BoxMetadata, box, instance, runEngine string, autoGen bool, refreshSecret []string) (provisioned []deploykit.CollectedSecret, fallbackEnv []string, resolutions []secretResolution, err error) {
	cred := deploykit.CredentialAccessViaExecutor(ctx, ex)
	candyOwned := deploykit.CollectSecretsFromLabels(box, meta.Secret)
	credBacked, dkResolutions := deploykit.CollectCandySecretAccepts(box, instance, meta, credServiceVNC, cred)
	collected := append(append([]deploykit.CollectedSecret{}, candyOwned...), credBacked...)
	collected, _ = deploykit.ApplySecretRefresh(collected, refreshSecret)
	provisioned, fallbackEnv, err = deploykit.ProvisionPodmanSecrets(runEngine, box, instance, collected, autoGen, credServiceVNC, cred)
	if err != nil {
		return nil, nil, nil, err
	}
	resolutions = make([]secretResolution, len(dkResolutions))
	for i, r := range dkResolutions {
		resolutions[i] = secretResolution{Name: r.Name, Source: r.Source, Resolved: r.Resolved, Required: r.Required}
	}
	return provisioned, fallbackEnv, resolutions, nil
}

// resolveEncUnattendedUnlock reports whether this deploy's encrypted volumes can be unlocked at
// boot with no human running a command — the QuadletConfig.UnattendedUnlock input.
//
// hasEncrypted short-circuits it: a deploy with no encrypted volumes has nothing to unlock and
// autostarts unconditionally, so asking would be a pointless credential-store round trip (and on
// a locked keyring, a needless probe) for an answer the emitters never read.
func resolveEncUnattendedUnlock(ctx context.Context, ex *sdk.Executor, box string, hasEncrypted bool) bool {
	if !hasEncrypted {
		return false
	}
	return deploykit.EncPassphraseUnattended(box, deploykit.CredentialAccessViaExecutor(ctx, ex))
}

// resolvePodHookSecretEnv resolves the post_enable hook's secret env — the plugin-side port of the
// former hostBuildPodConfigHookSecretEnv seam.
func resolvePodHookSecretEnv(ctx context.Context, ex *sdk.Executor, meta *spec.BoxMetadata, box, instance string) []string {
	return deploykit.ResolveHookSecretEnv(box, instance, meta, credServiceVNC, deploykit.CredentialAccessViaExecutor(ctx, ex))
}
