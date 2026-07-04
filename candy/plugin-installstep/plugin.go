// Package installstep is the importable, DUAL-PLACEMENT charly class:step plugin that serves
// the BUILD-context OpEmit leg for the compiler-emitted builtin InstallStep kinds. Two
// sub-categories, distinguished by whether the OpEmit render needs the host build engine:
//
//   - PURE (C1.1) — file, shell-hook, shell-snippet, service-packaged, service-custom,
//     repo-change, apk-install, reboot. Each render is pure string formatting from the compiler-produced
//     spec.InstallStepView (the SAME serializable view the deploy walk consumes), so the plugin
//     needs no host build engine — it returns the Containerfile fragment directly from OpEmit.
//     apk-install (C1.1) and reboot (C1.6) are the NO-OP-emit members: both declare Emits=false and
//     return an empty fragment (an image build reboots nothing / installs no apk); reboot's real
//     effect is its DEPLOY leg (the host-side guest reboot over RunHostStep), unchanged by this plugin.
//   - HOST-COUPLED (C1.2/C1.3/C1.4/C1.5) — system-packages, builder, local-pkg-install, and op. Their
//     build-context render needs the host build ENGINE (system-packages: the DistroDef format
//     templates + RenderTemplate; builder: the multi-stage buildStageContext + RenderTemplate engine;
//     local-pkg-install: renderLocalPkgImageInstall — buildLocalPkgOnHost + host-dir staging for a
//     dev bed, the release-download RUN for production; op: the RICHEST — Generator.emitTasks, the
//     full per-verb render pipeline with COPY staging + op coalescing), which cannot cross the
//     process boundary, so their OpEmit calls back the host's "step-emit" host-builder over the
//     reverse channel (Executor.HostBuild) and ECHOES the returned spec.EmitReply. The engine stays
//     in core (charly/step_emit_hostbuild.go: stepEmitSystemPackages / stepEmitBuilder /
//     stepEmitLocalPkgInstall / stepEmitOp); the plugin only REQUESTS it.
//
// The DEPLOY leg for ALL these kinds STAYS in sdk/kit.WalkPlans (walkFile / walkShellHook
// / …; system-packages + builder are host-engine kinds driven via RunHostStep →
// renderHostPackageCommand / runVenueBuilderStep; op is the act-OpStep resolveProvisionScript /
// renderOpCommand path), which renders them over the executor reverse channel; this plugin serves
// ONLY OpEmit (the pod-overlay build-emit the host's OCITarget splices).
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
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

const calver = "2026.182.1600"

// opEmit mirrors charly's OpEmit selector ("emit"). This plugin serves ONLY the build-context
// emit leg — every other op is a no-op acknowledgment (the deploy leg is sdk/kit.WalkPlans,
// never this plugin).
const opEmit = "emit"

// stepEmitKind is the host "step-emit" host-builder kind the HOST-COUPLED system-packages word
// requests over the reverse channel (charly/step_emit_hostbuild.go).
const stepEmitKind = "step-emit"

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
	// (an image build reboots nothing), so it declares Emits=false and never reaches emitViaHostBuild.
	wordReboot = "reboot"
	// wordSystemPackages (C1.2), wordBuilder (C1.3), wordLocalPkgInstall (C1.4), and wordOp (C1.5)
	// are HOST-COUPLED: their OpEmit calls back the host build engine over the reverse channel.
	wordSystemPackages  = "system-packages"
	wordBuilder         = "builder"
	wordLocalPkgInstall = "local-pkg-install"
	wordOp              = "op"
)

// hostCoupledStepWords is the set of step words whose OpEmit delegates the fragment render to the
// host "step-emit" host-builder (they cannot format their Containerfile fragment from the step VIEW
// alone). Every other served word is PURE (renderFragment). Kept as a set so adding the next
// host-coupled kind is a one-line change.
var hostCoupledStepWords = map[string]bool{
	wordSystemPackages:  true,
	wordBuilder:         true,
	wordLocalPkgInstall: true,
	wordOp:              true,
}

