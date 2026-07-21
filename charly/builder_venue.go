package main

// builder_venue.go — the VENUE-AGNOSTIC BuilderStep execution path, shared (R3) by
// the VM deploy target AND the RunHostStep host-engine channel (the host-served reverse
// leg). It builds a BuilderStep's artifacts on the HOST (where podman + the builder
// images live) and installs them onto an arbitrary venue via the DeployExecutor:
//
//   - aur (s.LocalPkg != nil): produces .pkg.tar.zst files in a host staging dir, then
//     ships + installs them onto the venue via the SHARED deploykit.TransferAndInstallPkgs leg.
//   - npm/pixi/cargo (home-artifact): produces user-home subdirs (~/.npm-global,
//     ~/.pixi, ~/.cargo) baking the VENUE home path, tars them, and extracts into the
//     venue user's $HOME over the executor.
//
// The dep-builder engine itself (deploykit.BuildDepPkgsOnHost → kit.BuilderRun → podman) is a
// PURE sdk/deploykit function (W3) — this file's ONE remaining core dependency is the injected
// image-resolve/ensure closures (resolveImageRefForEnsure / EnsureImagePresent → `charly box
// build`, a genuine loader action), which this function closes over build.Cfg/build.ProjectDir
// and passes in. This function is the orchestration that drives the dep-builder against a venue
// executor — the SAME body the VM target used (extracted verbatim, behavior-preserving), so an
// out-of-process deploy/step plugin driving a BuilderStep over the RunHostStep reverse channel
// runs the IDENTICAL machinery a built-in VM deploy runs — no second implementation.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// buildEngineContext is the host-ENGINE context the reverse channel carries so the
// host-served RunHostStep leg can run the in-core machinery a HOST-ENGINE step kind needs:
// a BuilderStep's host build (EnsureImagePresent + BuilderRun need the project Config + dir
// to resolve a short / namespace-qualified builder image and to fall back to a local
// `charly box build`), and a SystemPackagesStep's host package-install render (the format's
// phase.install.host template lives in the resolved DistroConfig). The deploy lifecycle
// supplies it (the DeployContext's Cfg + Dir + DistroCfg for an external deploy substrate;
// the Local/VM target's own Cfg + ProjectDir + DistroCfg for an external `run: plugin:`
// step). The ENGINE itself is never carried across the process boundary — only this
// descriptor is.
type buildEngineContext struct {
	Cfg        *Config
	ProjectDir string
	// DistroCfg is the resolved distro: vocabulary the SystemPackagesStep host render
	// (deploykit.RenderHostPackageCommand) needs to look up the format's phase.install.host
	// template. Zero for an Invoke whose plan has no SystemPackagesStep.
	DistroCfg *buildkit.DistroConfig

	// The following are populated ONLY by the pod-overlay BUILD-emit path (the
	// buildEngineContext). They no longer feed an in-core renderer directly (the former
	// HOST-COUPLED system-packages/builder/local-pkg-install/op step-emitters — C1.2-C1.5 — were
	// relocated onto candy/plugin-installstep's OWN "resolved-project"-built deploykit.Generator,
	// K5-Unit-6b): ociSpliceClassStepEmit (charly/oci_step_emit.go) reads Generator/Box to populate
	// the class:step OpEmit's spec.BuildEnv scalars (Image=Box.Name, DevLocalPkg=Generator.DevLocalPkg)
	// so the plugin can render without a per-render host round-trip. They are zero for every
	// deploy-leg buildEngineContext — the Builder DEPLOY leg is runVenueBuilderStep and the
	// LocalPkgInstall DEPLOY leg is deploykit.ExecLocalPkgInstall (separate host-engine paths
	// driven via RunHostStep), which read none of them.
	Generator *Generator
	Box       *buildkit.ResolvedBox
	// ImageBuildDir is the per-image (pod-overlay) build dir — rides the class:step OpEmit's
	// BuildEnv.ImageBuildDir so candy/plugin-installstep's emitLocalPkgInstall can stage a
	// dev-mode locally-built package the SAME way deploykit's renderLocalPkgImageDevInstall did. It
	// is buildEngineContext.ImageBuildDir, NOT Generator.BuildDir (the overlay build dir differs
	// from the project .build root). Zero for every deploy-leg context.
	ImageBuildDir string
	// ContextRelPrefix is the build-context-relative prefix for staged inline content — rides the
	// class:step OpEmit's BuildEnv.ContextRelPrefix so candy/plugin-installstep's emitOp can pass it
	// to dg.EmitTasks, staging a write: op's content-addressed COPY source under the correct
	// .build/<image>/_inline path. It is buildEngineContext.ContextRelPrefix. Zero for every
	// deploy-leg context.
	ContextRelPrefix string
}

