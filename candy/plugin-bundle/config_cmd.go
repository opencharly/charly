package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// config_cmd.go — the K4-C move of the `charly bundle` CONFIG-MANAGEMENT subcommands
// (show/export/import/reset/status) out of charly core. Every handler below calls ONLY
// already-sdk-portable deploykit/kit functions; the ONE host touch each needs is a narrow,
// established seam — HostBuild("resolved-project") for export's project-load (the SAME seam
// compile.go already uses) and HostBuild("deploy-config-save") for import/reset's deploy-state
// WRITE (the ONE piece that still needs the host: the per-entry marshal callback resugars each
// plan step via the host-owned pluginPrimaries registry, a live in-process table a separate
// module cannot reach). IMPORT-PURITY: imports ONLY github.com/opencharly/sdk (deploykit/kit/
// spec are subpackages); never charly/.

// fetchResolvedProject fetches the project envelope via the established HostBuild
// ("resolved-project") seam — the SAME call compile.go's compileDeployPlans makes.
func fetchResolvedProject(dir string) (*spec.ResolvedProject, error) {
	if cmdExec == nil {
		return nil, fmt.Errorf("bundle config: no host reverse channel (command not compiled-in?)")
	}
	reqJSON, err := json.Marshal(spec.ResolvedProjectRequest{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("bundle config: marshal resolved-project request: %w", err)
	}
	replyJSON, err := cmdExec.HostBuild(cmdCtx, "resolved-project", reqJSON)
	if err != nil {
		return nil, fmt.Errorf("bundle config: fetch resolved-project envelope: %w", err)
	}
	var rp spec.ResolvedProject
	if err := json.Unmarshal(replyJSON, &rp); err != nil {
		return nil, fmt.Errorf("bundle config: decode resolved-project envelope: %w", err)
	}
	return &rp, nil
}

// saveDeployConfig persists dc via the narrow HostBuild("deploy-config-save") seam.
func saveDeployConfig(dc *deploykit.BundleConfig) error {
	configJSON, err := json.Marshal(dc)
	if err != nil {
		return fmt.Errorf("bundle config: marshal deploy config: %w", err)
	}
	return hostDeploySeam("deploy-config-save", spec.DeployConfigSaveRequest{ConfigJSON: configJSON})
}

func marshalConfigToStdout(dc *deploykit.BundleConfig) error {
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

// runBundleShow serves `charly bundle show [box]`.
func runBundleShow(box, instance string) error {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil || len(dc.Bundle) == 0 {
		fmt.Println("No charly.yml configured")
		return nil
	}
	if box != "" {
		key := deploykit.DeployKey(box, instance)
		entry, ok := dc.Bundle[key]
		if !ok {
			fmt.Printf("No overrides for box %q\n", key)
			return nil
		}
		out := &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{key: entry}}
		return marshalConfigToStdout(out)
	}
	return marshalConfigToStdout(dc)
}

// runBundleExport serves `charly bundle export [boxes...]`.
func runBundleExport(boxes []string, output string, all bool) error {
	var dc *deploykit.BundleConfig
	if all {
		dir, _ := os.Getwd()
		rp, err := fetchResolvedProject(dir)
		if err != nil {
			return fmt.Errorf("loading charly.yml: %w", err)
		}
		dc = deploykit.ExportAllBox(rp)
	} else {
		loaded, err := deploykit.LoadBundleConfig()
		if err != nil {
			return err
		}
		if loaded == nil || len(loaded.Bundle) == 0 {
			fmt.Fprintln(os.Stderr, "No charly.yml overrides to export")
			return nil
		}
		dc = loaded
	}
	if len(boxes) > 0 {
		dc = filterDeployBox(dc, boxes)
	}
	if output != "" {
		data, err := yaml.Marshal(dc)
		if err != nil {
			return err
		}
		if err := os.WriteFile(output, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", output, err)
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", output)
		return nil
	}
	return marshalConfigToStdout(dc)
}

// runBundleImport serves `charly bundle import <files...>`.
func runBundleImport(files []string, replace bool, box string) error {
	var inputs []*deploykit.BundleConfig
	for _, f := range files {
		dc, err := deploykit.LoadDeployFile(f)
		if err != nil {
			return err
		}
		inputs = append(inputs, dc)
	}

	var base *deploykit.BundleConfig
	if !replace {
		existing, err := deploykit.LoadBundleConfig()
		if err != nil {
			return err
		}
		base = existing
	}
	if base == nil {
		base = &deploykit.BundleConfig{Bundle: make(map[string]spec.BundleNode)}
	}

	merged := deploykit.MergeDeployConfigs(append([]*deploykit.BundleConfig{base}, inputs...)...)

	if box != "" {
		entry, ok := merged.Bundle[box]
		if !ok {
			return fmt.Errorf("box %q not found in input files", box)
		}
		if !replace {
			existing, _ := deploykit.LoadBundleConfig()
			if existing != nil {
				existing.Bundle[box] = entry
				merged = existing
			} else {
				merged = &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{box: entry}}
			}
		} else {
			merged = &deploykit.BundleConfig{Bundle: map[string]spec.BundleNode{box: entry}}
		}
	}

	if err := saveDeployConfig(merged); err != nil {
		return err
	}

	path, _ := kit.DefaultDeployConfigPath()
	fmt.Fprintf(os.Stderr, "Imported %d file(s) into %s\n", len(files), path)
	return nil
}

// runBundleReset serves `charly bundle reset [box]`.
func runBundleReset(box, instance string) error {
	if box == "" {
		path, err := kit.DefaultDeployConfigPath()
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
		fmt.Printf("No overrides for box %q\n", box)
		return nil
	}

	key := deploykit.DeployKey(box, instance)
	if _, ok := dc.Bundle[key]; !ok {
		fmt.Printf("No overrides for box %q\n", key)
		return nil
	}

	deploykit.RemoveBoxDeploy(dc, key)

	if len(dc.Bundle) == 0 {
		path, _ := kit.DefaultDeployConfigPath()
		_ = os.Remove(path)
		fmt.Printf("Removed overrides for %q (charly.yml now empty, removed)\n", key)
		return nil
	}

	if err := saveDeployConfig(dc); err != nil {
		return err
	}
	fmt.Printf("Removed overrides for %q\n", key)
	return nil
}

// runBundleStatus serves `charly bundle status`.
func runBundleStatus() error {
	dc, err := deploykit.LoadBundleConfig()
	if err != nil {
		return err
	}

	qdir, qdirErr := kit.QuadletDir()
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

	deployToStem := make(map[string]string)
	stemToDeploy := make(map[string]string)
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

	for key, stem := range deployToStem {
		if !quadletBoxes[stem] {
			fmt.Printf("%-40s charly.yml: yes  quadlet: no   (stale config)\n", key)
		}
	}
	for stem := range quadletBoxes {
		if key, ok := stemToDeploy[stem]; ok {
			fmt.Printf("%-40s charly.yml: yes  quadlet: yes  (ok)\n", key)
		} else {
			fmt.Printf("%-40s charly.yml: no   quadlet: yes  (no overrides)\n", stem)
		}
	}

	return nil
}
