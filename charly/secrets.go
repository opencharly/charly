package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"golang.org/x/term"
)

// secrets.go — container secret collection + podman secret provisioning. TRACKED
// FINAL/K5 EXIT (DEPLOY-wave W1 audit, 2026-07-20): ResolveCredential/DefaultCredentialStore
// route through connectPluginByWord to verb:credential — the core provider registry, the
// same clause-M coupling as enc.go (see its header). Registered FINAL/K5 inventory
// alongside enc.go's InvokeProvider rewrite, not this wave.

// generateRandomSecretToken returns `byteCount` random bytes encoded as
// url-safe base64 (RFC 4648 §5). For byteCount=32 this produces a 44-char
// string (43 base64 chars + 1 `=` pad).
//
// Url-safe base64 was chosen over hex because it is a strict superset of
// what every secret consumer in the codebase needs:
//   - Postgres / VNC / generic passwords: accept any string. Base64 has
//     more entropy per character (6 bits vs 4) so 32 random bytes pack
//     into 44 chars instead of hex's 64 chars.
//   - Apache Airflow's AIRFLOW__CORE__FERNET_KEY: REQUIRES url-safe
//     base64 of exactly 32 bytes (cryptography.fernet.Fernet's documented
//     format). Hex strings are rejected with `binascii.Error: Invalid
//     base64-encoded string`. This is the load-bearing fix — prior hex
//     output forced every Fernet-using candy (airflow today, more
//     tomorrow) to ship a workaround that regenerated the key.
//   - gocryptfs / Podman / KeePassXC: accept any string; format-agnostic.
//
// Url-safe (vs standard base64) avoids `+` and `/` characters that would
// need shell-escaping in `[Service] Environment=...` quadlet lines and
// in `--password` CLI args. The `=` padding is benign in every consumer.
//
// All consumers in this codebase treat the return value as an opaque
// string — none decode it back to bytes — so the format change is
// invisible to existing keyring-stored secrets (which remain in
// whatever format they were originally stored).
func generateRandomSecretToken(byteCount int) string {
	b := make([]byte, byteCount)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.URLEncoding.EncodeToString(b)
}

// promptPassword reads a password from the terminal without echo — the interactive
// secret-entry path in ProvisionPodmanSecrets (a `charly config`-time operator prompt).
// The `charly secrets` CLI moved to candy/plugin-secrets, but this in-deploy prompt stays
// in core (it runs inside the deploy provisioning path, not the secrets CLI).
func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	return string(pw), nil
}

// generateAndStoreSecret generates a 32-byte url-safe base64 token (44
// chars; Fernet-key-compatible — see generateRandomSecretToken), persists
// it to the active credential store at (service, key), and returns the
// value with the "auto-generated" source classification. Persistence
// failures are logged to stderr but not returned as errors — the
// in-memory value is still usable for the current charly invocation.
//
// Used by:
//   - ProvisionPodmanSecrets — config-time CollectedSecret provisioning
//     when --password=auto is in effect.
//   - ensureCandySecret (layer_secrets.go) — deploy-time secret_requires
//     resolution on host/VM/SSH targets when the value is missing.
func generateAndStoreSecret(service, key string) (val, source string) {
	val = generateRandomSecretToken(32)
	if err := DefaultCredentialStore().Set(service, key, val); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not persist auto-generated secret %s/%s: %v\n",
			service, key, err)
	}
	return val, "auto-generated"
}

// LabelSecretEntry represents a secret requirement in an OCI image label.
// Only metadata is stored — never the secret value. CUE-sourced in spec (boxmetadata.cue, P2B)
// + aliased in-place; carried on BoxMetadata.Secret (ai.opencharly.secret).
type LabelSecretEntry = spec.LabelSecretEntry

// CollectedSecret (a fully-resolved secret ready for provisioning + the quadlet
// Secret= directive) is a deploykit resolved-runtime type now, referenced directly
// as deploykit.CollectedSecret — it moved to sdk/deploykit with the pod config-write
// mechanism (P11). Service/Key/RotateOnConfig are populated by CollectCandySecretAccepts
// for credential-store-backed secrets (secret_accepts / secret_requires); zero for
// candy-owned secrets (the CollectSecretsFromLabels path). Service/Key override the
// ResolveCredential lookup (defaults Service="charly/secret", Key=SecretName);
// RotateOnConfig=true makes ProvisionPodmanSecrets bypass the podmanSecretExists
// short-circuit and rm+recreate every reconcile (candy-owned secrets keep it false —
// you cannot re-init a live postgres cluster with a rotated password).

