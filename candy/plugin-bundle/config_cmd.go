package bundle

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// config_cmd.go — the K4-C move of the `charly bundle` CONFIG-MANAGEMENT subcommands
// (show/export/import/reset/status) out of charly core. Every handler below calls ONLY
// already-sdk-portable deploykit/kit functions. The reads/writes reach the host ONLY for what a
// separate module genuinely cannot hold: InvokeProvider("build","project") for export's
// project-load (the SAME seam compile.go already uses), the "pod-config-load-bundle" read seam
// (loadBundleConfig), and the "loader-threaded" Primaries snapshot. import/reset's deploy-state
// WRITE now runs PLUGIN-SIDE — deploykit.SaveBundleConfig with the plugin's OWN loader-backed
// reader + a marshal callback that resugars each plan step from the loader-threaded Primaries
// (deployMarshalNode), NOT the deleted host "deploy-config-save" seam (#55 K4 config-write
// seam-collapse). IMPORT-PURITY: imports ONLY github.com/opencharly/sdk (deploykit/kit/spec are
// subpackages); never charly/.
//
// Bed-robustness batch item 5 (the placement-dependent silent-no-op class): every READ below
// goes through the package-local loadBundleConfig() (ephemeral.go), which resolves the per-host
// overlay via the "pod-config-load-bundle" HostBuild seam — NEVER the raw deploykit.LoadBundleConfig()
// (which no-ops errorlessly unless the calling process happens to have registered
// deploykit.DeployStateHost at init — true ONLY while command:bundle stays compiled-in, a
// per-BUILD placement fact, never an authoring guarantee). This was DORMANT (not an active bug)
// because plugin-bundle is compiled-in TODAY, but every one of these 6 call sites would have
// silently degraded to "no charly.yml configured" the moment plugin-bundle is ever built
// out-of-process — exactly the failure mode plugin-deploy-vm's vmPrepareVenue hit for real
// (lifecycle.go, same batch). Fixed prophylactically here rather than left for the next
// placement change to rediscover.

// fetchResolvedProject moved to compile.go (R3 — the SINGLE resolved-project envelope fetch, shared
// by the config leg, the per-shape compile, and the walk's ref classification). The 3-arg form takes
// (dir, extraCandyRefs, includeDisabled); this config caller passes (dir, nil, false).

// deployMarshalNode builds the per-entry node-form marshal callback deploykit.SaveBundleConfig /
// SaveDeployState take. It resugars each plan step via the loader-threaded Primaries snapshot
// (fetchLoaderPrimaries) — the SAME registry-derived D-fact the deleted host deploy-config-save
// leg fed to deploykit.MarshalBundleNode via loaderThreaded().Primaries. Sourcing Primaries
// PLUGIN-SIDE is what lets the deploy-state WRITE run here instead of over a host seam (#55 K4).
func deployMarshalNode() func(name string, node *deploykit.BundleNode) (*yaml.Node, error) {
	primaries := fetchLoaderPrimaries()
	return func(_ string, node *deploykit.BundleNode) (*yaml.Node, error) {
		return deploykit.MarshalBundleNode(node, primaries)
	}
}

// saveDeployConfig persists dc PLUGIN-SIDE via deploykit.SaveBundleConfig directly (#55 K4
// config-write seam-collapse — the narrow HostBuild("deploy-config-save") host leg is deleted).
// loadBundleConfig is the plugin's own loader-backed reader for the write path's fail-safe
// re-check, so the write no longer depends on the host's DeployStateHost registration.
func saveDeployConfig(dc *deploykit.BundleConfig) error {
	return deploykit.SaveBundleConfig(dc, deployMarshalNode(), loadBundleConfig)
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
	dc, err := loadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil || len(dc.Bundle) == 0 {
		fmt.Println("No charly.yml configured")
		return nil
	}
	if box != "" {
		key := spec.DeployKey(box, instance)
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
		rp, err := fetchResolvedProject(dir, nil, false)
		if err != nil {
			return fmt.Errorf("loading charly.yml: %w", err)
		}
		dc = deploykit.ExportAllBox(rp)
	} else {
		loaded, err := loadBundleConfig()
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
		existing, err := loadBundleConfig()
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
			existing, _ := loadBundleConfig()
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

	dc, err := loadBundleConfig()
	if err != nil {
		return err
	}
	if dc == nil {
		fmt.Printf("No overrides for box %q\n", box)
		return nil
	}

	key := spec.DeployKey(box, instance)
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
	dc, err := loadBundleConfig()
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
			img, inst := spec.ParseDeployKey(key)
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
