package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
	"gopkg.in/yaml.v3"
)

// deployShowCmd displays the current charly.yml content. The CLI grammar for
// `charly bundle show` lives in the command:bundle plugin (candy/plugin-bundle);
// this struct is reconstructed from spec.DeployConfigRequest by the deploy-config
// host-build seam (op "show").
type deployShowCmd struct {
	Box      string
	Instance string
}

func (c *deployShowCmd) Run() error {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil || len(dc.Bundle) == 0 {
		fmt.Println("No charly.yml configured")
		return nil
	}

	if c.Box != "" {
		key := deploykit.DeployKey(c.Box, c.Instance)
		entry, ok := dc.Bundle[key]
		if !ok {
			fmt.Printf("No overrides for box %q\n", key)
			return nil
		}
		// Print just this image's config
		out := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{key: entry}}
		return marshalToStdout(out)
	}

	return marshalToStdout(dc)
}

// deployExportCmd exports the current effective runtime configuration.
type deployExportCmd struct {
	Boxes  []string
	Output string
	All    bool
}

func (c *deployExportCmd) Run() error {
	if c.All {
		return c.exportAll()
	}
	return c.exportOverrides()
}

func (c *deployExportCmd) exportAll() error {
	dir, _ := os.Getwd()
	// #67 keystone (K5-Unit-1): ExportAllBox reads the RESOLVED-PROJECT envelope, not the
	// live *Config graph. buildResolvedProjectFromDir is the same load+project entry the
	// "resolved-project" HostBuild seam wraps; it returns an empty envelope for a
	// project-less dir (no ErrNoCharlyYml propagation) — matching the former
	// LoadConfigRaw-fail-tolerant behaviour.
	rp, err := buildResolvedProjectFromDir(dir, ResolveOpts{})
	if err != nil {
		return fmt.Errorf("loading charly.yml: %w", err)
	}
	dc := deploykit.ExportAllBox(rp)
	if len(c.Boxes) > 0 {
		dc = filterDeployBox(dc, c.Boxes)
	}
	return c.output(dc)
}

func (c *deployExportCmd) exportOverrides() error {
	dc, err := deploykit.LoadBundleConfig()
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

func (c *deployExportCmd) output(dc *deploykit.BundleConfig) error {
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

// deployImportCmd loads charly.yml file(s) into ~/.config/charly/charly.yml.
type deployImportCmd struct {
	Files   []string
	Replace bool
	Box     string
}

func (c *deployImportCmd) Run() error {
	// Load input files
	var inputs []*deploykit.BundleConfig
	for _, f := range c.Files {
		dc, err := deploykit.LoadDeployFile(f)
		if err != nil {
			return err
		}
		inputs = append(inputs, dc)
	}

	// Start with existing or empty
	var base *deploykit.BundleConfig
	if !c.Replace {
		existing, err := deploykit.LoadBundleConfig()
		if err != nil {
			return err
		}
		base = existing
	}
	if base == nil {
		base = &deploykit.BundleConfig{Bundle: make(map[string]spec.BundleNode)}
	}

	// Merge input files left-to-right
	merged := deploykit.MergeDeployConfigs(append([]*deploykit.BundleConfig{base}, inputs...)...)

	// Filter to single image if requested
	if c.Box != "" {
		entry, ok := merged.Bundle[c.Box]
		if !ok {
			return fmt.Errorf("box %q not found in input files", c.Box)
		}
		// Preserve other images from existing config, replace only the target
		if !c.Replace {
			existing, _ := deploykit.LoadBundleConfig()
			if existing != nil {
				existing.Bundle[c.Box] = entry
				merged = existing
			} else {
				merged = &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{c.Box: entry}}
			}
		} else {
			merged = &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{c.Box: entry}}
		}
	}

	if err := saveBundleConfigNodeForm(merged); err != nil {
		return err
	}

	path, _ := DeployConfigPath()
	fmt.Fprintf(os.Stderr, "Imported %d file(s) into %s\n", len(c.Files), path)
	return nil
}

// deployResetCmd removes charly.yml overrides.
type deployResetCmd struct {
	Box      string
	Instance string
}

func (c *deployResetCmd) Run() error {
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

	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil {
		fmt.Printf("No overrides for box %q\n", c.Box)
		return nil
	}

	key := deploykit.DeployKey(c.Box, c.Instance)
	if _, ok := dc.Bundle[key]; !ok {
		fmt.Printf("No overrides for box %q\n", key)
		return nil
	}

	deploykit.RemoveBoxDeploy(dc, key)

	if len(dc.Bundle) == 0 {
		// No images left — remove the file
		path, _ := DeployConfigPath()
		_ = os.Remove(path)
		fmt.Printf("Removed overrides for %q (charly.yml now empty, removed)\n", key)
		return nil
	}

	if err := saveBundleConfigNodeForm(dc); err != nil {
		return err
	}
	fmt.Printf("Removed overrides for %q\n", key)
	return nil
}

// deployStatusCmd shows sync status between charly.yml and quadlet files.
type deployStatusCmd struct{}

func (c *deployStatusCmd) Run() error {
	dc, err := deploykit.LoadBundleConfig()
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
			img, inst := deploykit.ParseDeployKey(key)
			stem := strings.TrimPrefix(kit.ContainerNameInstance(img, inst), "charly-")
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

func marshalToStdout(dc *deploykit.BundleConfig) error {
	data, err := yaml.Marshal(dc)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

func filterDeployBox(dc *deploykit.BundleConfig, names []string) *deploykit.BundleConfig {
	filtered := &deploykit.BundleConfig{Bundle: make(map[string]spec.BundleNode)}
	for _, name := range names {
		if entry, ok := dc.Bundle[name]; ok {
			filtered.Bundle[name] = entry
		}
	}
	return filtered
}
