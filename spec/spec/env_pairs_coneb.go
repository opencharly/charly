package spec

// env_pairs_coneb.go — the env-map → sorted KEY=VALUE pairs helper, RELOCATED to the spec/spec
// fabric slice (#55 coneB build-render cone, Class A — a pure stdlib-only transform over the
// deploy env E-envelope, co-located with the label/env value types). The body is verbatim from
// sdk/kit/env.go; sdk/kit re-exports it (var EnvMapToPairs = spec.EnvMapToPairs) so existing
// kit.EnvMapToPairs call sites (sdk/deploykit's deploy_file.go + read_labels.go, kit/box_metadata.go)
// are unchanged. spec/container's ExtractMetadata (box_metadata_coneb.go) calls spec.EnvMapToPairs
// directly — the canonical home.

import "sort"

// EnvMapToPairs converts the deploy schema's env map into sorted KEY=VALUE
// pairs (the OCI-label wire + env-resolution chain form).
func EnvMapToPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
