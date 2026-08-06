package deploypod

// secret_migration.go — the credential-backed secret migration + CLI-env scrub helpers
// relocated plugin-side from charly/config_secret_migration.go (#55 coneC Unit C4). The two
// pre-resolution helpers for the credential-backed secrets feature (plan §2.4 + §2.5):
//
//  1. migratePlaintextEnvSecret — scans an image's existing charly.yml env: list for entries
//     now declared as secret_accepts/secret_requires on the image, moves those values into
//     the credential store, removes them from charly.yml, and writes a charly.yml.bak.<unix>
//     backup before the first mutation. One-time automatic upgrade with a rollback point.
//
//  2. scrubSecretCLIEnv — pre-scrub for `charly config -e NAME=VAL` flags: if NAME is a
//     secret_accepts/secret_requires entry, the value is stored in the credential store and
//     the NAME=VAL pair is removed from the CLI env slice — plaintext credentials never reach
//     saveFleet / the quadlet writer. Plain env_accepts/env_requires entries are untouched.
//
// Both helpers are pure library code: they read/write via the verb:credential-backed
// deploykit.CredentialAccessViaExecutor + the plugin-side saveFleet + kit.DefaultDeployConfigPath,
// so they run identically compiled-in or out-of-process (the plugin-side reverse channel the
// rest of candy/plugin-deploy-pod already uses). The former charly-core DefaultCredentialStore
// host singleton + host deploy-state save-callback loader-seam are gone — no host round-trip.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// secretDeclaredOnBox returns the set of env var names an image declares as credential-backed
// (secret_accepts or secret_requires). Returns a non-nil empty set when meta is nil or has no
// secret declarations.
func secretDeclaredOnBox(meta *spec.BoxMetadata) map[string]bool {
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

// secretKeyForDep returns the (service, key) tuple used to look up a secret in the credential
// store. When the candy author set an explicit `key: charly/api-key/openrouter` override, that's
// parsed into its two segments; otherwise the default (charly/secret, dep.Name) is returned. The
// format is enforced by validateSecretDeps at build time, so this is purely a structural split.
func secretKeyForDep(dep spec.EnvDependency) (service, key string) {
	if dep.Key != "" {
		if idx := strings.LastIndex(dep.Key, "/"); idx >= 0 {
			return dep.Key[:idx], dep.Key[idx+1:]
		}
	}
	return "charly/secret", dep.Name
}

// migratePlaintextEnvSecret scans dc.Fleet[deployKey(image, instance)].Env for any KEY=VAL
// entries whose KEY is declared as secret_accepts/secret_requires on the given image metadata.
// For each match it writes VAL into the credential store at the candy-declared (service, key),
// removes KEY=VAL from the in-memory entry.Env, creates a charly.yml.bak.<unix> backup before
// the first mutation (one per call), persists the cleaned dc via the plugin-side saveFleet, and
// logs a per-entry notice to stderr. Returns (migrated, err). Idempotent: a second run on a
// cleaned charly.yml is a no-op; a host that never had plaintext is a no-op.
func migratePlaintextEnvSecret(ctx context.Context, ex *sdk.Executor, dc *deploykit.FleetConfig, meta *spec.BoxMetadata, image, instance string) (int, error) {
	if dc == nil || dc.Fleet == nil {
		return 0, nil
	}
	declared := secretDeclaredOnBox(meta)
	if len(declared) == 0 {
		return 0, nil
	}

	key := spec.DeployKey(image, instance)
	entry, ok := dc.Fleet[key]
	if !ok || len(entry.Env) == 0 {
		return 0, nil
	}

	// Partition existing entry.Env into (a) plaintext that stays and (b) credential-backed
	// entries to migrate. Preserve order of the plaintext half so unrelated env vars round-trip
	// unchanged.
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

	// Backup charly.yml before any mutation. One backup per call regardless of how many move.
	backupPath, err := writeDeployBackup()
	if err != nil {
		return 0, fmt.Errorf("writing charly.yml backup before migration: %w", err)
	}

	// dep name → full EnvDependency to honor any `key:` override on the candy declaration.
	depByName := map[string]spec.EnvDependency{}
	for _, dep := range meta.SecretRequire {
		depByName[dep.Name] = dep
	}
	for _, dep := range meta.SecretAccept {
		depByName[dep.Name] = dep
	}

	cred := deploykit.CredentialAccessViaExecutor(ctx, ex)
	migrated := 0
	var migratedNames []string
	for _, p := range toMigrate {
		dep := depByName[p.depName]
		service, credKey := secretKeyForDep(dep)
		if err := cred.Write(service, credKey, p.value); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not migrate %s to credential store (%s/%s): %v\n", p.depName, service, credKey, err)
			// Keep the plaintext entry so the user isn't left without a value; retry after fixing.
			staying[p.depName] = p.value
			continue
		}
		fmt.Fprintf(os.Stderr, "Migrated plaintext %s from charly.yml to credential store (%s/%s)\n", p.depName, service, credKey)
		migrated++
		migratedNames = append(migratedNames, p.depName)
	}

	if migrated == 0 {
		// Nothing moved (all Write calls failed). charly.yml is unchanged; backup is redundant.
		return 0, nil
	}

	entry.Env = staying
	dc.Fleet[key] = entry
	// Persist through the locked read-modify-write cycle, and express the cleaning as "delete
	// exactly the keys that reached the credential store" rather than "write back the Env map I
	// computed". Overwriting wholesale would discard any env var a concurrent `charly config` for
	// this same deploy added between this function's read and its write — the lost-update class
	// this whole path was carrying.
	if _, err := mutateFleet(ctx, ex, "charly config migrate-plaintext-secret", func(d *deploykit.FleetConfig) (bool, error) {
		fresh, ok := d.Fleet[key]
		if !ok || len(fresh.Env) == 0 {
			return false, nil
		}
		changed := false
		for _, name := range migratedNames {
			if _, present := fresh.Env[name]; present {
				delete(fresh.Env, name)
				changed = true
			}
		}
		if !changed {
			return false, nil
		}
		d.Fleet[key] = fresh
		return true, nil
	}); err != nil {
		return migrated, fmt.Errorf("persisting cleaned charly.yml after migration: %w (backup at %s)", err, backupPath)
	}
	fmt.Fprintf(os.Stderr, "Backed up previous charly.yml to %s (rollback: mv %s %s)\n", backupPath, backupPath, deployConfigPathOrEmpty())
	return migrated, nil
}

