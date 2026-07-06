package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// This file implements the two pre-resolution helpers for the credential-
// backed secrets feature (plan §2.4 and §2.5):
//
//  1. MigratePlaintextEnvSecret — scans an image's existing charly.yml env:
//     list for entries that are now declared as secret_accepts/secret_requires
//     on the image, moves those values into the credential store, removes
//     them from charly.yml, and writes a charly.yml.bak.<unix-timestamp>
//     backup before the first mutation. Gives existing deployments an
//     automatic one-time upgrade with a rollback point preserved.
//
//  2. scrubSecretCLIEnv — pre-scrub for `charly config -e NAME=VAL` flags: if
//     NAME is declared as a secret_accepts/secret_requires entry on the
//     target image, the value is stored in the credential store and the
//     NAME=VAL pair is removed from the CLI env slice. This prevents
//     plaintext credentials from ever reaching saveDeployState or the
//     quadlet writer. Plain env_accepts/env_requires entries are untouched.
//
// Both helpers are pure library code — they read and write via the
// CredentialStore interface and the DeployConfigPath hook, so tests can
// drive them against a temp directory without touching the user's real
// charly.yml or keyring.

// secretDeclaredOnBox returns the set of env var names an image declares
// as credential-backed (secret_accepts or secret_requires). Returns a
// non-nil empty set when meta is nil or has no secret declarations.
func secretDeclaredOnBox(meta *BoxMetadata) map[string]bool {
	names := map[string]bool{}
	if meta == nil {
		return names
	}
	for _, dep := range meta.SecretRequire {
		names[dep.Name] = true
	}
	for _, dep := range meta.SecretAccept {
		names[dep.Name] = true
	}
	return names
}

// secretDepNames returns the flat list of env var names declared as
// credential-backed on an image. Used by the config_image.go Run() call
// site to populate SaveDeployStateInput.SecretNames for the defense-in-depth
// scrub in saveDeployState. Returns nil (not an empty slice) when meta has
// no secret declarations — matches the rest of the omitempty-style API.
func secretDepNames(meta *BoxMetadata) []string {
	if meta == nil || (len(meta.SecretRequire) == 0 && len(meta.SecretAccept) == 0) {
		return nil
	}
	names := make([]string, 0, len(meta.SecretRequire)+len(meta.SecretAccept))
	for _, dep := range meta.SecretRequire {
		names = append(names, dep.Name)
	}
	for _, dep := range meta.SecretAccept {
		names = append(names, dep.Name)
	}
	return names
}

// secretKeyForDep returns the (service, key) tuple used to look up a secret
// in the credential store. When the candy author set an explicit `key:
// charly/api-key/openrouter` override, that's parsed into its two segments;
// otherwise the default (charly/secret, dep.Name) is returned. The format is
// enforced by validateSecretDeps at build time, so this is purely a
// structural split — no validation is re-run here.
func secretKeyForDep(dep EnvDependency) (service, key string) {
	if dep.Key != "" {
		// Key format validated: ^charly/<service>/<key>$ — the service is
		// everything before the final "/", the key is the last segment.
		// (Mirrors the split in layer_secrets.go; LastIndex avoids any
		// dependency on the literal prefix length.)
		if idx := strings.LastIndex(dep.Key, "/"); idx >= 0 {
			return dep.Key[:idx], dep.Key[idx+1:]
		}
	}
	return "charly/secret", dep.Name
}

// MigratePlaintextEnvSecret scans dc.Deploy[deployKey(image, instance)].Env
// for any KEY=VAL entries whose KEY is declared as secret_accepts or
// secret_requires on the given image metadata. For each match, it:
//
//   - writes VAL into the credential store at the candy-declared (service, key)
//   - removes KEY=VAL from the in-memory dc.Env slice
//   - creates a charly.yml.bak.<unix-timestamp> backup before the first
//     mutation (one backup per call, even if multiple entries are migrated)
//   - persists the cleaned dc via SaveBundleConfig
//   - logs a per-entry informational notice to stderr
//
// Returns (migrated int, err error) where migrated is the number of entries
// moved from charly.yml to the credential store. Returns zero migrated when
// dc is nil, meta has no secret declarations, or no matching entries exist —
// in all those cases the function is a safe no-op.
//
// This is idempotent: running it a second time on a now-clean charly.yml is
// a no-op. Running it on a host that never had plaintext credentials is a
// no-op.
func MigratePlaintextEnvSecret(dc *BundleConfig, meta *BoxMetadata, image, instance string) (int, error) {
	if dc == nil || dc.Bundle == nil {
		return 0, nil
	}
	declared := secretDeclaredOnBox(meta)
	if len(declared) == 0 {
		return 0, nil
	}

	key := deployKey(image, instance)
	entry, ok := dc.Bundle[key]
	if !ok || len(entry.Env) == 0 {
		return 0, nil
	}

	// Partition existing entry.Env into (a) plaintext that stays put and
	// (b) credential-backed entries to migrate. Preserve order of the
	// plaintext half so unrelated env vars round-trip unchanged.
	type pending struct {
		depName string
		value   string
	}
	staying := map[string]string{}
	var toMigrate []pending
	for name, val := range entry.Env {
		if !declared[name] {
			staying[name] = val
			continue
		}
		toMigrate = append(toMigrate, pending{depName: name, value: val})
	}

	if len(toMigrate) == 0 {
		return 0, nil
	}

	// Backup charly.yml before any mutation, so the user has a rollback
	// point. One backup per call regardless of how many entries are moved.
	backupPath, err := writeDeployBackup()
	if err != nil {
		return 0, fmt.Errorf("writing charly.yml backup before migration: %w", err)
	}

	// Build a lookup from dep name → full EnvDependency so we can honor any
	// `key:` override on the candy declaration.
	depByName := map[string]EnvDependency{}
	for _, dep := range meta.SecretRequire {
		depByName[dep.Name] = dep
	}
	for _, dep := range meta.SecretAccept {
		depByName[dep.Name] = dep
	}

	store := DefaultCredentialStore()
	migrated := 0
	for _, p := range toMigrate {
		dep := depByName[p.depName]
		service, credKey := secretKeyForDep(dep)
		if err := store.Set(service, credKey, p.value); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not migrate %s to credential store (%s/%s): %v\n", p.depName, service, credKey, err)
			// Keep the plaintext entry so the user isn't left without a
			// value; they can retry after fixing the backend.
			staying[p.depName] = p.value
			continue
		}
		fmt.Fprintf(os.Stderr, "Migrated plaintext %s from charly.yml to credential store (%s/%s)\n", p.depName, service, credKey)
		migrated++
	}

	if migrated == 0 {
		// Nothing actually moved (all Set calls failed). The charly.yml is
		// unchanged, so the backup we wrote is redundant but harmless.
		return 0, nil
	}

	entry.Env = staying
	dc.Bundle[key] = entry
	if err := SaveBundleConfig(dc); err != nil {
		return migrated, fmt.Errorf("persisting cleaned charly.yml after migration: %w (backup at %s)", err, backupPath)
	}
	fmt.Fprintf(os.Stderr, "Backed up previous charly.yml to %s (rollback: mv %s %s)\n", backupPath, backupPath, deployConfigPathOrEmpty())
	return migrated, nil
}

