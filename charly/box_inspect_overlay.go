package main

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
)

// box_inspect_overlay.go — the hidden `charly __box-inspect-overlay` reentry behind the COMPILED-IN
// candy/plugin-box `charly box inspect --format tunnel|bind_mounts` words. The plugin renders every
// other inspect format from the generic spec.ResolvedProject envelope, but tunnel + bind_mounts read
// the DEPLOY OVERLAY (charly.yml) — deploy-mode state the build-mode envelope deliberately does not
// carry — so the plugin reaches this retained core command over HostBuild("cli").
//
// K5-doomed: dies when the deploy-overlay read moves into the plugin over sdk kits (the config-resolve
// seam family collapse). Until then it preserves the EXACT former InspectCmd tunnel/bind_mounts output.

// InspectOverlayCmd renders the two deploy-overlay-only inspect formats for one box.
type InspectOverlayCmd struct {
	Box             string `arg:"" help:"Box name"`
	Format          string `long:"format" help:"Overlay format: tunnel | bind_mounts"`
	Instance        string `short:"i" long:"instance" help:"Instance name"`
	IncludeDisabled bool   `long:"include-disabled" help:"Operate on boxes with enabled: false (does not modify charly.yml)"`
}

func (c *InspectOverlayCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}
	// Resolve the box FIRST (matching the former InspectCmd.runFromConfig ordering: a bad box name
	// errors before any format renders, for both overlay formats).
	calverTag := ComputeCalVer()
	resolved, err := ResolveBox(cfg, c.Box, calverTag, dir, ResolveOpts{IncludeDisabled: c.IncludeDisabled})
	if err != nil {
		return err
	}

	switch c.Format {
	case "bind_mounts":
		// bind_mounts are deploy-time only; show charly.yml volume config.
		if overlay, ok := deploykit.LoadDeployConfigForRead("charly box inspect bind_mounts").Lookup(c.Box, c.Instance); ok {
			for _, dv := range overlay.Volume {
				fmt.Printf("%s\t%s\t%s\t%s\n", dv.Name, dv.Host, dv.Path, dv.Type)
			}
		}
		return nil
	case "tunnel":
		c.formatTunnel(cfg, dir, resolved)
		return nil
	default:
		return fmt.Errorf("unknown overlay format field: %s (want tunnel | bind_mounts)", c.Format)
	}
}

// formatTunnel prints the deploy-time tunnel config for the box. Tunnel lives off BoxConfig/ResolvedBox
// (deploy-only) — it resolves from BundleNode.Tunnel via charly.yml. Any resolution failure is silently
// skipped (no tunnel output), matching the prior inline behaviour.
func (c *InspectOverlayCmd) formatTunnel(cfg *Config, dir string, resolved *buildkit.ResolvedBox) {
	overlay, ok := deploykit.LoadDeployConfigForRead("charly box inspect tunnel").Lookup(c.Box, c.Instance)
	if !ok || overlay.Tunnel == nil {
		return
	}
	layers, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return
	}
	portProtos := make(map[string]string)
	boxPorts, _ := CollectBoxPorts(cfg, layers, c.Box)
	tc := ResolveTunnelConfig(overlay.Tunnel, c.Box, "", layers, resolved.Candy, portProtos, boxPorts)
	if tc == nil || len(tc.Ports) == 0 {
		return
	}
	fmt.Println("PORT\tACCESS\tPROTOCOL\tHOSTNAME")
	for _, tp := range tc.Ports {
		access := "private"
		if tp.Public {
			access = "public"
		}
		hostname := tp.Hostname
		if hostname == "" {
			hostname = "-"
		}
		fmt.Printf("%d\t%s\t%s\t%s\n", tp.Port, access, tp.Protocol, hostname)
	}
}
