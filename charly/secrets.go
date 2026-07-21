package main

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// secrets.go — container secret collection + podman secret provisioning THIN CORE SHIMS.
// Narrowed by Cutover B unit 6b (the InvokeProvider-generalization family): the
// orchestration (generateAndStoreSecret/ProvisionPodmanSecrets/resolveSecretValue/
// CollectCandySecretAccepts/resolveHookSecretEnv — each called ResolveCredential/
// DefaultCredentialStore directly or transitively, the core provider registry) moved to
// sdk/deploykit/secret_provision.go, taking an injected deploykit.CredentialAccess instead
// (the SAME shape enc.go's coreCredentialAccess bundles) — charly-core and a future
// out-of-process caller now share ONE implementation (R3). The portable leaf helpers (token
// generation, password prompting, podman-secret CRUD, label reconstruction, credential-key
// mapping) already lived in sdk/deploykit/secret_probe.go (Cutover B-1).

// generateAndStoreSecret is the thin core wrapper — the orchestration lives in
// deploykit.GenerateAndStoreSecret (unit 6b). Used by ensureCandySecret (layer_secrets.go).
func generateAndStoreSecret(service, key string) (val, source string) {
	return deploykit.GenerateAndStoreSecret(service, key, coreCredentialAccess())
}

// ProvisionPodmanSecrets creates podman secrets from the credential store (thin core
// wrapper — the orchestration lives in deploykit.ProvisionPodmanSecrets, unit 6b).
func ProvisionPodmanSecrets(engine, boxName, instance string, secrets []deploykit.CollectedSecret, autoGenerate bool) (provisioned []deploykit.CollectedSecret, fallbackEnv []string, err error) {
	return deploykit.ProvisionPodmanSecrets(engine, boxName, instance, secrets, autoGenerate, CredServiceVNC, coreCredentialAccess())
}

// resolveSecretValue is the thin core wrapper — the orchestration lives in
// deploykit.ResolveSecretValue (unit 6b).
func resolveSecretValue(s deploykit.CollectedSecret, boxName, instance string) (value, source string) {
	return deploykit.ResolveSecretValue(s, boxName, instance, CredServiceVNC, ResolveCredential)
}

// SecretResolution is the core-facing alias of the moved orchestration's result type
// (deploykit.SecretResolution, unit 6b) — preserves the SecretResolution name existing
// callers (Step 5/6's checkMissingSecretRequires) use.
type SecretResolution = deploykit.SecretResolution

// CollectCandySecretAccepts is the thin core wrapper — the orchestration lives in
// deploykit.CollectCandySecretAccepts (unit 6b).
func CollectCandySecretAccepts(boxName, instance string, meta *spec.BoxMetadata) (collected []deploykit.CollectedSecret, resolutions []SecretResolution) {
	return deploykit.CollectCandySecretAccepts(boxName, instance, meta, CredServiceVNC, coreCredentialAccess())
}

// resolveHookSecretEnv is the thin core wrapper — the orchestration lives in
// deploykit.ResolveHookSecretEnv (unit 6b).
func resolveHookSecretEnv(boxName, instance string, meta *spec.BoxMetadata) []string {
	return deploykit.ResolveHookSecretEnv(boxName, instance, meta, CredServiceVNC, coreCredentialAccess())
}

// LabelSecretEntry represents a secret requirement in an OCI image label.
// Only metadata is stored — never the secret value. CUE-sourced in spec (boxmetadata.cue, P2B)
// + aliased in-place; carried on spec.BoxMetadata.Secret (ai.opencharly.secret).
type LabelSecretEntry = spec.LabelSecretEntry

// CollectedSecret (a fully-resolved secret ready for provisioning + the quadlet
// Secret= directive) is a deploykit resolved-runtime type, referenced directly
// as deploykit.CollectedSecret — it moved to sdk/deploykit with the pod config-write
// mechanism (P11). Service/Key/RotateOnConfig are populated by CollectCandySecretAccepts
// for credential-store-backed secrets (secret_accepts / secret_requires); zero for
// candy-owned secrets (the deploykit.CollectSecretsFromLabels path). Service/Key override
// the ResolveCredential lookup (defaults Service="charly/secret", Key=SecretName);
// RotateOnConfig=true makes ProvisionPodmanSecrets bypass the deploykit.PodmanSecretExists
// short-circuit and rm+recreate every reconcile (candy-owned secrets keep it false —
// you cannot re-init a live postgres cluster with a rotated password).
