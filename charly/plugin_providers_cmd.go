package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// PluginProvidersCmd is the hidden `charly __plugin-providers <candy-dir>` introspection:
// it prints the `<class>:<word>` capabilities a candy's `plugin.providers` declares, one per
// line — the SAME list emitBakedPlugins bakes into an in-IMAGE `.providers` manifest and the
// parse-time prescan reads. It is the SINGLE SOURCE for a baked plugin's word manifest: the
// PKGBUILD's host install (/usr/lib/charly/plugins, beside the charly binary) uses it to emit
// the `.providers` file from the candy declaration, so a CLI-served COMMAND word
// (command:secrets — which C1.1 DROPS from the gRPC Describe, since a command plugin is
// dispatched by syscall.Exec, not the registry) is never missed. Reuses collectPluginProviders
// (plugin_prescan.go) so the manifest can never drift from emitBakedPlugins / the prescan (R3).
type PluginProvidersCmd struct {
	Dir string `arg:"" help:"Candy directory containing charly.yml"`
}

func (c *PluginProvidersCmd) Run() error {
	data, err := os.ReadFile(filepath.Join(c.Dir, UnifiedFileName))
	if err != nil {
		return fmt.Errorf("reading candy manifest: %w", err)
	}
	var root any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing candy manifest %s: %w", c.Dir, err)
	}
	var providers []string
	collectPluginProviders(root, &providers)
	for _, p := range providers {
		fmt.Println(p)
	}
	return nil
}
