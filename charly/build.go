package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// BuildCmd builds container images
type BuildCmd struct {
	Boxes           []string `arg:"" optional:"" help:"Boxes to build (default: all enabled; the sentinel 'all' is equivalent). Supports remote refs (github.com/org/repo/box[@version])"`
	Push            bool     `long:"push" help:"Push to registry after building"`
	Tag             string   `long:"tag" help:"Override tag (default: CalVer)"`
	Platform        string   `long:"platform" help:"Target platform (default: host platform)"`
	Cache           string   `long:"cache" help:"Build cache type: registry, image, gha, none (default: auto)" env:"CHARLY_BUILD_CACHE"`
	NoCache         bool     `long:"no-cache" help:"Disable build cache entirely"`
	Jobs            int      `long:"jobs" help:"Max concurrent image builds per DAG level (0=auto: defaults.jobs, else 4)" env:"CHARLY_BUILD_JOBS"`
	PodmanJobs      int      `long:"podman-jobs" help:"Stages per podman build (0=auto: min(NCPU, defaults.podman_jobs_cap))" env:"CHARLY_PODMAN_JOBS"`
	IncludeDisabled bool     `long:"include-disabled" help:"Build boxes with enabled: false in charly.yml (does not modify the file). Use for one-off operational rebuilds without flipping authored config."`
	DevLocalPkg     bool     `long:"dev-local-pkg" help:"Build localpkg candies (the charly toolchain) from LOCAL in-development source instead of downloading the published release. Set automatically for disposable check-bed image builds so a bed tests in-development code; never on a production box build."`

	// podmanJobsCap is the resolved ceiling for the auto podman-jobs calc,
	// sourced from defaults.podman_jobs_cap in Run() (0 → podmanJobsCapFallback).
	// Not a CLI flag — the cap is a project-wide config knob; per-build
	// overrides go through --podman-jobs / CHARLY_PODMAN_JOBS.
	podmanJobsCap int
}

// ensureBuilderImageBuilt resolves an internal builder-image name to its newest
// local CalVer tag, BUILDING it on demand when it isn't in local storage. This
// makes bootstrap image/VM builds fully automatic — no manual
// `charly box build <builder>` prerequisite. A ref containing "/" (a full registry
// ref) is returned unchanged. Shared by the kind:box bootstrap path
// (BuildCmd) and the kind:vm bootstrap path (vm_bootstrap.go) — one helper, both
// call sites.
func ensureBuilderImageBuilt(engine, builderRef string) (string, error) {
	if strings.Contains(builderRef, "/") {
		return builderRef, nil
	}
	if resolved, err := kit.ResolveLocalImageRef(engine, builderRef); err == nil {
		return resolved, nil
	}
	fmt.Fprintf(os.Stderr, "Builder image %q not in local storage — building it automatically...\n", builderRef)
	// Recurse on the dependency image through the SAME build:box dispatch the CLI uses
	// (dispatchBoxBuild → the compiled-in candy/plugin-build DRIVE → HostBuild("build-prep")):
	// the podman drive lives in the candy now (P8b), so the host cannot build inline. The
	// in-proc reverse channel makes this re-entrant call cheap (no socket). Reached from the
	// build-prep bootstrap pre-pass AND the vm bootstrap path.
	if err := dispatchBoxBuild(spec.BuildRequest{Boxes: []string{builderRef}, IncludeDisabled: true}); err != nil {
		return "", fmt.Errorf("auto-building builder image %q: %w", builderRef, err)
	}
	resolved, err := kit.ResolveLocalImageRef(engine, builderRef)
	if err != nil {
		return "", fmt.Errorf("builder image %q still not found after auto-build: %w", builderRef, err)
	}
	return resolved, nil
}

