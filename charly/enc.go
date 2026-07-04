package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/opencharly/sdk/spec"
)

// ResolvedBindMount is ready for -v flags.
// Represents a volume backed by a host path (either plain bind or encrypted gocryptfs).
type ResolvedBindMount struct {
	Name      string // e.g. "secrets"
	HostPath  string // effective host path (plain: expanded host, encrypted: plain dir)
	ContPath  string // container path (expanded)
	Encrypted bool   // for status/mount checks
}

// encryptedVolumeName returns the directory name for an encrypted volume: charly-<image>-<name>
func encryptedVolumeName(boxName, name string) string {
	return "charly-" + boxName + "-" + name
}

// encryptedCipherDir returns the cipher directory path for an encrypted bind mount.
func encryptedCipherDir(storagePath, boxName, name string) string {
	return filepath.Join(storagePath, encryptedVolumeName(boxName, name), "cipher")
}

// encryptedPlainDir returns the plain (FUSE mount point) directory path.
func encryptedPlainDir(storagePath, boxName, name string) string {
	return filepath.Join(storagePath, encryptedVolumeName(boxName, name), "plain")
}

// resolveEncVolumeDir returns the volume directory for an encrypted volume.
// If the volume has an explicit Host path, use it directly.
// Otherwise, use the global default: <storagePath>/charly-<image>-<name>.
func resolveEncVolumeDir(vol DeployVolumeConfig, defaultStoragePath, boxName string) string {
	if vol.Host != "" {
		return expandHostHome(vol.Host)
	}
	return filepath.Join(defaultStoragePath, encryptedVolumeName(boxName, vol.Name))
}

// isEncryptedInitialized checks if gocryptfs has been initialized (gocryptfs.conf exists).
func isEncryptedInitialized(cipherDir string) bool {
	_, err := os.Stat(filepath.Join(cipherDir, "gocryptfs.conf"))
	return err == nil
}

// isEncryptedMounted checks if the plain dir is a FUSE mount by reading /proc/mounts.
var isEncryptedMounted = defaultIsEncryptedMounted

func defaultIsEncryptedMounted(plainDir string) bool {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck

	// Resolve symlinks for comparison
	resolved, err := filepath.EvalSymlinks(plainDir)
	if err != nil {
		resolved = plainDir
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 {
			mountPoint, err := filepath.EvalSymlinks(fields[1])
			if err != nil {
				mountPoint = fields[1]
			}
			if mountPoint == resolved && fields[2] == "fuse.gocryptfs" {
				return true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: /proc/mounts scan error: %v\n", err)
	}
	return false
}

// encPlanFor host-prelifts the per-volume gocryptfs execution plan for the given
// box/instance, filtered to `volume` when non-empty. It loads the deploy config
// (loadEncryptedVolume — loader, stays core), resolves each volume's cipher/plain
// dirs (resolveEncVolumeDir — deploy-model path convention, stays core), and probes
// the initialized/mounted state (isEncryptedInitialized/isEncryptedMounted — the
// probes the mandatorily-core verifyBindMounts also uses). scopeDir is the scope-unit
// directory component: deployStorageDir(box,instance) for mount/unmount/passwd, or the
// bare box name for the ensure path (a pre-existing derivation difference, identical
// for the common empty-instance case, preserved exactly by this cutover). The result
// is the self-contained plan candy/plugin-enc executes over OpExecute.
func encPlanFor(boxName, instance, volume, scopeDir string) ([]spec.EncVolumePlan, error) {
	mounts, storagePath, err := loadEncryptedVolume(boxName, instance)
	if err != nil {
		return nil, err
	}
	storageDir := deployStorageDir(boxName, instance)
	var plan []spec.EncVolumePlan
	for _, m := range mounts {
		if volume != "" && m.Name != volume {
			continue
		}
		volDir := resolveEncVolumeDir(m, storagePath, storageDir)
		cipherDir := filepath.Join(volDir, "cipher")
		plainDir := filepath.Join(volDir, "plain")
		plan = append(plan, spec.EncVolumePlan{
			Name:        m.Name,
			CipherDir:   cipherDir,
			PlainDir:    plainDir,
			ScopeUnit:   fmt.Sprintf("charly-enc-%s-%s", scopeDir, m.Name),
			Initialized: isEncryptedInitialized(cipherDir),
			Mounted:     isEncryptedMounted(plainDir),
		})
	}
	return plan, nil
}

// fuseConfPath is the fuse.conf location; a package var so tests point it elsewhere.
var fuseConfPath = "/etc/fuse.conf"

// fuseAllowOtherEnabled reports whether fuse.conf has an ACTIVE (uncommented, value-less)
// `user_allow_other` line. Every charly encrypted-volume mount uses `gocryptfs -allow_other`
// (so rootless podman keep-id can reach the FUSE plain dir), and fusermount3 REFUSES
// -allow_other unless this option is set. An absent/unreadable file counts as not enabled.
func fuseAllowOtherEnabled() bool {
	data, err := os.ReadFile(fuseConfPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "user_allow_other" {
			return true
		}
	}
	return false
}

// encExecViaPlugin resolves verb:enc and Invokes its OpExecute with the host-prelifted
// plan. plugin-enc is compiled-in, so this is an in-proc JSON envelope (no socket) —
// the passphrase never leaves the process. Mirrors egress.go / k8s_generate.go.
func encExecViaPlugin(in spec.EncExecInput) error {
	// Preflight: the mount methods run `gocryptfs -allow_other`, which fusermount3 rejects
	// unless `user_allow_other` is set in fuse.conf. Fail fast with the exact fix (before any
	// volume mounts partway) instead of surfacing the raw fusermount3 error mid-plan.
	if in.Method == spec.EncMethodMount || in.Method == spec.EncMethodEnsure {
		if !fuseAllowOtherEnabled() {
			return fmt.Errorf("encrypted volumes require 'user_allow_other' in %s (gocryptfs -allow_other, for rootless-podman keep-id access) — enable it with: echo user_allow_other | sudo tee -a %s", fuseConfPath, fuseConfPath)
		}
	}
	prov, ok := providerRegistry.resolve(ClassVerb, "enc")
	if !ok {
		return fmt.Errorf("enc plugin (verb:enc) not registered — charly built without candy/plugin-enc")
	}
	params, err := marshalJSON(in)
	if err != nil {
		return fmt.Errorf("enc marshal input: %w", err)
	}
	res, err := prov.Invoke(context.Background(), &Operation{Reserved: "enc", Op: OpExecute, Params: params})
	if err != nil {
		return fmt.Errorf("enc invoke: %w", err)
	}
	var reply spec.EncExecReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return fmt.Errorf("enc decode reply: %w", err)
		}
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
		generated := generateRandomSecretToken(32)
		store := DefaultCredentialStore()
		if err := store.Set("charly/enc", boxName, generated); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not persist enc passphrase for %s: %v\n", boxName, err)
		}
		fmt.Fprintf(os.Stderr, "Generated encryption passphrase for %s\n", boxName)
		return generated, nil
	}
	// 4. Interactive prompt
	return askPassword("charly-"+boxName, "Passphrase for charly-"+boxName+":")
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
	plan, err := encPlanFor(boxName, instance, volume, deployStorageDir(boxName, instance))
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
	plan, err := encPlanFor(boxName, instance, volume, deployStorageDir(boxName, instance))
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

