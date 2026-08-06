package fleet

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// config_cmd.go — the K4-C move of the `charly fleet` CONFIG-MANAGEMENT subcommands
// (show/export/import/reset/status) out of charly core. Every handler below calls ONLY
// already-sdk-portable deploykit/kit functions. The reads/writes reach the host ONLY for what a
// separate module genuinely cannot hold: InvokeProvider("build","project") for export's
// project-load (the SAME seam compile.go already uses), the loaderkit.LoadHostFleetConfigViaExecutor
// overlay read (loadFleetConfig), and the "loader-threaded" Primaries snapshot. import/reset's deploy-state
// WRITE now runs PLUGIN-SIDE — deploykit.SaveFleetConfig with the plugin's OWN loader-backed
// reader + a marshal callback that resugars each plan step from the loader-threaded Primaries
// (deployMarshalNode), NOT the deleted host "deploy-config-save" seam (#55 K4 config-write
// seam-collapse). IMPORT-PURITY: imports ONLY github.com/opencharly/sdk (deploykit/kit/spec are
// subpackages); never charly/.
//
// Bed-robustness batch item 5 (the placement-dependent silent-no-op class): every READ below
// goes through the package-local loadFleetConfig() (ephemeral.go), which resolves the per-host
// overlay via the cycle-free loaderkit.LoadHostFleetConfigViaExecutor read — NEVER the raw deploykit.LoadFleetConfig()
// (which no-ops errorlessly unless the calling process happens to have registered
// deploykit.DeployStateHost at init — true ONLY while command:fleet stays compiled-in, a
// per-BUILD placement fact, never an authoring guarantee). This was DORMANT (not an active bug)
// because plugin-fleet is compiled-in TODAY, but every one of these 6 call sites would have
// silently degraded to "no charly.yml configured" the moment plugin-fleet is ever built
// out-of-process — exactly the failure mode plugin-deploy-vm's vmPrepareVenue hit for real
// (lifecycle.go, same batch). Fixed prophylactically here rather than left for the next
// placement change to rediscover.

// fetchResolvedProject moved to compile.go (R3 — the SINGLE resolved-project envelope fetch, shared
// by the config leg, the per-shape compile, and the walk's ref classification). The 3-arg form takes
// (dir, extraCandyRefs, includeDisabled); this config caller passes (dir, nil, false).

// deployMarshalNode builds the per-entry node-form marshal callback deploykit.SaveFleetConfig /
// SaveDeployState take. It resugars each plan step via the loader-threaded Primaries snapshot
// (fetchLoaderPrimaries) — the SAME registry-derived D-fact the deleted host deploy-config-save
// leg fed to deploykit.MarshalFleetNode via loaderThreaded().Primaries. Sourcing Primaries
// PLUGIN-SIDE is what lets the deploy-state WRITE run here instead of over a host seam (#55 K4).
func deployMarshalNode() func(name string, node *deploykit.FleetNode) (*yaml.Node, error) {
	primaries := fetchLoaderPrimaries()
	return func(_ string, node *deploykit.FleetNode) (*yaml.Node, error) {
		return deploykit.MarshalFleetNode(node, primaries)
	}
}

// saveDeployConfig persists dc PLUGIN-SIDE via deploykit.SaveFleetConfig directly (#55 K4
// config-write seam-collapse — the narrow HostBuild("deploy-config-save") host leg is deleted).
// loadFleetConfig is the plugin's own loader-backed reader for the write path's fail-safe
// re-check, so the write no longer depends on the host's DeployStateHost registration.
func saveDeployConfig(dc *deploykit.FleetConfig) error {
	return deploykit.SaveFleetConfig(dc, deployMarshalNode(), loadFleetConfig)
}

// mutateDeployConfig runs one locked read-modify-write cycle over the per-host deploy overlay,
// supplying this plugin's reader + persist callback to the SHARED deploykit.MutateFleetConfig
// cycle. Every write in this plugin goes through it: the mutation runs against a config re-read
// INSIDE the lock, so a concurrent `charly config` / `charly vm create` / bed runner write is
// merged onto rather than clobbered.
//
// It replaces this package's former private lock helper — one of three identical per-candy copies,
// and the one that guarded only the vm-entry removal while `charly fleet import`, `charly fleet
// reset` and the three ephemeral writers below took no lock at all.
func mutateDeployConfig(mutate deploykit.FleetConfigMutator) error {
	_, err := deploykit.MutateFleetConfig(loadFleetConfig, saveDeployConfig, mutate)
	return err
}

func marshalConfigToStdout(dc *deploykit.FleetConfig) error {
	data, err := yaml.Marshal(dc)
	if err != nil {
		return err
	}
	fmt.Print(string(data))
	return nil
}