// normalizeBoxArgs canonicalises the positional box selection shared by
// `charly box build` and `charly box generate`. The lone sentinel `all`
// (case-insensitive) collapses to nil — i.e. "every enabled box" — so
// `charly box build all` / `charly box generate all` behave identically to the
// bare no-argument form. Any other slice (including a literal "all" alongside
// other names) passes through unchanged: the sentinel fires ONLY when it is the
// sole argument, so a box that happens to be named "all" is still reachable via
// an explicit two-name invocation.
func normalizeBoxArgs(boxes []string) []string {
	if len(boxes) == 1 && strings.EqualFold(boxes[0], "all") {
		return nil
	}
	return boxes
}

// boxResolveOpts builds the ResolveOpts that scope a generate/build to a set of
// explicitly-named boxes. It is the SINGLE source of the box-selection rule for
// both `charly box build` and `charly box generate` (R3): an empty slice means
// "all enabled boxes" (no scoping); a non-empty slice pins those names into the
// resolved set (RequestedBoxes) and, when --include-disabled is set, relaxes the
// enabled: false gate for exactly those names (IncludeDisabledNames) so the
// override never widens the working set globally. Callers pass boxes already run
// through normalizeBoxArgs.
func boxResolveOpts(boxes []string, includeDisabled bool) ResolveOpts {
	opts := ResolveOpts{IncludeDisabled: includeDisabled}
	if len(boxes) == 0 {
		return opts
	}
	opts.RequestedBoxes = boxes
	if includeDisabled {
		opts.IncludeDisabledNames = make(map[string]bool, len(boxes))
		for _, name := range boxes {
			opts.IncludeDisabledNames[name] = true
		}
	}
	return opts
}

func (c *BuildCmd) Run() error {
	// Normalize the `all` sentinel to nil BEFORE any per-name interpretation
	// (remote-ref dispatch, include-passthrough, the resolver) so every surface
	// agrees that "no specific boxes" means "all enabled".
	c.Boxes = normalizeBoxArgs(c.Boxes)

	handled, dir, err := c.checkRemoteRefsAndPivot()
	if handled {
		return err
	}

	// Compute the build tag ONCE host-side so the retention activity-lock floor and
	// the built images (build-prep's NewGenerator) agree on ONE CalVer —
	// ComputeCalVer is clock-derived, so resolving it in two places would diverge.
	tag := c.Tag
	if tag == "" {
		tag = ComputeCalVer()
	}

	// Retention floor: mark this build LIVE (acquireBuildActivityLock) so a
	// concurrent sibling's retention prune respects our tag floor (liveBuildFloor).
	// Held across the whole build (the candy podman drive) + the post-build prune.
	// The PER-IMAGE build lock moved INTO the candy (kit.AcquireImageBuildLock) so
	// distinct leaves fan out in parallel while a shared intermediate builds once.
	buildActivityRelease, err := acquireBuildActivityLock(tag)
	if err != nil {
		return err
	}
	defer func() { _ = buildActivityRelease() }()

	// The podman DRIVE runs in the compiled-in candy/plugin-build (build:box); the
	// host is a PREP + RESOLVE-PROJECT envelope seam provider (HostBuild("build-prep")).
	// P8b reversed the P8 "permanent facade" — the podman DRIVE lives in the candy; #67 moved the
	// render DRIVE to sdk/deploykit + plugin-build, so the host no longer renders Containerfiles.
	if err := dispatchBoxBuild(spec.BuildRequest{
		Boxes:           c.Boxes,
		Tag:             tag,
		Dir:             dir,
		IncludeDisabled: c.IncludeDisabled,
		DevLocalPkg:     c.DevLocalPkg,
		Push:            c.Push,
		Platform:        c.Platform,
		Cache:           c.Cache,
		NoCache:         c.NoCache,
		Jobs:            c.Jobs,
		PodmanJobs:      c.PodmanJobs,
	}); err != nil {
		return err
	}

	// Reusable-artifact retention (host POST-step; skipped for push): prune old
	// CalVer tags + stale .build/_candy dirs down to defaults.keep_images. It is
	// host-side (podman image tooling, shared with `charly clean`) and runs
	// AFTER the candy build drive completes, under the activity lock held above.
	if !c.Push {
		pruneAfterBuild(dir)
	}
	return nil
}