// scrubSecretCLIEnv walks the caller's -e KEY=VAL slice and, for any KEY
// declared as a secret_accepts or secret_requires entry on the target image,
// stores the value in the credential store and strips the pair from the
// slice. Plain env_accepts/env_requires entries pass through unchanged.
//
// This is the §2.5 "one-shot import" path: after a successful scrub, the
// caller's env list no longer contains the credential value, so it cannot
// reach saveDeployState, the quadlet writer, or any other downstream. The
// normal secret resolution path picks up the stored value on the same
// charly config invocation.
//
// Returns (cleaned []string, imported int). cleaned is the new -e list
// (never nil — returns an empty slice when all entries were migrated);
// imported is the number of credentials moved into the store.
func scrubSecretCLIEnv(cliEnv []string, meta *BoxMetadata) ([]string, int) {
	if len(cliEnv) == 0 {
		return cliEnv, 0
	}
	declared := secretDeclaredOnBox(meta)
	if len(declared) == 0 {
		return cliEnv, 0
	}

	depByName := map[string]EnvDependency{}
	if meta != nil {
		for _, dep := range meta.SecretRequire {
			depByName[dep.Name] = dep
		}
		for _, dep := range meta.SecretAccept {
			depByName[dep.Name] = dep
		}
	}

	store := DefaultCredentialStore()
	cleaned := make([]string, 0, len(cliEnv))
	imported := 0
	for _, kv := range cliEnv {
		name, val, found := strings.Cut(kv, "=")
		if !found || !declared[name] {
			cleaned = append(cleaned, kv)
			continue
		}
		dep := depByName[name]
		service, credKey := secretKeyForDep(dep)
		if err := store.Set(service, credKey, val); err != nil {
			// On Set failure, keep the CLI entry in place so the user's
			// deployment isn't silently broken; the normal env_resolution
			// path will still pick up the -e value via ResolveCredential's
			// env-first chain.
			fmt.Fprintf(os.Stderr, "Warning: could not import %s into credential store (%s/%s): %v — CLI -e value will be used directly\n", name, service, credKey, err)
			cleaned = append(cleaned, kv)
			continue
		}
		fmt.Fprintf(os.Stderr, "Imported %s into credential store (%s/%s)\n", name, service, credKey)
		imported++
	}
	return cleaned, imported
}

// writeDeployBackup copies the current charly.yml (if it exists) to
// charly.yml.bak.<unix-timestamp> and returns the backup path. Returns
// (empty, nil) when there's no charly.yml to back up — a first-time run is
// not an error. The .bak file is written with 0600 to match the original.
//
// Reuses the pattern from ConfigMigrateSecretsCmd in credential_store.go:290
// but with a timestamped suffix so multiple migrations don't clobber each
// other.
func writeDeployBackup() (string, error) {
	path, err := DeployConfigPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	backupPath := fmt.Sprintf("%s.bak.%d", path, time.Now().Unix())
	if err := os.WriteFile(backupPath, data, 0600); err != nil {
		return "", fmt.Errorf("writing %s: %w", backupPath, err)
	}
	return backupPath, nil
}

// deployConfigPathOrEmpty returns the current deploy config path or empty
// string when the lookup fails. Used for user-facing rollback hints, where
// "cp <backup> <path>" with an empty path is better than a cascade of
// wrapped errors.
func deployConfigPathOrEmpty() string {
	if path, err := DeployConfigPath(); err == nil {
		return path
	}
	return ""
}
