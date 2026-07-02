package main

// install_build.go — the InstallPlan compiler.
//
// BuildDeployPlan walks a resolved image plus its candy set and produces
// an InstallPlan (charly/install_plan.go) — the IR that both the OCI target
// (Containerfile emission) and the host target (shell + podman execution)
// consume.
//
// This function is intentionally pure: given the same inputs, it produces
// the same InstallPlan regardless of filesystem or environment. Side
// effects happen later, inside DeployTarget.Emit implementations.
//
// Logic here replaces the per-candy walk inside writeCandySteps
// (generate.go:1075-1208). Instead of emitting Containerfile text
// directly, we emit structured InstallSteps that know *what* to do —
// leaving *how* to render up to each target.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
)

// HostContext carries host-side information the compiler needs to decide
// (a) which format section to pick for the host target, (b) which
// builder image to run user-scope builders in, (c) whether to gate
// AUR-specific steps, etc. For the OCI target, the caller passes a
// zero-value HostContext (the compiler ignores host-only choices when
// Target is "oci").
type HostContext struct {
	// Target selects compilation mode. "" or "oci" means "compile for
	// container build"; "host" means "compile for direct host execution".
	// Primarily affects which steps get VenueSkip (container-only fields
	// like ports:/volumes: are skipped on "host").
	Target string

	// Distro is the resolved host distro tag, e.g. "fedora:43". Used to
	// pick the right format section when compiling for a host target
	// whose distro differs from the image's primary distro.
	Distro string

	// GlibcVersion is the host's glibc major.minor as reported by
	// `ldd --version`. Used by the host target's preflight check against
	// the selected builder image. Optional; empty means skip the check.
	GlibcVersion string

	// BuilderImage overrides the default builder-image selection for
	// VenueContainerBuilder steps. Populated from --builder-image. ""
	// means "use the embedded build vocabulary's default".
	BuilderImage string

	// BuilderContext carries the host-side build PRE-PASS result: each externalized
	// detection-builder's per-candy stage context + teardown ops, keyed by
	// builderCtxKey(candy, builder). Populated by preresolveBuilderContexts BEFORE
	// this pure compile (the deploy command path); read by collectBuilderContext +
	// compileBuilderSteps so the compiler NEVER dials a builder plugin (purity). Nil
	// when no pre-pass ran (a direct BuildDeployPlan caller / test) or no externalized
	// builder is triggered → the affected builder gets base-only context, no teardown.
	BuilderContext map[string]builderPreresolved
}

// BuildDeployPlan compiles one Candy into an InstallPlan.
//
// For whole-image deploys, the caller iterates the ordered candy list
// (from ResolveCandyOrder) and builds one plan per candy, then merges
// them. Keeping one plan per candy makes refcounting trivial in the
// ledger: each candy's teardown is independent.
func BuildDeployPlan(layer *Candy, img *ResolvedBox, hostCtx HostContext) (*InstallPlan, error) {
	if layer == nil {
		return nil, fmt.Errorf("BuildDeployPlan: nil candy")
	}
	if img == nil {
		return nil, fmt.Errorf("BuildDeployPlan: nil image %q", layer.Name)
	}

	plan := &InstallPlan{
		Box:             img.Name,
		Version:         layer.Version,
		Distro:          primaryDistroTag(img, hostCtx),
		Candy:           layer.Name,
		CandiesIncluded: []string{layer.Name},
	}

	// 0. Shell-env hook: vars + env + path_append — applies regardless of
	// target. OCI target will emit as ENV directives; host target as
	// env.d file + managed-block PATH.
	if hook := compileShellHookStep(layer, img); hook != nil {
		plan.Steps = append(plan.Steps, hook)
	}

	// 1. System packages — distro tag wins over build format sections.
	// When compiling for the host target, pick the format matching the
	// host distro instead of the image's primary format.
	pkgSteps := compileSystemPackageSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, pkgSteps...)

	// 2. Multi-stage builders triggered by candy manifest files. Emitted
	// BEFORE the candy's tasks so a task that consumes the builder's home
	// artifacts (e.g. selkies' web-copy reads the pixi/build.sh output at
	// ~/.local/share/selkies-build) finds them already in place. This
	// matches the image build, where every builder stage's /home is COPYed
	// into the main stage up front (before any candy install step) — the
	// builder runs in an isolated stage/image and never depends on the
	// candy's own tasks, so running it first is always safe. On a cross-host
	// deploy the BuilderStep extracts the home artifacts into the guest home,
	// which the subsequent OpStep then relocates; the OCI target hoists the
	// builder stages regardless of step order, so its Containerfile is
	// unchanged.
	builderSteps := compileBuilderSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, builderSteps...)

	// 2.5. Bundled PKGBUILD (`localpkg:`) — build on the host + pacman -U on a
	// pac deploy target. Emitted BEFORE the candy's tasks so the package is
	// already installed when the candy's own `cmd:` task runs: the charly candy's
	// task is pacman-aware (it does nothing when opencharly-git is present and
	// only curls a binary otherwise), so the package must land first or the
	// curl branch shadows the proper /usr/bin/charly with a stale /usr/local/bin/charly.
	// Skipped on image build (no makepkg in a container) and on non-pac targets.
	if pkgStep := compileLocalPkgStep(layer, img, hostCtx); pkgStep != nil {
		plan.Steps = append(plan.Steps, pkgStep)
	}

	// 3. The install timeline: the candy plan's build/deploy-context run: steps,
	// lowered into typed install steps (or generic OpSteps).
	taskSteps := compileOpSteps(layer, img)
	plan.Steps = append(plan.Steps, taskSteps...)

	// 4. Services: both legacy `service:` (supervisord INI fragments) and
	// `system_services:` (systemd unit names). After the Task 6 migration
	// lands these both flow through a unified `services:` schema; for now
	// we read the two fields independently.
	svcSteps := compileServiceSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, svcSteps...)

	// 5. Shell-init snippets: the candy manifest `shell:` block. Generic body +
	// per-shell sub-blocks (bash/zsh/fish/sh). Selection rule: per-shell
	// wins over generic with ${SHELL_NAME} substitution.
	shellSteps := compileShellSnippetSteps(layer, img, hostCtx)
	plan.Steps = append(plan.Steps, shellSteps...)

	// 6. Android apps: the candy manifest `apk:` package format. Compiled into ONE
	// ApkInstallStep regardless of target; the android deploy preresolver reads it
	// host-side (collectAndroidInstalls) and the external deploy:android plugin
	// installs the apps — every DeployTarget records a skip. See ApkInstallStep.
	if apkStep := compileApkStep(layer); apkStep != nil {
		plan.Steps = append(plan.Steps, apkStep)
	}

	// 7. Reboot: the candy manifest `reboot: true`. Emitted LAST so the reboot
	// follows every install step of this candy. Only the vm deploy acts
	// on it (the host's rebootVenueAndWait reboots the guest + waits); OCI/pod/k8s skip it (no machine
	// at build time); the local deploy target skips + warns (never reboots the
	// operator host unattended). See RebootStep.
	if layer.reboot {
		plan.Steps = append(plan.Steps, &RebootStep{CandyName: layer.Name})
	}

	return plan, nil
}