// builderStepImage resolves the builder image ref for a BuilderStep:
// --builder-image override → the compiled BuilderStep.BuilderImage. The builder always
// runs on the HOST (podman); the venue never needs a container runtime.
func builderStepImage(s *deploykit.BuilderStep, opts deploykit.EmitOpts) (string, error) {
	image := opts.BuilderImageOverride
	if image == "" {
		image = s.BuilderImage
	}
	if image == "" {
		return "", fmt.Errorf("no builder image for %s (candy=%s); set --builder-image or define builder.%s in charly.yml",
			s.Builder, s.CandyName, s.Builder)
	}
	return image, nil
}

// runVenueBuilderStep builds a BuilderStep on the HOST and installs the artifacts onto
// the venue the executor addresses. Routes by OUTPUT shape, not builder name: a builder
// that produces package FILES carries the format's local_pkg contract (s.LocalPkg, set
// by the compiler for the aur builder) and goes through the build-on-host → transfer →
// package-install leg; everything else is a home-artifact builder (pixi/npm/cargo) whose
// ~/.pixi / ~/.npm-global / ~/.cargo output is tarred into the venue home. An unknown
// builder with neither shape has no host build script (deploykit.RenderBuilderScript errors on a
// nil BuilderDef cell); --skip-incompatible skips it.
func runVenueBuilderStep(ctx context.Context, exec deploykit.DeployExecutor, venueHome string, build buildEngineContext, s *deploykit.BuilderStep, opts deploykit.EmitOpts) error {
	if s.LocalPkg == nil {
		if s.BuilderDef == nil || buildkit.BuilderPhaseTemplate(s.BuilderDef, spec.PhaseInstall, spec.VenueHostNative) == "" {
			if opts.SkipIncompatible {
				fmt.Fprintf(os.Stderr, "builder step %q (candy=%s) skipped: no phase.install.host cell (--skip-incompatible)\n", s.Builder, s.CandyName)
				return nil
			}
			return fmt.Errorf("builder %q on venue target has no phase.install.host cell in the embedded build vocabulary (candy=%s). Run with --skip-incompatible to skip, or add the host cell", s.Builder, s.CandyName)
		}
		return runVenueHomeArtifactBuilder(ctx, exec, venueHome, build, s, opts)
	}

	image, err := builderStepImage(s, opts)
	if err != nil {
		return err
	}
	// Gate on the format's local_pkg.probe (e.g. `command -v pacman`) succeeding on the
	// VENUE — config-driven, not a hardcoded distro/builder-name check.
	if !deploykit.VenueHasPkgManager(ctx, exec, s.LocalPkg, opts) {
		return fmt.Errorf("builder %q (candy=%s) builds %s package files but the venue has no %s package manager (local_pkg.probe %q failed); cannot install the built packages",
			s.Builder, s.CandyName, s.LocalPkg.DepBuilder, s.LocalPkg.DepBuilder, s.LocalPkg.Probe)
	}

	// Build the aur packages on the HOST through the SHARED host-side dep-build helper
	// (R3) — the builder runs on the host (podman); the venue never needs a container
	// runtime. The package glob comes from the format config. The image resolve/ensure
	// seams are INJECTED closures (deploykit.BuildDepPkgsOnHost imports no *Config) —
	// closing over build.Cfg/build.ProjectDir here, the one genuine core dependency.
	matches, err := deploykit.BuildDepPkgsOnHost(ctx, s.LocalPkg, s.BuilderDef, image, deploykit.ExtractStringSlice(s.RawStageContext, "packages"), s.CandyDir,
		func(img string) (string, error) { return resolveImageRefForEnsure(img, build.Cfg, build.ProjectDir) },
		func(ctx context.Context, img string) error {
			return EnsureImagePresent(ctx, img, build.Cfg, build.ProjectDir)
		},
		opts)
	if err != nil {
		return fmt.Errorf("venue aur builder: %w", err)
	}
	if opts.DryRun {
		return nil
	}

	// Ship the built packages to the venue and install them via the SHARED, config-driven
	// transfer+install leg (R3). The install command (e.g. `pacman -U`) comes from the
	// format's local_pkg.install_template and is the upgrade form, so a re-run after a
	// partial failure replaces the staging content idempotently.
	installErr := deploykit.TransferAndInstallPkgs(ctx, exec, s.LocalPkg, matches, opts)
	cleanupErr := deploykit.CleanupBuiltPackageFiles(matches)
	return errors.Join(installErr, cleanupErr)
}