// pruneAfterBuild runs the post-build retention prune host-side (best-effort,
// warn-only): old-CalVer image-tag retention (keep_images) + stale build-staging
// dir cleanup. It reads keep_images from a lightweight project-config load (the
// full model already prepped in build-prep; this is a cheap charly.yml read)
// and resolves the engine via ResolveRuntime. Runs after the candy build drive
// (build:box) completes.
func pruneAfterBuild(dir string) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return
	}
	keep := resolveIntPtr(cfg.Defaults.KeepImages, nil, keepImagesFallback)
	if keep > 0 {
		if rt, rtErr := ResolveRuntime(); rtErr == nil {
			engine := EngineBinary(rt.BuildEngine)
			if removed, err := pruneImagesByRetention(engine, keep, false); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: image retention prune: %v\n", err)
			} else if len(removed) > 0 {
				fmt.Fprintf(os.Stderr, "Pruned %d old image tag(s) (keep_images=%d)\n", len(removed), keep)
			}
		}
	}
	if removed := pruneBuildCandyDirs(filepath.Join(dir, ".build"), keep, false); len(removed) > 0 {
		fmt.Fprintf(os.Stderr, "Pruned %d build-staging dir(s) under .build/_candy\n", len(removed))
	}
}

// dispatchBoxBuild routes `charly box build` through its compiled-in plugin word (build:box) over
// the F10 HostBuild seam. (`charly box generate` was externalized to candy/plugin-box, which
// invokes build:generate directly via the InvokeProvider reverse leg — see candy/plugin-box.)
func dispatchBoxBuild(req spec.BuildRequest) error { return dispatchBuild("box", req) }

// dispatchBuild invokes the compiled-in build:<word> plugin, threading the IN-PROC reverse
// channel onto the ctx (sdk.ContextWithExecutor) so the plugin's Invoke reaches HostBuild without
// a go-plugin broker — the compiled-in placement of the reverse channel. The plugin echoes a
// spec.BuildReply; a non-empty reply.Error is surfaced as the command error. build:<word> is
// compiled in (candy/plugin-build in compiled_plugins:), so the provider is always in-proc here.
func dispatchBuild(word string, req spec.BuildRequest) error {
	prov, ok := providerRegistry.resolve(ClassBuild, word)
	if !ok {
		return fmt.Errorf("build dispatch: no build:%s provider registered (candy/plugin-build must be compiled in via compiled_plugins:)", word)
	}
	params, err := marshalJSON(req)
	if err != nil {
		return err
	}
	// The reverse server carries no venue executor (HostBuild needs only the host build-engine,
	// reconstructed from req.Dir) and an empty build context (the host-builder rebuilds it from
	// Dir), so a bare executorReverseServer{} is enough for the HostBuild leg.
	ctx := sdk.ContextWithExecutor(context.Background(),
		sdk.NewInProcExecutor(&inprocExecutorClient{srv: &executorReverseServer{}}))
	res, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: OpBuild, Params: params})
	if err != nil {
		return err
	}
	var reply spec.BuildReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return fmt.Errorf("build dispatch: decode reply: %w", err)
		}
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return nil
}

