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
// coreCredentialAccess is the ONLY survivor: the credential-store adapter secrets.go still binds
// (deploykit.GenerateAndStoreSecret / ProvisionPodmanSecrets / CollectCandySecretAccepts /
// ResolveHookSecretEnv). It stays until the secrets seam-death folds it plugin-side too.

// coreCredentialAccess bundles charly-core's ResolveCredential/DefaultCredentialStore adapter
// (credential_plugin.go — itself registry-coupled, connectPluginByWordRef to verb:credential)
// into the deploykit.CredentialAccess shape secret orchestration in sdk/deploykit needs.
func coreCredentialAccess() deploykit.CredentialAccess {
	return deploykit.CredentialAccess{
		Resolve: ResolveCredential,
		Write:   func(service, key, value string) error { return DefaultCredentialStore().Set(service, key, value) },
	}
}
