package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
	"golang.org/x/term"
)

// secrets.go — container secret collection + podman secret provisioning. TRACKED
// FINAL/K5 EXIT (DEPLOY-wave W1 audit, 2026-07-20): ResolveCredential/DefaultCredentialStore
// route through connectPluginByWord to verb:credential — the core provider registry, the
// same clause-M coupling as enc.go (see its header). Registered FINAL/K5 inventory
// alongside enc.go's InvokeProvider rewrite, not this wave.
//
// The portable leaf helpers (token generation, password prompting, podman-secret CRUD,
// label reconstruction, credential-key mapping) live in sdk/deploykit/secret_probe.go
// (Cutover B-1 fix round — a pr-validator FAIL caught these left duplicated in-file on
// the first pass; every call site below now calls the deploykit copy, and the in-file
// originals are deleted, per R3/R5). What stays HERE (core): ProvisionPodmanSecrets/
// resolveSecretValue/CollectCandySecretAccepts/resolveHookSecretEnv/generateAndStoreSecret
// — each calls ResolveCredential/DefaultCredentialStore directly or transitively (the
// core provider registry), registered FINAL/K5 inventory alongside enc.go's credential
// family.

// generateAndStoreSecret generates a 32-byte url-safe base64 token (44
// chars; Fernet-key-compatible — see deploykit.GenerateRandomSecretToken),
// persists it to the active credential store at (service, key), and returns
// the value with the "auto-generated" source classification. Persistence
// failures are logged to stderr but not returned as errors — the
// in-memory value is still usable for the current charly invocation.
//
// Used by:
//   - ProvisionPodmanSecrets — config-time CollectedSecret provisioning
//     when --password=auto is in effect.
//   - ensureCandySecret (layer_secrets.go) — deploy-time secret_requires
//     resolution on host/VM/SSH targets when the value is missing.
func generateAndStoreSecret(service, key string) (val, source string) {
	val = deploykit.GenerateRandomSecretToken(32)
	if err := DefaultCredentialStore().Set(service, key, val); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not persist auto-generated secret %s/%s: %v\n",
			service, key, err)
	}
	return val, "auto-generated"
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
		if !s.RotateOnConfig && deploykit.PodmanSecretExists(engine, s.Name) {
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
					entered, promptErr := deploykit.PromptPassword(prompt)
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

		if err := deploykit.EnsurePodmanSecret(engine, s.Name, val); err != nil {
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
		val, src := ResolveCredential(envLookup, deploykit.CredServiceForSecret(s.Env, CredServiceVNC), deploykit.CredKeyForSecret(boxName, instance), "")
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
func CollectCandySecretAccepts(boxName, instance string, meta *spec.BoxMetadata) (collected []deploykit.CollectedSecret, resolutions []SecretResolution) {
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
func resolveHookSecretEnv(boxName, instance string, meta *spec.BoxMetadata) []string {
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
