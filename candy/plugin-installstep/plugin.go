// Package installstep is the importable, DUAL-PLACEMENT charly class:step plugin that serves
// the BUILD-context OpEmit leg for the compiler-emitted builtin InstallStep kinds. Two
// sub-categories, distinguished by whether the OpEmit render needs the resolved-project structure:
//
//   - PURE (C1.1) — file, shell-hook, shell-snippet, service-packaged, service-custom,
//     repo-change, apk-install, reboot. Each render is pure string formatting from the compiler-produced
//     spec.InstallStepView (the SAME serializable view the deploy walk consumes), so the plugin
//     needs no project structure — it returns the Containerfile fragment directly from OpEmit.
//     apk-install (C1.1) and reboot (C1.6) are the NO-OP-emit members: both declare Emits=false and
//     return an empty fragment (an image build reboots nothing / installs no apk); reboot's real
//     effect is its DEPLOY leg (the host-side guest reboot over RunHostStep), unchanged by this plugin.
//   - HOST-COUPLED (C1.2/C1.3/C1.4/C1.5) — system-packages, builder, local-pkg-install, and op. Their
//     build-context render needs the box/candy/distro/builder STRUCTURE a bare InstallStepView cannot
//     carry (system-packages: the box's resolved DistroDef; builder: the box's BuilderConfig + the
//     candy's detected manifest/lockfile/build-script + ExternalizedBuilders; local-pkg-install: none
//     — a pure function of the step + a few build-context SCALARS; op: the candy's plan.Ops rendered
//     through the SAME task-emission pipeline WriteCandySteps drives). Rather than calling back a
//     host-side renderer, this plugin fetches the "resolved-project" envelope ONCE per project dir
//     (HostBuild("resolved-project"), cached — the SAME generic seam candy/plugin-box/plugin-bundle/
//     plugin-check already consume) and constructs its OWN *deploykit.Generator from it via the
//     SHARED deploykit.NewRenderGeneratorFromProject helper — the identical construction source
//     candy/plugin-build (the box-build render) and candy/plugin-deploy-pod (the overlay render) use
//     (R3/DRY) — then computes each fragment DIRECTLY (dg.EmitTasks / dg.BuildStageContext /
//     kit.BuilderResolve / deploykit.RenderLocalPkgImageInstall), no per-render host round-trip. The
//     few per-invocation SCALARS a resolved-project snapshot cannot carry (which box this render is
//     for, whether this is a dev-bed build, and the build-context-relative dir an Op step's inline
//     content stages under) ride the SAME OpEmit Invoke's spec.BuildEnv (op.Env) every word already
//     receives — Image/DevLocalPkg/ImageBuildDir/ContextRelPrefix — so there is exactly ONE host
//     round-trip per HOST-COUPLED word's OpEmit: HostBuild("resolved-project"), and only once per
//     project dir (cached across every step of the SAME build).
//
// The DEPLOY leg for ALL these kinds STAYS in sdk/kit.WalkPlans (walkFile / walkShellHook
// / …; system-packages + builder are host-engine kinds driven via RunHostStep →
// deploykit.RenderHostPackageCommand / runVenueBuilderStep; op is the act-OpStep resolveProvisionScript /
// renderOpCommand path), which renders them over the executor reverse channel; this plugin serves
// ONLY OpEmit (the pod-overlay build-emit the host's deploykit.OCITarget splices).
//
// Placement is free: charly COMPILES this candy IN (listed in charly.yml compiled_plugins:,
// registered in-process via registerCompiledPlugin), and the SAME provider serves OUT-OF-PROCESS
// over go-plugin gRPC via the cmd/serve shim — zero authoring change either way.
//
// The OpEmit payload is a spec.InstallStepView; there is NO authored plugin_input (these steps are
// compiler-emitted from declarative candy fields, never authored as a `plugin:` step), so no
// capability declares an InputDef and the shipped CUE schema is vestigial (present only to satisfy
// the plugin load gate).
package installstep

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.203.0900"

// opEmit mirrors charly's OpEmit selector ("emit"). This plugin serves ONLY the build-context
// emit leg — every other op is a no-op acknowledgment (the deploy leg is sdk/kit.WalkPlans,
// never this plugin).
const opEmit = "emit"