// scrubSecretCLIEnv walks the caller's -e KEY=VAL slice and, for any KEY declared as a
// secret_accepts/secret_requires entry on the target image, stores the value in the credential
// store and strips the pair from the slice. Plain env_accepts/env_requires entries pass through
// unchanged. After a successful scrub the caller's env list no longer carries the credential
// value, so it cannot reach saveFleet, the quadlet writer, or any downstream.
//
// Returns (cleaned, imported). cleaned is the new -e list (never nil — empty slice when all
// entries migrated); imported is the number of credentials moved into the store.
func scrubSecretCLIEnv(ctx context.Context, ex *sdk.Executor, cliEnv []string, meta *spec.BoxMetadata) ([]string, int) {
	if len(cliEnv) == 0 {
		return cliEnv, 0
	}
	declared := secretDeclaredOnBox(meta)
	if len(declared) == 0 {
		return cliEnv, 0
	}

	depByName := map[string]spec.EnvDependency{}
	if meta != nil {
		for _, dep := range meta.SecretRequire {
			depByName[dep.Name] = dep
		}
		for _, dep := range meta.SecretAccept {
			depByName[dep.Name] = dep
		}
	}

	cred := deploykit.CredentialAccessViaExecutor(ctx, ex)
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
		if err := cred.Write(service, credKey, val); err != nil {
			// On Write failure, keep the CLI entry so the deployment isn't silently broken; the
			// normal env_resolution path picks up the -e value via ResolveCredential's env-first chain.
			fmt.Fprintf(os.Stderr, "Warning: could not import %s into credential store (%s/%s): %v — CLI -e value will be used directly\n", name, service, credKey, err)
			cleaned = append(cleaned, kv)
			continue
		}
		fmt.Fprintf(os.Stderr, "Imported %s into credential store (%s/%s)\n", name, service, credKey)
		imported++
	}
	return cleaned, imported
}

// writeDeployBackup copies the current charly.yml (if it exists) to charly.yml.bak.<unix> and
// returns the backup path. Returns ("", nil) when there's no charly.yml to back up — a first-time
// run is not an error. The .bak file is written 0600 to match the original.
func writeDeployBackup() (string, error) {
	path, err := kit.DefaultDeployConfigPath()
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

// deployConfigPathOrEmpty returns the current deploy config path or empty string when the lookup
// fails. Used for user-facing rollback hints, where "cp <backup> <path>" with an empty path is
// better than a cascade of wrapped errors.
func deployConfigPathOrEmpty() string {
	if path, err := kit.DefaultDeployConfigPath(); err == nil {
		return path
	}
	return ""
}
