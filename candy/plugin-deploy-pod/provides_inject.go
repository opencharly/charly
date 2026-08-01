package deploypod

import (
	"fmt"
	"os"
	"sort"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// provides_inject.go — env_provides / mcp_provides injection into the per-host charly.yml deploy
// config, relocated from charly-core's config_image.go (P11 seam-death — the
// pod-config-inject-env-provides / pod-config-inject-mcp-provides HostBuild seams are DELETED). The
// plugin resolves the provides templates ITSELF (deploykit.ResolveTemplate) and mutates the loaded
// BundleConfig in place; runConfig persists via the EXISTING plugin-side saveBundle seam — the SAME
// loadDeploy→modify→saveBundle pattern the secrets/sidecar persist already uses (R3). The locked
// whole-file write + the marshal resugar run plugin-side via deploykit.SaveBundleConfig (the former
// host pod-config-save-bundle seam + the host save-callback are deleted, #55 coneC-dsh) — so no
// per-kind provides knowledge remains in core.

// injectEnvProvidesInto resolves env_provides templates and stores them in dc.Provides.Env.
// Returns true if any env vars were added or changed. portMap is a {containerPort -> hostPort}
// lookup used by ResolveTemplate to substitute {{.HostPort N}} placeholders (nil degrades to the
// literal container port — only safe for candies that don't use the placeholder).
func injectEnvProvidesInto(dc *deploykit.BundleConfig, boxName, instance string, envProvides map[string]string, portMap map[int]int) bool {
	if dc == nil || len(envProvides) == 0 {
		return false
	}
	if dc.Provides == nil {
		dc.Provides = &spec.ProvidesConfig{}
	}
	ctrName := kit.ContainerNameInstance(boxName, instance)
	source := spec.DeployKey(boxName, instance)
	changed := false
	for _, key := range sortedProvidesKeys(envProvides) {
		value := deploykit.ResolveTemplate(envProvides[key], ctrName, portMap)
		resolved := spec.EnvProvideEntry{Name: key, Value: value, Source: source}

		// Check if already set to same value (dedup by name+source)
		found := false
		for i, existing := range dc.Provides.Env {
			if existing.Name == key && existing.Source == source {
				if existing.Value == value {
					found = true
					break
				}
				dc.Provides.Env[i] = resolved
				found = true
				changed = true
				break
			}
		}
		if !found {
			dc.Provides.Env = append(dc.Provides.Env, resolved)
			changed = true
		}
		if changed {
			fmt.Fprintf(os.Stderr, "Env provides injected: %s=%s\n", key, value)
		}
	}
	return changed
}

// injectMCPProvidesInto resolves mcp_provides templates and adds them to dc.Provides.MCP.
// Returns true if any servers were added or changed.
func injectMCPProvidesInto(dc *deploykit.BundleConfig, boxName, instance string, mcpProvides []spec.MCPServerYAML, portMap map[int]int) bool {
	if dc == nil || len(mcpProvides) == 0 {
		return false
	}
	if dc.Provides == nil {
		dc.Provides = &spec.ProvidesConfig{}
	}
	ctrName := kit.ContainerNameInstance(boxName, instance)
	source := spec.DeployKey(boxName, instance)
	changed := false

	// Remove stale entries from this source (handles name changes on re-config)
	var cleaned []spec.MCPProvideEntry
	for _, e := range dc.Provides.MCP {
		if e.Source != source {
			cleaned = append(cleaned, e)
		}
	}
	if len(cleaned) != len(dc.Provides.MCP) {
		dc.Provides.MCP = cleaned
	}

	for _, mcp := range mcpProvides {
		url := deploykit.ResolveTemplate(mcp.URL, ctrName, portMap)
		transport := mcp.Transport
		if transport == "" {
			transport = "http"
		}
		// Disambiguate MCP name for instances so consumers see unique servers
		mcpName := mcp.Name
		if instance != "" {
			mcpName = mcp.Name + "-" + instance
		}
		resolved := spec.MCPProvideEntry{Name: mcpName, URL: url, Transport: transport, Source: source}

		found := false
		for i, existing := range dc.Provides.MCP {
			if existing.Name == mcpName && existing.Source == source {
				if existing.URL == resolved.URL && existing.Transport == resolved.Transport {
					found = true
					break
				}
				dc.Provides.MCP[i] = resolved
				found = true
				changed = true
				break
			}
		}
		if !found {
			dc.Provides.MCP = append(dc.Provides.MCP, resolved)
			changed = true
		}
		if changed {
			fmt.Fprintf(os.Stderr, "MCP provides injected: %s → %s\n", mcpName, url)
		}
	}
	return changed
}

// sortedProvidesKeys returns the keys of a string map in sorted order (deterministic output).
func sortedProvidesKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