// The step words this plugin serves. The word is the lowercase-hyphenated reserved name; the host
// maps each InstallStep kind to its word in pluginEmitStepWords (charly/provider_step.go).
const (
	wordFile            = "file"
	wordShellHook       = "shell-hook"
	wordShellSnippet    = "shell-snippet"
	wordServicePackaged = "service-packaged"
	wordServiceCustom   = "service-custom"
	wordRepoChange      = "repo-change"
	wordApkInstall      = "apk-install"
	// wordReboot (C1.6) is PURE + NO-OP-emit like apk-install: its build fragment is empty
	// (an image build reboots nothing), so it declares Emits=false and never reaches emitHostCoupled.
	wordReboot = "reboot"
	// wordSystemPackages (C1.2), wordBuilder (C1.3), wordLocalPkgInstall (C1.4), and wordOp (C1.5)
	// are HOST-COUPLED: their OpEmit renders directly against the resolved-project envelope (or,
	// for local-pkg-install, purely off the BuildEnv scalars).
	wordSystemPackages  = "system-packages"
	wordBuilder         = "builder"
	wordLocalPkgInstall = "local-pkg-install"
	wordOp              = "op"
)

// hostCoupledStepWords is the set of step words whose OpEmit needs more than the bare step VIEW to
// render (project/box/candy structure, or per-invocation build-context scalars). Every other served
// word is PURE (renderFragment). Kept as a set so adding the next host-coupled kind is a one-line
// change.
var hostCoupledStepWords = map[string]bool{
	wordSystemPackages:  true,
	wordBuilder:         true,
	wordLocalPkgInstall: true,
	wordOp:              true,
}

// NewProvider returns the step provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the class:step capabilities, each with its declared StepContract, via
// sdk.NewMeta → BuildCapabilities. Only Emits is load-bearing here: the host's pod-overlay
// deploykit.OCITarget consults it to decide whether to Invoke OpEmit (true) or skip (false, apk-install /
// reboot). Scope/Venue/Gate are nominal — these kinds' deploy leg is sdk/kit.WalkPlans, which
// reads the per-instance view.Scope/Venue computed on the concrete step, so the static contract's
// Scope/Venue/Gate are never consulted. The HOST-COUPLED system-packages (C1.2) + builder (C1.3) +
// local-pkg-install (C1.4) + op (C1.5) Emits=true too — their OpEmit renders directly against the
// resolved-project envelope.
func NewMeta() pb.PluginMetaServer {
	emit := func(word string, emits bool) sdk.ProvidedCapability {
		return sdk.ProvidedCapability{
			Class:        "step",
			Word:         word,
			StepContract: &sdk.StepContract{Scope: "system", Venue: 0, Gate: "", Emits: emits},
		}
	}
	return sdk.NewMeta(calver,
		[]sdk.ProvidedCapability{
			emit(wordFile, true),
			emit(wordShellHook, true),
			emit(wordShellSnippet, true),
			emit(wordServicePackaged, true),
			emit(wordServiceCustom, true),
			emit(wordRepoChange, true),
			emit(wordApkInstall, false),
			emit(wordReboot, false),
			emit(wordSystemPackages, true),
			emit(wordBuilder, true),
			emit(wordLocalPkgInstall, true),
			emit(wordOp, true),
		},
		schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke serves the BUILD-context OpEmit leg: decode the compiler-produced spec.InstallStepView
// (op.Params), render the word's Containerfile fragment, and return it as a spec.EmitReply. Any op
// other than OpEmit is a no-op ack (the deploy leg lives in sdk/kit.WalkPlans, not here).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != opEmit {
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	}
	// The HOST-COUPLED kinds (system-packages C1.2, builder C1.3, local-pkg-install C1.4, op C1.5)
	// render directly against the resolved-project envelope (or, for local-pkg-install, purely off
	// the BuildEnv scalars) instead of formatting from the bare step view. The other (PURE) kinds
	// format their fragment directly.
	if hostCoupledStepWords[req.GetReserved()] {
		return emitHostCoupled(ctx, req)
	}
	var view spec.InstallStepView
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &view); err != nil {
			return nil, fmt.Errorf("plugin-installstep: decode InstallStepView for %q: %w", req.GetReserved(), err)
		}
	}
	frag, err := renderFragment(req.GetReserved(), view)
	if err != nil {
		return nil, err
	}
	return replyFragment(frag)
}

