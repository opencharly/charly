package main

// deploy_target_pod.go — PodDeployTarget deploys an
// InstallPlan as a running container.
//
// For container deploys, the same InstallPlan produced by
// BuildDeployPlan is consumed by two sub-systems:
//
//   1. Overlay Containerfile synthesis — when the charly.yml has
//      `add_candy:` entries, we generate a new Containerfile that
//      inherits FROM the base image and applies the extra candies'
//      install steps on top. The overlay image is then passed to the
//      existing quadlet/podman machinery.
//
//   2. Container startup — after any overlay build, delegate to the
//      existing `charly start` path (start.go) which already handles
//      volume setup, tunnel config, traefik routes, env-provides wiring,
//      etc.
//
// For v1, PodDeployTarget.Emit acts as a thin bridge: it
// synthesizes the overlay image when needed, then hands off to the
// existing deploy pipeline. Later passes can migrate more of
// start.go's logic in here.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PodDeployTarget applies an InstallPlan as a container.
type PodDeployTarget struct {
	// DeployName is the name under which this container is known in
	// charly.yml and in the systemd-quadlet layer.
	DeployName string

	// BaseImage is the image ref the overlay inherits from. May be the
	// project's own image (e.g. fedora-coder:2026.004.0021) or a remote
	// ref already pulled into local storage.
	BaseImage string

	// Engine is "podman" or "docker". Defaults to "podman".
	Engine string

	// DistroDef + BuilderConfig feed the OCITarget used for overlay
	// synthesis. Supplied by the caller (deploy command wiring).
	DistroDef     *DistroDef
	BuilderConfig *BuilderConfig

	// Generator + Box are required for the overlay builder to render
	// task steps (package installs, cmd runs, etc.) as actual RUN
	// directives in the Containerfile. Without them the emitter degrades
	// to "no Generator context" comments and the overlay contains no
	// install logic — producing an image byte-identical to BaseImage.
	Generator *Generator
	Box       *ResolvedBox

	// OverlayBuildDir is where the synthesized Containerfile + build
	// context lives. Defaults to .build/overlay-<deploy-name>/.
	OverlayBuildDir string

	// Executor is the DeployExecutor used for the `podman build`
	// invocation. Defaults to ShellExecutor when nil — matching
	// the pre-tree-schema behavior of building overlays on the
	// invoking host. When set to a NestedExecutor (the tree walker
	// does this for nested container nodes), the build runs in the
	// parent venue. Build context files are shipped via
	// Executor.PutFile before the build runs.
	Executor DeployExecutor

	// DryRunWriter receives dry-run text. Nil means os.Stderr.
	DryRunWriter *os.File

	// overlayImageRef is populated by Emit when an overlay was built;
	// read via OverlayImageRef() after Emit returns.
	overlayImageRef string
}

