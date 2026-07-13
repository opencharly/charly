package main

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
)

// merge.go — the `charly box merge` CLI. It resolves a box (or every merge.auto box) to a
// spec.MergeRequest and forwards it to verb:oci (invokeOciMerge, oci_plugin.go); the
// go-containerregistry layer-MERGE engine itself lives OUT-OF-PROCESS in candy/plugin-oci
// (the P14a cutover), so charly core no longer imports go-containerregistry. The plugin
// returns a spec.MergeReply — the progress Notes the host prints + a per-merge Error.
//
// MIGRATION INVENTORY (north-star §4.4): this thinned `charly box merge` CLI shell is UNTIL-K5
// (command-dispersal — every CLI verb becomes a command plugin; main.go knows zero verbs). The
// box-resolution it does (ResolveBox → ref + limits) rides the envelope spine then; the verb:oci
// merge leg is already externalized (this file only builds the request + prints the reply).

// MergeCmd merges small layers in a built container image.
type MergeCmd struct {
	Box        string `arg:"" optional:"" help:"Box name from charly.yml"`
	All        bool   `long:"all" help:"Merge all images with merge.auto enabled"`
	MaxMB      int    `long:"max-mb" help:"Maximum size of a merged layer (MB)"`
	MaxTotalMB int    `long:"max-total-mb" help:"Maximum total image size for merge (MB, 0=no limit)"`
	Tag        string `long:"tag" help:"Image CalVer tag (empty = newest local CalVer resolved via the ai.opencharly.version OCI label)"`
	DryRun     bool   `long:"dry-run" help:"Print merge plan without modifying the image"`
}

// defaultMaxMB / defaultMaxTotalMB are the CLI-resolution defaults (CLI flag → box config →
// default). The plugin applies the SAME safety defaults when a request arrives with 0.
const defaultMaxMB = 128
const defaultMaxTotalMB = 0 // 0 = no limit

func (c *MergeCmd) Run() error {
	if c.Box == "" && !c.All {
		return fmt.Errorf("specify a box name or use --all")
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	if c.All {
		return c.runAll(cfg)
	}
	return c.runOne(cfg, c.Box)
}

// runAll merges all images that have merge.auto enabled.
func (c *MergeCmd) runAll(cfg *Config) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return err
	}

	images, err := cfg.ResolveAllBox("unused", dir, ResolveOpts{})
	if err != nil {
		return err
	}

	// Merge in dependency order so base images are merged before children
	order, err := ResolveBoxOrder(images, layers)
	if err != nil {
		return err
	}

	merged := 0
	for _, name := range order {
		resolved := images[name]
		if resolved.Merge == nil || !resolved.Merge.Auto {
			continue
		}
		fmt.Fprintf(os.Stderr, "\n--- %s ---\n", name)
		if err := c.runOne(cfg, name); err != nil {
			// Per-image merge failures are non-fatal
			fmt.Fprintf(os.Stderr, "Warning: skipping merge for %s: %v\n", name, err)
			continue
		}
		merged++
	}

	if merged == 0 {
		fmt.Fprintf(os.Stderr, "No images have merge.auto enabled\n")
	}
	return nil
}

// runOne merges a single image: resolve the box → ref + limits + engine, then hand a
// spec.MergeRequest to verb:oci and print the reply's progress Notes.
func (c *MergeCmd) runOne(cfg *Config, boxName string) error {
	dir, _ := os.Getwd()
	resolved, err := cfg.ResolveBox(boxName, "unused", dir, ResolveOpts{})
	if err != nil {
		return err
	}

	// Determine max_mb: CLI flags -> the box config -> default
	maxMB := defaultMaxMB
	if resolved.Merge != nil && resolved.Merge.MaxMB > 0 {
		maxMB = resolved.Merge.MaxMB
	}
	if c.MaxMB > 0 {
		maxMB = c.MaxMB
	}

	// Determine max_total_mb: CLI flags -> the box config -> default
	maxTotalMB := defaultMaxTotalMB
	if resolved.Merge != nil && resolved.Merge.MaxTotalMB > 0 {
		maxTotalMB = resolved.Merge.MaxTotalMB
	}
	if c.MaxTotalMB > 0 {
		maxTotalMB = c.MaxTotalMB
	}

	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)

	// Resolve build engine for save/load
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	reply, err := invokeOciMerge(spec.MergeRequest{
		ImageRef:   imageRef,
		Engine:     rt.BuildEngine,
		MaxMB:      maxMB,
		MaxTotalMB: maxTotalMB,
		DryRun:     c.DryRun,
	})
	if err != nil {
		return err
	}
	for _, note := range reply.Notes {
		fmt.Fprintln(os.Stderr, note)
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}