// replyFragment marshals a rendered Containerfile fragment into the spec.EmitReply wire shape every
// OpEmit path (pure or host-coupled) returns.
func replyFragment(frag string) (*pb.InvokeReply, error) {
	j, err := json.Marshal(spec.EmitReply{Fragment: frag})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

// genCache holds the *deploykit.Generator built from the "resolved-project" envelope, keyed by
// project dir — fetched + constructed ONCE per project dir (typically once per process lifetime:
// a compiled-in placement shares the host's process for one charly invocation; an out-of-process
// placement shares its own subprocess for the same), then reused across every HOST-COUPLED step
// this OpEmit renders for the SAME build.
var genCache sync.Map // string (dir) -> *deploykit.Generator

// getGenerator returns the cached *deploykit.Generator for dir, fetching + constructing it on a
// cache miss: HostBuild("resolved-project") (the SAME generic envelope seam candy/plugin-box /
// candy/plugin-bundle / candy/plugin-check already consume) → deploykit.NewRenderGeneratorFromProject
// (the SAME shared construction source candy/plugin-build + candy/plugin-deploy-pod use, R3/DRY).
func getGenerator(ctx context.Context, exec *sdk.Executor, dir string, devLocalPkg bool) (*deploykit.Generator, error) {
	if cached, ok := genCache.Load(dir); ok {
		return cached.(*deploykit.Generator), nil
	}
	reqJSON, err := json.Marshal(spec.ResolvedProjectRequest{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("marshal resolved-project request: %w", err)
	}
	resJSON, err := exec.HostBuild(ctx, "resolved-project", reqJSON)
	if err != nil {
		return nil, fmt.Errorf("host resolved-project: %w", err)
	}
	var rp spec.ResolvedProject
	if err := json.Unmarshal(resJSON, &rp); err != nil {
		return nil, fmt.Errorf("decode resolved-project envelope: %w", err)
	}
	dg, err := deploykit.NewRenderGeneratorFromProject(ctx, exec, &rp, dir, devLocalPkg)
	if err != nil {
		return nil, fmt.Errorf("construct render generator: %w", err)
	}
	// A concurrent cache miss for the SAME dir constructs two Generators; the second store wins
	// harmlessly (both are equivalent snapshots of the same resolved-project envelope).
	genCache.Store(dir, dg)
	return dg, nil
}

// candyByName resolves a candy by its INTRINSIC bare name against dg.Candies — the plugin-side
// mirror of charly's own candyByName (charly/generate.go): a LOCAL candy is keyed bare == Name, so
// the direct lookup hits; a REMOTE candy (e.g. a deploy's add_candy: pulled via ExtraCandyRefs) is
// keyed under its fully-qualified ref, so the direct bare lookup MISSES and this falls back to
// matching the candy's own Name.
func candyByName(dg *deploykit.Generator, name string) deploykit.CandyModel {
	if dg == nil {
		return nil
	}
	if c, ok := dg.Candies[name]; ok && c != nil {
		return c
	}
	for _, c := range dg.Candies {
		if c != nil && c.GetName() == name {
			return c
		}
	}
	return nil
}

// emitHostCoupled serves a HOST-COUPLED step word's OpEmit: decode the step view + the BuildEnv
// scalars, then render the fragment DIRECTLY — local-pkg-install purely from the BuildEnv scalars
// (deploykit.RenderLocalPkgImageInstall needs no project structure at all); system-packages/builder/op
// against the cached "resolved-project"-built *deploykit.Generator.
func emitHostCoupled(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var env spec.BuildEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return nil, fmt.Errorf("plugin-installstep: decode build env for %q: %w", req.GetReserved(), err)
		}
	}
	var view spec.InstallStepView
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &view); err != nil {
			return nil, fmt.Errorf("plugin-installstep: decode InstallStepView for %q: %w", req.GetReserved(), err)
		}
	}

	if req.GetReserved() == wordLocalPkgInstall {
		frag, err := emitLocalPkgInstall(view, env)
		if err != nil {
			return nil, err
		}
		return replyFragment(frag)
	}

	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-installstep: reach host reverse channel for %q: %w", req.GetReserved(), err)
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("plugin-installstep: resolve project dir: %w", err)
	}
	dg, err := getGenerator(ctx, exec, dir, env.DevLocalPkg)
	if err != nil {
		return nil, fmt.Errorf("plugin-installstep: %q: %w", req.GetReserved(), err)
	}

	var frag string
	switch req.GetReserved() {
	case wordSystemPackages:
		frag, err = emitSystemPackages(dg, view, env)
	case wordBuilder:
		frag, err = emitBuilder(dg, view, env)
	case wordOp:
		frag, err = emitOp(dg, view, env)
	default:
		return nil, fmt.Errorf("plugin-installstep: unhandled host-coupled step word %q", req.GetReserved())
	}
	if err != nil {
		return nil, err
	}
	return replyFragment(frag)
}

