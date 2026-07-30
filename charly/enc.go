package main

import (
	"github.com/opencharly/sdk/deploykit"
)

// enc.go — the residual encrypted-volume credential-access adapter. Every enc EXECUTION path is
// now plugin-owned: the state-probe/plan builders live in sdk/deploykit (enc_probe.go /
// enc_passphrase.go); the CLI leaves (`charly config status/mount/unmount/passwd`) in
// candy/plugin-pod (enc_cmd.go); the START/STOP lifecycle AND the `charly config setup` ensure/
// unmount both drive verb:enc DIRECTLY via candy/plugin-deploy-pod's own InvokeProvider
// (enc_tunnel_resolve.go's resolvePodEncEnsurePlan/resolvePodEncUnmountPlan + resolve.go /
// config_setup.go) — the former pod-config-enc-mounts HostBuild seam + its core encExecViaPlugin/
// encUnmount/ensureEncryptedMounts/resolveEncPassphrase shims are RETIRED (this cone).
//
// coreCredentialAccess is the survivor: the host credential-store adapter layer_secrets.go's
// resolveCandySecrets injects into deploykit.ResolveSecretForCandy for the pod-overlay build
// (build_overlay.go — the deploy-candy-secrets host seam was DELETED in #55 K4; command:bundle
// resolves secrets plugin-side). The credential STORE itself is plugin-secrets; this is the host
// adapter — floor-M, the same class as credential_plugin.go. (The former secrets.go
// generateAndStoreSecret wrapper is deleted — its layer-secret caller resolves via deploykit
// directly after coneB's layer_secrets→deploykit move.)

// coreCredentialAccess bundles charly-core's ResolveCredential/DefaultCredentialStore adapter
// (credential_plugin.go — itself registry-coupled, connectPluginByWordRef to verb:credential)
// into the deploykit.CredentialAccess shape secret orchestration in sdk/deploykit needs.
func coreCredentialAccess() deploykit.CredentialAccess {
	return deploykit.CredentialAccess{
		Resolve: ResolveCredential,
		Write:   func(service, key, value string) error { return DefaultCredentialStore().Set(service, key, value) },
	}
}