// compileApkStep turns a candy's `apk:` package list into a single
// ApkInstallStep, or nil if the candy declares no apk packages. The `apk`
// format is target-agnostic at compile time — the step carries the specs and
// each DeployTarget decides whether to execute (android) or skip (everything
// else), exactly as the IR intends for venue-specific work.
func compileApkStep(layer *Candy) InstallStep {
	apks := layer.Apk()
	if len(apks) == 0 {
		return nil
	}
	return &ApkInstallStep{
		Packages:  append([]ApkPackageSpec(nil), apks...),
		CandyName: layer.Name,
		CandyDir:  layer.SourceDir,
	}
}

// compileLocalPkgStep turns a candy's `localpkg:` field into a single
// LocalPkgInstallStep, or nil if the candy declares none. Like compileApkStep
// it is target-agnostic at compile time — the step carries the author's
// PKGBUILD hint plus the candy dir AND the deploy project dir (os.Getwd, the
// same handle compileServiceSteps reads) as the two anchors the emit-time
// walk-up search uses to locate the PKGBUILD. Each DeployTarget decides whether
// to build+install (localpkg-capable host/guest), skip (image build, non-pac
// targets, android, k8s).
//
// The localpkg mechanism is fully config-driven: the format's `local_pkg:`
// block (resolved here via DistroDef.LocalPkgFormat, the SAME DistroDef the
// system-package steps read) supplies the build/install templates, package
// glob, foreign-deps query, probe, and dependency-builder name. BuilderImage is
// resolved for that block's DepBuilder the same way builder steps resolve theirs
// (resolveBuilderImage) so the executor can build the package's dependency
// closure through the EXISTING builder before installing. When the distro
// declares no localpkg-capable format, LocalPkg is nil and the executor skips;
// when no dep builder resolves, BuilderImage is "" and the dep-build is skipped
// with a clear log (the candy's own curl/COPY fallback still covers
// non-localpkg targets).
func compileLocalPkgStep(layer *Candy, img *ResolvedBox, _ HostContext) InstallStep {
	// The target distro must declare a localpkg-capable package format, AND the
	// candy must point that format at a source dir. Resolve the format FIRST so
	// the per-format `localpkg:` map picks the matching source (pac→pkg/arch,
	// rpm→pkg/fedora, deb→pkg/debian). Either missing → no step (the candy's own
	// curl/COPY task is the fallback on formats with no native package).
	if img.DistroDef == nil {
		return nil
	}
	fmtName, lp := img.DistroDef.LocalPkgFormat(img.Pkg)
	if lp == nil {
		return nil
	}
	ref := layer.LocalPkg(fmtName)
	if ref == "" {
		return nil
	}
	projectDir, _ := os.Getwd()
	return &LocalPkgInstallStep{
		PkgbuildRef: ref,
		CandyName:   layer.Name,
		CandyDir:    layer.SourceDir,
		ProjectDir:  projectDir,
		Format:      fmtName,
		LocalPkg:    lp,
	}
}

// MergePlan combines a list of per-candy plans into one whole-image
// plan. Used by deploy targets that want to see all steps in one
// sequence (for sudo batching, overall dry-run display, etc.) while
// preserving per-candy provenance for the ledger.
func MergePlan(plans []*InstallPlan, image string, addCandies []string) *InstallPlan {
	out := &InstallPlan{
		Box:        image,
		AddCandies: append([]string(nil), addCandies...),
	}
	if len(plans) == 0 {
		return out
	}
	out.Distro = plans[0].Distro
	for _, p := range plans {
		if p == nil {
			continue
		}
		out.Steps = append(out.Steps, p.Steps...)
		out.CandiesIncluded = append(out.CandiesIncluded, p.Candy)
	}
	out.DeployID = computeDeployID(image, out.CandiesIncluded, addCandies)
	return out
}