// venueBuilderTarName names the per-invocation transfer tarball on the venue.
// uniqueScope (the host staging dir's MkdirTemp suffix) makes two concurrent
// deploys of the same candy to the same venue collision-free; the extract and
// cleanup legs below quote it, so it travels verbatim.
func venueBuilderTarName(candyName, uniqueScope string) string {
	return "/tmp/charly-builder-" + candyName + "-" + uniqueScope + ".tar.gz"
}

// runVenueHomeArtifactBuilder runs a user-home builder (npm/pixi/cargo) on the HOST into
// a staging dir bind-mounted AS the venue home, then ships the produced home subdirs into
// the venue user's $HOME over the executor.
//
// The critical move is running the builder with HOME = the VENUE home PATH (venueHome).
// npm shebangs, cargo binary rpaths, and pixi env activation scripts bake the
// install-prefix path; baking the venue's home means the artifacts work unchanged once
// extracted into the venue's real $HOME. Build caches (.cache/) are excluded from the
// transfer — they're large and the venue doesn't need them.
func runVenueHomeArtifactBuilder(ctx context.Context, dexec deploykit.DeployExecutor, venueHome string, build buildEngineContext, s *deploykit.BuilderStep, opts deploykit.EmitOpts) error {
	image, err := builderStepImage(s, opts)
	if err != nil {
		return err
	}
	if venueHome == "" && !opts.DryRun {
		return fmt.Errorf("runVenueHomeArtifactBuilder: venue home unresolved (candy=%s)", s.CandyName)
	}
	if venueHome == "" {
		venueHome = "/home/charly" // dry-run placeholder; never written
	}

	// Host staging dir mounted AS the venue home inside the builder, so the builder
	// writes ~/.npm-global etc. to a host-side dir while baking the venue's home path
	// into shebangs/configs.
	stageHost, err := os.MkdirTemp("", "charly-venue-builder-")
	if err != nil {
		return fmt.Errorf("builder staging mkdir: %w", err)
	}
	RegisterTempCleanup(stageHost)
	defer func() { _ = os.RemoveAll(stageHost); UnregisterTempCleanup(stageHost) }()

	bindMounts := map[string]string{venueHome: stageHost}
	envVars := kit.UserScopeEnv(venueHome)
	script, err := deploykit.RenderBuilderScript(s, venueHome)
	if err != nil {
		return err
	}

	out, err := kit.BuilderRun(opts.ContextOrDefault(), deploykit.BuilderRunOpts{
		BuilderImage: image,
		CandyDir:     s.CandyDir,
		ScriptBody:   script,
		BindMounts:   bindMounts,
		Env:          envVars,
		HostHome:     venueHome,
		DryRun:       opts.DryRun,
		RunAsRoot:    true,
		// Inject the image resolve/ensure seams closing over Cfg + ProjectDir so
		// BuilderRun can resolve a namespace-qualified / short builder ref (e.g. a
		// bed's install_opts.builder_image: arch.arch-builder) to its concrete image
		// — newest-local, or built on-demand from the project — instead of only
		// accepting a full registry ref. Mirrors deploykit.BuildDepPkgsOnHost's own injected
		// ResolveImage/EnsureImage closures (sdk/deploykit/localpkg.go).
		ResolveImage: func(img string) (string, error) { return resolveImageRefForEnsure(img, build.Cfg, build.ProjectDir) },
		EnsureImage: func(ctx context.Context, img string) error {
			return EnsureImagePresent(ctx, img, build.Cfg, build.ProjectDir)
		},
	})
	if len(out) > 0 {
		_, _ = os.Stderr.Write(out)
	}
	if err != nil {
		return fmt.Errorf("venue %s builder (candy=%s): %w", s.Builder, s.CandyName, err)
	}
	if opts.DryRun {
		return nil
	}

	// Collect the produced home subdirs, skipping build caches.
	entries, err := os.ReadDir(stageHost)
	if err != nil {
		return fmt.Errorf("reading builder staging dir: %w", err)
	}
	var transferDirs []string
	for _, e := range entries {
		if e.Name() == ".cache" {
			continue
		}
		transferDirs = append(transferDirs, e.Name())
	}
	if len(transferDirs) == 0 {
		return fmt.Errorf("%s builder for candy %q produced no home artifacts in %s; check the builder output above",
			s.Builder, s.CandyName, stageHost)
	}

	// Tar the artifacts into a single tarball on the host.
	tarDir, err := os.MkdirTemp("", "charly-venue-builder-tar-")
	if err != nil {
		return fmt.Errorf("tar staging mkdir: %w", err)
	}
	RegisterTempCleanup(tarDir)
	defer func() { _ = os.RemoveAll(tarDir); UnregisterTempCleanup(tarDir) }()
	tarball := filepath.Join(tarDir, "artifacts.tar.gz")
	tarArgs := append([]string{"-C", stageHost, "-czf", tarball}, transferDirs...)
	tarCmd := exec.CommandContext(ctx, "tar", tarArgs...)
	tarCmd.Stderr = os.Stderr
	if err := tarCmd.Run(); err != nil {
		return fmt.Errorf("tar builder artifacts: %w", err)
	}

	// Ship to the venue and extract into the venue user's $HOME AS the venue user, so
	// ownership + baked paths are correct. The tarball name carries the host staging
	// dir's unique MkdirTemp suffix: a fixed per-candy name would let two concurrent
	// deploys of the SAME candy to the SAME venue interleave (the second PutFile
	// overwriting the first's tarball before its extract runs).
	venueTar := venueBuilderTarName(s.CandyName, filepath.Base(tarDir))
	if err := dexec.PutFile(ctx, tarball, venueTar, 0o644, false, opts); err != nil {
		return fmt.Errorf("transfer builder artifacts: %w", err)
	}
	// Extract AS THE VENUE USER so the home artifacts (~/.npm-global, ~/.cargo, ~/.pixi)
	// end up owned by the venue user, not root.
	extractScript := fmt.Sprintf("set -e\nmkdir -p \"$HOME\"\ntar -C \"$HOME\" -xzf %s\n", kit.ShellQuote(venueTar))
	if err := dexec.RunUser(ctx, extractScript, opts); err != nil {
		return fmt.Errorf("extracting builder artifacts on venue: %w", err)
	}
	// Remove the tarball AS ROOT: PutFile placed it via `sudo install`, so it is
	// root-owned, and /tmp is sticky (1777) — the venue user can't remove a root-owned
	// file there. Cleaning up as root avoids leaving a root-owned tarball behind (and
	// previously aborted the deploy under the extract script's `set -e`).
	if err := dexec.RunSystem(ctx, fmt.Sprintf("rm -f %s\n", kit.ShellQuote(venueTar)), opts); err != nil {
		return fmt.Errorf("removing builder tarball on venue: %w", err)
	}
	return nil
}