// renderOverlayServices hooks into the existing Generator init-fragment
// pipeline (generate.go:375-605) to render service: blocks from overlay
// candies into proper fragment files, emit a scratch stage that holds
// them, and emit a RUN step that APPENDS the rendered fragments to the
// base image's existing /etc/supervisord.conf. Reuses RenderService +
// generateInitFragments so all the init-system-specific logic (scope,
// overrides, packaged vs custom, etc.) is a single source of truth.
// Returns (scratchStageBlock, runAppendBlock, error).
// scratchStageBlock: the `FROM scratch AS <init>-overlay` + COPY lines
// to place BEFORE the main image FROM.
// runAppendBlock: the `RUN --mount=... cat >> /etc/supervisord.conf`
// to place AFTER all overlay install tasks in the main image stage.
func (t *PodDeployTarget) renderOverlayServices(overlayCandies []string) (string, string, error) {
	if t.Generator == nil || t.Box == nil || t.Generator.InitConfig == nil {
		return "", "", nil
	}
	candyOrder := append([]string{}, t.Box.Candy...)
	candyOrder = append(candyOrder, overlayCandies...)
	initName, initDef := t.Generator.InitConfig.ResolveInitSystem(t.Generator.Candies, candyOrder, t.Box.InitSystem)
	if initDef == nil || initDef.ServiceSchema == nil {
		return "", "", nil
	}
	var anySvc bool
	for _, n := range overlayCandies {
		l := t.Generator.candyByName(n)
		if l != nil && l.HasInit(initName) {
			anySvc = true
			break
		}
	}
	if !anySvc {
		return "", "", nil
	}
	overlayImageName := "overlay-" + t.DeployName
	// Point the Generator at the overlay build dir so generateInitFragments
	// writes fragments there. OverlayBuildDir is already relative to the
	// project dir (the build-context root), so the Containerfile can COPY
	// from that path directly — no abs/rel gymnastics needed.
	overlayDir := t.OverlayBuildDir
	if overlayDir == "" {
		overlayDir = filepath.Join(".build", "overlay-"+t.DeployName)
	}
	savedBuildDir := t.Generator.BuildDir
	t.Generator.BuildDir = overlayDir
	defer func() { t.Generator.BuildDir = savedBuildDir }()
	if err := t.Generator.generateInitFragments(overlayImageName, initName, initDef, overlayCandies); err != nil {
		return "", "", fmt.Errorf("overlay service fragments: %w", err)
	}

	var stage strings.Builder
	stageName := initDef.StageName + "-overlay"
	fmt.Fprintf(&stage, "FROM scratch AS %s\n", stageName)
	for i, candyName := range overlayCandies {
		l := t.Generator.candyByName(candyName)
		if l == nil || !l.HasInit(initName) {
			continue
		}
		// Short name, not the slashed remote map key.
		fileName := fmt.Sprintf("%02d-%s.conf", i+1, l.Name)
		srcRel := filepath.Join(overlayDir, overlayImageName, initDef.FragmentDir, fileName)
		fmt.Fprintf(&stage, "COPY %s /supervisor-overlay/%s\n", srcRel, fileName)
	}
	stage.WriteString("\n")

	var run strings.Builder
	run.WriteString("\n# Append overlay service fragments to base /etc/supervisord.conf\n")
	fmt.Fprintf(&run, "RUN --mount=type=bind,from=%s,source=/supervisor-overlay,target=/supervisor-overlay \\\n", stageName)
	run.WriteString("    sh -c 'for f in /supervisor-overlay/*.conf; do echo; cat \"$f\"; done >> /etc/supervisord.conf'\n")
	return stage.String(), run.String(), nil
}

// exec returns the configured executor, defaulting to the local one.
func (t *PodDeployTarget) exec() DeployExecutor {
	if t.Executor == nil {
		return ShellExecutor{}
	}
	return t.Executor
}

// Name identifies this target.
func (t *PodDeployTarget) Name() string { return "pod" }

// OverlayImageRef returns the overlay image reference that was built,
// or the base image when no overlay was needed. Caller passes this to
// the quadlet/start machinery.
func (t *PodDeployTarget) OverlayImageRef() string {
	if t.overlayImageRef != "" {
		return t.overlayImageRef
	}
	return t.BaseImage
}

// Emit is the DeployTarget entry point. Handles overlay synthesis when
// the plan set has any candies that aren't part of the base image.
// Does NOT perform the final container start — that stays in start.go
// via DeployUpCmd.
func (t *PodDeployTarget) Emit(plans []*InstallPlan, opts EmitOpts) error {
	if t.Engine == "" {
		t.Engine = "podman"
	}

	// Determine which plans represent overlay candies (add_candy:)
	// rather than candies already baked into the base image. v1 heuristic:
	// a plan's Candy is in any plan's AddCandies list → overlay.
	//
	// An EMPTY plan set is the no-add_candy case for an externalized pod (the
	// external-substrate compileNodePlans skips the primary box plan — the box's candies
	// are already baked into the base image): there is no overlay to synthesize, but the
	// deploy-name alias must still be tagged below so `charly config/start <deploy-name>`
	// resolves the base image when deploy-name != image-name. (collectOverlayCandies([]) is
	// [], so the len==0 branch handles it.)
	overlayCandies := collectOverlayCandies(plans)
	if len(overlayCandies) == 0 {
		// Nothing to overlay — the existing base image is deploy-ready.
		t.overlayImageRef = t.BaseImage
		// Schema v3: still tag the base as `<registry>/<deploy-name>:
		// latest` so `charly config/start <deploy-name>` can resolve it by
		// deployment name when deploy-name != image-name (e.g. a pod
		// deployment `check-sway-browser-vnc-pod` targeting image `sway-browser-vnc`).
		if opts.DryRun {
			return nil
		}
		if t.DeployName != "" && t.BaseImage != "" {
			if err := t.tagDeployAlias(opts); err != nil {
				return err
			}
		}
		return nil
	}

	// Synthesize overlay Containerfile.
	return t.buildOverlay(plans, overlayCandies, opts)
}