// computeDeployID returns a deterministic hex hash that identifies a
// specific deploy (image + ordered candy set + add_candy). Used as the
// ledger key so re-deploys of the same config are recognizable and
// candy-refcount bookkeeping is stable.
func computeDeployID(box string, layers, addCandies []string) string {
	h := sha256.New()
	h.Write([]byte(box))
	h.Write([]byte{0})
	for _, l := range layers {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	h.Write([]byte{1})
	for _, l := range addCandies {
		h.Write([]byte(l))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// primaryDistroTag picks the distro tag this plan is materialized against.
// img.Distro is the AUTHORITATIVE deploy-target distro chain and always wins:
// syntheticHostBox sets the operator host's tags, syntheticVmBox the GUEST's,
// and ResolveBox the image's — so a vm deploy resolves the guest distro, a host
// deploy the operator's, a pod/image deploy the image's. For a host deploy
// img.Distro[0] == hostCtx.Distro (both from DetectHostDistro; PrimaryTag() ==
// Tags[0]), so this is byte-identical to the old host path while fixing vm
// deploys (whose hostCtx.Distro is the OPERATOR's distro, NOT the guest's — the
// detectHostContext default). hostCtx.Distro is only a fallback when img carries
// no distro. (Package resolution already uses img via compileSystemPackageSteps,
// so making the plan's distro img-authoritative also removes a latent
// inconsistency, never introduces one.)
func primaryDistroTag(img *ResolvedBox, hostCtx HostContext) string {
	if len(img.Distro) > 0 {
		return img.Distro[0]
	}
	if hostCtx.Distro != "" {
		return hostCtx.Distro
	}
	return ""
}

// ---------------------------------------------------------------------------
// Shell hook compilation
// ---------------------------------------------------------------------------

// compileShellHookStep returns a ShellHookStep for the candy's env: and
// path_append: fields, or nil if the candy contributes neither.
//
// Home resolution is DEFERRED: `~`/`$HOME` are rewritten to the literal
// `{{.Home}}` token rather than expanded against img.Home. Each DeployTarget
// resolves the token at emit time against the home of the actual deploy
// destination — img.Home for the OCI/pod-overlay build, the host home for
// the local deploy target, and the GUEST home (via the SSH executor's ResolveHome)
// for the vm deploy. Baking img.Home here was wrong for VM deploys: the
// synthetic plan's Home was the host operator's home, so env.d on the guest
// pointed at /home/<operator> instead of /home/<guest-user>. See
// InstallPlan.ResolveHome.
func compileShellHookStep(layer *Candy, _ *ResolvedBox) *ShellHookStep {
	envCfg, _ := layer.EnvConfig()
	if envCfg == nil {
		return nil
	}
	if len(envCfg.Vars) == 0 && len(envCfg.PathAppend) == 0 {
		return nil
	}
	vars := make(map[string]string, len(envCfg.Vars))
	for k, v := range envCfg.Vars {
		vars[k] = ExpandPath(v, HomeToken)
	}
	paths := make([]string, 0, len(envCfg.PathAppend))
	for _, p := range envCfg.PathAppend {
		paths = append(paths, ExpandPath(p, HomeToken))
	}
	return &ShellHookStep{
		CandyName: layer.Name,
		EnvVars:   vars,
		PathAdd:   paths,
	}
}

// ---------------------------------------------------------------------------
// Shell-init snippet compilation
// ---------------------------------------------------------------------------

// compileShellSnippetSteps returns one ShellSnippetStep per (candy, shell)
// pair the candy contributes. Selection rule (mirrors TagPkgConfig):
//  1. If layer.Shell.ByShell[shell] exists, render it.
//  2. Else if layer.Shell.Init is non-empty, render the generic body with
//     ${SHELL_NAME} substituted to the active shell name.
//  3. Else this shell gets nothing from this candy.
//
// path_append entries are rendered into the snippet body using
// shell-appropriate syntax (PATH=... for bash/zsh/sh, fish_add_path for
// fish) before the step is emitted, so each DeployTarget emitter can
// write the snippet bytes verbatim.
//
// Destination resolution is target-aware:
//   - Container build (hostCtx.Target empty / "oci"): system-wide
//     drop-in (/etc/profile.d/charly-<candy>-<shell>.sh, /etc/fish/conf.d/
//     charly-<candy>.fish). UseDropin=true.
//   - target:local, target:vm (hostCtx.Target = "host" or "vm"):
//     bash/zsh/sh → managed-block append in the user's rc file (UseDropin
//     =false); fish → per-candy drop-in in ~/.config/fish/conf.d/
//     (UseDropin=true).
//
// Returns nil when the candy has no shell: block.
func compileShellSnippetSteps(layer *Candy, img *ResolvedBox, hostCtx HostContext) []InstallStep {
	cfg := layer.Shell()
	if cfg == nil {
		return nil
	}
	out := make([]InstallStep, 0, len(ShellAllowlist))
	// Home resolution is deferred for DEPLOY targets (host/vm): emit the
	// `{{.Home}}` token so each target resolves it at emit time against the
	// real destination home (host home for local, GUEST home for vm). The
	// container BUILD path (empty/oci target) keeps img.Home — there the
	// image's resolved Home IS the runtime home. See InstallPlan.ResolveHome.
	snippetHome := img.Home
	if hostCtx.Target == "host" || hostCtx.Target == "vm" {
		snippetHome = HomeToken
	}
	// Stable iteration: walk the allowlist in fixed order so plan output
	// is deterministic across runs.
	for _, shell := range []string{"bash", "zsh", "fish", "sh"} {
		spec, body, paths, ok := resolveShellSpec(cfg, shell)
		if !ok {
			continue
		}
		dest, useDropin := shellSnippetDestination(layer.Name, shell, hostCtx, snippetHome, spec.Path)
		if dest == "" {
			continue
		}
		// Render path_append into the body using shell-appropriate syntax.
		body = appendShellPathLines(body, paths, shell, snippetHome)
		if body == "" {
			continue
		}
		out = append(out, &ShellSnippetStep{
			CandyName:   layer.Name,
			Origin:      layer.Name,
			Shell:       shell,
			Snippet:     body,
			PathAppend:  paths,
			Destination: dest,
			Marker:      layer.Name,
			UseDropin:   useDropin,
			Priority:    cfg.Priority,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveShellSpec applies the per-shell-wins-over-generic selection rule.
// Returns the resolved spec, the rendered init body (with ${SHELL_NAME}
// substituted for the generic case), the path_append slice, and whether
// the shell has anything to contribute. Generic init wins when no
// per-shell override exists; per-shell wins otherwise.
func resolveShellSpec(cfg *ShellConfig, shell string) (*ShellSpec, string, []string, bool) {
	if cfg == nil {
		return nil, "", nil, false
	}
	if spec, ok := cfg.ByShell()[shell]; ok && spec != nil {
		// Per-shell override. We do NOT substitute ${SHELL_NAME} here —
		// the override is meant to be shell-specific, so the author wrote
		// the literal shell syntax already.
		body := spec.Init
		paths := append([]string(nil), spec.PathAppend...)
		if body == "" && len(paths) == 0 {
			return spec, "", nil, false
		}
		return spec, body, paths, true
	}
	// Fall back to generic body. Substitute ${SHELL_NAME}.
	if cfg.Init == "" && len(cfg.PathAppend) == 0 {
		return nil, "", nil, false
	}
	body := strings.ReplaceAll(cfg.Init, "${SHELL_NAME}", shell)
	paths := append([]string(nil), cfg.PathAppend...)
	// Synthesize a spec view for the caller (carries Path override if set
	// at the generic level).
	view := &ShellSpec{Init: body, PathAppend: paths, Path: cfg.Path}
	return view, body, paths, true
}

// shellSnippetDestination resolves the destination file path for a candy's
// snippet given the shell and target. When pathOverride is non-empty, it
// takes precedence (with ~/ expansion via ExpandPath). Returns the
// resolved path and a UseDropin discriminator (true: full-file write;
// false: managed-block append).
func shellSnippetDestination(candyName, shell string, hostCtx HostContext, home, pathOverride string) (string, bool) {
	isHost := hostCtx.Target == "host" || hostCtx.Target == "vm"
	if pathOverride != "" {
		expanded := ExpandPath(pathOverride, home)
		// Author override implies they know what they want; fish + drop-in
		// only differ from rc-append by where the path points, so treat
		// any override as drop-in (whole-file write) for predictability.
		return expanded, true
	}
	if isHost {
		switch shell {
		case "bash":
			return ExpandPath("~/.bashrc", home), false
		case "zsh":
			return ExpandPath("~/.zshrc", home), false
		case "sh":
			return ExpandPath("~/.profile", home), false
		case "fish":
			return ExpandPath(fmt.Sprintf("~/.config/fish/conf.d/charly-%s.fish", candyName), home), true
		}
		return "", false
	}
	// Container build (oci/empty target): system-wide drop-in files.
	switch shell {
	case "bash", "zsh", "sh":
		return fmt.Sprintf("/etc/profile.d/charly-%s-%s.sh", candyName, shell), true
	case "fish":
		return fmt.Sprintf("/etc/fish/conf.d/charly-%s.fish", candyName), true
	}
	return "", false
}

// appendShellPathLines appends path_append entries to the snippet body
// using shell-appropriate syntax. bash/zsh/sh use POSIX `PATH=$PATH:X`
// exports; fish uses `fish_add_path X`. Idempotent: if body already ends
// with a newline, no extra blank line is inserted.
func appendShellPathLines(body string, paths []string, shell, home string) string {
	if len(paths) == 0 {
		return body
	}
	var sb strings.Builder
	sb.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n")
	}
	for _, p := range paths {
		expanded := ExpandPath(p, home)
		switch shell {
		case "fish":
			fmt.Fprintf(&sb, "fish_add_path -gP %s\n", shellQuote(expanded))
		default:
			fmt.Fprintf(&sb, "export PATH=\"$PATH:%s\"\n", expanded)
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// System package compilation
// ---------------------------------------------------------------------------

// compileSystemPackageSteps emits SystemPackagesStep(s) for the candy's
// package sections, honoring the distro-tag-wins-over-build-format rule
// from today's writeCandySteps.
//
// Phase 1: walk img.Distro tags in order; first match wins. If a tag
// section is found, emit ONE install step using that section and stop.
//
// Phase 2: if no tag matched, walk img.BuildFormats in order; emit one
// install step per format section that has packages. (Today's
// writeCandySteps does this to support images that install both rpm and
// aur packages, for example.)
//
// For the IR we additionally break each install into the three-phase
// structure (prepare/install/cleanup) so the host target can gate
// PhasePrepare on --allow-repo-changes. The embedded vocabulary (charly/charly.yml)
// currently has only one phase per format (the monolithic install_template); Task 4 will split
// templates into phases. Until then, we emit everything as PhaseInstall.
// compileSystemPackageSteps resolves a candy's package surface for an image via
// the distro-specificity CASCADE and emits ONE SystemPackagesStep for the
// image's primary format. img.Distro is the most-specific-first tag chain
// (e.g. [debian:13, debian] or [ubuntu:24.04, ubuntu]); the candy's top-level
// package: list (layer.TopPackages) is the always-included BASE.
//
//   - packages = UNION of the base + every matching tag section's packages
//     (dedup), so a shared package at one level plus extras at another accumulate.
//   - repo/copr/options/exclude/module = the MOST-SPECIFIC matching level that
//     declares the field wins (a version's repo overrides the bare distro's).
//
// img.Distro is most-specific-first, so iterating it in REVERSE (least → most
// specific) and letting later writes win yields most-specific-wins for the Raw
// extras while packages union regardless of order. fedora/arch reach their
// packages via the bare-distro tag (img.Distro = [fedora] / [arch]); there is no
// format-section fallback — the deb collapse that caused non-deterministic repo
// selection is gone (per-distro tag sections never share a mutable section).
func compileSystemPackageSteps(layer *Candy, img *ResolvedBox, _ HostContext) []InstallStep {
	if img.DistroDef == nil {
		return nil
	}
	formatDef := img.DistroDef.Format[img.Pkg]
	if formatDef == nil {
		return nil
	}
	pkgs, raw, matched := resolveCascadePackages(layer, img)
	if !matched && len(pkgs) == 0 {
		return nil
	}
	return []InstallStep{buildSystemPackagesStep(img.Pkg, PhaseInstall, pkgs, raw, formatDef.CacheMount)}
}

// resolveCascadePackages is THE single distro-specificity cascade resolver,
// shared by EVERY package-emitting path — the deploy compiler
// (compileSystemPackageSteps) AND the image-build Containerfile emitter
// (generate.go writeCandySteps). There is exactly one resolution so build and
// deploy can never diverge.
//
// It computes the primary-format package set for a candy on an image: the
// candy's top-level `package:` BASE, UNION every matching distro tag section
// walked most-specific-first over img.Distro (deduped), with the Raw extras
// repo/copr/options/exclude/module resolved MOST-SPECIFIC-WINS. Returns the
// package list, the rendered Raw install context (including the unioned
// `package` list), and whether any tag section matched.
// cascadeTagChain returns the full per-candy cascade tag chain MOST-SPECIFIC
// FIRST: the image's distro chain (e.g. [debian:13, debian]) followed by the
// package-format FAMILY tag (img.Pkg = deb/pac/rpm) as the LEAST-specific level.
// A `distro: deb:` candy block therefore applies to EVERY deb-format distro
// (debian + ubuntu + their versions), `pac:` to arch + cachyos, `rpm:` to
// fedora — the family-generic level of the YAML-configured
// deb/pac/rpm → distro → version hierarchy. img.Pkg is the primary package format
// declared by the embedded vocabulary (charly/charly.yml), so the hierarchy lives entirely in YAML.
//
// Distro INHERITANCE is the complementary YAML mechanism: img.Distro is already
// expanded (at resolve time, expandPackageInheritance) to include any
// `inherit_packages: true` ancestor, so a cachyos image/VM carries [cachyos, …,
// arch] and a `distro: arch:` block DOES reach cachyos — while ubuntu (no flag)
// stays isolated from debian. Both knobs live entirely in the embedded vocabulary (charly/charly.yml).
func cascadeTagChain(img *ResolvedBox) []string {
	chain := append([]string(nil), img.Distro...)
	if img.Pkg != "" {
		chain = append(chain, img.Pkg)
	}
	return chain
}

func resolveCascadePackages(layer *Candy, img *ResolvedBox) (pkgs []string, raw map[string]any, matched bool) {
	seen := map[string]bool{}
	add := func(in []string) {
		for _, p := range in {
			if p != "" && !seen[p] {
				pkgs = append(pkgs, p)
				seen[p] = true
			}
		}
	}
	// Always-included base first (stable ordering).
	add(layer.TopPackages())

	raw = map[string]any{}
	chain := cascadeTagChain(img)
	for _, c := range slices.Backward(chain) { // least → most specific: format → distro → version
		cfg := layer.TagSection(c)
		if cfg == nil {
			continue
		}
		matched = true
		add(cfg.Package)
		// Most-specific level writes last → wins for each Raw extra.
		for _, k := range []string{"repo", "copr", "options", "exclude", "module"} {
			if v, ok := cfg.Raw[k]; ok && v != nil {
				raw[k] = v
			}
		}
	}
	raw["package"] = pkgs
	return pkgs, raw, matched
}

// buildSystemPackagesStep constructs a SystemPackagesStep from a
// PackageSection's Raw map, extracting the well-known structured fields
// (repos, options, copr, modules, exclude, keys) for gate checkuation
// while also preserving the full Raw map for template rendering later.
func buildSystemPackagesStep(format string, phase Phase, packages []string, raw map[string]any, cacheMounts []CacheMountDef) *SystemPackagesStep {
	step := &SystemPackagesStep{
		Format:            format,
		Phase:             phase,
		Packages:          append([]string(nil), packages...),
		Options:           extractStringSlice(raw, "options"),
		Copr:              extractStringSlice(raw, "copr"),
		Modules:           extractStringSlice(raw, "modules"),
		Exclude:           extractStringSlice(raw, "exclude"),
		Keys:              extractStringSlice(raw, "keys"),
		RawInstallContext: raw,
	}
	// Repos live under the canonical "repo" key — the schema YAML tag
	// (DistroPackages.Repo `yaml:"repo"`), what derivePackageSectionsFromCalamares
	// writes, and what NewInstallContext reads on the build path. Use toMapSlice
	// so a []map[string]any value (the Calamares Repo shape) converts correctly:
	// the prior `raw["repos"].([]interface{})` was wrong on BOTH the key (plural)
	// and the type assertion, so step.Repos stayed empty and the deploy path never
	// added a candy's apt/dnf repo (e.g. the charly candy's tailscale repo on deb).
	for _, m := range toMapSlice(raw["repo"]) {
		step.Repos = append(step.Repos, RepoSpec{Raw: m})
	}
	for _, cm := range cacheMounts {
		step.CacheMount = append(step.CacheMount, CacheMountSpec{Dst: cm.Dst, Sharing: cm.Sharing})
	}
	return step
}

// ---------------------------------------------------------------------------
// Task compilation
// ---------------------------------------------------------------------------

// compileOpSteps turns the candy's install timeline into InstallSteps. The
// timeline is the candy's `plan:` `run:` steps (StepKind()==KwRun) whose
// context includes build/deploy, in declared (cache-stable) order — the fold
// that retired the separate `task:` list. Each run op either LOWERS into an
// existing typed step (the `plugin: package` verb → SystemPackagesStep and the
// `plugin: service` verb → ServicePackagedStep, each via its TypedStepProvider) so
// emit + reversal are REUSED, or stays a generic OpStep (install verbs + command +
// the RenderProvisionScript plugin verbs). A run: step scoped runtime-only is
// plan-runtime provisioning the check Runner executes — NOT the install
// timeline — and is skipped here (avoids double-execution); check:/agent-*/
// include: steps are never lowered.
func compileOpSteps(layer *Candy, img *ResolvedBox) []InstallStep {
	var out []InstallStep
	for i := range layer.plan {
		step := &layer.plan[i]
		kw, err := step.StepKind()
		if err != nil || kw != KwRun {
			continue
		}
		op := &step.Op
		// A run: step scoped runtime-only (and NOT build/deploy) is handled by
		// the check Runner live; everything else is the install timeline.
		if opInContext(op, CtxRuntime) && !opInContext(op, CtxBuild) && !opInContext(op, CtxDeploy) {
			continue
		}
		if s := compileActOp(op, layer, img); s != nil {
			out = append(out, s)
		}
	}
	return out
}

// compileActOp lowers a single install-timeline op into the right InstallStep via the
// TypedStepProvider seam: a `plugin:` verb whose provider lowers into a typed step
// (package → SystemPackagesStep, service → ServicePackagedStep) is constructed via that
// provider's ConstructStep, so its Reverse() records the LOAD-BEARING reversals — a generic
// OpStep would drop them. Every other verb (the install verbs + command + the
// RenderProvisionScript plugin verbs) stays a generic OpStep. Callers decide which ops reach
// here — compileOpSteps passes the plan's build/deploy-context run: steps (the install
// timeline is act by definition, regardless of a verb's runtime-context do:assert default).
func compileActOp(op *Op, layer *Candy, img *ResolvedBox) InstallStep {
	verb, err := op.Kind()
	if err != nil {
		return nil
	}
	userDir, _ := resolveUserSpec(op.RunAs, img)
	// A `plugin:` verb whose provider lowers into a TYPED install step (package →
	// SystemPackagesStep, service → ServicePackagedStep) constructs that step here —
	// BEFORE the generic OpStep fallthrough — so its Reverse() records the load-bearing
	// reversals. The typed step then flows through the SAME Emit{OCI,Local,VM} + Reverse()
	// as before the verb was extracted. A `plugin:` verb whose provider is NOT a
	// TypedStepProvider (command, the RenderProvisionScript verbs) falls through to OpStep,
	// unchanged.
	if verb == "plugin" {
		// VERB-FIRST PRECEDENCE. A `run: plugin: <word>` whose word resolves as a VERB is a
		// verb act — resolved here BEFORE the class:step authored-external-step branch below.
		// This matters because a class:step WORD can COLLIDE with a verb word: `file` is both
		// `verb:file` (an in-proc ProvisionActor that drops a file) AND `step:file` (the C1.1
		// build-emit-only class:step plugin candy/plugin-installstep). The author's
		// `run: plugin: file` means the VERB; it must NOT be hijacked into an `external:file`
		// step (which the deploy walk would route to OpExecute — a leg the build-emit-only
		// plugin cannot serve). So the class:step branch is reached ONLY when the word is NOT a
		// verb (an authored external step KIND like examplestepkind).
		if prov, ok := providerRegistry.ResolveVerb(op.Plugin); ok {
			if stepprov, ok := prov.(TypedStepProvider); ok {
				return stepprov.ConstructStep(op, layer, img)
			}
			// An EXTERNAL (out-of-process) plugin verb has no in-proc ProvisionActor
			// shell — it EXECUTES its deploy-context effect at deploy over the E3b
			// reverse channel (Invoke(OpExecute) WITH the live executor), and bakes its
			// build-context fragment via Invoke(OpEmit). Route it to ExternalPluginStep.
			// The discriminator is the executorInvoker capability, which only the
			// grpcProvider (broker-carrying out-of-proc peer) satisfies — so `command`
			// and every built-in ProvisionActor verb fall through to the OpStep path
			// below (renderOpCommand), unchanged. The build-context counterpart
			// (emitTasks `case "plugin"`) stays the box-build seam; this is the
			// DEPLOY-context (Local/VM) + pod-overlay (OCI) leg.
			if _, ok := prov.(executorInvoker); ok {
				return &ExternalPluginStep{
					Op:           op,
					CandyName:    layer.Name,
					ResolvedUser: userDir,
					Distros:      img.Tags,
				}
			}
			// An in-proc verb (a ProvisionActor like `file`, or `command`) that is neither a
			// TypedStepProvider nor an out-of-process executorInvoker → the generic OpStep below
			// (its deploy act renders via resolveProvisionScript; its build-emit via emitTasks
			// `case "plugin"`). Deliberately NOT the class:step branch (verb-first, above).
		} else if sp, ok := providerRegistry.resolve(ClassStep, op.Plugin); ok {
			// The word is NOT a verb → an authored external step KIND: a class:step provider
			// DECLARING a StepContract (F3, e.g. examplestepkind). The opaque Payload is the op's
			// plugin_input, and Scope/Venue/Gate come from the plugin's declared contract. The
			// host walks it via the open default arm + dispatches OpExecute to the serving plugin
			// (executeExternalStep). The C1.1 build-emit-only class:step words never reach here —
			// `file` is a verb (handled above), and the other six (shell-hook/shell-snippet/
			// service-packaged/service-custom/repo-change/apk-install) are compiler-emitted NATIVE
			// step kinds, never authored as a `run: plugin:` op.
			if carrier, ok := sp.(stepContractCarrier); ok {
				if sc, ok := carrier.declaredStepContract(); ok {
					payload, _ := marshalJSON(op.PluginInput)
					return &externalStep{
						Word:      op.Plugin,
						ScopeV:    sc.Scope,
						VenueV:    sc.Venue,
						GateV:     sc.Gate,
						Payload:   payload,
						CandyName: layer.Name,
					}
				}
			}
		}
	}
	// Install verbs (mkdir/copy/write/link/download/setcap/build) + command →
	// a generic OpStep (existing emit + Reverse). Snapshot layer.vars so the
	// host/local renderer can emit `export K=V` (build-time gets these via
	// Containerfile ENV). Tokenize a home-relative `to:` so each DeployTarget
	// resolves it against the real destination home at emit.
	var candyVars map[string]string
	if len(layer.vars) > 0 {
		candyVars = make(map[string]string, len(layer.vars))
		maps.Copy(candyVars, layer.vars)
	}
	var resolvedTo string
	if op.To != "" {
		resolvedTo = ExpandPath(op.To, HomeToken)
	}
	return &OpStep{
		Op:           op,
		CandyName:    layer.Name,
		CandyDir:     layer.SourceDir,
		CtxPath:      layer.SourceDir,
		ResolvedUser: userDir,
		CandyVars:    candyVars,
		To:           resolvedTo,
		Distros:      img.Tags,
	}
}

// opStepScope classifies a resolved user directive into install scope — root
// (or empty / 0) is system, everything else user. Shared by the OpStep and
// service-act lowering so the scope rule lives in one place.
func opStepScope(userDir string) Scope {
	if userDir == "" || userDir == "root" || userDir == "0" || userDir == "0:0" {
		return ScopeSystem
	}
	return ScopeUser
}

// ---------------------------------------------------------------------------
// Builder compilation
// ---------------------------------------------------------------------------

// compileBuilderSteps emits one BuilderStep per triggered multi-stage or
// inline builder. Detection matches today's Generator.candyNeedsBuilder
// logic: DetectFiles (pixi/npm/cargo) and DetectConfig (aur).
func compileBuilderSteps(layer *Candy, img *ResolvedBox, hostCtx HostContext) []InstallStep {
	if img.BuilderConfig == nil {
		return nil
	}
	// Deterministic builder iteration order — matches today's BuilderNames.
	names := img.BuilderConfig.BuilderNames()
	var out []InstallStep
	for _, bName := range names {
		bDef := img.BuilderConfig.Builder[bName]
		if bDef == nil {
			continue
		}
		if !candyNeedsBuilderStep(layer, bDef) {
			continue
		}
		step := &BuilderStep{
			Builder:    bName,
			CandyName:  layer.Name,
			CandyDir:   layer.SourceDir,
			Phase:      PhaseInstall,
			BuilderDef: bDef,
		}
		step.BuilderImage = resolveBuilderImage(bName, img, hostCtx)
		step.RawStageContext = collectBuilderContext(layer, bName, img, hostCtx)
		// The builder-specific teardown ops are PRE-RESOLVED host-side (the build pre-pass,
		// builder_preresolve.go) and stashed here so BuilderStep.Reverse() is a pure getter — no
		// RPC at its host-side call sites. Nil when no pre-pass ran or a custom builder.
		if pre, ok := hostCtx.BuilderContext[builderCtxKey(layer.Name, bName)]; ok {
			step.PreResolvedReverse = pre.Reverse
		}

		// aur produces .pkg.tar.zst files in the container at /tmp/aur-pkgs/
		// and we need to pull them out on the host target. The container
		// (OCI) target ignores Artifacts — it uses COPY --from directly.
		// The host/VM targets install those package files via the SAME
		// config-driven transfer+install leg as the localpkg step, so carry
		// the package format's localpkg contract (install command + glob)
		// resolved from the embedded build vocabulary — no hardcoded pacman/glob in the executor.
		if bName == "aur" {
			step.Artifacts = []ArtifactRef{{
				ContainerPath: "/tmp/aur-pkgs/",
				HostPath:      "", // host-target populates at emit time (tmpdir)
				Chown:         false,
			}}
			if img.DistroDef != nil {
				if _, lp := img.DistroDef.LocalPkgFormat(img.Pkg); lp != nil {
					step.LocalPkg = lp
				}
			}
		}
		out = append(out, step)
	}
	return out
}

// candyNeedsBuilderStep mirrors Generator.candyNeedsBuilder without
// requiring a Generator receiver — the compiler doesn't have one.
func candyNeedsBuilderStep(layer *Candy, bDef *BuilderDef) bool {
	if bDef == nil {
		return false
	}
	for _, f := range bDef.DetectFiles {
		if candyHasFile(layer, f) {
			return true
		}
	}
	if bDef.DetectConfig != "" {
		section := layer.FormatSection(bDef.DetectConfig)
		if section != nil && len(section.Packages) > 0 {
			return true
		}
	}
	return false
}

// resolveBuilderImage selects the builder image for a given builder name.
// Priority: hostCtx override > image.Builder map > "" (caller must error
// or fall back to a sensible default).
func resolveBuilderImage(name string, img *ResolvedBox, hostCtx HostContext) string {
	if hostCtx.BuilderImage != "" {
		return hostCtx.BuilderImage
	}
	if img.Builder != nil {
		if ref, ok := img.Builder[name]; ok {
			return ref
		}
	}
	return ""
}

// collectBuilderContext extracts the per-builder ledger/teardown context: the base keys
// ("layer"/"builder"/"home") plus the builder-specific keys ("env_name"/"packages"/…) the
// BuilderStep's Reverse() method reads. The builder-specific keys are PRE-RESOLVED host-side
// (the build pre-pass, builder_preresolve.go) and carried on hostCtx.BuilderContext — pixi →
// env_name, aur → packages/replaces; cargo/npm record nothing (the host target reads
// Cargo.toml/package.json at install time). This stays a PURE function of its inputs: it reads
// the pre-populated map and NEVER dials a builder plugin (the externalization invariant). A
// custom candy builder (no externalized plugin) or a direct caller with no pre-pass gets
// base-only context.
func collectBuilderContext(layer *Candy, builderName string, img *ResolvedBox, hostCtx HostContext) map[string]any {
	ctx := map[string]any{
		"layer":   layer.Name,
		"builder": builderName,
		"home":    img.Home,
	}
	if pre, ok := hostCtx.BuilderContext[builderCtxKey(layer.Name, builderName)]; ok {
		for k, v := range pre.Context {
			ctx[k] = v
		}
	}
	return ctx
}

// stringSliceFromYAML coerces a YAML-decoded value into []string. The
// decoder produces []interface{} for sequences; we tolerate already-
// stringified slices for callers that pre-process.
func stringSliceFromYAML(v any) ([]string, bool) {
	switch s := v.(type) {
	case []string:
		return append([]string(nil), s...), true
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Service compilation
// ---------------------------------------------------------------------------

// compileServiceSteps turns the candy's service declarations into IR
// steps. Preference order:
//
//  1. Unified `services:` list (Task 6) — preferred when present.
//     Each entry becomes either a ServicePackagedStep (use_packaged:)
//     or a ServiceCustomStep (full spec).
//
// compileServiceSteps — unified schema only. Each ServiceEntry becomes
// either a ServicePackagedStep (use_packaged:) or a ServiceCustomStep
// (custom exec). Legacy fields (raw-INI service:, system_services:) are
// gone — external candies must run `charly migrate`.
//
// **Init-system polymorphism filter (2026-05).** When a candy declares
// the same service `name:` twice — once with `use_packaged:` and once
// with custom `exec:` (the mixed-entry polymorphism pattern documented
// in CLAUDE.md "Init-system polymorphism via mixed `service:` entries"
// and `/charly-build:layer` "Service Declaration") — the compiler picks ONE
// based on the target's init system:
//
//   - systemd target (host / vm) → emit the packaged form, SKIP the
//     custom-exec sibling (the packaged unit handles the daemon).
//   - supervisord target (oci/pod build) → SKIP the packaged form
//     (supervisord can't consume systemd units), emit the custom form.
//
// For singleton entries (no mixed pair), each entry renders as-is on
// the matching init system; mismatched singletons (e.g. a lone
// `use_packaged:` entry on a supervisord target) are silently skipped.
//
// For systemd targets, the compiler ALSO pre-populates UnitText/UnitPath
// on the ServiceCustomStep by calling RenderService, so executors don't
// need a runtime lazy-render step. This consolidates what used to live
// in three different places (the deleted in-proc VM-target lazy fallback,
// the OCI build's per-entry routing, and the legacy nothing-rendered
// path on the local deploy target) into ONE compile-time filter.
// serviceRenderDistros returns the distro tag chain a service entry's distro:
// filter is matched against for a deploy target. img.Distro is the AUTHORITATIVE
// deploy-target chain and always wins (mirrors primaryDistroTag): syntheticVmBox
// sets the GUEST chain (e.g. ["debian:13","debian"]), syntheticHostBox the
// operator host's, ResolveBox the image's. Using hostCtx.Distro here was the
// bug that made a vm deploy filter services against the OPERATOR's distro
// (arch) instead of the guest's (debian) — detectHostContext defaults
// hostCtx.Target to "host" + the operator distro even for a vm deploy, so a
// hostCtx-wins rule mis-scoped every vm/pod service. For a host deploy
// img.Distro is the operator's own tag chain (a SUPERSET of the single
// PrimaryTag, so arch-derivative hosts like cachyos now also match `arch:`
// entries). hostCtx.Distro is only a fallback when img carries no distro.
func serviceRenderDistros(img *ResolvedBox, hostCtx HostContext) []string {
	if len(img.Distro) > 0 {
		return img.Distro
	}
	if hostCtx.Distro != "" {
		return []string{hostCtx.Distro}
	}
	return nil
}

// serviceEntryAppliesToDistro reports whether a service entry should render for
// the given distro tag chain. An entry with an EMPTY distro: list applies to
// EVERY distro (the backward-compatible default — every pre-existing candy's
// services keep rendering everywhere). A non-empty list restricts the entry to
// the named distros, matched against either a bare distro name ("debian") or a
// full versioned tag ("debian:13") anywhere in the chain. This is the service
// analogue of a check step's exclude_distros: — the mechanism that lets ONE
// candy carry per-distro-divergent packaged units (modular virtqemud.socket on
// Fedora/Arch vs monolithic libvirtd.socket on Debian/Ubuntu) without a
// <name>-host sibling candy (CLAUDE.md R3).
func serviceEntryAppliesToDistro(entry *ServiceEntry, distros []string) bool {
	if len(entry.Distro) == 0 {
		return true
	}
	for _, tag := range distros {
		if slices.Contains(entry.Distro, tag) {
			return true
		}
		if base, _, found := strings.Cut(tag, ":"); found && slices.Contains(entry.Distro, base) {
			return true
		}
	}
	return false
}

func compileServiceSteps(layer *Candy, img *ResolvedBox, hostCtx HostContext) []InstallStep {
	var out []InstallStep
	initIsSystemd := hostCtx.Target == "host" || hostCtx.Target == "vm"
	distros := serviceRenderDistros(img, hostCtx)

	// Detect mixed-entry pairs: which names have a use_packaged form? Only
	// entries that APPLY to this target's distro count — a Fedora/Arch-only
	// packaged form must not suppress a Debian/Ubuntu exec sibling of the same
	// name (see serviceEntryAppliesToDistro).
	namesWithPackaged := map[string]bool{}
	for i := range layer.Service() {
		if layer.Service()[i].IsPackaged() && serviceEntryAppliesToDistro(&layer.Service()[i], distros) {
			namesWithPackaged[layer.Service()[i].Name] = true
		}
	}

	// Lazy-loaded systemd InitDef + render context — only loaded if the
	// target is systemd AND at least one custom entry needs rendering.
	var systemdDef *InitDef
	var renderCtx ServiceRenderContext
	loadedSystemd := false
	loadSystemd := func() bool {
		if loadedSystemd {
			return systemdDef != nil
		}
		loadedSystemd = true
		dir, err := os.Getwd()
		if err != nil {
			return false
		}
		_, _, initCfg, err := LoadBuildConfigForBox(dir)
		if err != nil || initCfg == nil {
			return false
		}
		def, ok := initCfg.Init["systemd"]
		if !ok || def == nil {
			return false
		}
		systemdDef = def
		renderCtx = ServiceRenderContext{
			Candy:         layer.Name,
			SystemUnitDir: "/etc/systemd/system",
		}
		// Service home, like shell-snippet home, must be the DESTINATION user's
		// home — not the build host's. For host/vm deploys defer it via the
		// {{.Home}} token (InstallPlan.ResolveHome substitutes the real guest /
		// host home at emit); for a container-systemd build the image's resolved
		// Home is the runtime home. (os.UserHomeDir() — the operator's home — was
		// the service-side instance of the VM $HOME bug.)
		svcHome := img.Home
		if hostCtx.Target == "host" || hostCtx.Target == "vm" {
			svcHome = HomeToken
		}
		if svcHome != "" {
			renderCtx.Home = svcHome
			renderCtx.UserUnitDir = svcHome + "/.config/systemd/user"
		}
		return true
	}

	for i := range layer.Service() {
		entry := &layer.Service()[i]
		// Per-distro filter: an entry with a distro: list renders only on the
		// named distros (see serviceEntryAppliesToDistro).
		if !serviceEntryAppliesToDistro(entry, distros) {
			continue
		}
		scope := ScopeSystem
		if entry.EffectiveScope() == "user" {
			scope = ScopeUser
		}

		if entry.IsPackaged() {
			// supervisord can't consume systemd packaged units.
			if !initIsSystemd {
				continue
			}
			out = append(out, &ServicePackagedStep{
				Unit:        ensureServiceSuffix(entry.UsePackaged),
				TargetScope: scope,
				Enable:      entry.Enable,
				CandyName:   layer.Name,
			})
			continue
		}

		// Custom-exec entry. On systemd targets, if a same-name
		// use_packaged sibling exists, the packaged form wins —
		// skip the custom entry entirely (mixed-pair polymorphism).
		if initIsSystemd && namesWithPackaged[entry.Name] {
			continue
		}

		step := &ServiceCustomStep{
			Name:        fmt.Sprintf("charly-%s-%s", layer.Name, entry.Name),
			TargetScope: scope,
			Enable:      entry.Enable,
			CandyName:   layer.Name,
		}

		// On systemd targets, pre-render the unit text now so the
		// executor doesn't need a lazy fallback. On supervisord
		// targets, the supervisord init pipeline renders its own
		// fragment — leave UnitText empty.
		if initIsSystemd && loadSystemd() {
			entryClone := *entry
			entryClone.Name = step.Name
			rendered, rerr := RenderService(&entryClone, systemdDef, renderCtx)
			if rerr == nil && rendered != nil {
				step.UnitText = rendered.UnitText
				step.UnitPath = rendered.UnitPath
			}
		}

		out = append(out, step)
	}
	return out
}

// ensureServiceSuffix adds `.service` if missing. Distinguishes unit
// types: if the caller already included a suffix (e.g. "foo.timer",
// "foo.socket"), leave it alone.
func ensureServiceSuffix(unit string) string {
	if unit == "" {
		return unit
	}
	known := []string{".service", ".timer", ".socket", ".path", ".target", ".mount", ".slice"}
	for _, s := range known {
		if strings.HasSuffix(unit, s) {
			return unit
		}
	}
	return unit + ".service"
}

// ---------------------------------------------------------------------------
// Debug helpers
// ---------------------------------------------------------------------------

// DescribePlan returns a short human-readable summary of a plan. Used by
// --dry-run table output and by TestDescribePlan for golden comparison.
func DescribePlan(p *InstallPlan) string {
	if p == nil {
		return "<nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Plan: candy=%s image=%s distro=%s steps=%d\n",
		p.Candy, p.Box, p.Distro, len(p.Steps))
	counts := map[StepKind]int{}
	for _, s := range p.Steps {
		counts[s.Kind()]++
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		fmt.Fprintf(&b, "  %s: %d\n", k, counts[StepKind(k)])
	}
	return b.String()
}