func filterDeployBox(dc *deploykit.FleetConfig, names []string) *deploykit.FleetConfig {
	filtered := &deploykit.FleetConfig{Fleet: make(map[string]spec.FleetNode)}
	for _, name := range names {
		if entry, ok := dc.Fleet[name]; ok {
			filtered.Fleet[name] = entry
		}
	}
	return filtered
}

// runFleetShow serves `charly fleet show [box]`.
func runFleetShow(box, instance string) error {
	dc, err := loadFleetConfig()
	if err != nil {
		return err
	}
	if dc == nil || len(dc.Fleet) == 0 {
		fmt.Println("No charly.yml configured")
		return nil
	}
	if box != "" {
		key := spec.DeployKey(box, instance)
		entry, ok := dc.Fleet[key]
		if !ok {
			fmt.Printf("No overrides for box %q\n", key)
			return nil
		}
		out := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{key: entry}}
		return marshalConfigToStdout(out)
	}
	return marshalConfigToStdout(dc)
}

// runFleetExport serves `charly fleet export [boxes...]`.
func runFleetExport(boxes []string, output string, all bool) error {
	var dc *deploykit.FleetConfig
	if all {
		dir, _ := os.Getwd()
		rp, err := fetchResolvedProject(dir, nil, false)
		if err != nil {
			return fmt.Errorf("loading charly.yml: %w", err)
		}
		dc = deploykit.ExportAllBox(rp)
	} else {
		loaded, err := loadFleetConfig()
		if err != nil {
			return err
		}
		if loaded == nil || len(loaded.Fleet) == 0 {
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

// runFleetImport serves `charly fleet import <files...>`.
func runFleetImport(files []string, replace bool, box string) error {
	var inputs []*deploykit.FleetConfig
	for _, f := range files {
		dc, err := deploykit.LoadDeployFile(f)
		if err != nil {
			return err
		}
		inputs = append(inputs, dc)
	}

	// The merge runs INSIDE the locked cycle against a freshly-read overlay (dc), so an import
	// racing a concurrent `charly config` merges onto that writer's entries instead of discarding
	// them. `--replace` still means wholesale replacement: it merges the input files onto an EMPTY
	// base rather than onto dc.
	if err := mutateDeployConfig(func(dc *deploykit.FleetConfig) (bool, error) {
		base := dc
		if replace {
			base = &deploykit.FleetConfig{Fleet: make(map[string]spec.FleetNode)}
		}
		merged := deploykit.MergeDeployConfigs(append([]*deploykit.FleetConfig{base}, inputs...)...)
		if box != "" {
			entry, ok := merged.Fleet[box]
			if !ok {
				return false, fmt.Errorf("box %q not found in input files", box)
			}
			if replace {
				merged = &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{box: entry}}
			} else {
				// Single-box import: only that entry changes; every other fresh entry stays.
				dc.Fleet[box] = entry
				merged = dc
			}
		}
		*dc = *merged
		return true, nil
	}); err != nil {
		return err
	}

	path, _ := kit.DefaultDeployConfigPath()
	fmt.Fprintf(os.Stderr, "Imported %d file(s) into %s\n", len(files), path)
	return nil
}

// runFleetReset serves `charly fleet reset [box]`.
func runFleetReset(box, instance string) error {
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

	key := spec.DeployKey(box, instance)
	found, emptied := false, false
	// Locked cycle: the entry is looked up and removed in the SAME hold as the write, so a reset
	// racing a concurrent overlay writer can neither miss a just-written entry nor resurrect one.
	// The emptied branch removes the file outright, so it reports changed=false — nothing to save.
	if err := mutateDeployConfig(func(dc *deploykit.FleetConfig) (bool, error) {
		if _, ok := dc.Fleet[key]; !ok {
			return false, nil
		}
		found = true
		deploykit.RemoveBoxDeploy(dc, key)
		if len(dc.Fleet) == 0 {
			path, _ := kit.DefaultDeployConfigPath()
			_ = os.Remove(path)
			emptied = true
			return false, nil
		}
		return true, nil
	}); err != nil {
		return err
	}
	switch {
	case !found:
		fmt.Printf("No overrides for box %q\n", key)
	case emptied:
		fmt.Printf("Removed overrides for %q (charly.yml now empty, removed)\n", key)
	default:
		fmt.Printf("Removed overrides for %q\n", key)
	}
	return nil
}

// runFleetStatus serves `charly fleet status`.
func runFleetStatus() error {
	dc, err := loadFleetConfig()
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
		for key := range dc.Fleet {
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