// ListProvisionedSecretNames returns the engine-side podman secrets
// provisioned for a box (the charly-<box>-* names, sidecar secrets
// included), sorted — the charly-native replacement for ad-hoc
// `podman secret ls` verification (surfaced on `charly status <box>` detail).
func ListProvisionedSecretNames(engineBin, boxName string) []string {
	out, err := exec.Command(engineBin, "secret", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		return nil
	}
	prefix := "charly-" + boxName + "-"
	var names []string
	for n := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if n != "" && strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// ApplySecretRefresh marks the named secrets (matched by their manifest
// SecretName; the literal "all" matches every secret) RotateOnConfig for
// THIS provisioning run, so ProvisionPodmanSecrets removes and recreates
// their podman secrets — the charly-native replacement for the retired
// ad-hoc `podman secret rm` re-provisioning path. Returns the requested
// names that matched nothing so the caller can surface typos. NOTE: a
// candy-owned auto-generated secret gets a NEW random value on refresh;
// services that persisted the old value (an initialized database) must be
// re-initialized by the operator.
func ApplySecretRefresh(secrets []deploykit.CollectedSecret, refresh []string) ([]deploykit.CollectedSecret, []string) {
	if len(refresh) == 0 {
		return secrets, nil
	}
	all := false
	hit := map[string]bool{}
	for _, r := range refresh {
		if r == "all" {
			all = true
			continue
		}
		hit[r] = false
	}
	for i := range secrets {
		name := secrets[i].SecretName
		if _, requested := hit[name]; all || requested {
			secrets[i].RotateOnConfig = true
			if _, requested := hit[name]; requested {
				hit[name] = true
			}
		}
	}
	var unmatched []string
	for name, matched := range hit {
		if !matched {
			unmatched = append(unmatched, name)
		}
	}
	sort.Strings(unmatched)
	return secrets, unmatched
}

// CollectSecretsFromLabels reconstructs secrets from image label metadata.
func CollectSecretsFromLabels(boxName string, labelSecrets []LabelSecretEntry) []deploykit.CollectedSecret {
	secrets := make([]deploykit.CollectedSecret, 0, len(labelSecrets))
	for _, ls := range labelSecrets {
		secrets = append(secrets, deploykit.CollectedSecret{
			Name:       "charly-" + boxName + "-" + ls.Name,
			Target:     ls.Target,
			Env:        ls.Env,
			SecretName: ls.Name,
		})
	}
	return secrets
}

// ProvisionPodmanSecrets creates podman secrets from the credential store.
// Returns the secrets that were successfully provisioned and any that fell back to env vars.
func ProvisionPodmanSecrets(engine, boxName, instance string, secrets []deploykit.CollectedSecret, autoGenerate bool) (provisioned []deploykit.CollectedSecret, fallbackEnv []string, err error) { //nolint:unparam // error return kept for interface/API stability
	if engine == "docker" {
		fmt.Fprintln(os.Stderr, "NOTE: Docker secrets require Swarm mode (not available).")
		fmt.Fprintln(os.Stderr, "Falling back to environment variable injection for secrets.")
		fmt.Fprintln(os.Stderr, "This is less secure — secret values will be visible in 'docker inspect'.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Consider using Podman for better secrets support:")
		fmt.Fprintln(os.Stderr, "  charly config set engine.run podman")
		// Fall back to env vars for all secrets
		for _, s := range secrets {
			if s.Env != "" {
				val, _ := resolveSecretValue(s, boxName, instance)
				if val != "" {
					fallbackEnv = append(fallbackEnv, s.Env+"="+val)
				}
			}
		}
		return nil, fallbackEnv, nil
	}

	if len(secrets) > 0 {
		fmt.Fprintln(os.Stderr, "Provisioning container secrets:")
	}
	// promptedValues caches values entered interactively for a given podman secret name.
	// Two CollectedSecrets sharing the same Name (but different Env vars) only prompt once.
	promptedValues := make(map[string]string)
	interactive := term.IsTerminal(int(os.Stdin.Fd()))

	for _, s := range secrets {
		// Short-circuit: if a podman secret already exists, keep it
		// unconditionally — unless RotateOnConfig is true, in which case we
		// always re-resolve and re-create so credential rotation via
		// `charly secrets set <name> <new>` takes effect on the next charly config.
		//
		// The default (RotateOnConfig=false) is correct for candy-owned
		// secrets like immich's db-password: overwriting would break a live
		// postgres cluster. RotateOnConfig=true is set by
		// CollectCandySecretAccepts for secret_accepts/secret_requires
		// entries, whose whole point is to reflect the current credential
		// store value on every reconcile. See plan §2.3.
		if !s.RotateOnConfig && podmanSecretExists(engine, s.Name) {
			fmt.Fprintf(os.Stderr, "  %-40s → kept (already provisioned)\n", s.Name)
			provisioned = append(provisioned, s)
			continue
		}

		val, source := resolveSecretValue(s, boxName, instance)
		if val == "" {
			switch {
			case autoGenerate:
				// Auto-generate: reuse if same podman secret name already generated
				if cached, ok := promptedValues[s.Name]; ok {
					val = cached
					source = "auto-generated"
				} else {
					val, source = generateAndStoreSecret("charly/secret", s.Name)
					promptedValues[s.Name] = val
				}
			case interactive:
				if cached, ok := promptedValues[s.Name]; ok {
					val = cached
					source = "user input"
				} else {
					prompt := fmt.Sprintf("Enter value for secret '%s'", s.SecretName)
					if s.Env != "" {
						prompt += fmt.Sprintf(" (%s)", s.Env)
					}
					prompt += ": "
					entered, promptErr := promptPassword(prompt)
					if promptErr != nil {
						fmt.Fprintf(os.Stderr, "  %-40s → prompt failed: %v\n", s.Name, promptErr)
						continue
					}
					if entered == "" {
						fmt.Fprintf(os.Stderr, "  %-40s → skipped (no value entered)\n", s.Name)
						continue
					}
					store := DefaultCredentialStore()
					if storeErr := store.Set("charly/secret", s.Name, entered); storeErr != nil {
						fmt.Fprintf(os.Stderr, "  Warning: could not persist secret '%s': %v\n", s.Name, storeErr)
					}
					promptedValues[s.Name] = entered
					val = entered
					source = "user input"
				}
			default:
				fmt.Fprintf(os.Stderr, "  %-40s → no value configured\n", s.Name)
				fmt.Fprintf(os.Stderr, "\nWARNING: Secret '%s' has no value configured.\n", s.SecretName)
				fmt.Fprintf(os.Stderr, "The container may fail to start properly.\n\n")
				fmt.Fprintf(os.Stderr, "To set it:\n")
				if s.Env != "" {
					fmt.Fprintf(os.Stderr, "  %s=xxx charly config %s  (env var override)\n", s.Env, boxName)
				}
				fmt.Fprintf(os.Stderr, "  charly secrets set charly/secret %s\n\n", s.Name)
				continue
			}
		}

		if err := ensurePodmanSecret(engine, s.Name, val); err != nil {
			fmt.Fprintf(os.Stderr, "  %-40s → FAILED: %v\n", s.Name, err)
			// Fall back to env var if available
			if s.Env != "" {
				fallbackEnv = append(fallbackEnv, s.Env+"="+val)
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-40s → created (from %s)\n", s.Name, source)
		provisioned = append(provisioned, s)
	}

	if len(provisioned) > 0 {
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Note: Secrets are mounted at /run/secrets/<name> inside the container.")
		fmt.Fprintf(os.Stderr, "To update a secret after changing it: charly update %s\n", boxName)
	}

	return provisioned, fallbackEnv, nil
}

// SecretArgs returns --secret flags for container run (direct mode).
func SecretArgs(secrets []deploykit.CollectedSecret) []string {
	args := make([]string, 0, 2*len(secrets))
	for _, s := range secrets {
		args = append(args, "--secret", fmt.Sprintf("%s,target=%s", s.Name, s.Target))
	}
	return args
}

// resolveSecretValue looks up the value for a secret from the credential store.
//
// When CollectedSecret.Service and CollectedSecret.Key are both non-empty,
// they take precedence over the default lookup chain: the credential store
// is queried exactly at (Service, Key) with the Env var as the env override.
// This is the path used by secret_accepts / secret_requires entries
// synthesized by CollectCandySecretAccepts, where the candy author may have
// set `key: charly/api-key/openrouter` to point at a shared credential namespace.
//
// When Service/Key are unset, the default chain (used by candy-owned secrets)
// applies: env var → charly/secret/<podman-name> → charly/secret/<bare-secret-name>.
func resolveSecretValue(s deploykit.CollectedSecret, boxName, instance string) (value, source string) {
	// Explicit override from CollectCandySecretAccepts: query exactly once at
	// (Service, Key), allowing the Env var to win via ResolveCredential's
	// env-first chain.
	if s.Service != "" && s.Key != "" {
		val, src := ResolveCredential(s.Env, s.Service, s.Key, "")
		return val, src
	}

	// Default chain for candy-owned secrets (pre-existing behavior).
	// If the secret has an associated env var, check it first.
	//
	// Multi-tailnet path: when the sidecar resolution set HostEnv to a
	// templated host-side env var name (e.g. TS_AUTHKEY_ARMADILLO_QUAIL_TS_NET),
	// the env-var lookup uses THAT name — the container-side Env (TS_AUTHKEY)
	// is only the QUADLET TARGET, not the host-side source. Without this
	// split, multi-tailnet operators couldn't store per-tailnet keys in
	// `.secrets` (a single TS_AUTHKEY var means a single tailnet).
	envLookup := s.Env
	if s.HostEnv != "" {
		envLookup = s.HostEnv
	}
	if envLookup != "" {
		val, src := ResolveCredential(envLookup, credServiceForSecret(s.Env), credKeyForSecret(boxName, instance), "")
		if val != "" {
			return val, src
		}
	}
	// Try by full podman secret name (e.g. "charly-immich-db-password") — matches `charly secrets set charly/secret charly-immich-db-password`
	if val, src := ResolveCredential("", "charly/secret", s.Name, ""); val != "" {
		return val, src
	}
	// Fallback: try by bare secret name (e.g. "db-password")
	val, src := ResolveCredential("", "charly/secret", s.SecretName, "")
	return val, src
}

// SecretResolution records the result of resolving a single secret_accepts or
// secret_requires entry against the credential store. Returned alongside the
// []CollectedSecret list from CollectCandySecretAccepts so downstream callers
// (checkMissingSecretRequires in Step 5/6) can distinguish "required but
// missing" from "optional and absent" with actionable remediation.
type SecretResolution struct {
	Name     string // env var name (e.g., "OPENROUTER_API_KEY")
	Source   string // ResolveCredential source classification (env/keyring/config/locked/unavailable/default)
	Resolved bool   // true iff a non-empty value was obtained
	Required bool   // true iff the entry came from secret_requires (not secret_accepts)
}

// CollectCandySecretAccepts synthesizes CollectedSecret entries from an
// image's secret_accepts and secret_requires label metadata, resolving each
// against the credential store and returning:
//
//   - []CollectedSecret: one entry per secret whose value was successfully
//     resolved (non-empty). Entries carry Service/Key overrides from the
//     candy manifest `key:` field (default: charly/secret/<env-var-name>) and
//     RotateOnConfig=true so every charly config reconciles them with the
//     latest credential store value (see plan §2.3).
//   - []SecretResolution: one entry per input spec, reporting the source
//     classification and whether the resolution succeeded. Required entries
//     with Resolved=false are later caught by checkMissingSecretRequires as
//     a hard-fail condition.
//
// This function does NOT touch the podman secret store — that's the job of
// ProvisionPodmanSecrets. It only reads from the credential store. No network
// calls, no filesystem mutations, safe to run speculatively.
func CollectCandySecretAccepts(boxName, instance string, meta *BoxMetadata) (collected []deploykit.CollectedSecret, resolutions []SecretResolution) {
	if meta == nil {
		return nil, nil
	}

	resolveOne := func(dep spec.EnvDependency, required bool) {
		// Parse the optional Key override (<service>/<key> form, validated
		// at build time by validateSecretDeps). Default is charly/secret/<name>.
		service := "charly/secret"
		key := dep.Name
		if dep.Key != "" {
			// Key format is already validated (must match ^charly/.../...$).
			// Service is everything before the final '/', key is the last
			// segment (LastIndex avoids depending on the literal prefix length).
			if idx := strings.LastIndex(dep.Key, "/"); idx >= 0 {
				service = dep.Key[:idx]
				key = dep.Key[idx+1:]
			}
		}

		cs := deploykit.CollectedSecret{
			Name:           "charly-" + boxName + "-" + envVarNameToPodmanSecretSlug(dep.Name),
			Target:         "", // type=env directive doesn't use Target
			Env:            dep.Name,
			SecretName:     dep.Name,
			Service:        service,
			Key:            key,
			RotateOnConfig: true,
		}

		val, src := resolveSecretValue(cs, boxName, instance)

		res := SecretResolution{
			Name:     dep.Name,
			Source:   src,
			Resolved: val != "",
			Required: required,
		}
		resolutions = append(resolutions, res)

		if val != "" {
			collected = append(collected, cs)
		}
	}

	for _, dep := range meta.SecretRequire {
		resolveOne(dep, true)
	}
	for _, dep := range meta.SecretAccept {
		resolveOne(dep, false)
	}

	return collected, resolutions
}

// resolveHookSecretEnv returns `NAME=value` entries for every secret_accept /
// secret_require value that resolves from the credential store, so lifecycle
// hooks (post_enable / pre_remove) receive credential-backed secrets EXPLICITLY
// via `podman exec -e`. This is load-bearing: the CLI `-e` form of these secrets
// is scrubbed from c.Env by scrubSecretCLIEnv (never plaintext in charly.yml),
// and a podman `type=env` secret is not reliably inherited by `podman exec`, so
// a hook that consumes a secret (e.g. github-runner's registration token) would
// otherwise never see it. Generic across every hook+secret candy (R3); inert
// (returns nil) when the image declares no secrets or none resolve.
func resolveHookSecretEnv(boxName, instance string, meta *BoxMetadata) []string {
	collected, _ := CollectCandySecretAccepts(boxName, instance, meta)
	var env []string
	for _, s := range collected {
		if s.Env == "" {
			continue
		}
		if val, _ := resolveSecretValue(s, boxName, instance); val != "" {
			env = append(env, s.Env+"="+val)
		}
	}
	return env
}

// credServiceForSecret maps well-known env vars to credential services.
func credServiceForSecret(envVar string) string {
	switch envVar {
	case "VNC_PASSWORD":
		return CredServiceVNC
	default:
		return "charly/secret"
	}
}

// credKeyForSecret returns the credential key for an image/instance pair.
func credKeyForSecret(boxName, instance string) string {
	if instance != "" {
		return boxName + "-" + instance
	}
	return boxName
}

// podmanSecretExists checks whether a podman secret with the given name already exists.
func podmanSecretExists(engine, name string) bool {
	binary := kit.EngineBinary(engine)
	cmd := exec.Command(binary, "secret", "inspect", name)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// ensurePodmanSecret creates or replaces a podman secret.
func ensurePodmanSecret(engine, name, value string) error {
	binary := kit.EngineBinary(engine)
	// Remove existing secret (ignore error if doesn't exist)
	rmCmd := exec.Command(binary, "secret", "rm", name)
	rmCmd.Stderr = nil
	_ = rmCmd.Run()

	// Create new secret from stdin
	createCmd := exec.Command(binary, "secret", "create", name, "-")
	createCmd.Stdin = strings.NewReader(value)
	if output, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("podman secret create %s: %w\n%s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// RemovePodmanSecrets removes podman secrets for an image (best-effort).
func RemovePodmanSecrets(engine string, secrets []deploykit.CollectedSecret) {
	binary := kit.EngineBinary(engine)
	for _, s := range secrets {
		cmd := exec.Command(binary, "secret", "rm", s.Name)
		cmd.Stderr = nil
		_ = cmd.Run()
	}
}
