package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// bootstrap.go — the privileged builder-bootstrap DRIVE (a `from: builder:<name>` image),
// relocated from charly core's BuildCmd.runPrivilegedBootstrap. The host no longer runs this:
// the plugin-side resolve (K3 U6) resolves WHICH builder def applies (a charly.yml lookup that needs the
// live *Config/*BuilderConfig graph) and rides it back on the per-box descriptor
// (From/BootstrapBuilderImage/DistroDef/BootstrapBuilder — the buildwire.cue #BuildResolveBox
// extension), but the actual PRIVILEGED EXEC runs HERE, alongside the podman drive it was
// always adjacent to — the candy already owns buildkit.RunPrivileged (the RunPrivileged dedup
// cutover) and every other dependency (buildkit.RenderBootstrapScript/BootstrapPackagesForBox/
// RenderPacstrapExtraConf/RenderRuntimePacmanConf) is already sdk-importable.

// buildDir resolves the empty-Dir convention (a BuildRequest with no Dir means "the process's
// cwd", the SAME resolution resolveBuildEngine applies plugin-side) — since this candy is
// COMPILED-IN (the same process as the host), os.Getwd() here matches the host's resolution.
// Falls back to "." on an os.Getwd failure (never fatal for a bootstrap-path join).
func buildDir(reqDir string) string {
	if reqDir != "" {
		return reqDir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// bootstrapImages runs the privileged builder-bootstrap for every descriptor in buildSet that
// declares a `from: builder:` source (d.From non-empty), BEFORE the podman build loop starts —
// mirrors the former host-side pre-pass ordering (every bootstrap staged up front, then the
// build proceeds). Skips descriptors with no bootstrap source (the common case, d.From == "").
func bootstrapImages(ctx context.Context, ex *sdk.Executor, dir, engine string, boxSet []spec.BuildResolveBox) error {
	for _, d := range boxSet {
		if d.From == "" {
			continue
		}
		if err := runPrivilegedBootstrap(ctx, ex, dir, engine, d); err != nil {
			return fmt.Errorf("bootstrapping %s: %w", d.Name, err)
		}
	}
	return nil
}

// runPrivilegedBootstrap runs ONE image's privileged builder-bootstrap: renders the bootstrap
// script from the resolved distro/builder descriptors and executes it via buildkit.RunPrivileged,
// staging the output tarball at .build/<box>/<builder>.tar.gz. A no-op when the tarball is
// already staged (cheap stat, mirrors the former host-side skip).
func runPrivilegedBootstrap(ctx context.Context, ex *sdk.Executor, dir, engine string, d spec.BuildResolveBox) error {
	builderName := strings.TrimPrefix(d.From, "builder:")
	if d.BootstrapBuilderImage == "" {
		return fmt.Errorf("box %s: from: builder:%s requires bootstrap_builder_image: in charly.yml", d.Name, builderName)
	}
	builder := d.BootstrapBuilder
	if builder == nil {
		return fmt.Errorf("box %s: builder %q is not declared in charly.yml", d.Name, builderName)
	}
	if !builder.IsBootstrap() {
		return fmt.Errorf("box %s: builder %q is not kind: bootstrap (got kind=%q)", d.Name, builderName, builder.Kind)
	}
	if d.DistroDef == nil {
		return fmt.Errorf("box %s: distro has no resolved DistroDef", d.Name)
	}

	output := builder.OutputArtifact
	if output == "" {
		output = "/out/rootfs.tar.gz"
	}
	outDest := filepath.Join(dir, ".build", d.Name, fmt.Sprintf("%s.tar.gz", builderName))

	if _, err := os.Stat(outDest); err == nil {
		fmt.Fprintf(os.Stderr, "Bootstrap %s already staged at %s — skipping\n", builderName, outDest)
		return nil
	}

	builderRef, err := ensureBuilderImageBuilt(ctx, ex, engine, d.BootstrapBuilderImage)
	if err != nil {
		return err
	}

	renderCtx := struct {
		Distro            *spec.ResolvedDistro
		Packages          []string
		ExtraPacmanConf   string
		RuntimePacmanConf string
		ExtraAptSources   string
		Arch              string
		Variant           string
	}{
		Distro:   d.DistroDef,
		Packages: buildkit.BootstrapPackagesForBox(d.DistroDef),
	}
	renderCtx.ExtraPacmanConf = buildkit.RenderPacstrapExtraConf(d.DistroDef.Pacstrap)
	runtimeConf, rerr := buildkit.RenderRuntimePacmanConf(d.DistroDef.Pacstrap)
	if rerr != nil {
		return rerr
	}
	renderCtx.RuntimePacmanConf = runtimeConf
	// Debian-family security/backports apt sources injected before stage-2.
	if d.DistroDef.Debootstrap != nil && len(d.DistroDef.Debootstrap.ExtraRepos) > 0 {
		var b strings.Builder
		for _, r := range d.DistroDef.Debootstrap.ExtraRepos {
			suite := r.Suite
			if suite == "" {
				suite = d.DistroDef.Debootstrap.Suite
			}
			components := r.Components
			if components == "" {
				components = d.DistroDef.Debootstrap.Components
				if components == "" {
					components = "main"
				}
			}
			fmt.Fprintf(&b, "echo 'deb %s %s %s' > /target/etc/apt/sources.list.d/%s.list\n", r.URL, suite, components, r.Name)
		}
		renderCtx.ExtraAptSources = b.String()
	}

	script, err := buildkit.RenderBootstrapScript(builder, renderCtx)
	if err != nil {
		return fmt.Errorf("rendering bootstrap script for %s: %w", d.Name, err)
	}

	fmt.Fprintf(os.Stderr, "\n--- Bootstrap (%s) for %s ---\n", builderName, d.Name)
	if err := buildkit.RunPrivileged(buildkit.PrivilegedRun{
		Image:      builderRef,
		Script:     script,
		OutputPath: output,
		OutputDest: outDest,
	}); err != nil {
		return fmt.Errorf("running %s for %s: %w", builderName, d.Name, err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %s\n", outDest)
	return nil
}

// ensureBuilderImageBuilt resolves an internal builder-image name to its newest local CalVer
// tag, BUILDING it on demand when it isn't in local storage (fully automatic — no manual
// `charly box build <builder>` prerequisite). A ref containing "/" (a full registry ref) is
// returned unchanged. The auto-build recurses through runBoxBuild directly (in-process — the
// podman drive already lives in this candy, so no HostBuild/registry round-trip is needed for
// the recursive build the way the former host-side helper needed dispatchBoxBuild).
func ensureBuilderImageBuilt(ctx context.Context, ex *sdk.Executor, engine, builderRef string) (string, error) {
	if strings.Contains(builderRef, "/") {
		return builderRef, nil
	}
	if resolved, err := kit.ResolveLocalImageRef(engine, builderRef); err == nil {
		return resolved, nil
	}
	fmt.Fprintf(os.Stderr, "Builder image %q not in local storage — building it automatically...\n", builderRef)
	if _, err := runBoxBuild(ctx, ex, spec.BuildRequest{Boxes: []string{builderRef}, IncludeDisabled: true}); err != nil {
		return "", fmt.Errorf("auto-building builder image %q: %w", builderRef, err)
	}
	resolved, err := kit.ResolveLocalImageRef(engine, builderRef)
	if err != nil {
		return "", fmt.Errorf("builder image %q still not found after auto-build: %w", builderRef, err)
	}
	return resolved, nil
}
