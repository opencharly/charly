package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// enc.go — encrypted-volume in-core SHIM (C16a), narrowed by Cutover B unit 2: every
// state-probe/plan-building function (resolveEncVolumeDir/isEncryptedInitialized/
// isEncryptedMounted/fuseAllowOtherEnabled/encPlanFor/loadEncryptedVolume/encServiceFilename/
// removeEncryptedVolumes/encStatus/askPassword) moved to sdk/deploykit (enc_probe.go) — they
// were genuinely portable, no registry/credential coupling. verifyBindMounts/
// hasEncryptedBindMounts/cipherPopulatedPlainEmpty were DELETED outright (dead code — zero
// non-test callers; candy/plugin-deploy-pod already carries its own live copies,
// config_setup_helpers.go's *Local functions). What remains is the genuinely registry/
// credential-coupled core: encExecViaPlugin calls providerRegistry.resolve + invokeTyped
// DIRECTLY (the host provider registry itself — clause-M kernel), and resolveEncPassphrase*/
// awaitKeyringUnlockViaPlugin route through ResolveCredential/DefaultCredentialStore, which
// are ALSO registry-coupled (connectPluginByWord to verb:credential). Neither is portable as
// a bare move — becoming plugin-callable needs the sdk.Executor.InvokeProvider rewrite
// candy/plugin-deploy-pod's podTunnelOp already proves the shape of, threaded through every
// caller. Registered FINAL/K5 inventory (credential-sensitive, deliberately not attempted
// without its own review).

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

// resolveEncPassphrase resolves the gocryptfs passphrase for an image.
// Resolution order: GOCRYPTFS_PASSWORD env var → credential store (keyring/config) → auto-generate or interactive prompt.
func resolveEncPassphrase(boxName string, autoGenerate bool) (string, error) {
	// 1. Test/CI override
	if pw := os.Getenv("GOCRYPTFS_PASSWORD"); pw != "" {
		return pw, nil
	}
	// 2. Credential store (keyring / config)
	if val, _ := ResolveCredential("", "charly/enc", boxName, ""); val != "" {
		return val, nil
	}
	// 3. Auto-generate if requested
	if autoGenerate {
		generated := deploykit.GenerateRandomSecretToken(32)
		store := DefaultCredentialStore()
		if err := store.Set("charly/enc", boxName, generated); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not persist enc passphrase for %s: %v\n", boxName, err)
		}
		fmt.Fprintf(os.Stderr, "Generated encryption passphrase for %s\n", boxName)
		return generated, nil
	}
	// 4. Interactive prompt
	return deploykit.AskPassword("charly-"+boxName, "Passphrase for charly-"+boxName+":")
}

// encMountDeadline bounds how long resolveEncPassphraseForMount will retry
// transient failures (source="unavailable") before giving up.
// source="locked" does NOT use this — it uses event-driven DBus signal
// waiting with no deadline (see awaitKeyringUnlockViaPlugin).
var encMountDeadline = 2 * time.Minute

// encMountPollPeriod is the interval between retry attempts for
// source="unavailable" only.
var encMountPollPeriod = 5 * time.Second

// resolveEncPassphraseForMount resolves the gocryptfs passphrase with
// backend-aware and failure-aware retry behavior.
//
// Under systemd (INVOCATION_ID set) with a keyring-capable backend:
//   - If the store is temporarily locked ("locked") or unreachable
//     ("unavailable"), retry every encMountPollPeriod until encMountDeadline
//     elapses, then fail with a clear diagnostic.
//   - If the store answered and the credential is NOT stored ("default"),
//     fail immediately with an actionable error — no amount of polling
//     will conjure a credential that was never stored.
//
// Explicit non-keyring backends under systemd: try resolve once, fail fast
// if not found. No polling.
//
// Interactive callers fall back to resolveEncPassphrase which can prompt.
//
// Defect D fix: the previous implementation polled forever on src=="default"
// and had no deadline, so a misconfigured keyring + TimeoutStartSec=0 quadlet
// was unrecoverable without manual intervention. source="unavailable" is now
// bounded at encMountDeadline; source="locked" waits indefinitely via DBus
// signal subscription (zero CPU between events) until the user unlocks the
// keyring; source="default" fails immediately. The DBus subscription itself
// runs OUT-OF-PROCESS in candy/plugin-secrets (the Secret Service owner) —
// charly's core no longer links godbus; see awaitKeyringUnlockViaPlugin.
func resolveEncPassphraseForMount(boxName string) (string, error) {
	if os.Getenv("INVOCATION_ID") == "" {
		return resolveEncPassphrase(boxName, false)
	}
	backend := resolveSecretBackend()
	resolver := func() (string, string) {
		return ResolveCredential("", "charly/enc", boxName, "")
	}
	return resolveEncPassphraseForMountWithResolver(boxName, backend, resolver, resetDefaultCredentialStore, awaitKeyringUnlockViaPlugin)
}

