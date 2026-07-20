package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// config_image.go — the residual charly-core body behind `charly config`'s enc-only leaves +
// the loader-coupled provides-injection helpers. P13-KERNEL direction-flip: BoxConfigSetupCmd
// and BoxConfigRemoveCmd (the config-setup/remove ORCHESTRATION — resolveDeployRef,
// prepareQuadletEnv, resolveSidecars, runConfig, runConfigDirect, parseVolumeFlags,
// persistResourceCaps, directPodmanArgs, directDeployMarker*, IsDirectDeploy,
// checkMissingEnvRequires/checkMissingSecretRequires/warnMissingMCPRequires,
// updateAllDeployedQuadlets) moved to candy/plugin-deploy-pod (sdk.OpConfigSetup/
// sdk.OpConfigRemove — see host_build_pod_config.go + host_build_pod_config_seams.go). What
// stays: Status/Mount/Unmount/Passwd (already one-line forwards to enc.go, itself FINAL/K5-
// deferred registry-coupled inventory) + injectEnvProvides/injectMCPProvides (loader-coupled —
// called from the pod-config-inject-env-provides/pod-config-inject-mcp-provides seam handlers).

// BoxConfigStatusCmd shows encrypted volume status.
type BoxConfigStatusCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BoxConfigStatusCmd) Run() error {
	return encStatus(c.Box, c.Instance)
}

// BoxConfigMountCmd mounts encrypted volumes.
type BoxConfigMountCmd struct {
	Box      string `arg:"" help:"Box name"`
	Volume   string `long:"volume" help:"Only mount this volume (by name)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BoxConfigMountCmd) Run() error {
	return encMount(c.Box, c.Instance, c.Volume)
}

// BoxConfigUnmountCmd unmounts encrypted volumes.
type BoxConfigUnmountCmd struct {
	Box      string `arg:"" help:"Box name"`
	Volume   string `long:"volume" help:"Only unmount this volume (by name)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BoxConfigUnmountCmd) Run() error {
	return encUnmount(c.Box, c.Instance, c.Volume)
}

// BoxConfigPasswdCmd changes the gocryptfs password.
type BoxConfigPasswdCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BoxConfigPasswdCmd) Run() error {
	return encPasswd(c.Box, c.Instance)
}

// injectEnvProvides resolves env_provides templates and stores them in charly.yml provides.env.
// Returns true if any env vars were added or changed.
//
// portMap is a {containerPort -> hostPort} lookup used by resolveTemplate
// to substitute {{.HostPort N}} placeholders against the resolved port
// mapping list. nil is accepted (HostPort substitutions degrade to the
// literal container port — only safe for candies that don't actually use
// the placeholder).
func injectEnvProvides(boxName, instance string, envProvides map[string]string, portMap map[int]int) (bool, error) {
	if len(envProvides) == 0 {
		return false, nil
	}

	dc, err := deploykit.LoadDeployConfigForWrite("injectEnvProvides")
	if err != nil {
		return false, err
	}
	if dc.Provides == nil {
		dc.Provides = &deploykit.ProvidesConfig{}
	}

	ctrName := kit.ContainerNameInstance(boxName, instance)
	changed := false

	// Sort keys for deterministic output
	keys := sortedStringMapKeys(envProvides)
	for _, key := range keys {
		tmpl := envProvides[key]
		value := deploykit.ResolveTemplate(tmpl, ctrName, portMap)
		source := deploykit.DeployKey(boxName, instance)
		resolved := deploykit.EnvProvideEntry{
			Name:   key,
			Value:  value,
			Source: source,
		}

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

	if changed {
		if err := saveBundleConfigNodeForm(dc); err != nil {
			return false, fmt.Errorf("saving deploy config: %w", err)
		}
	}
	return changed, nil
}

// injectMCPProvides resolves mcp_provides templates and adds them to charly.yml.
// Returns true if any servers were added or changed.
//
// portMap is a {containerPort -> hostPort} lookup used by resolveTemplate
// to substitute {{.HostPort N}} placeholders. nil is accepted.
func injectMCPProvides(boxName, instance string, mcpProvides []spec.MCPServerYAML, portMap map[int]int) (bool, error) {
	if len(mcpProvides) == 0 {
		return false, nil
	}

	dc, err := deploykit.LoadDeployConfigForWrite("injectMCPProvides")
	if err != nil {
		return false, err
	}
	if dc.Provides == nil {
		dc.Provides = &deploykit.ProvidesConfig{}
	}

	ctrName := kit.ContainerNameInstance(boxName, instance)
	source := deploykit.DeployKey(boxName, instance)
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
		resolved := spec.MCPProvideEntry{
			Name:      mcpName,
			URL:       url,
			Transport: transport,
			Source:    source,
		}

		// Check if already set to same value
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

	if changed {
		if err := saveBundleConfigNodeForm(dc); err != nil {
			return false, fmt.Errorf("saving deploy config: %w", err)
		}
	}
	return changed, nil
}

// sortedStringMapKeys returns the keys of a string map in sorted order.
func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
