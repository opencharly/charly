package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dotenvLoaded tracks which env var names were loaded from .env (for source attribution in charly config list).
var dotenvLoaded = make(map[string]bool)

// DotenvLoaded reports whether a given env var name was loaded from the project .env file.
func DotenvLoaded(name string) bool {
	return dotenvLoaded[name]
}

// resetDotenvLoaded clears the tracking map (for testing).
func resetDotenvLoaded() {
	dotenvLoaded = make(map[string]bool)
}

// LoadProcessDotenv loads .env from dir into the process environment.
// Variables already set in the environment are NOT overwritten (real env wins).
// Silently returns nil if .env does not exist.
func LoadProcessDotenv(dir string) error {
	envPath := filepath.Join(dir, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return nil
	}

	entries, err := ParseEnvFile(envPath)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		before, after, ok := strings.Cut(entry, "=")
		if !ok {
			// Bare KEY (no value) — skip, inheriting from host is meaningless here
			continue
		}
		key := before
		value := after

		// Only set if NOT already in environment (real env takes precedence)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
			dotenvLoaded[key] = true
		}
	}

	return nil
}

// ParseEnvFile reads a .env file and returns KEY=VALUE strings.
// Skips comments (#), blank lines, and supports KEY=VALUE and KEY="VALUE" (strips quotes).
// Compatible with docker --env-file format.
func ParseEnvFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading env file %s: %w", path, err)
	}
	return ParseEnvBytes(data)
}

// ParseEnvBytes parses KEY=VALUE entries from raw bytes.
// Skips comments (#), blank lines, and strips surrounding quotes from values.
func ParseEnvBytes(data []byte) ([]string, error) {
	var envs []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip blank lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Must contain = for KEY=VALUE format
		before, after, ok := strings.Cut(line, "=")
		if !ok {
			// KEY without value — pass through as-is (docker behavior: inherits from host)
			envs = append(envs, line)
			continue
		}

		key := before
		value := after

		// Strip surrounding quotes from value
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		envs = append(envs, key+"="+value)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parsing env data: %w", err)
	}

	return envs, nil
}

// LoadWorkspaceEnv loads env vars from a workspace .env file (if it exists).
// Does NOT run direnv — direnv modifies the host env before charly runs.
// Returns nil, nil if no .env file found.
func LoadWorkspaceEnv(workspace string) ([]string, error) {
	envPath := filepath.Join(workspace, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return nil, nil
	}
	return ParseEnvFile(envPath)
}

// ResolveEnvVars merges env vars from multiple sources.
// Priority (last wins for duplicate keys): global env < deploy config < workspace .env < CLI --env-file < CLI -e flags.
func ResolveEnvVars(globalEnv []string, deployEnv []string, deployEnvFile string, envDir string, cliEnvFile string, cliEnv []string) ([]string, error) {
	var all []string

	// 0. Global env vars from charly.yml (lowest priority — service discovery)
	all = append(all, globalEnv...)

	// 1. Per-image deploy config env vars
	all = append(all, deployEnv...)

	// 2. Deploy config env file
	if deployEnvFile != "" {
		expanded := expandHostHome(deployEnvFile)
		vars, err := ParseEnvFile(expanded)
		if err != nil {
			return nil, err
		}
		all = append(all, vars...)
	}

	// 3. Workspace .env file
	if envDir != "" {
		vars, err := LoadWorkspaceEnv(envDir)
		if err != nil {
			return nil, err
		}
		all = append(all, vars...)
	}

	// 4. CLI --env-file
	if cliEnvFile != "" {
		vars, err := ParseEnvFile(cliEnvFile)
		if err != nil {
			return nil, err
		}
		all = append(all, vars...)
	}

	// 5. CLI -e flags (highest priority)
	all = append(all, cliEnv...)

	// Deduplicate: last value for each key wins, then normalize NO_PROXY
	return normalizeNoProxy(deduplicateEnv(all)), nil
}

// normalizeNoProxy converts semicolons to commas in NO_PROXY/no_proxy values.
// Semicolons were a workaround for Kong's comma-splitting of []string flags.
// Standard HTTP clients (Python, curl, Go) require comma-separated NO_PROXY.
func normalizeNoProxy(envs []string) []string {
	for i, e := range envs {
		before, after, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		key := before
		if key == "NO_PROXY" || key == "no_proxy" {
			val := after
			envs[i] = key + "=" + strings.ReplaceAll(val, ";", ",")
		}
	}
	return envs
}

// enrichNoProxy appends container hostnames to NO_PROXY when a proxy is configured.
// Chrome does not support CIDR ranges in NO_PROXY — only exact hostnames and domain suffixes.
// This ensures container-to-container traffic bypasses the proxy.
func enrichNoProxy(envs []string, containerNames []string) []string {
	if len(containerNames) == 0 {
		return envs
	}
	hasProxy := false
	noProxyIdx := -1
	for i, e := range envs {
		before, _, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		key := before
		if key == "HTTP_PROXY" || key == "HTTPS_PROXY" ||
			key == "http_proxy" || key == "https_proxy" {
			hasProxy = true
		}
		if key == "NO_PROXY" || key == "no_proxy" {
			noProxyIdx = i
		}
	}
	if !hasProxy {
		return envs
	}
	// Build set of existing NO_PROXY entries
	existing := map[string]bool{}
	var key, val string
	if noProxyIdx >= 0 {
		idx := strings.IndexByte(envs[noProxyIdx], '=')
		key = envs[noProxyIdx][:idx]
		val = envs[noProxyIdx][idx+1:]
		for entry := range strings.SplitSeq(val, ",") {
			existing[strings.TrimSpace(entry)] = true
		}
	} else {
		key = "NO_PROXY"
	}
	// Append missing container names
	var additions []string
	for _, name := range containerNames {
		if !existing[name] {
			additions = append(additions, name)
		}
	}
	if len(additions) == 0 {
		return envs
	}
	newVal := val
	if newVal != "" {
		newVal += ","
	}
	newVal += strings.Join(additions, ",")
	entry := key + "=" + newVal
	if noProxyIdx >= 0 {
		envs[noProxyIdx] = entry
	} else {
		envs = append(envs, entry)
	}
	return envs
}

// deduplicateEnv deduplicates env vars, keeping the last value for each key.
func deduplicateEnv(envs []string) []string {
	seen := make(map[string]int) // key -> index in result
	var result []string

	for _, e := range envs {
		key := e
		if before, _, ok := strings.Cut(e, "="); ok {
			key = before
		}

		if prevIdx, ok := seen[key]; ok {
			// Replace previous entry
			result[prevIdx] = e
		} else {
			seen[key] = len(result)
			result = append(result, e)
		}
	}

	return result
}