// tagDeployAlias tags t.overlayImageRef under
// `<registry>/<deploy-name>:<calver>` so deployment-name-keyed commands
// (`charly config setup`, `charly start`) resolve the image correctly when
// deploy-name differs from image-name (schema v3). Registry comes from
// the base image's `ai.opencharly.registry` OCI label.
//
// CalVer-only — no `:latest` alias is emitted. The short-name resolver
// (`local_image.go`) uses the highest-CalVer match for a given deploy
// name, which correctly picks the freshly-tagged alias here.
func (t *PodDeployTarget) tagDeployAlias(opts EmitOpts) error {
	registry := readImageRegistry(t.Engine, t.overlayImageRef)
	calver := ComputeCalVer()
	aliasRef := t.DeployName + ":" + calver
	if registry != "" {
		aliasRef = registry + "/" + t.DeployName + ":" + calver
	}
	if aliasRef == t.overlayImageRef {
		return nil
	}
	tagScript := fmt.Sprintf("%s tag %s %s",
		t.Engine, deployShellQuote(t.overlayImageRef), deployShellQuote(aliasRef))
	if err := t.exec().RunUser(opts.ContextOrDefault(), tagScript, opts); err != nil {
		return fmt.Errorf("deploy-name alias tag: %w", err)
	}
	return nil
}

