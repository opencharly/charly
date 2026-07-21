package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// enc.go — encrypted-volume in-core SHIM (C16a), narrowed by Cutover B unit 2 and further
// by Cutover B unit 6b (the InvokeProvider-generalization family): every state-probe/
// plan-building function (resolveEncVolumeDir/isEncryptedInitialized/isEncryptedMounted/
// fuseAllowOtherEnabled/encPlanFor/loadEncryptedVolume/encServiceFilename/
// removeEncryptedVolumes/encStatus/askPassword) moved to sdk/deploykit (enc_probe.go, unit 2),
// and the passphrase-RESOLUTION orchestration (resolveEncPassphrase/
// resolveEncPassphraseForMount/resolveEncPassphraseForMountWithResolver/retryUnavailable/
// encNotStoredError) moved to sdk/deploykit (enc_passphrase.go, unit 6b) — it takes an
// injected deploykit.CredentialAccess instead of calling ResolveCredential/
// DefaultCredentialStore by name, so charly-core's thin wrappers below and a future
// out-of-process caller share ONE implementation (R3). What remains genuinely core-only:
// encExecViaPlugin (providerRegistry.resolve + invokeTyped — the host registry itself,
// clause-M) and awaitKeyringUnlockViaPlugin (needs the CONCRETE core CredentialStore's
// awaitUnlock/credentialAwaiter — threaded into deploykit's function as an injected
// `waiter` closure, exactly like the resolver/reset closures it already took).

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

// resolveEncPassphraseForMount resolves the gocryptfs passphrase with backend-aware and
// failure-aware retry behavior (thin wrapper — the orchestration lives in
// deploykit.ResolveEncPassphraseForMount, unit 6b). Defect D fix (preserved): the previous
// implementation polled forever on src=="default" and had no deadline, so a misconfigured
// keyring + TimeoutStartSec=0 quadlet was unrecoverable without manual intervention.
// source="unavailable" is bounded at deploykit.EncMountDeadline; source="locked" waits
// indefinitely via DBus signal subscription (zero CPU between events, via
// awaitKeyringUnlockViaPlugin below) until the user unlocks the keyring; source="default"
// fails immediately.
func resolveEncPassphraseForMount(boxName string) (string, error) {
	backend := resolveSecretBackend()
	return deploykit.ResolveEncPassphraseForMount(boxName, backend, coreCredentialAccess(), resetDefaultCredentialStore, awaitKeyringUnlockViaPlugin)
}

// awaitKeyringUnlockViaPlugin is the production keyring-unlock waiter wired into
// resolveEncPassphraseForMount (source="locked"). The Secret Service (godbus) lives
// OUT-OF-PROCESS in candy/plugin-secrets, so the event-driven DBus PropertiesChanged
// subscription + the backstop re-probe run THERE: this delegates to the active store's
// awaitUnlock, which RPCs verb:credential `await-unlock` and BLOCKS until the keyring
// unlocks or ctx is cancelled. ctx carries the core's SIGINT/SIGTERM cancellation, which
// gRPC propagates to the plugin's Invoke so `systemctl stop` ends the wait cleanly.
//
// The resolver/reset closures are unused here — the plugin re-probes its OWN store across
// the process boundary; they remain on the waiter seam for the in-core retry paths and the
// test fakes. A store that cannot await (only a non-keyring test fake reaches this, since
// the production store is always the keyring-capable pluginCredentialStore) is a loud error.
func awaitKeyringUnlockViaPlugin(
	ctx context.Context,
	boxName string,
	_ func() (string, string),
	_ func(),
) (string, string, error) {
	store := DefaultCredentialStore()
	aw, ok := store.(credentialAwaiter)
	if !ok {
		return "", "", fmt.Errorf("active credential store %q cannot wait for keyring unlock", store.Name())
	}
	return aw.awaitUnlock(ctx, "charly/enc", boxName)
}

// encMount mounts encrypted volumes for an image.
// If volume is non-empty, only that volume is mounted.
// Uses resolveEncPassphraseForMount which waits for keyring unlock under systemd.
//
// Fast path: if every requested volume is already mounted (scope units still
// alive from a previous mount), return nil without querying the credential
// store at all. This makes service restarts resilient to keyring breakage —
// the most common operational case is "restart when everything is still
// mounted", and it has no passphrase dependency.
func encMount(boxName, instance, volume string) error {
	plan, err := deploykit.EncPlanFor(boxName, instance, volume, deploykit.DeployStorageDir(boxName, instance))
	if err != nil {
		return err
	}

	// Fast path: if every requested volume is already mounted, skip the passphrase
	// lookup entirely (host-prelifted mount state, so a broken keyring never blocks a
	// restart-when-everything-is-mounted).
	requested := len(plan)
	mounted := 0
	for _, p := range plan {
		if p.Mounted {
			mounted++
		}
	}
	if requested > 0 && mounted == requested {
		fmt.Fprintf(os.Stderr, "All encrypted volumes for %s already mounted (%d/%d)\n", boxName, mounted, requested)
		return nil
	}

	passphrase, err := resolveEncPassphraseForMount(boxName)
	if err != nil {
		return err
	}
	return encExecViaPlugin(spec.EncExecInput{
		Method:     spec.EncMethodMount,
		ImageID:    "charly-" + boxName,
		BoxName:    boxName,
		Passphrase: passphrase,
		Volumes:    plan,
	})
}

// encUnmount unmounts encrypted volumes for an image.
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

// encStatus prints the status of encrypted bind mounts for an image. Thin forward to the
// portable deploykit implementation (Cutover B unit 2).
func encStatus(boxName, instance string) error {
	return deploykit.EncStatus(boxName, instance)
}

// encPasswd changes the gocryptfs password for all encrypted volumes of an image.
func encPasswd(boxName, instance string) error {
	plan, err := deploykit.EncPlanFor(boxName, instance, "", deploykit.DeployStorageDir(boxName, instance))
	if err != nil {
		return err
	}

	if len(plan) == 0 {
		return fmt.Errorf("image %q has no encrypted bind mounts", boxName)
	}

	// All volumes must be unmounted before changing password.
	for _, m := range plan {
		if m.Mounted {
			return fmt.Errorf("encrypted volume %q is still mounted; run 'charly config unmount %s' first", m.Name, boxName)
		}
	}

	volID := "charly-" + boxName

	oldPass, err := deploykit.AskPassword(volID+"-old", "Current passphrase:")
	if err != nil {
		return err
	}

	newPass, err := deploykit.AskPassword(volID+"-new", "New passphrase:")
	if err != nil {
		return err
	}

	confirmPass, err := deploykit.AskPassword(volID+"-confirm", "Confirm new passphrase:")
	if err != nil {
		return err
	}

	if newPass != confirmPass {
		return fmt.Errorf("new passphrase and confirmation do not match")
	}

	return encExecViaPlugin(spec.EncExecInput{
		Method:  spec.EncMethodPasswd,
		ImageID: volID,
		BoxName: boxName,
		OldPass: oldPass,
		NewPass: newPass,
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