// encStatus prints the status of encrypted bind mounts for an image.
func encStatus(boxName, instance string) error {
	mounts, storagePath, err := loadEncryptedVolume(boxName, instance)
	if err != nil {
		return err
	}

	if len(mounts) == 0 {
		fmt.Println("No encrypted bind mounts configured")
		return nil
	}

	fmt.Printf("%-20s %-12s %-8s %s\n", "NAME", "INITIALIZED", "MOUNTED", "PATH")
	for _, m := range mounts {
		volDir := resolveEncVolumeDir(m, storagePath, deployStorageDir(boxName, instance))
		cipherDir := filepath.Join(volDir, "cipher")
		plainDir := filepath.Join(volDir, "plain")

		initialized := "no"
		if isEncryptedInitialized(cipherDir) {
			initialized = "yes"
		}
		mounted := "no"
		if isEncryptedMounted(plainDir) {
			mounted = "yes"
		}
		fmt.Printf("%-20s %-12s %-8s %s\n", m.Name, initialized, mounted, plainDir)
	}
	return nil
}

// encPasswd changes the gocryptfs password for all encrypted volumes of an image.
func encPasswd(boxName, instance string) error {
	plan, err := encPlanFor(boxName, instance, "", deployStorageDir(boxName, instance))
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

	oldPass, err := askPassword(volID+"-old", "Current passphrase:")
	if err != nil {
		return err
	}

	newPass, err := askPassword(volID+"-new", "New passphrase:")
	if err != nil {
		return err
	}

	confirmPass, err := askPassword(volID+"-confirm", "Confirm new passphrase:")
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
	plan, err := encPlanFor(boxName, instance, "", boxName)
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

// askPassword prompts for a password using systemd-ask-password.
// id is a unique identifier for kernel keyring caching, prompt is shown to the user.
var askPassword = defaultAskPassword

func defaultAskPassword(id, prompt string) (string, error) {
	cmd := exec.Command("systemd-ask-password",
		"--id="+id, "--timeout=0", "--echo=masked", prompt)
	// Ensure tty access for interactive prompt
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("systemd-ask-password: %w", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// loadEncryptedVolume loads encrypted volume configs from charly.yml for an image.
// Returns the deploy volume configs with type=encrypted and the encrypted storage path.
func loadEncryptedVolume(boxName, instance string) ([]DeployVolumeConfig, string, error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return nil, "", err
	}

	// Propagate LoadBundleConfig errors instead of swallowing them. A
	// schema error (e.g. the 2026-05-12 require-image cutover rejecting
	// pre-cutover deploy.yml entries) used to silently degrade to "no
	// encrypted volumes", which broke the encMount short-circuit and
	// drove the call into resolveEncPassphraseForMount → systemd-ask-
	// password → indefinite hang waiting for stdin. Surfacing the error
	// turns that hang into a clean error message with a remediation
	// hint pointing at `charly migrate`.
	dc, err := LoadBundleConfig()
	if err != nil {
		return nil, "", fmt.Errorf("loading deploy config for encrypted volumes: %w", err)
	}
	if dc == nil {
		return nil, rt.EncryptedStoragePath, nil
	}

	overlay, ok := dc.Bundle[deployKey(boxName, instance)]
	if !ok {
		return nil, rt.EncryptedStoragePath, nil
	}

	var encrypted []DeployVolumeConfig
	for _, dv := range overlay.Volume {
		if dv.Type == "encrypted" {
			encrypted = append(encrypted, dv)
		}
	}
	return encrypted, rt.EncryptedStoragePath, nil
}

// encServiceFilename returns the systemd service filename for a legacy crypto companion unit.
// Used only for cleanup of stale enc services from older charly versions.
func encServiceFilename(boxName string) string {
	return containerName(boxName) + "-enc.service"
}

// hasEncryptedBindMounts returns true if any bind mount is encrypted.
func hasEncryptedBindMounts(mounts []ResolvedBindMount) bool {
	for _, m := range mounts {
		if m.Encrypted {
			return true
		}
	}
	return false
}

// verifyBindMounts checks that all bind mounts are ready to use:
// - Plain mounts: host directory must exist
// - Encrypted mounts: must be mounted (FUSE)
//
// For encrypted mounts where FUSE is unmounted, an extra discrimination
// fires: if the cipher dir on disk holds real encrypted data (anything
// beyond the gocryptfs metadata files) AND the plain mount target is
// empty, we surface a louder error spelling out the data-loss risk.
// That's the immich-2026-04-incident shape — quadlet without ExecStartPre,
// FUSE never re-mounted after a reboot, container about to bind an empty
// plain/ over a populated cipher tree and start writing plaintext on top.
// The previous generic "not mounted" message was indistinguishable from
// a fresh-setup state where no harm exists yet.
func verifyBindMounts(mounts []ResolvedBindMount, boxName string) error {
	for _, m := range mounts {
		if m.Encrypted {
			if !isEncryptedMounted(m.HostPath) {
				cipherDir := filepath.Join(filepath.Dir(m.HostPath), "cipher")
				if cipherPopulatedPlainEmpty(cipherDir, m.HostPath) {
					return fmt.Errorf(
						"encrypted volume %q: cipher dir at %s is populated but plain mount at %s is empty — refusing to start (would write plaintext over encrypted data); run 'charly config mount %s' first",
						m.Name, cipherDir, m.HostPath, boxName,
					)
				}
				return fmt.Errorf("encrypted bind mount %q for image %q is not mounted; run 'charly config mount %s' first", m.Name, boxName, boxName)
			}
		} else {
			info, err := os.Stat(m.HostPath)
			if err != nil {
				return fmt.Errorf("bind mount %q: host path %q: %w", m.Name, m.HostPath, err)
			}
			if !info.IsDir() {
				return fmt.Errorf("bind mount %q: host path %q is not a directory", m.Name, m.HostPath)
			}
		}
	}
	return nil
}

// cipherPopulatedPlainEmpty reports whether the gocryptfs cipher directory
// holds user data (anything beyond the gocryptfs.conf + gocryptfs.diriv
// metadata files) AND the plain mount target is empty. The combination
// means FUSE is unmounted on top of a populated vault — letting a
// container start now would silently bind the empty plain/ as a plaintext
// directory and write new data on top of the encrypted tree.
//
// Returns false on stat errors (the surrounding error path will surface
// those — this helper is only a discrimination hint).
func cipherPopulatedPlainEmpty(cipherDir, plainDir string) bool {
	plainEntries, err := os.ReadDir(plainDir)
	if err != nil || len(plainEntries) > 0 {
		return false
	}
	cipherEntries, err := os.ReadDir(cipherDir)
	if err != nil {
		return false
	}
	for _, e := range cipherEntries {
		switch e.Name() {
		case "gocryptfs.conf", "gocryptfs.diriv":
			continue
		}
		return true
	}
	return false
}