// emitSystemPackages renders the SystemPackages InstallStep's BUILD-context (container-venue)
// Containerfile fragment: reconstruct the concrete step from the wire view, resolve the box's
// DistroDef from the resolved-project envelope (nil-safe — buildkit.WrapDistroDef/FindFormat
// tolerate a nil def, producing the SAME "no distro definition" error the former in-core render
// did for a synthetic/no-box path), and render the format's phase.install.container template via
// the SAME sdk/buildkit render buildkit.RenderSystemPackagesFragment the box-build path shares.
func emitSystemPackages(dg *deploykit.Generator, view spec.InstallStepView, env spec.BuildEnv) (string, error) {
	step, err := deploykit.StepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*deploykit.SystemPackagesStep)
	if !ok {
		return "", fmt.Errorf("plugin-installstep: view kind %q is not a SystemPackagesStep", view.Kind)
	}
	var distroDef *spec.ResolvedDistro
	if img := dg.Boxes[env.Image]; img != nil {
		distroDef = img.DistroDef
	}
	return buildkit.RenderSystemPackagesFragment(s.Format, s.Phase, s.RawInstallContext, buildkit.WrapDistroDef(distroDef))
}

// emitBuilder renders the Builder InstallStep's BUILD-context (container-venue) Containerfile
// fragment: reconstruct the concrete step from the wire view, resolve the box + its BuilderConfig +
// the candy from the resolved-project envelope, and render via the SAME kit.BuilderResolve (for an
// EXTERNALIZED detection builder — pixi/npm/aur/cargo) or buildkit.RenderTemplate (a project-custom,
// non-externalized inline builder) the box-build path + the former in-core render both used. A
// missing box/BuilderConfig/candy yields the SAME informative skip-comment the former in-core
// render produced (synthetic paths); an undefined builder or a template error is a LOUD failure
// (never a silent empty bake, R4).
func emitBuilder(dg *deploykit.Generator, view spec.InstallStepView, env spec.BuildEnv) (string, error) {
	step, err := deploykit.StepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*deploykit.BuilderStep)
	if !ok {
		return "", fmt.Errorf("plugin-installstep: view kind %q is not a BuilderStep", view.Kind)
	}

	img := dg.Boxes[env.Image]
	if img == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — skipped, no Image context\n",
			s.Builder, s.CandyName), nil
	}
	if img.BuilderConfig == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — skipped, no BuilderConfig\n",
			s.Builder, s.CandyName), nil
	}
	bDef, ok := img.BuilderConfig.Builder[s.Builder]
	if !ok || bDef == nil {
		return "", fmt.Errorf("builder %q: not defined in BuilderConfig", s.Builder)
	}

	layer := candyByName(dg, s.CandyName)
	if layer == nil {
		return fmt.Sprintf("# Builder: %s (layer=%s) — layer not found in scan\n",
			s.Builder, s.CandyName), nil
	}

	// Inline builders (cargo): render the in-candy RUN with the builder's inline context; no
	// separate FROM stage. Switch USER to the image user for the inline builder steps. An
	// EXTERNALIZED inline builder (cargo) renders via kit.BuilderResolve (the SAME render the
	// box-build path and the builder plugin's own OpResolve use, R3); a custom one via its
	// vocabulary install_template.
	if bDef.Inline {
		ctx := &spec.BuildStageContext{
			LayerStage:  layer.GetName(),
			UID:         img.UID,
			GID:         img.GID,
			CacheMounts: bDef.CacheMount,
		}
		if dg.ExternalizedBuilders[s.Builder] {
			reply, err := kit.BuilderResolve(s.Builder, deploykit.BuilderResolveInputFrom(layer.GetName(), s.Builder, bDef, ctx))
			if err != nil {
				return "", fmt.Errorf("inline builder %s: %w", s.Builder, err)
			}
			return fmt.Sprintf("USER %d\n", img.UID) + reply.InlineFragment, nil
		}
		rendered, err := buildkit.RenderTemplate(s.Builder+"-inline", bDef.InstallTemplate, ctx)
		if err != nil {
			return "", fmt.Errorf("inline builder %s: %w", s.Builder, err)
		}
		return fmt.Sprintf("USER %d\n", img.UID) + rendered, nil
	}

	// Multi-stage builders (pixi/npm/aur): emit the stage via the Generator's BuildStageContext
	// helper. Only externalized (plugin) builders have a multi-stage; a custom builder must be an
	// external_builder plugin.
	builderRef := ""
	if img.Builder != nil {
		builderRef = img.Builder[s.Builder]
	}
	ctx := dg.BuildStageContext(layer, s.Builder, bDef, img, builderRef)
	if ctx == nil {
		return "", fmt.Errorf("buildStageContext returned nil for %s", s.Builder)
	}
	if !dg.ExternalizedBuilders[s.Builder] {
		return "", fmt.Errorf("multi-stage builder %s is not an externalized plugin builder (a custom builder must be an external_builder plugin)", s.Builder)
	}
	reply, err := kit.BuilderResolve(s.Builder, deploykit.BuilderResolveInputFrom(layer.GetName(), s.Builder, bDef, ctx))
	if err != nil {
		return "", fmt.Errorf("multi-stage builder %s: %w", s.Builder, err)
	}
	return reply.Stage, nil
}