// checkRemoteRefsAndPivot dispatches to a remote build when any image arg is a
// remote ref, or when cwd's charly.yml auto-pivots a locally-undeclared image to
// its single remote include (so `cd ~/projects/ecovoyage && charly box build
// versa` transparently rebuilds from upstream source without any flags; the
// workspace's deploy/check overlays are picked up later by deploy-mode commands,
// image build doesn't need them). Returns (handled=true, "", err) when Run
// should return immediately — err carries the buildRemote result or an os.Getwd
// failure — and (false, dir, nil) when the build should proceed locally from dir.
func (c *BuildCmd) checkRemoteRefsAndPivot() (bool, string, error) {
	// Check if any image arg is a remote ref
	for _, img := range c.Boxes {
		ref := StripURLScheme(img)
		if IsRemoteImageRef(ref) {
			return true, "", c.buildRemote(ref)
		}
	}

	dir, err := os.Getwd()
	if err != nil {
		return true, "", err
	}

	if remoteRef, ok := detectRemoteIncludePassthrough(dir, c.Boxes); ok {
		return true, "", c.buildRemote(remoteRef)
	}
	return false, dir, nil
}

// resolveBuildTunables fills the build-speed knobs (Jobs / PodmanJobs /
// PodmanJobsCap / Cache) from project defaults: when the CLI flag / env layer
// left them unset. A named fallback applies later if config is silent too.
func (c *BuildCmd) resolveBuildTunables(def BoxConfig) {
	if c.Jobs == 0 {
		c.Jobs = resolveIntPtr(def.Jobs, nil, 0)
	}
	if c.PodmanJobs == 0 {
		c.PodmanJobs = resolveIntPtr(def.PodmanJobs, nil, 0)
	}
	c.podmanJobsCap = resolveIntPtr(def.PodmanJobsCap, nil, 0)
	if c.Cache == "" {
		c.Cache = def.Cache
	}
}

// pacstrapMicroarchRe matches pacman microarchitecture-level tokens (e.g.
// x86_64_v3) embedded in a repo Server URL. CachyOS's cachyos-v3 repos serve
// such packages; pacman rejects them unless the matching token is in
// Architecture.
var pacstrapMicroarchRe = regexp.MustCompile(`x86_64_v[0-9]+`)

// renderPacstrapExtraConf builds the pacman.conf fragment appended to
// /etc/pacman.conf inside the bootstrap container before `pacstrap` runs. It is
// the SINGLE source of truth for both the image bootstrap path
// (runPrivilegedBootstrap) and the VM bootstrap path (vm_bootstrap.go) — these
// previously each open-coded the rendering and drifted: the VM path dropped the
// per-repo SigLevel, so a SigLevel=Never repo (CachyOS) fell back to the
// default Required and `pacman -Sy` failed with "GPGME error: No data /
// corrupted PGP signature". Both paths now share this function.
//
// It emits, in order:
//  1. an [options] Architecture directive whenever any repo Server declares a
//     microarch variant (e.g. x86_64_v3). pacman's default Architecture (auto →
//     x86_64) otherwise rejects those packages with "package architecture is
//     not valid". Architecture is cumulative in pacman, so appending this to
//     the base config widens the accepted set rather than replacing it.
//  2. each [repo] block with its Server and (when set) SigLevel.
func renderPacstrapExtraConf(p *PacstrapDef) string {
	if p == nil || len(p.ExtraRepos) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var microarch []string
	for _, r := range p.ExtraRepos {
		for _, m := range pacstrapMicroarchRe.FindAllString(r.Server, -1) {
			if !seen[m] {
				seen[m] = true
				microarch = append(microarch, m)
			}
		}
	}
	sort.Strings(microarch)

	var b strings.Builder
	if len(microarch) > 0 {
		fmt.Fprintf(&b, "[options]\nArchitecture = x86_64 %s\n", strings.Join(microarch, " "))
	}
	for _, r := range p.ExtraRepos {
		fmt.Fprintf(&b, "[%s]\nServer = %s\n", r.Name, r.Server)
		if r.SigLevel != "" {
			fmt.Fprintf(&b, "SigLevel = %s\n", r.SigLevel)
		}
	}
	return b.String()
}

