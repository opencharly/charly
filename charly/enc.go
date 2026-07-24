package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// enc.go — encrypted-volume in-core SHIM (C16a), narrowed by Cutover B unit 2, Cutover B unit
// 6b (the InvokeProvider-generalization family), and wave γ (the config-time CLI port): every
// state-probe/plan-building function (resolveEncVolumeDir/isEncryptedInitialized/
// isEncryptedMounted/fuseAllowOtherEnabled/encPlanFor/loadEncryptedVolume/encServiceFilename/
// removeEncryptedVolumes/encStatus/askPassword) moved to sdk/deploykit (enc_probe.go, unit 2);
// the passphrase-RESOLUTION orchestration moved to sdk/deploykit (enc_passphrase.go, unit 6b);
// and the CLI-invoked `charly config status/mount/unmount/passwd` LEAVES (encMount/encStatus/
// encPasswd + their mount-side passphrase resolution: resolveEncPassphraseForMount/
// awaitKeyringUnlockViaPlugin) moved wholesale to candy/plugin-pod (enc_cmd.go, wave γ) — that
// plugin already holds a real reverse-channel *sdk.Executor with InvokeProvider capability
// (mirroring candy/plugin-deploy-pod/lifecycle.go's ALREADY-LIVE start/stop pattern), so those
// leaves dispatch verb:enc / verb:credential DIRECTLY, no core round-trip needed.
//
// What remains genuinely core-only: it is STILL called from the pod-config-enc-mounts seam
// (host_build_pod_config_seams.go, the `charly config setup`/Setup-orchestration family — a
// DIFFERENT, out-of-this-wave family per the wave γ scoping map). The START-lifecycle plan
// builders (former pod_lifecycle_resolve.go's resolvePodEncEnsure/resolvePodEncUnmount/
// resolvePodTunnel) moved to candy/plugin-deploy-pod (enc_tunnel_resolve.go, wave γ — the SAME
// InvokeProvider extension this file's header already describes for the CLI leaves), so
// pod_lifecycle_resolve.go is DELETED; the Setup family below is the only remaining caller of
// this file's core-only surface: encExecViaPlugin (providerRegistry.resolve + invokeTyped — the
// host registry itself, clause-M), coreCredentialAccess (the credential-store adapter
// resolveEncPassphrase/ensureEncryptedMounts still need), resolveEncPassphrase (the ensure-path,
// non-mount passphrase resolution), encUnmount (still called by hostBuildPodConfigEncMounts), and
// ensureEncryptedMounts (the `charly start` transparent-setup path).

// coreCredentialAccess bundles charly-core's ResolveCredential/DefaultCredentialStore adapter
// (credential_plugin.go — itself registry-coupled, connectPluginByWordRef to verb:credential)
// into the deploykit.CredentialAccess shape enc/secret orchestration in sdk/deploykit needs.
func coreCredentialAccess() deploykit.CredentialAccess {
	return deploykit.CredentialAccess{
		Resolve: ResolveCredential,
		Write:   func(service, key, value string) error { return DefaultCredentialStore().Set(service, key, value) },
	}
}

// encExecViaPlugin resolves verb:enc and Invokes its OpExecute with the host-prelifted
// plan. plugin-enc is compiled-in, so this is an in-proc JSON envelope (no socket) —
// the passphrase never leaves the process. Mirrors egress.go / k8s_generate.go.
func encExecViaPlugin(in spec.EncExecInput) error {
	// Preflight: the mount methods run `gocryptfs -allow_other`, which fusermount3 rejects
	// unless `user_allow_other` is set in fuse.conf. Fail fast with the exact fix (before any
	// volume mounts partway) instead of surfacing the raw fusermount3 error mid-plan.
	if in.Method == spec.EncMethodMount || in.Method == spec.EncMethodEnsure {
		if !deploykit.FuseAllowOtherEnabled() {
			return fmt.Errorf("encrypted volumes require 'user_allow_other' in /etc/fuse.conf (gocryptfs -allow_other, for rootless-podman keep-id access) — enable it with: echo user_allow_other | sudo tee -a /etc/fuse.conf")
		}
	}
	prov, ok := providerRegistry.resolve(ClassVerb, "enc")
	if !ok {
		return fmt.Errorf("enc plugin (verb:enc) not registered — charly built without candy/plugin-enc")
	}
	reply, err := invokeTyped[spec.EncExecInput, spec.EncExecReply](context.Background(), prov, "enc", OpExecute, in)
	if err != nil {
		return fmt.Errorf("enc invoke: %w", err)
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	return nil
}

// resolveEncPassphrase resolves the gocryptfs passphrase for an image (thin wrapper —
// the orchestration lives in deploykit.ResolveEncPassphrase, unit 6b).
func resolveEncPassphrase(boxName string, autoGenerate bool) (string, error) {
	return deploykit.ResolveEncPassphrase(boxName, autoGenerate, coreCredentialAccess())
}

// encUnmount unmounts encrypted volumes for an image. Still core-resident: called by
// hostBuildPodConfigEncMounts (host_build_pod_config_seams.go), the `charly config setup`
// Setup-orchestration family — a different family than wave γ's CLI-leaf port, not yet folded in.
// If volume is non-empty, only that volume is unmounted.
func encUnmount(boxName, instance, volume string) error {
	plan, err := deploykit.EncPlanFor(boxName, instance, volume, deploykit.DeployStorageDir(boxName, instance))
	if err != nil {
		return err
	}
	return encExecViaPlugin(spec.EncExecInput{
		Method:  spec.EncMethodUnmount,
		ImageID: "charly-" + boxName,
		BoxName: boxName,
		Volumes: plan,
	})
}

// ensureEncryptedMounts auto-initializes and mounts encrypted volumes as needed.
// Called by charly start to transparently handle encrypted volume setup without
// requiring the user to run charly config init/mount manually first.
// Resolves the enc passphrase once (keyring → config → interactive prompt).
func ensureEncryptedMounts(boxName, instance string, autoGenerate bool) error {
	// The ensure path historically derives the scope-unit from the bare box name
	// (mount/unmount/passwd use deployStorageDir); identical for the common
	// empty-instance case. Preserved exactly by this cutover.
	plan, err := deploykit.EncPlanFor(boxName, instance, "", boxName)
	if err != nil || len(plan) == 0 {
		return nil // no encrypted mounts configured (load error swallowed, as before)
	}

	anyNotReady := false
	for _, m := range plan {
		if !m.Initialized || !m.Mounted {
			anyNotReady = true
			break
		}
	}
	if !anyNotReady {
		return nil
	}

	passphrase, err := resolveEncPassphrase(boxName, autoGenerate)
	if err != nil {
		return fmt.Errorf("resolving enc passphrase for %s: %w", boxName, err)
	}
	return encExecViaPlugin(spec.EncExecInput{
		Method:     spec.EncMethodEnsure,
		ImageID:    "charly-" + boxName,
		BoxName:    boxName,
		Passphrase: passphrase,
		Volumes:    plan,
	})
}