// NewProvider returns the step provider for in-proc registration or out-of-proc serving.
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta returns the capability/schema describer.
func NewMeta() pb.PluginMetaServer { return &meta{} }

type provider struct{ pb.UnimplementedProviderServer }

// Invoke serves the BUILD-context OpEmit leg: decode the compiler-produced spec.InstallStepView
// (op.Params), render the word's Containerfile fragment, and return it as a spec.EmitReply. Any op
// other than OpEmit is a no-op ack (the deploy leg lives in sdk/kit.WalkPlans, not here).
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != opEmit {
		return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
	}
	// The HOST-COUPLED kinds (system-packages C1.2, builder C1.3, local-pkg-install C1.4, op C1.5)
	// need the host build engine, so instead of formatting a fragment here they call back the host's
	// "step-emit" host-builder and echo the returned spec.EmitReply. The other (PURE) kinds format
	// their fragment directly.
	if hostCoupledStepWords[req.GetReserved()] {
		return emitViaHostBuild(ctx, req)
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
	j, err := json.Marshal(spec.EmitReply{Fragment: frag})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

// emitViaHostBuild serves a HOST-COUPLED step word's OpEmit by delegating the fragment render to the
// host build engine over the reverse channel. It reaches the host executor (in-proc context executor
// when compiled-in, go-plugin broker when served out-of-process — sdk.ExecutorForInvoke), wraps the
// step VIEW (op.Params, verbatim) + the BuildEnv distros (op.Env) into a spec.StepEmitRequest, and
// calls Executor.HostBuild("step-emit", …). The host-builder returns a spec.EmitReply JSON, which
// this ECHOES as the Invoke result (the SAME reply shape a PURE word returns). The host maps the
// step word to its in-core renderer (charly/step_emit_hostbuild.go: stepEmitters).
func emitViaHostBuild(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, req.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("plugin-installstep: reach host reverse channel for %q: %w", req.GetReserved(), err)
	}
	var env spec.BuildEnv
	if len(req.GetEnvJson()) > 0 {
		if err := json.Unmarshal(req.GetEnvJson(), &env); err != nil {
			return nil, fmt.Errorf("plugin-installstep: decode build env for %q: %w", req.GetReserved(), err)
		}
	}
	sreq, err := json.Marshal(spec.StepEmitRequest{
		Word:    req.GetReserved(),
		Payload: req.GetParamsJson(),
		Distros: env.Distros,
	})
	if err != nil {
		return nil, err
	}
	reply, err := exec.HostBuild(ctx, stepEmitKind, sreq)
	if err != nil {
		return nil, fmt.Errorf("plugin-installstep: host step-emit for %q: %w", req.GetReserved(), err)
	}
	return &pb.InvokeReply{ResultJson: reply}, nil
}

// renderFragment dispatches by step word to the pure per-kind Containerfile renderer. Each render
// reproduces the former OCITarget.emit<Kind> body verbatim, reading the SAME fields off the view.
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

type meta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe advertises the class:step capabilities, each with its declared StepContract. Only Emits
// is load-bearing here: the host's pod-overlay OCITarget consults it to decide whether to Invoke
// OpEmit (true) or skip (false, apk-install / reboot). Scope/Venue/Gate are nominal — these kinds' deploy leg
// is sdk/kit.WalkPlans, which reads the per-instance view.Scope/Venue computed on the
// concrete step, so the static contract's Scope/Venue/Gate are never consulted. The HOST-COUPLED
// system-packages (C1.2) + builder (C1.3) + local-pkg-install (C1.4) + op (C1.5) Emits=true too —
// their OpEmit delegates to the host "step-emit" host-builder.
func (meta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) {
	emit := func(word string, emits bool) sdk.ProvidedCapability {
		return sdk.ProvidedCapability{
			Class:        "step",
			Word:         word,
			StepContract: &sdk.StepContract{Scope: "system", Venue: 0, Gate: "", Emits: emits},
		}
	}
	return sdk.BuildCapabilities(calver,
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
		schemaFS, "schema")
}
