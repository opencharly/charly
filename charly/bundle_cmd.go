package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// BundleCmd manages deployments and charly.yml overrides.
//
// The `add` and `del` subcommands (added in the BuildTarget refactor)
// apply a box/candy plan to a target: either a container (named
// anything) or the local host (literal name "host"). The existing
// config-management subcommands (export/import/show/reset/path/status)
// remain unchanged — they manipulate charly.yml itself.
type BundleCmd struct {
	Add BundleAddCmd `cmd:"" help:"Apply a deploy: 'host' targets the local system; any other name targets a container"`
	Del BundleDelCmd `cmd:"" help:"Tear down a deploy by name"`

	FromImage BundleFromBoxCmd `cmd:"" name:"from-box" help:"Source-less deploy from a built image's baked OCI labels (no charly.yml project). Pod by default; --cluster targets K8s"`

	Export BundleExportCmd `cmd:"" help:"Export effective config as charly.yml"`
	Import BundleImportCmd `cmd:"" help:"Import charly.yml file(s) into config"`
	Path   BundlePathCmd   `cmd:"" help:"Print charly.yml file path"`
	Reset  BundleResetCmd  `cmd:"" help:"Remove charly.yml overrides"`
	Show   BundleShowCmd   `cmd:"" help:"Show current charly.yml overrides"`
	Status BundleStatusCmd `cmd:"" help:"Show sync status between charly.yml and quadlet files"`
}

// BundleShowCmd displays the current charly.yml content.
type BundleShowCmd struct {
	Box      string `arg:"" optional:"" help:"Show overrides for a specific box"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BundleShowCmd) Run() error {
	dc, err := LoadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil || len(dc.Bundle) == 0 {
		fmt.Println("No charly.yml configured")
		return nil
	}

	if c.Box != "" {
		key := deployKey(c.Box, c.Instance)
		entry, ok := dc.Bundle[key]
		if !ok {
			fmt.Printf("No overrides for box %q\n", key)
			return nil
		}
		// Print just this image's config
		out := &BundleConfig{Bundle: map[string]BundleNode{key: entry}}
		return marshalToStdout(out)
	}

	return marshalToStdout(dc)
}

// BundleExportCmd exports the current effective runtime configuration.
type BundleExportCmd struct {
	Boxes  []string `arg:"" optional:"" help:"Boxes to export (default: all with overrides)"`
	Output string   `short:"o" help:"Write to file instead of stdout"`
	All    bool     `help:"Export all enabled boxes with all runtime fields"`
}

func (c *BundleExportCmd) Run() error {
	if c.All {
		return c.exportAll()
	}
	return c.exportOverrides()
}

func (c *BundleExportCmd) exportAll() error {
	dir, _ := os.Getwd()
	cfg, err := LoadConfigRaw(dir)
	if err != nil {
		return fmt.Errorf("loading charly.yml: %w", err)
	}
	dc := ExportAllBox(cfg)
	if len(c.Boxes) > 0 {
		dc = filterDeployBox(dc, c.Boxes)
	}
	return c.output(dc)
}

func (c *BundleExportCmd) exportOverrides() error {
	dc, err := LoadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil || len(dc.Bundle) == 0 {
		fmt.Fprintln(os.Stderr, "No charly.yml overrides to export")
		return nil
	}
	if len(c.Boxes) > 0 {
		dc = filterDeployBox(dc, c.Boxes)
	}
	return c.output(dc)
}

func (c *BundleExportCmd) output(dc *BundleConfig) error {
	if c.Output != "" {
		data, err := yaml.Marshal(dc)
		if err != nil {
			return err
		}
		if err := os.WriteFile(c.Output, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", c.Output, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", c.Output)
		return nil
	}
	return marshalToStdout(dc)
}

// BundleImportCmd loads charly.yml file(s) into ~/.config/charly/charly.yml.
type BundleImportCmd struct {
	Files   []string `arg:"" help:"Deploy YAML files to import (merged left-to-right)"`
	Replace bool     `help:"Replace entire charly.yml instead of merging with existing"`
	Box     string   `long:"box" help:"Import only this box's config"`
}

func (c *BundleImportCmd) Run() error {
	// Load input files
	var inputs []*BundleConfig
	for _, f := range c.Files {
		dc, err := LoadDeployFile(f)
		if err != nil {
			return err
		}
		inputs = append(inputs, dc)
	}

	// Start with existing or empty
	var base *BundleConfig
	if !c.Replace {
		existing, err := LoadBundleConfig()
		if err != nil {
			return err
		}
		base = existing
	}
	if base == nil {
		base = &BundleConfig{Bundle: make(map[string]BundleNode)}
	}

	// Merge input files left-to-right
	merged := MergeDeployConfigs(append([]*BundleConfig{base}, inputs...)...)

	// Filter to single image if requested
	if c.Box != "" {
		entry, ok := merged.Bundle[c.Box]
		if !ok {
			return fmt.Errorf("box %q not found in input files", c.Box)
		}
		// Preserve other images from existing config, replace only the target
		if !c.Replace {
			existing, _ := LoadBundleConfig()
			if existing != nil {
				existing.Bundle[c.Box] = entry
				merged = existing
			} else {
				merged = &BundleConfig{Bundle: map[string]BundleNode{c.Box: entry}}
			}
		} else {
			merged = &BundleConfig{Bundle: map[string]BundleNode{c.Box: entry}}
		}
	}

	if err := SaveBundleConfig(merged); err != nil {
		return err
	}

	path, _ := DeployConfigPath()
	fmt.Fprintf(os.Stderr, "Imported %d file(s) into %s\n", len(c.Files), path)
	return nil
}

// BundleResetCmd removes charly.yml overrides.
type BundleResetCmd struct {
	Box      string `arg:"" optional:"" help:"Box to reset (omit to clear all)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *BundleResetCmd) Run() error {
	if c.Box == "" {
		// Clear entire charly.yml
		path, err := DeployConfigPath()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No charly.yml to remove")
				return nil
			}
			return err
		}
		fmt.Println("Removed charly.yml")
		return nil
	}

	dc, err := LoadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil {
		fmt.Printf("No overrides for box %q\n", c.Box)
		return nil
	}

	key := deployKey(c.Box, c.Instance)
	if _, ok := dc.Bundle[key]; !ok {
		fmt.Printf("No overrides for box %q\n", key)
		return nil
	}

	RemoveBoxDeploy(dc, key)

	if len(dc.Bundle) == 0 {
		// No images left — remove the file
		path, _ := DeployConfigPath()
		_ = os.Remove(path)
		fmt.Printf("Removed overrides for %q (charly.yml now empty, removed)\n", key)
		return nil
	}

	if err := SaveBundleConfig(dc); err != nil {
		return err
	}
	fmt.Printf("Removed overrides for %q\n", key)
	return nil
}