// renderRuntimePacmanConf renders the booted-guest /etc/pacman.conf for a
// pacstrap distro. `runtime_pacman_conf` is a Go text/template evaluated
// against the PacstrapDef, so the repo list is derived from the SINGLE
// `extra_repo` source (`{{ range .ExtraRepos }}`) rather than a second
// hand-maintained verbatim copy — eliminating the install-vs-runtime drift
// that left a stale `cachyos-extra` (HTML-stub mirror) in one surface. The
// template adds only the runtime-specific framing ([options] header + Arch
// core/extra). A legacy verbatim config (no template actions) renders to
// itself. Returns "" when unset; surfaces malformed-template errors.
func renderRuntimePacmanConf(p *PacstrapDef) (string, error) {
	if p == nil || strings.TrimSpace(p.RuntimePacmanConf) == "" {
		return "", nil
	}
	tmpl, err := template.New("runtime_pacman_conf").Parse(p.RuntimePacmanConf)
	if err != nil {
		return "", fmt.Errorf("parsing runtime_pacman_conf template: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, p); err != nil {
		return "", fmt.Errorf("rendering runtime_pacman_conf: %w", err)
	}
	return b.String(), nil
}

func (c *BuildCmd) runPrivilegedBootstrap(engine, dir, boxName string, img *ResolvedBox) error {
	if !strings.HasPrefix(img.From, "builder:") {
		return nil
	}
	builderName := strings.TrimPrefix(img.From, "builder:")
	if img.BootstrapBuilderImage == "" {
		return fmt.Errorf("box %s: from: builder:%s requires bootstrap_builder_image: in charly.yml", boxName, builderName)
	}
	if img.BuilderConfig == nil {
		return fmt.Errorf("box %s: charly.yml builder: section is empty", boxName)
	}
	builder, ok := img.BuilderConfig.Builder[builderName]
	if !ok {
		return fmt.Errorf("box %s: builder %q is not declared in charly.yml", boxName, builderName)
	}
	if !builder.IsBootstrap() {
		return fmt.Errorf("box %s: builder %q is not kind: bootstrap (got kind=%q)", boxName, builderName, builder.Kind)
	}
	if img.DistroDef == nil {
		return fmt.Errorf("box %s: distro %v has no resolved DistroDef", boxName, img.Distro)
	}

	output := builder.OutputArtifact
	if output == "" {
		output = "/out/rootfs.tar.gz"
	}
	outDest := filepath.Join(dir, ".build", boxName, fmt.Sprintf("%s.tar.gz", builderName))

	// Skip rebuild when the staged tarball is already present and the
	// builder image hash hasn't changed. Cheap stat is enough for now;
	// content-addressing is a future optimization.
	if _, err := os.Stat(outDest); err == nil {
		fmt.Fprintf(os.Stderr, "Bootstrap %s already staged at %s — skipping\n", builderName, outDest)
		return nil
	}

	// Resolve the builder image ref. Internal kind:box names get
	// resolved to the newest local CalVer tag via the same machinery
	// as `charly shell <name>` so build never tries to pull a `:latest`
	// that charly doesn't emit.
	// Resolve + auto-build the bootstrap builder image on demand (fully automatic).
	builderRef, err := ensureBuilderImageBuilt(engine, img.BootstrapBuilderImage)
	if err != nil {
		return err
	}

	ctx := struct {
		Distro            *DistroDef
		Packages          []string
		ExtraPacmanConf   string
		RuntimePacmanConf string
		ExtraAptSources   string
		Arch              string
		Variant           string
	}{
		Distro:   img.DistroDef,
		Packages: bootstrapPackagesForBox(img),
	}
	// CachyOS et al. need extra repo blocks (+ an Architecture directive for
	// microarch repos) injected into pacman.conf before pacstrap so the new
	// packages resolve from the right repos. Shared with the VM bootstrap path.
	// RuntimePacmanConf is rendered from the SAME extra_repo source (single
	// source of truth) and written into the booted guest's /etc/pacman.conf.
	if img.DistroDef != nil {
		ctx.ExtraPacmanConf = renderPacstrapExtraConf(img.DistroDef.Pacstrap)
		runtimeConf, rerr := renderRuntimePacmanConf(img.DistroDef.Pacstrap)
		if rerr != nil {
			return rerr
		}
		ctx.RuntimePacmanConf = runtimeConf
	}
	// Debian-family security/backports apt sources injected before stage-2.
	if img.DistroDef.Debootstrap != nil && len(img.DistroDef.Debootstrap.ExtraRepos) > 0 {
		var b strings.Builder
		for _, r := range img.DistroDef.Debootstrap.ExtraRepos {
			suite := r.Suite
			if suite == "" {
				suite = img.DistroDef.Debootstrap.Suite
			}
			components := r.Components
			if components == "" {
				components = img.DistroDef.Debootstrap.Components
				if components == "" {
					components = "main"
				}
			}
			fmt.Fprintf(&b, "echo 'deb %s %s %s' > /target/etc/apt/sources.list.d/%s.list\n", r.URL, suite, components, r.Name)
		}
		ctx.ExtraAptSources = b.String()
	}

	script, err := renderBootstrapScript(builder, ctx)
	if err != nil {
		return fmt.Errorf("rendering bootstrap script for %s: %w", boxName, err)
	}

	fmt.Fprintf(os.Stderr, "\n--- Bootstrap (%s) for %s ---\n", builderName, boxName)
	if err := RunPrivileged(PrivilegedRun{
		Image:      builderRef,
		Script:     script,
		OutputPath: output,
		OutputDest: outDest,
	}); err != nil {
		return fmt.Errorf("running %s for %s: %w", builderName, boxName, err)
	}
	fmt.Fprintf(os.Stderr, "Wrote %s\n", outDest)
	return nil
}

// bootstrapPackagesForBox returns base + per-image bootstrap packages.
// Per-image overrides aren't currently surfaced via charly.yml; this
// returns just the distro defaults for now.
//
// Mirrors baseBootstrapPackages in vm_bootstrap.go but at the OCI-image
// build path (the box config `from: builder:<name>` consumers). Same dispatch
// rules: Pacstrap.BasePackages for pacstrap-flavored, Debootstrap.BasePackages
// for debootstrap-flavored.
func bootstrapPackagesForBox(img *ResolvedBox) []string {
	if img.DistroDef == nil {
		return nil
	}
	if img.DistroDef.Pacstrap != nil {
		return img.DistroDef.Pacstrap.BasePackages
	}
	if img.DistroDef.Debootstrap != nil {
		return img.DistroDef.Debootstrap.BasePackages
	}
	return nil
}

// podmanJobsCapFallback is the ceiling on the auto-computed
// `podman build --jobs` value, used ONLY when defaults.podman_jobs_cap is
// absent from project config. The operative ceiling is
// charly.yml `defaults.podman_jobs_cap`; this conservative constant just
// keeps configs that don't declare the key on a safe value. The per-build
// override is --podman-jobs / CHARLY_PODMAN_JOBS. (See CHANGELOG/ for the
// podman-5.7.x blob-reuse SIGABRT race that originally motivated a hard cap.)
const podmanJobsCapFallback = 4

// jobsFallback is the outer image-level concurrency (images per DAG level)
// used when neither --jobs / CHARLY_BUILD_JOBS nor defaults.jobs is set.
const jobsFallback = 4

// numCPU is a package-level alias for runtime.NumCPU so tests can inject
// a fixed value via the init in build_jobs_test.go.
var numCPU = runtime.NumCPU

// resolvePodmanJobs returns the --jobs value to pass to `podman build`.
// An explicit override (>0, from --podman-jobs / CHARLY_PODMAN_JOBS /
// defaults.podman_jobs) wins. Otherwise the value is CPU-proportional,
// capped at `cap` (defaults.podman_jobs_cap, else podmanJobsCapFallback):
// min(numCPU(), cap). A cap < 1 falls back to podmanJobsCapFallback.
func resolvePodmanJobs(override, jobsCap int) int {
	if override > 0 {
		return override
	}
	if jobsCap < 1 {
		jobsCap = podmanJobsCapFallback
	}
	n := numCPU()
	if n < jobsCap {
		return n
	}
	return jobsCap
}

// hostPlatform returns the host platform in OCI format.
func hostPlatform() string {
	arch := runtime.GOARCH
	return "linux/" + arch
}

// detectRemoteIncludePassthrough inspects cwd's charly.yml for a
// single `@github.com/owner/repo/...charly.yml:ref` include. If
// found AND the requested image isn't declared locally in the
// workspace (i.e. the image lives upstream), returns the synthesized
// remote-image-ref `@github.com/owner/repo/<image>:ref` plus true.
// Otherwise returns ("", false) and the normal local build flow runs.
//
// Designed to be conservative: only fires when (a) there's exactly
// one include, (b) it's a remote @github.com/...charly.yml ref,
// (c) the user asked for a single image, and (d) the workspace
// charly.yml has no local `image:` entry of that name.
func detectRemoteIncludePassthrough(dir string, boxes []string) (string, bool) {
	if len(boxes) != 1 {
		return "", false
	}
	boxName := boxes[0]
	unifiedPath := filepath.Join(dir, UnifiedFileName)
	data, err := os.ReadFile(unifiedPath)
	if err != nil {
		return "", false
	}
	var peek struct {
		// Read the `import:` list generically (items are either bare strings —
		// flat imports — or single-key `alias: ref` maps — namespaced imports).
		Import []any                      `yaml:"import" json:"import"`
		Box    map[string]json.RawMessage `yaml:"box" json:"box"`
	}
	if err := yaml.Unmarshal(data, &peek); err != nil {
		return "", false
	}
	// The passthrough fires only for a thin project whose SOLE import is one
	// flat remote ref (a single-string import naming another repo). A project
	// with namespaced imports or multiple imports uses the normal build path.
	var stringImports []string
	for _, it := range peek.Import {
		if s, ok := it.(string); ok {
			stringImports = append(stringImports, s)
		}
	}
	if len(peek.Import) != 1 || len(stringImports) != 1 {
		return "", false
	}
	// If the image is declared locally, keep the normal local path.
	if _, hasLocal := peek.Box[boxName]; hasLocal {
		return "", false
	}
	inc := stringImports[0]
	if !strings.HasPrefix(inc, "@") {
		return "", false
	}
	// Parse `@github.com/owner/repo/...:ref` and substitute the image name.
	bare := strings.TrimPrefix(inc, "@")
	versionIdx := strings.LastIndex(bare, ":")
	var version string
	pathPart := bare
	if versionIdx > 0 {
		pathPart = bare[:versionIdx]
		version = bare[versionIdx+1:]
	}
	// pathPart is e.g. github.com/opencharly/charly/charly.yml.
	// Strip the trailing filename to get the repo root.
	slashIdx := strings.LastIndex(pathPart, "/")
	if slashIdx < 0 {
		return "", false
	}
	repoRoot := pathPart[:slashIdx]
	// Synthesize @github.com/owner/repo/<image>[:ref].
	ref := "@" + repoRoot + "/" + boxName
	if version != "" {
		ref += ":" + version
	}
	return ref, true
}

// buildRemote builds a remote image ref locally from its cached source.
func (c *BuildCmd) buildRemote(ref string) error {
	tag := c.Tag
	if tag == "" {
		// charly is CalVer-only. A remote build with no explicit CalVer
		// gets a fresh one at build time — matching the local
		// `charly box build` behaviour (generate.go:ComputeCalVer).
		tag = ComputeCalVer()
	}

	ctx, err := ResolveRemoteImage(ref, tag)
	if err != nil {
		return err
	}

	return ctx.BuildImage(nil, tag)
}

// filterBox filters the build order to only include the requested images
// and their dependencies.
func filterBox(order []string, requested []string, boxes map[string]*ResolvedBox) ([]string, error) {
	// Validate requested images exist
	for _, name := range requested {
		if _, ok := boxes[name]; !ok {
			return nil, fmt.Errorf("unknown box %q", name)
		}
	}

	// Collect requested images and their transitive deps (Base + format builders +
	// BootstrapBuilderImage). Routed through boxDirectDeps in graph.go so this
	// walker stays in lockstep with ResolveBoxOrder + ResolveBoxLevels — see
	// the helper's docstring for the rationale (2026-05 cachyos-pacstrap-builder
	// regression). includeFormatBuilders=true here unconditionally because filtered
	// build sets must always include format-builder images that the requested
	// targets need at build time, regardless of BoxNeedsBuilder.
	needed := make(map[string]bool)
	var addDeps func(name string)
	addDeps = func(name string) {
		if needed[name] {
			return
		}
		needed[name] = true
		img := boxes[name]
		for _, dep := range boxDirectDeps(name, img, boxes, true) {
			addDeps(dep)
		}
	}
	for _, name := range requested {
		addDeps(name)
	}

	// Filter order preserving dependency order
	var filtered []string
	for _, name := range order {
		if needed[name] {
			filtered = append(filtered, name)
		}
	}
	return filtered, nil
}

// ensureCharlyBinaryFresh rebuilds candy/charly/bin/charly when any image whose
// resolved candy chain includes the `charly` candy is in scope for the
// current build. Without this, podman build would COPY whatever stale
// binary happens to live at candy/charly/bin/charly — silently baking obsolete
// CLI behaviour into the image. Skipped (with a one-line warning) when
// `go` is not on PATH, so an end-user with a packaged charly install does
// not see a hard error.
func ensureCharlyBinaryFresh(dir string, boxes map[string]*ResolvedBox, requested []string) error {
	in := requested
	if len(in) == 0 {
		in = make([]string, 0, len(boxes))
		for name := range boxes {
			in = append(in, name)
		}
	}
	needs := false
	for _, name := range in {
		img, ok := boxes[name]
		if !ok {
			continue
		}
		if slices.Contains(img.Candy, "charly") {
			needs = true
		}
		if needs {
			break
		}
	}
	if !needs {
		return nil
	}

	binPath := filepath.Join(dir, DefaultCandyDir, "charly", "bin", "charly")
	srcDir := filepath.Join(dir, "charly")

	// Downstream workspaces (project trees that `import:` upstream
	// opencharly via `@github.com/...`) don't ship the charly Go source.
	// Without ./charly to rebuild from, there's nothing to refresh — the
	// embedded candy chain will use the cached upstream binary at
	// <upstream-cache>/candy/charly/bin/charly which is already up-to-date
	// relative to upstream's charly source.
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return nil
	}

	upToDate, err := charlyBinaryUpToDate(binPath, srcDir)
	if err == nil && upToDate {
		return nil
	}

	if _, err := exec.LookPath("go"); err != nil {
		fmt.Fprintf(os.Stderr, "charly: warning: `go` not on PATH; skipping candy/charly/bin/charly rebuild (image will use existing binary)\n")
		return nil
	}

	fmt.Fprintf(os.Stderr, "charly: rebuilding candy/charly/bin/charly from ./charly before image build\n")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// charlyBinaryUpToDate returns true when binPath exists and is newer than
// every .go file under srcDir. Returns (false, nil) for any file system
// state that warrants a rebuild (missing binary, missing source dir).
func charlyBinaryUpToDate(binPath, srcDir string) (bool, error) {
	binStat, err := os.Stat(binPath)
	if err != nil {
		return false, nil
	}
	binMtime := binStat.ModTime()
	upToDate := true
	walkErr := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(binMtime) {
			upToDate = false
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return false, walkErr
	}
	return upToDate, nil
}
