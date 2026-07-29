package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// pkg.go — the candy-side DRIVE behind the build:pkg word (K3 build-tail move, coneB-pkgcmd): build
// standalone, downloadable native package ARTIFACTS (.pkg.tar.zst / .rpm / .deb) for a candy's
// localpkg sources. Byte-equivalent to the former charly/pkg_cmd.go BoxPkgCmd.Run (DELETED) — the
// hidden core `__box-pkg` reentry it ran behind is gone; candy/plugin-box's dispatchPkg now
// Invokes build:pkg directly (the SAME shape as build:box/build:generate/build:ensure).
//
// Reuses the EXACT K1-loader seam infrastructure resolveBuildEngine (resolve.go) established: LOAD
// via loaderkit.LoadUnified(loaderkit.LoadSeamsFromExecutor), local candy SCAN via the
// buildengine-scan-local host leg + loaderkit.ScanCandyFromLocal, and distro VOCABULARY via
// resolveDistroLeg's InvokeProvider(kind:distro) closure — no host registry callback dragged into
// the plugin. deploykit.BuildLocalPkgOnHost / ResolveLocalPkgDir / CleanupBuiltPackageFiles are
// already pure sdk (W3) and run here directly, same as the former core Run did.

// runBoxPkg is the build:pkg Invoke body.
func runBoxPkg(ctx context.Context, ex *sdk.Executor, req spec.BuildPkgRequest) ([]string, error) {
	dir := req.Dir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = cwd
	}
	candyName := req.Candy
	if candyName == "" {
		candyName = "charly"
	}
	outDir := req.Out
	if outDir == "" {
		outDir = "dist"
	}
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(dir, outDir)
	}

	// --- LOAD the project plugin-side (K1 reverse legs, mirrors resolveBuildEngine step 1) ---
	loaderEx := &buildLoaderExecutor{ctx: ctx, ex: ex}
	uf, ok, err := loaderkit.LoadUnified(dir, loaderkit.LoadSeamsFromExecutor(loaderEx))
	if err != nil {
		return nil, err
	}
	if !ok || uf == nil {
		return nil, fmt.Errorf("box pkg: no project found at %s", dir)
	}

	// --- distro VOCABULARY (mirrors resolveBuildEngine step 2 — distro only, pkg needs no builder/init) ---
	distroCfg := loaderkit.ProjectDistroConfig(uf, resolveDistroLeg(ctx, ex))

	// --- SCAN candies: local (host leg) + remote fetch fixpoint (mirrors resolveBuildEngine step 3) ---
	rr := spec.ResolvedProjectRequest{Dir: dir}
	localScanned, err := scanLocalLeg(ctx, ex, rr)
	if err != nil {
		return nil, err
	}
	layers, err := loaderkit.ScanCandyFromLocal(localScanned, nil, scanSeamsLeg(ctx, ex, rr))
	if err != nil {
		return nil, err
	}

	lyr := layers[candyName]
	if lyr == nil {
		return nil, fmt.Errorf("candy %q not found in %s/candy", candyName, dir)
	}

	formats := req.Format
	if len(formats) == 0 {
		formats = lyr.LocalPkgFormats()
	}
	if len(formats) == 0 {
		return nil, fmt.Errorf("candy %q declares no localpkg sources", candyName)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir %s: %w", outDir, err)
	}

	var written []string
	opts := deploykit.EmitOpts{}
	buildCtx := opts.ContextOrDefault()
	for _, format := range formats {
		src := lyr.LocalPkg(format)
		if src == "" {
			return written, fmt.Errorf("candy %q declares no localpkg source for format %q", candyName, format)
		}
		lp := lookupLocalPkgDef(distroCfg, format)
		if lp == nil {
			return written, fmt.Errorf("no distro in the embedded build vocabulary declares a local_pkg block for format %q", format)
		}
		srcDir := deploykit.ResolveLocalPkgDir(src, lyr.GetSourceDir(), dir, lp.SourceSentinel)
		if srcDir == "" {
			return written, fmt.Errorf("package source %q for format %q not found (sentinel %q)", src, format, lp.SourceSentinel)
		}
		fmt.Fprintf(os.Stderr, "Building %s package for candy %q from %s\n", format, candyName, srcDir)
		files, berr := deploykit.BuildLocalPkgOnHost(buildCtx, lp, srcDir, deploykit.EmitOpts{})
		if berr != nil {
			return written, fmt.Errorf("building %s package: %w", format, berr)
		}
		var copyErr error
		for _, f := range files {
			dst := filepath.Join(outDir, filepath.Base(f))
			if cerr := copyFileTo(f, dst); cerr != nil {
				copyErr = fmt.Errorf("copying %s to %s: %w", f, dst, cerr)
				break
			}
			written = append(written, dst)
			fmt.Printf("%s\n", dst)
		}
		if err := errors.Join(copyErr, deploykit.CleanupBuiltPackageFiles(files)); err != nil {
			return written, err
		}
	}
	return written, nil
}

// lookupLocalPkgDef finds the first distro in the build config that declares a local_pkg block for
// the given package format, returning its contract. Byte-identical to the former core function.
func lookupLocalPkgDef(dc *spec.DistroConfig, format string) *deploykit.LocalPkgDef {
	if dc == nil {
		return nil
	}
	names := make([]string, 0, len(dc.Distro))
	for name := range dc.Distro {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if fn, lp := dc.Distro[name].LocalPkgFormat(format); lp != nil && fn == format {
			return lp
		}
	}
	return nil
}

// copyFileTo copies a file's contents (mode 0644) to dst. Byte-identical to the former core function.
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