// BundlePathCmd prints the charly.yml file path.
type BundlePathCmd struct{}

func (c *BundlePathCmd) Run() error {
	path, err := DeployConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// BundleStatusCmd shows sync status between charly.yml and quadlet files.
type BundleStatusCmd struct{}

func (c *BundleStatusCmd) Run() error {
	dc, err := LoadBundleConfig()
	if err != nil {
		return err
	}

	// Enumerate quadlet files
	qdir, qdirErr := quadletDir()
	quadletBoxes := make(map[string]bool)
	if qdirErr == nil {
		entries, readErr := os.ReadDir(qdir)
		if readErr == nil {
			for _, e := range entries {
				name := e.Name()
				if strings.HasPrefix(name, "charly-") && strings.HasSuffix(name, ".container") {
					boxName := strings.TrimSuffix(strings.TrimPrefix(name, "charly-"), ".container")
					if boxName != "" {
						quadletBoxes[boxName] = true
					}
				}
			}
		}
	}

	// Map deploy keys to quadlet stems for cross-referencing
	// e.g., "selkies-desktop/foo" → quadlet stem "selkies-desktop-foo"
	deployToStem := make(map[string]string) // deploy key → quadlet stem
	stemToDeploy := make(map[string]string) // quadlet stem → deploy key
	if dc != nil {
		for key := range dc.Bundle {
			img, inst := parseDeployKey(key)
			stem := strings.TrimPrefix(containerNameInstance(img, inst), "charly-")
			deployToStem[key] = stem
			stemToDeploy[stem] = key
		}
	}

	if len(deployToStem) == 0 && len(quadletBoxes) == 0 {
		fmt.Println("No charly.yml entries and no quadlet files found")
		return nil
	}

	// Stale charly.yml entries (no quadlet)
	for key, stem := range deployToStem {
		if !quadletBoxes[stem] {
			fmt.Printf("%-40s charly.yml: yes  quadlet: no   (stale config)\n", key)
		}
	}
	// Both exist or quadlet only
	for stem := range quadletBoxes {
		if key, ok := stemToDeploy[stem]; ok {
			fmt.Printf("%-40s charly.yml: yes  quadlet: yes  (ok)\n", key)
		} else {
			fmt.Printf("%-40s charly.yml: no   quadlet: yes  (no overrides)\n", stem)
		}
	}

	return nil
}

// --- helpers ---

func marshalToStdout(dc *BundleConfig) error {
	data, err := yaml.Marshal(dc)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

func filterDeployBox(dc *BundleConfig, names []string) *BundleConfig {
	filtered := &BundleConfig{Bundle: make(map[string]BundleNode)}
	for _, name := range names {
		if entry, ok := dc.Bundle[name]; ok {
			filtered.Bundle[name] = entry
		}
	}
	return filtered
}