// emitLocalPkgInstall renders the LocalPkgInstall InstallStep's BUILD-context Containerfile
// fragment: reconstruct the concrete step from the wire view, then call
// deploykit.RenderLocalPkgImageInstall — a PURE function of its step argument (no project
// structure needed at all): a PRODUCTION box DOWNLOADS the published release, a DISPOSABLE bed
// BUILDS the in-development package and COPYs it in (env.DevLocalPkg); a distro with no
// localpkg-capable format (LocalPkg==nil) renders nothing. The overlay/deploy path never sets
// DevLocalPkg, so the pod-overlay build-emit takes the production leg.
func emitLocalPkgInstall(view spec.InstallStepView, env spec.BuildEnv) (string, error) {
	step, err := deploykit.StepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*deploykit.LocalPkgInstallStep)
	if !ok {
		return "", fmt.Errorf("plugin-installstep: view kind %q is not a LocalPkgInstallStep", view.Kind)
	}
	return deploykit.RenderLocalPkgImageInstall(s, env.DevLocalPkg, env.ImageBuildDir, env.Image)
}

// emitOp renders the Op InstallStep's BUILD-context Containerfile fragment: reconstruct the
// *OpStep from the wire view, look the candy up by its bare name (candyByName — nil-safe, with the
// remote qualified-key add_candy fallback), and drive the SAME Generator.EmitTasks the box build
// (WriteCandySteps) uses, for the ONE op the step carries. A synthetic path without a resolved box
// yields the SAME informative comment the former in-core render produced; a candy the scan never
// saw is a LOUD error (never a silent empty bake, R4).
func emitOp(dg *deploykit.Generator, view spec.InstallStepView, env spec.BuildEnv) (string, error) {
	step, err := deploykit.StepFromView(view)
	if err != nil {
		return "", err
	}
	s, ok := step.(*deploykit.OpStep)
	if !ok {
		return "", fmt.Errorf("plugin-installstep: view kind %q is not an OpStep", view.Kind)
	}
	img := dg.Boxes[env.Image]
	if img == nil {
		kind, _ := s.Op.Kind()
		return fmt.Sprintf("# Task: %s (layer=%s) — no Generator context\n", kind, s.CandyName), nil
	}
	layer := candyByName(dg, s.CandyName)
	if layer == nil {
		return "", fmt.Errorf("task emit: candy %q not found", s.CandyName)
	}
	var b strings.Builder
	if _, err := dg.EmitTasks(&b, layer, img, []spec.Op{*s.Op}, env.ImageBuildDir, env.ContextRelPrefix); err != nil {
		return "", err
	}
	return b.String(), nil
}

// renderFragment dispatches by step word to the pure per-kind Containerfile renderer. Each render
// reproduces the former deploykit.OCITarget.emit<Kind> body verbatim, reading the SAME fields off the view.
func renderFragment(word string, v spec.InstallStepView) (string, error) {
	switch word {
	case wordFile:
		return renderFile(v), nil
	case wordShellHook:
		return renderShellHook(v), nil
	case wordShellSnippet:
		return renderShellSnippet(v), nil
	case wordServicePackaged:
		return renderServicePackaged(v), nil
	case wordServiceCustom:
		return renderServiceCustom(v), nil
	case wordRepoChange:
		return renderRepoChange(v), nil
	case wordApkInstall:
		// apk-install declares Emits=false, so the host never invokes OpEmit for it (no device at
		// image-build time; the android deploy preresolver reads the step at deploy). Kept for
		// completeness — returns an empty fragment.
		return "", nil
	case wordReboot:
		// reboot (C1.6) declares Emits=false, so the host never invokes OpEmit for it (an image
		// build reboots nothing). Its real effect is the DEPLOY leg: the host reboots a charly-owned
		// VM guest + waits for the boot_id change over RunHostStep (rebootVenueAndWait), unchanged by
		// this plugin. Kept for completeness — returns an empty fragment (mirrors apk-install).
		return "", nil
	default:
		return "", fmt.Errorf("plugin-installstep: unknown step word %q", word)
	}
}

