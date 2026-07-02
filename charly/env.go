package main

import (
	"maps"
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