// collectOverlayCandies returns the set of candy names declared as
// add_candy in any plan's meta. v1 heuristic: union all plans'
// AddCandies slices.
func collectOverlayCandies(plans []*InstallPlan) []string {
	seen := make(map[string]bool)
	var out []string
	for _, p := range plans {
		for _, n := range p.AddCandies {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// overlayOCITarget builds the OCITarget that renders the overlay Containerfile.
//
// ContextRelPrefix MUST equal BuildDir (both the overlay build dir, relative to
// the build-context root = the project dir): emitWrite stages a write: step's
// inline content to `<BuildDir>/_inline/<candy>/<hash>` and the matching COPY in
// the Containerfile references it by `<ContextRelPrefix>/_inline/<candy>/<hash>`.
// When ContextRelPrefix is empty the COPY drops the build-dir prefix and resolves
// to `<context>/_inline/...` — a path that does not exist — so the overlay build
// fails at `COPY … _inline/<candy>/<hash>: stat: no such file or directory`. This
// mirrors the full build, which sets contextRelPrefix = .build/<boxName> to match
// its buildDir (generate.go writeCandySteps). buildDir is already relative to the
// build-context root (PrepareVenue / OverlayBuildDir convention), so it serves as
// both the on-disk write root and the context-relative COPY prefix.
func (t *PodDeployTarget) overlayOCITarget(buildDir string) *OCITarget {
	return &OCITarget{
		DistroDef:        t.DistroDef,
		BuilderConfig:    t.BuilderConfig,
		Generator:        t.Generator,
		Box:              t.Box,
		BuildDir:         buildDir,
		ContextRelPrefix: buildDir,
	}
}

// buildOverlay synthesizes an overlay Containerfile and builds the image.
func (t *PodDeployTarget) buildOverlay(plans []*InstallPlan, overlayCandies []string, opts EmitOpts) error {
	dir := t.OverlayBuildDir
	if dir == "" {
		dir = filepath.Join(".build", "overlay-"+t.DeployName)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("overlay build dir: %w", err)
	}
	// Stage REMOTE add_candy candies' source trees into .build/_candy/<name>.<version>/
	// — the SAME staging the full build runs (generate.go Generate → createRemoteCandyCopies,
	// R3: ONE staging implementation, shared). Each overlay candy gets a `FROM scratch AS
	// <name>` context stage (emitted below) whose `COPY <candyCopySource>/ /` resolves, for a
	// REMOTE candy, to `.build/_candy/<name>.<version>/` (candyCopySource). A copy:/cmd: step
	// references that stage via `--from=<name>` / `--mount=type=bind,from=<name>`, so buildah
	// actually BUILDS the stage and its COPY source MUST exist on disk; without this staging
	// the overlay build fails at `COPY .build/_candy/<name>.<version>/: no such file or
	// directory`. (A write:-only candy never references the stage — emitWrite's inline COPY
	// reads the staged _inline file from the build context directly — which is why the
	// write:-only marker did not surface this; the per-candy scratch stage was emitted but
	// unreferenced, so buildah never built it.) createRemoteCandyCopies only stages
	// layer.Remote candies, so a LOCAL add_candy candy (candyCopySource → candy/<name>/,
	// already under the project root) is a no-op. The overlay Generator's BuildDir == g.Dir +
	// "/.build" (NewGenerator default), so the staged path matches candyCopySource's literal
	// ".build/_candy/…" prefix relative to the project-root build context.
	if t.Generator != nil {
		if err := t.Generator.createRemoteCandyCopies(); err != nil {
			return fmt.Errorf("staging remote overlay candies: %w", err)
		}
	}
	// Render overlay Containerfile via OCITarget. Generator + Box are
	// required for task emission to produce RUN directives (without them
	// the emitter emits "no Generator context" comments — the overlay
	// then contains no install logic).
	oci := t.overlayOCITarget(dir)
	// Only emit for the overlay candies.
	filtered := filterPlansByCandies(plans, overlayCandies)
	if err := oci.Emit(filtered, opts); err != nil {
		return err
	}

	var cf bytes.Buffer
	fmt.Fprintf(&cf, "# Overlay Containerfile for deploy %q\n", t.DeployName)
	fmt.Fprintf(&cf, "# Extra candies: %s\n\n", strings.Join(overlayCandies, ", "))
	// Emit per-layer context stages before the main FROM. The tasks
	// emitted by oci.Emit() reference these via `--mount=type=bind,
	// from=<layer>`, same as the full-build Containerfile does (see
	// generate.go:289). Without these stages the bind mounts fail with
	// "no stage or image found with that name."
	if t.Generator != nil {
		for _, candyName := range overlayCandies {
			layer := t.Generator.candyByName(candyName)
			if layer == nil {
				continue
			}
			// candyCopySource is keyed by the candy's STORE key (candyMapKey),
			// not its bare name — a remote add_candy candy's COPY source lives at
			// .build/_candy/<name>.<version>, reachable only via the qualified key.
			fmt.Fprintf(&cf, "FROM scratch AS %s\n", layer.Name)
			fmt.Fprintf(&cf, "COPY %s/ /\n\n", t.Generator.candyCopySource(candyMapKey(layer)))
		}
	}
	// Render service: entries from overlay candies — emits a scratch
	// stage holding the rendered fragments AND a RUN-append line to be
	// placed inside the main image stage below. Uses the Generator's
	// init-fragment pipeline (same path as the full image build).
	var svcStage, svcAppend string
	if t.Generator != nil && t.Box != nil {
		var svcErr error
		svcStage, svcAppend, svcErr = t.renderOverlayServices(overlayCandies)
		if svcErr != nil {
			return svcErr
		}
	}
	// Service scratch stage must come BEFORE the main FROM so buildah
	// sees it when the main-stage RUN does `--mount=type=bind,from=<stage>`.
	if svcStage != "" {
		cf.WriteString(svcStage)
	}
	fmt.Fprintf(&cf, "FROM %s\n\n", t.BaseImage)
	// Match the full-build convention: reset to USER root after FROM so
	// candy tasks with `user: root` (most install/config tasks) run with
	// the correct privileges. Full build does this in generate.go:467.
	cf.WriteString("USER root\n\n")
	cf.WriteString(oci.String())
	// Append service fragments inside the MAIN image stage (after all
	// candy tasks). This extends the base image's /etc/supervisord.conf
	// instead of replacing it.
	if svcAppend != "" {
		cf.WriteString(svcAppend)
	}
	// Merge overlay-candy security into the base image's
	// LabelSecurity and re-emit so `charly config` (quadlet generator)
	// picks up intrinsic requirements declared by add_candy — e.g.
	// k3s-server's `security: { privileged, cgroupns: host,
	// devices: [/dev/fuse] }`. Without this, add_candy security
	// blocks are silently dropped because only the base image's
	// candy-merged security made it into the base image's own label.
	if overlayLabel := t.renderOverlaySecurityLabel(overlayCandies); overlayLabel != "" {
		cf.WriteString(overlayLabel)
	}

	// Restore base image's USER directive. The overlay set `USER root`
	// up at line ~324 so package installs work; without restoration,
	// USER=root leaks into the resulting image and breaks every
	// downstream invariant that depends on the base running as a
	// non-root user (rootless nested podman, claude's
	// --dangerously-skip-permissions, etc.). Symptom of the regression
	// before this fix: the harness sandbox with add_candy: [virtualization]
	// flipped from uid=1000 to uid=0, breaking the harness's claude
	// invocation across every iteration.
	if baseMeta, err := ExtractMetadata(t.Engine, t.BaseImage); err == nil && baseMeta != nil && baseMeta.User != "" && baseMeta.User != "root" {
		fmt.Fprintf(&cf, "\nUSER %s\n", baseMeta.User)
	}

	cfPath := filepath.Join(dir, "Containerfile")
	if err := os.WriteFile(cfPath, cf.Bytes(), 0644); err != nil {
		return err
	}

	// Deterministic overlay tag: hash of base + sorted candy set.
	tag := overlayTagFor(t.BaseImage, overlayCandies)
	t.overlayImageRef = fmt.Sprintf("%s-overlay:%s", t.DeployName, tag)

	if opts.DryRun {
		fmt.Fprintf(t.stderr(), "[dry-run] %s build -f %s -t %s %s\n",
			t.Engine, cfPath, t.overlayImageRef, dir)
		return nil
	}

	// Build context is the PROJECT ROOT (Generator.Dir), not the overlay
	// build dir — the emitted Containerfile has `COPY candy/<name>/ /`
	// paths that are relative to the project root, same as the full
	// build (see generate.go:candyCopySource).
	buildContext := dir
	if t.Generator != nil && t.Generator.Dir != "" {
		buildContext = t.Generator.Dir
	}

	// Containerfile path on the host (host-side absolute) is also
	// host-only; nested execution can't see it directly.
	cfPathInVenue := cfPath
	venueBuildContext := buildContext

	// Route the podman build via the configured executor. On the root
	// (ShellExecutor) this is equivalent to the prior direct
	// exec.CommandContext call. On a NestedExecutor the command runs
	// in the parent venue — translate host-side paths (Containerfile,
	// build context) to venue-side paths via the parent's bind-mount
	// mappings declared in charly.yml. Pre-C10 this errored out
	// unconditionally; with the path translator we can continue when
	// the parent venue has the project tree bind-mounted.
	if nested, ok := t.Executor.(*NestedExecutor); ok && nested != nil {
		venuePath, ok := translateHostPathToVenue(buildContext, opts.ParentNode)
		if !ok {
			return fmt.Errorf("PodDeployTarget: nested container overlay build inside %s requires the project tree at %s to be bind-mounted into the parent venue (set `volumes: [{name: project, type: bind, host: %s, path: /workspace}]` on the parent charly.yml entry, then re-run)", nested.Venue(), buildContext, buildContext)
		}
		venueBuildContext = venuePath
		// The Containerfile lives inside the build context, so its
		// venue-side path follows the same translation.
		if cfVenue, ok := translateHostPathToVenue(cfPath, opts.ParentNode); ok {
			cfPathInVenue = cfVenue
		}
	}

	buildScript := fmt.Sprintf("%s build -f %s -t %s %s",
		t.Engine, deployShellQuote(cfPathInVenue), deployShellQuote(t.overlayImageRef), deployShellQuote(venueBuildContext))
	if err := t.exec().RunUser(opts.ContextOrDefault(), buildScript, opts); err != nil {
		return fmt.Errorf("overlay build: %w", err)
	}

	// Schema v3: tag the overlay under `<registry>/<deploy-name>:
	// latest`. See tagDeployAlias.
	return t.tagDeployAlias(opts)
}

// renderOverlaySecurityLabel merges the base image's baked
// LabelSecurity with each overlay candy's own `security:` block and
// returns a Containerfile LABEL directive that overwrites the
// base's label — or "" if no merge is needed. The resulting LABEL
// sits after all tasks in the overlay stage so it wins on pull.
// Picked up at deploy time by `charly config` via ExtractMetadata.
func (t *PodDeployTarget) renderOverlaySecurityLabel(overlayCandies []string) string {
	if t.Engine == "" || t.BaseImage == "" || t.Generator == nil {
		return ""
	}
	// Start from the base image's existing security.
	baseMeta, _ := ExtractMetadata(t.Engine, t.BaseImage)
	var sec SecurityConfig
	if baseMeta != nil {
		sec = baseMeta.Security
	}
	// Merge each overlay candy's security on top. Same semantics as
	// CollectSecurity in generate.go: union caps/devices/security_opts,
	// OR privileged, last-writer for cgroupns, shm/memory tightest-wins.
	added := false
	for _, candyName := range overlayCandies {
		layer := t.Generator.candyByName(candyName)
		if layer == nil {
			continue
		}
		ls := layer.Security()
		if ls == nil {
			continue
		}
		added = true
		if ls.Privileged {
			sec.Privileged = true
		}
		if ls.CgroupNS != "" {
			sec.CgroupNS = ls.CgroupNS
		}
		sec.CapAdd = appendUnique(sec.CapAdd, ls.CapAdd...)
		sec.Devices = appendUnique(sec.Devices, ls.Devices...)
		sec.SecurityOpt = appendUnique(sec.SecurityOpt, ls.SecurityOpt...)
		sec.GroupAdd = appendUnique(sec.GroupAdd, ls.GroupAdd...)
		sec.Mounts = appendUnique(sec.Mounts, ls.Mounts...)
	}
	if !added {
		return ""
	}
	data, err := json.Marshal(sec)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("LABEL %s=%s\n", LabelSecurity, shellSingleQuote(string(data)))
}

// readImageRegistry reads the ai.opencharly.registry OCI label from
// an image. Used by the alias tagging to preserve the
// registry prefix the quadlet generator expects.
func readImageRegistry(engine, imageRef string) string {
	out, err := exec.Command(engine, "inspect", "--format", "{{index .Config.Labels \"ai.opencharly.registry\"}}", imageRef).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// filterPlansByCandies returns only the plans whose Candy is in names.
func filterPlansByCandies(plans []*InstallPlan, names []string) []*InstallPlan {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []*InstallPlan
	for _, p := range plans {
		if want[p.Candy] {
			out = append(out, p)
		}
	}
	return out
}

// overlayTagFor computes a deterministic short tag from the base image
// ref + the (sorted) overlay candy set. Same inputs → same tag, so
// re-deploys of the same config don't churn overlay images.
func overlayTagFor(base string, layers []string) string {
	sorted := append([]string(nil), layers...)
	sortStrings(sorted)
	h := sha256.New()
	h.Write([]byte(base))
	h.Write([]byte{0})
	for _, l := range sorted {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func (t *PodDeployTarget) stderr() *os.File {
	if t.DryRunWriter != nil {
		return t.DryRunWriter
	}
	return os.Stderr
}

// RemoveOverlayImage removes the overlay image produced by Emit. Used
// at `charly bundle del` time unless --keep-image is set.
func (t *PodDeployTarget) RemoveOverlayImage(opts EmitOpts) error {
	if t.overlayImageRef == "" || t.overlayImageRef == t.BaseImage {
		return nil
	}
	if opts.DryRun {
		fmt.Fprintf(t.stderr(), "[dry-run] %s rmi %s\n", t.Engine, t.overlayImageRef)
		return nil
	}
	cmd := exec.CommandContext(context.Background(), t.Engine, "rmi", t.overlayImageRef)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// translateHostPathToVenue maps a host-side absolute path to the
// equivalent path inside a parent venue, by walking the parent
// BundleNode's bind-mount volumes. Returns (venuePath, true)
// when a containing bind-mount is found; ("", false) otherwise.
//
// Used by C10' s pod-in-pod overlay build path: the nested podman
// runs in the parent venue and needs build-context paths expressed
// in the venue's filesystem view, not the host's.
//
// Example: parent has
//
//	volumes: [{name: project, type: bind, host: /home/user/repo, path: /workspace}]
//
// then translateHostPathToVenue("/home/user/repo/candy/x", parent)
// returns ("/workspace/candy/x", true).
func translateHostPathToVenue(hostPath string, parent *BundleNode) (string, bool) {
	if parent == nil || hostPath == "" {
		return "", false
	}
	// Normalize the input: the bind-mount Host fields are typically
	// expanded (no ~), absolute, and lack trailing slashes.
	clean := filepath.Clean(hostPath)
	for _, v := range parent.Volume {
		if v.Type != "bind" || v.Host == "" || v.Path == "" {
			continue
		}
		hostBase := filepath.Clean(v.Host)
		// hostPath must equal hostBase or be a subpath of it.
		if clean == hostBase {
			return filepath.Clean(v.Path), true
		}
		prefix := hostBase + string(filepath.Separator)
		if after, ok := strings.CutPrefix(clean, prefix); ok {
			rel := after
			return filepath.Join(v.Path, rel), true
		}
	}
	return "", false
}