// renderFile emits a file placement as COPY --chmod/--chown from the file's scratch-stage source.
func renderFile(v spec.InstallStepView) string {
	chmod := fmt.Sprintf("%04o", v.Mode&0o777)
	chown := ""
	if v.Owner != "" && v.Owner != "root" && v.Owner != "0" {
		chown = fmt.Sprintf(" --chown=%s", v.Owner)
	}
	return fmt.Sprintf("COPY --chmod=%s%s %s %s\n", chmod, chown, v.Source, v.Dest)
}

// renderShellHook emits `env:` and `path_append:` as ENV directives. Env keys are emitted in sorted
// order (deterministic — matching the box-build emitVarsEnv path).
func renderShellHook(v spec.InstallStepView) string {
	var b strings.Builder
	keys := make([]string, 0, len(v.EnvVars))
	for k := range v.EnvVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "ENV %s=%q\n", k, v.EnvVars[k])
	}
	if len(v.PathAdd) > 0 {
		// Prepend the additions (earlier-listed entries end up leftmost on the final PATH).
		parts := make([]string, 0, len(v.PathAdd)+1)
		parts = append(parts, v.PathAdd...)
		parts = append(parts, "$PATH")
		fmt.Fprintf(&b, "ENV PATH=%s\n", strings.Join(parts, ":"))
	}
	return b.String()
}

// renderShellSnippet renders a candy's per-shell init snippet into the container's system-wide
// drop-in directory via a heredoc with a snippet-hash-derived end-marker (anti-collision).
func renderShellSnippet(v spec.InstallStepView) string {
	if v.Snippet == "" {
		return ""
	}
	h := sha256.Sum256([]byte(v.Snippet))
	marker := fmt.Sprintf("CHARLY_SHELL_%s_%x", strings.ToUpper(v.Shell), h[:4])
	return fmt.Sprintf(
		"RUN mkdir -p %s && cat > %s <<'%s'\n%s\n%s\n",
		kit.ShellQuote(filepath.Dir(v.Destination)),
		kit.ShellQuote(v.Destination),
		marker,
		v.Snippet,
		marker,
	)
}

// renderServicePackaged renders an "enable packaged systemd unit" step: the optional drop-in as a
// heredoc file write, plus an enable marker comment (the packaged unit was installed by its package).
func renderServicePackaged(v spec.InstallStepView) string {
	var b strings.Builder
	if v.OverridesText != "" && v.OverridesPath != "" {
		fmt.Fprintf(&b, "RUN mkdir -p $(dirname %s) && cat > %s <<'CHARLY_DROPIN'\n%s\nCHARLY_DROPIN\n",
			v.OverridesPath, v.OverridesPath, v.OverridesText)
	}
	if v.Enable {
		scope := "system"
		if v.TargetScope == spec.ScopeUser {
			scope = "user"
		}
		fmt.Fprintf(&b, "# Service: enable packaged unit %s (scope=%s, layer=%s)\n",
			v.Unit, scope, v.CandyName)
	}
	return b.String()
}

// renderServiceCustom emits the custom-service marker; the rendered unit content travels via the
// init-fragment pipeline, not this build-emit.
func renderServiceCustom(v spec.InstallStepView) string {
	if v.UnitText == "" {
		return ""
	}
	return fmt.Sprintf("# Service: custom %s (layer=%s)\n# -- unit content follows in the init fragment pipeline --\n",
		v.Name, v.CandyName)
}

// renderRepoChange emits a structured repo file write.
func renderRepoChange(v spec.InstallStepView) string {
	return fmt.Sprintf("RUN mkdir -p $(dirname %s) && cat > %s <<'CHARLY_REPO'\n%s\nCHARLY_REPO\n",
		v.File, v.File, v.Content)
}
