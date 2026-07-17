package main

// pkg_cmd.go — `charly box pkg`: build standalone, downloadable native package
// ARTIFACTS (.pkg.tar.zst / .rpm / .deb) for a candy's localpkg sources.
//
// This is the release-artifact counterpart of the deploy-time localpkg step:
// both build the package the SAME way — through the format's embedded build vocabulary's
// `local_pkg.build_template` rendered by deploykit.BuildLocalPkgOnHost (R3) — so there is
// ONE per-format build definition and ZERO distro-specific Go here. The command
// is format-blind: it looks up each requested format's local_pkg block in
// the embedded build vocabulary and the candy's per-format source dir, builds, and copies the
// produced files into the output dir.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

// BoxPkgCmd builds native package artifacts for a candy's localpkg sources. The user-facing
// `charly box pkg` grammar now lives in the COMPILED-IN candy/plugin-box (command:pkg, nested
// under box); this struct is registered as the hidden `charly __box-pkg` reentry the plugin reaches
// over HostBuild("cli") — the pkg build engine (candy scan → the candy's localpkg source dir)
// needs the host build context the plugin cannot compute pre-K1; deploykit.BuildLocalPkgOnHost
// itself is a pure downstream call.
//
// K1-doomed: dies when plugin-box loads the project itself via sdk/loadkit (K1).
type BoxPkgCmd struct {
	Format []string `arg:"" optional:"" help:"Package formats to build (pac/rpm/deb). Default: every format the candy declares a localpkg source for."`
	Candy  string   `long:"candy" default:"charly" help:"Candy whose localpkg sources to build."`
	Out    string   `long:"out" default:"dist" help:"Output directory for the built package files."`
}

func (c *BoxPkgCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Load the local candies to find the requested candy's per-format localpkg
	// sources (config-driven — no hardcoded pkg/<dir> paths).
	layers, err := ScanCandy(dir)
	if err != nil {
		return fmt.Errorf("scanning candies: %w", err)
	}
	lyr := layers[c.Candy]
	if lyr == nil {
		return fmt.Errorf("candy %q not found in %s/candy", c.Candy, dir)
	}

	formats := c.Format
	if len(formats) == 0 {
		formats = lyr.LocalPkgFormats()
	}
	if len(formats) == 0 {
		return fmt.Errorf("candy %q declares no localpkg sources", c.Candy)
	}

	// Load the build config to resolve each format's local_pkg contract.
	dc, _, _, err := LoadBuildConfigForBox(dir)
	if err != nil {
		return fmt.Errorf("loading build config: %w", err)
	}

	outDir := c.Out
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(dir, outDir)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir %s: %w", outDir, err)
	}

	ctx := deploykit.EmitOpts{}.ContextOrDefault()
	for _, format := range formats {
		src := lyr.LocalPkg(format)
		if src == "" {
			return fmt.Errorf("candy %q declares no localpkg source for format %q", c.Candy, format)
		}
		lp := lookupLocalPkgDef(dc, format)
		if lp == nil {
			return fmt.Errorf("no distro in the embedded build vocabulary declares a local_pkg block for format %q", format)
		}
		srcDir := deploykit.ResolveLocalPkgDir(src, lyr.GetSourceDir(), dir, lp.SourceSentinel)
		if srcDir == "" {
			return fmt.Errorf("package source %q for format %q not found (sentinel %q)", src, format, lp.SourceSentinel)
		}
		fmt.Fprintf(os.Stderr, "Building %s package for candy %q from %s\n", format, c.Candy, srcDir)
		files, err := deploykit.BuildLocalPkgOnHost(ctx, lp, srcDir, deploykit.EmitOpts{})
		if err != nil {
			return fmt.Errorf("building %s package: %w", format, err)
		}
		for _, f := range files {
			dst := filepath.Join(outDir, filepath.Base(f))
			if err := copyFileTo(f, dst); err != nil {
				return fmt.Errorf("copying %s to %s: %w", f, dst, err)
			}
			fmt.Printf("%s\n", dst)
		}
	}
	return nil
}

// lookupLocalPkgDef finds the first distro in the build config that declares a
// local_pkg block for the given package format, returning its contract. The
// per-format build/install/glob/sentinel all come from this config — the only
// distro knowledge lives in the embedded build vocabulary (charly/charly.yml), never here.
func lookupLocalPkgDef(dc *buildkit.DistroConfig, format string) *LocalPkgDef {
	if dc == nil {
		return nil
	}
	names := make([]string, 0, len(dc.Distro))
	for name := range dc.Distro {
		names = append(names, name)
	}
	kit.SortStrings(names)
	for _, name := range names {
		if fn, lp := dc.Distro[name].LocalPkgFormat(format); lp != nil && fn == format {
			return lp
		}
	}
	return nil
}

// copyFileTo copies a file's contents (mode 0644) to dst.
func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close() //nolint:errcheck
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