// resolveEncPassphraseForMountWithResolver is the testable core of
// resolveEncPassphraseForMount. It accepts a resolver closure, a reset
// closure, and a waiter closure so tests can supply mock implementations
// without touching global state, environment variables, or DBus.
//
// The waiter is called when source="locked" under a keyring-capable backend.
// In production it is awaitKeyringUnlockViaPlugin (event-driven via DBus signals
// running out-of-process in candy/plugin-secrets); in tests it is a fake that
// returns immediately.
func resolveEncPassphraseForMountWithResolver(
	boxName, backend string,
	resolver func() (value, source string),
	reset func(),
	waiter func(ctx context.Context, boxName string, resolver func() (string, string), reset func()) (string, string, error),
) (string, error) {
	usesWaitingBackend := backend == "" || backend == "auto" || backend == "keyring"

	if !usesWaitingBackend {
		val, src := resolver()
		if val != "" {
			return val, nil
		}
		return "", fmt.Errorf(
			"encryption passphrase not found for charly/enc/%s (backend=%s, source=%s); "+
				"store with `charly secrets set charly/enc %s` or switch backend with `charly settings set secret_backend auto`",
			boxName, backend, src, boxName)
	}

	// Initial probe.
	val, src := resolver()
	if val != "" {
		return val, nil
	}

	// source="default" is terminal — credential is not stored anywhere.
	if src == "default" {
		return "", encNotStoredError(boxName, backend, src)
	}

	// source="locked" — keyring present but locked. Wait indefinitely via
	// DBus signal subscription (zero CPU cost between events).
	if src == "locked" && waiter != nil {
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		v, src2, err := waiter(ctx, boxName, resolver, reset)
		if err != nil {
			return "", fmt.Errorf("waiting for keyring unlock interrupted: %w", err)
		}
		if v != "" {
			return v, nil
		}
		return "", encNotStoredError(boxName, backend, src2)
	}

	// source="unavailable" — transient backend probe failure. Bounded poll.
	return retryUnavailable(boxName, backend, resolver, reset)
}

// encNotStoredError formats the terminal "credential not stored" error with
// actionable remediation hints.
func encNotStoredError(boxName, backend, src string) error {
	return fmt.Errorf(
		"encryption passphrase not available for charly/enc/%s "+
			"(backend=%s, source=%s). "+
			"Remediation: run `charly doctor` to check keyring health, "+
			"store with `charly secrets set charly/enc %s`, "+
			"or switch backend with `charly settings set secret_backend config`",
		boxName, backend, src, boxName)
}

// retryUnavailable polls the resolver with a bounded deadline for transient
// backend-probe failures (source="unavailable").
func retryUnavailable(
	boxName, backend string,
	resolver func() (string, string),
	reset func(),
) (string, error) {
	deadline := time.Now().Add(encMountDeadline)
	attempt := 0
	maxAttempts := max(int(encMountDeadline/encMountPollPeriod), 1)
	for {
		attempt++
		val, src := resolver()
		if val != "" {
			return val, nil
		}
		retryable := src == "locked" || src == "unavailable"
		if !retryable || !time.Now().Before(deadline) {
			return "", fmt.Errorf(
				"encryption passphrase not available for charly/enc/%s after %d attempt(s) "+
					"(backend=%s, source=%s, waited up to %v). "+
					"Remediation: run `charly doctor` to check keyring health, "+
					"store with `charly secrets set charly/enc %s`, "+
					"or switch backend with `charly settings set secret_backend config`",
				boxName, attempt, backend, src, encMountDeadline, boxName)
		}
		fmt.Fprintf(os.Stderr,
			"charly: waiting for credential store (charly-enc/%s, source=%s, attempt %d/%d)...\n",
			boxName, src, attempt, maxAttempts)
		time.Sleep(encMountPollPeriod)
		if reset != nil {
			reset()
		}
	}
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
