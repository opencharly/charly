package main

import (
	"maps"
	"sort"
	"strings"
)

// EnvConfig represents environment variables from a candy's manifest
type EnvConfig struct {
	Vars       map[string]string // KEY=value pairs (from env field)
	PathAppend []string          // PATH append entries (from path_append field)
}

// ExpandPath expands ~, ${HOME} and $HOME in a path string to the given home
// directory. ${HOME} is replaced before bare $HOME so the braced form is
// handled (a bare $HOME ReplaceAll would not match "${HOME}").
func ExpandPath(path string, home string) string {
	// Expand ~ at the start of the path
	if strings.HasPrefix(path, "~/") {
		path = home + path[1:]
	} else if path == "~" {
		path = home
	}

	// Expand ${HOME} then $HOME anywhere in the path
	path = strings.ReplaceAll(path, "${HOME}", home)
	path = strings.ReplaceAll(path, "$HOME", home)

	return path
}

// ExpandEnvConfig expands all ~ and $HOME references in an EnvConfig
func ExpandEnvConfig(cfg *EnvConfig, home string) *EnvConfig {
	expanded := &EnvConfig{
		Vars:       make(map[string]string),
		PathAppend: make([]string, len(cfg.PathAppend)),
	}

	for key, value := range cfg.Vars {
		expanded.Vars[key] = ExpandPath(value, home)
	}

	for i, path := range cfg.PathAppend {
		expanded.PathAppend[i] = ExpandPath(path, home)
	}

	return expanded
}

// MergeEnvConfigs merges multiple env configs, later configs override earlier
func MergeEnvConfigs(configs []*EnvConfig) *EnvConfig {
	merged := &EnvConfig{
		Vars:       make(map[string]string),
		PathAppend: []string{},
	}

	for _, cfg := range configs {
		if cfg == nil {
			continue
		}
		// Merge vars (later overrides earlier)
		maps.Copy(merged.Vars, cfg.Vars)
		// Accumulate PATH entries
		merged.PathAppend = append(merged.PathAppend, cfg.PathAppend...)
	}

	return merged
}

// envPairsToMap converts KEY=VALUE pairs (the CLI -e / label wire form) into
// the map form the deploy schema stores since the env-shape unification.
func envPairsToMap(pairs []string) map[string]string {
	if len(pairs) == 0 {
		return nil
	}
	out := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// envMapToPairs converts the deploy schema's env map into sorted KEY=VALUE
// pairs (the OCI-label wire + env-resolution chain form).
func envMapToPairs(env map[string]string) []string {
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
