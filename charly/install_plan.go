package main

// install_plan.go — the InstallPlan IR.
//
// Background (see plan file referenced in the final design): today's code
// walks Candy objects and emits Containerfile text directly in
// generate.go:writeCandySteps. That hardcodes "we're building an OCI image"
// into the generator. The IR defined here lifts the walk into structured
// data so the same plan can be consumed by:
//
//   - OCITarget        → deploy-mode pod-overlay (add_candy) Containerfile emission (charly bundle add <name>)
//   - ContainerDeploy  → deploy-mode overlay + quadlet (charly bundle add <name>)
//   - the local deploy target → deploy-mode host execution (charly bundle add host)
//
// `charly box build`/`generate` do NOT consume this IR — they emit Containerfile
// text directly via generate.go writeCandySteps→emitTasks. The IR is deploy-only.
//
// Keeping these three code paths behind one shared IR is the load-bearing
// move: every feature (service rendering, add_candy overlay, uninstall
// reversal) now lives in one place and applies to all three targets
// uniformly.
//
// This file defines only types and interfaces — no logic. The compiler that
// turns the candy manifest → InstallPlan lives in install_build.go; the emitters live
// in build_target_oci.go / deploy_target_pod.go / deploy_host_helpers.go.

import (
	"encoding/json"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// HomeToken is the deferred-home placeholder the compiler bakes into
// home-bearing step fields (env.d values, path_append entries, shell-snippet
// destinations) instead of expanding `~`/`$HOME` against a compile-time home.
// Each DeployTarget resolves it at emit time via InstallPlan.ResolveHome with
// the home of the ACTUAL destination — img.Home for the OCI/pod-overlay build,
// the host home for the external local deploy, the GUEST home for the external vm deploy
// (resolved host-side in externalDeployTarget.apply via the guest executor). This
// is what lets a `target: vm` deploy write env.d that points at the guest
// user's home (/home/<guest-user>) rather than the host operator's home.
// The `{{.Home}}` spelling matches the existing builder-artifact convention
// (generate.go:expandBuilderPath), so the two token systems stay aligned.
const HomeToken = "{{.Home}}"

// ---------------------------------------------------------------------------
// Scope — where the effect lands on the target filesystem.
// ---------------------------------------------------------------------------

// Scope is the spec-homed enum (sdk/spec/deploy_wire.go) — aliased here so
// the whole IR keeps spelling it `Scope`/`ScopeSystem`/… unchanged. It lives in
// spec because an out-of-process deploy/step/builder plugin (through the SDK)
// constructs it for a ReverseOp it returns across the process boundary; package
// main and the SDK therefore share ONE type (R3).
type Scope = spec.Scope

const (
	ScopeSystem      = spec.ScopeSystem
	ScopeUser        = spec.ScopeUser
	ScopeUserProfile = spec.ScopeUserProfile
)

// ---------------------------------------------------------------------------
// Venue / Phase / StepKind / Gate — the InstallPlan IR discriminator enums.
// Defined in sdk/spec (ir_enums.go); aliased here so the IR is importable
// without charly core (mirrors the Scope alias above). Internal enums — the
// StepView wire carries them as primitives, so no CUE (Venue/Phase are int
// iota enums cue exp gengotypes cannot express anyway).
// ---------------------------------------------------------------------------

type (
	Venue    = spec.Venue
	Phase    = spec.Phase
	StepKind = spec.StepKind
	Gate     = spec.Gate
)

const (
	VenueHostNative       = spec.VenueHostNative
	VenueContainerBuilder = spec.VenueContainerBuilder
	VenueSkip             = spec.VenueSkip

	PhasePrepare = spec.PhasePrepare
	PhaseInstall = spec.PhaseInstall
	PhaseCleanup = spec.PhaseCleanup

	StepKindSystemPackages  = spec.StepKindSystemPackages
	StepKindBuilder         = spec.StepKindBuilder
	StepKindOp              = spec.StepKindOp
	StepKindFile            = spec.StepKindFile
	StepKindServicePackaged = spec.StepKindServicePackaged
	StepKindServiceCustom   = spec.StepKindServiceCustom
	StepKindShellHook       = spec.StepKindShellHook
	StepKindShellSnippet    = spec.StepKindShellSnippet
	StepKindRepoChange      = spec.StepKindRepoChange
	StepKindApkInstall      = spec.StepKindApkInstall
	StepKindLocalPkgInstall = spec.StepKindLocalPkgInstall
	StepKindReboot          = spec.StepKindReboot
	StepKindExternalPlugin  = spec.StepKindExternalPlugin

	GateNone             = spec.GateNone
	GateAllowRepoChanges = spec.GateAllowRepoChanges
	GateAllowRootTasks   = spec.GateAllowRootTasks
	GateWithServices     = spec.GateWithServices
)

// ---------------------------------------------------------------------------
// ReverseOp — what the ledger records to un-do a step at teardown time.
// ---------------------------------------------------------------------------

// ReverseOpKind + ReverseOp are spec-homed (sdk/spec/deploy_wire.go) and
// aliased here. They live in spec because an out-of-process deploy/step/builder
// plugin (through the SDK) RETURNS ReverseOps across the process boundary for
// the host to record + replay; package main and the SDK share ONE type (R3).
// ReverseOpPluginScript is the generic recordable kind such a plugin returns.
type ReverseOpKind = spec.ReverseOpKind

const (
	ReverseOpPackageRemove  = spec.ReverseOpPackageRemove
	ReverseOpCargoUninstall = spec.ReverseOpCargoUninstall
	ReverseOpNpmUninstallG  = spec.ReverseOpNpmUninstallG
	ReverseOpPixiEnvRemove  = spec.ReverseOpPixiEnvRemove
	ReverseOpRmFileSystem   = spec.ReverseOpRmFileSystem
	ReverseOpRmFileUser     = spec.ReverseOpRmFileUser
	ReverseOpRmDirRecursive = spec.ReverseOpRmDirRecursive
	ReverseOpServiceDisable = spec.ReverseOpServiceDisable
	ReverseOpServiceRemove  = spec.ReverseOpServiceRemove
	ReverseOpRemoveDropin   = spec.ReverseOpRemoveDropin
	ReverseOpRestoreEnabled = spec.ReverseOpRestoreEnabled
	ReverseOpRemoveManaged  = spec.ReverseOpRemoveManaged
	ReverseOpRemoveEnvdFile = spec.ReverseOpRemoveEnvdFile
	ReverseOpRemoveRepoFile = spec.ReverseOpRemoveRepoFile
	ReverseOpCoprDisable    = spec.ReverseOpCoprDisable
	ReverseOpPluginScript   = spec.ReverseOpPluginScript
)

// ReverseOp is the spec-homed teardown action (see ReverseOpKind above).
type ReverseOp = spec.ReverseOp

// ---------------------------------------------------------------------------
// InstallStep — the primary IR element. Each step has one concrete type.
// ---------------------------------------------------------------------------

// InstallStep is the common interface every concrete step implements.
// Consumers (OCITarget / the local deploy target) switch on Kind() to dispatch
// to the right rendering or execution path.
// InstallStep is the polymorphic InstallPlan step interface, homed in sdk/spec
// (with the IR enums it returns + the InstallPlan container that holds
// []InstallStep). The 13 concrete step structs below implement spec.InstallStep
// structurally, so they need no change beyond this alias (P4).
type InstallStep = spec.InstallStep

// externalStep is an EXTERNAL, plugin-CONTRIBUTED install-step KIND (F3, closes C1): a step
// whose Kind() is "external:<word>", carried OPAQUELY (Payload) and whose Scope/Venue/Gate
// come from the serving class:step plugin's DECLARED StepContract (Describe), NOT from a
// compiled-in Go case. It is the generalization ExternalPluginStep is NOT: ExternalPluginStep
// wraps a VERB Op in the ONE fixed "ExternalPlugin" kind with a Go-fixed (advisory) contract;
// externalStep is a first-class per-word kind whose contract the PLUGIN declares — the carrier
// M2 needs to externalize the builtin step kinds (the compiler emits e.g. external:system-packages
// with a package-list Payload). Its host EXECUTION funnels through the SAME OpExecute-to-the-
// serving-plugin path ExternalPluginStep uses (dispatchExternalStepOp — R3); teardown ops are
// DYNAMIC (recorded from the OpExecute reply), so Reverse() returns the recorded slice.
type externalStep struct {
	Word       string          // the reserved step word; Kind() = "external:" + Word
	ScopeV     Scope           // plugin-declared (StepContract.scope)
	VenueV     Venue           // plugin-declared (StepContract.venue)
	GateV      Gate            // plugin-declared (StepContract.gate) — the step SKIPs if the gate is not enabled
	Payload    json.RawMessage // opaque per-kind input — the OpExecute params (plugin_input for an authored step; compiler-built for M2)
	CandyName  string          // owning candy (provenance + the ledger CandyRecord key)
	reverseOps []ReverseOp     // set DYNAMICALLY from the plugin's OpExecute reply (record-and-replay)
}

func (s *externalStep) Kind() StepKind       { return StepKind(externalStepKindPrefix + s.Word) }
func (s *externalStep) Scope() Scope         { return s.ScopeV }
func (s *externalStep) Venue() Venue         { return s.VenueV }
func (s *externalStep) RequiresGate() Gate   { return s.GateV }
func (s *externalStep) Reverse() []ReverseOp { return s.reverseOps }

// externalStepKindPrefix marks a StepKind string as an external (plugin-contributed) kind:
// "external:<word>". isExternalStepKind tests it. The open default arms in the walk
// (kit.WalkPlans) + the host RunHostStep dispatch route by this prefix with NO per-word case.
const externalStepKindPrefix = "external:"

// isExternalStepKind reports whether k is an external (plugin-contributed) step kind.
func isExternalStepKind(k StepKind) bool { return strings.HasPrefix(string(k), externalStepKindPrefix) }

// stepContract is a class:step plugin's DECLARED install-step contract (F3), decoded from its
// Describe capability (pb.StepContract / sdk.StepContract). compileActOp reads it (via the
// stepContractCarrier a provider implements) to build an externalStep carrying the
// plugin-declared Scope/Venue/Gate — the contract the host applies via the open default arm
// with NO compiled-in case.
type stepContract struct {
	Scope Scope
	Venue Venue
	Gate  Gate
	// Emits is the F-STEP-EMIT flag: the step produces a build-context Containerfile
	// FRAGMENT (the serving plugin answers Invoke(OpEmit) → spec.EmitReply.Fragment).
	// The pod-overlay OCITarget consults it via the open external-step arm — Emits=true →
	// bake the fragment; Emits=false → skip (a deploy-only external step, like apk on an
	// image build). Advisory for the DEPLOY leg (executeExternalStep ignores it); load-bearing
	// for the BUILD leg (OCITarget.emitExternalStep).
	Emits bool
}

// stepContractCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:step capability's declared StepContract. A nil/false
// return means the provider declares no step contract (every non-step capability).
type stepContractCarrier interface {
	declaredStepContract() (stepContract, bool)
}

// structuralKindCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:kind capability's STRUCTURAL flag (F5). true → the kind's
// OpLoad returns a spec.Deploy member tree the host folds into uf.Bundle; false (or not
// implemented) → the flat F4 path (opaque body → uf.PluginKinds).
type structuralKindCarrier interface {
	isStructuralKind() bool
}

// validatingKindCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider
// compiled-in) that carries a class:kind capability's VALIDATES flag (F7/C8). true → the host
// dispatches OpValidate to the kind at load (a deep plugin-owned check returning spec.Diagnostics,
// beyond the static CUE input-def gate); false (or not implemented) → only the static gate runs.
type validatingKindCarrier interface {
	isValidatingKind() bool
}

// phaseCarrier is implemented by a provider (grpcProvider out-of-proc, inprocProvider compiled-in)
// that carries its declared lifecycle PHASE (F9). A provider not implementing it (e.g. a builtin
// non-plugin provider) is treated as PhaseRuntime by phaseOfProvider.
type phaseCarrier interface {
	pluginPhase() string
}

// phaseOfProvider returns a provider's lifecycle phase (F9), defaulting to sdk.PhaseRuntime for a
// provider that declares none / is not a phaseCarrier.
func phaseOfProvider(p Provider) string {
	if pc, ok := p.(phaseCarrier); ok {
		if ph := pc.pluginPhase(); ph != "" {
			return ph
		}
	}
	return sdk.PhaseRuntime
}

// scopeFromName maps a declared scope NAME (the author-friendly form a class:step plugin ships
// in its StepContract) to the internal Scope. Unknown / "system" → ScopeSystem (the safe
// default — an external step's scope is advisory for the self-exec'ing plugin, used for ledger
// + batching provenance, not host sudo-wrapping).
func scopeFromName(name string) Scope {
	switch name {
	case "user":
		return ScopeUser
	case "user-profile":
		return ScopeUserProfile
	default:
		return ScopeSystem
	}
}

// ---------------------------------------------------------------------------
// InstallPlan — the top-level IR container.
// ---------------------------------------------------------------------------

// InstallPlan is the full ordered list of steps for one candy or one
// whole-image deploy. Compiled by BuildDeployPlan and consumed by any
// DeployTarget implementation.
//
// The compiler produces one InstallPlan per candy (then merges them in
// topological order for whole-image deploys). A whole-image deploy keeps
// candy boundaries visible so the ledger can refcount which candies
// participate in which deploys — crucial for correct uninstall.
type InstallPlan struct {
	// Identity — populated by the compiler.
	DeployID string // per-deploy unique ID (hash of image + add_candy list)
	Box      string // deployable box name (or candy name for single-candy deploys)
	Version  string // candy/box CalVer version
	Distro   string // resolved host distro tag, e.g. "fedora:43"
	Candy    string // candy name when this plan is for a single candy; "" for whole-image merges

	// The ordered step sequence.
	Steps []InstallStep

	// Provenance — used by teardown and status.
	CandiesIncluded []string          // ordered layer names this plan composes (for whole-image merges)
	AddCandies      []string          // layers added on top via charly.yml add_layers: (for provenance)
	BuilderImage    string            // selected builder image for VenueContainerBuilder steps
	Meta            map[string]string // free-form metadata (builder image, glibc version, …)
}

// wireView projects the rich in-core InstallPlan onto the JSON-roundtrippable
// spec.InstallPlanView the host marshals into an external deploy/step provider's
// op.Params. The Steps interface slice round-trips through the SINGLE stepsToView /
// stepsFromView converter (step_view.go) — an external deploy/step plugin walks the
// same ordered step IR the in-proc DeployTargets walk and EXECUTES it on the venue (R3;
// proven by the step-IR round-trip test). The remaining fields are identity + provenance.
func (p *InstallPlan) wireView() spec.InstallPlanView {
	if p == nil {
		return spec.InstallPlanView{}
	}
	return spec.InstallPlanView{
		DeployID:        p.DeployID,
		Box:             p.Box,
		Version:         p.Version,
		Distro:          p.Distro,
		Candy:           p.Candy,
		CandiesIncluded: p.CandiesIncluded,
		AddCandies:      p.AddCandies,
		BuilderImage:    p.BuilderImage,
		Meta:            p.Meta,
		Steps:           stepsToView(p.Steps),
	}
}

// ResolveHome substitutes the deferred HomeToken with a concrete home in
// every home-bearing step field, in place. Each DeployTarget calls this once
// at emit time with the home of its real destination: img.Home for the
// OCI/pod-overlay build, the host home for the external local deploy, the GUEST home
// (SSH executor ResolveHome) for the external vm deploy. Idempotent — fields without
// the token are left untouched, so a second call is a no-op.
//
// Covered fields: ShellHookStep env values + PathAdd, ShellSnippetStep Snippet
// + Destination + PathAppend, FileStep.Dest. OpStep cmd/content bodies are
// intentionally NOT touched — `~`/`$HOME` there shell-expand at runtime on the
// destination as the deploy user, which is already correct on every venue.
// BuilderStep is also untouched — its home is resolved separately by
// renderBuilderScript against the builder/guest home (see execBuilder).
func (p *InstallPlan) ResolveHome(home string) {
	if p == nil || home == "" {
		return
	}
	sub := func(s string) string { return strings.ReplaceAll(s, HomeToken, home) }
	for _, step := range p.Steps {
		switch s := step.(type) {
		case *ShellHookStep:
			for k, v := range s.EnvVars {
				s.EnvVars[k] = sub(v)
			}
			for i, pth := range s.PathAdd {
				s.PathAdd[i] = sub(pth)
			}
		case *ShellSnippetStep:
			s.Snippet = sub(s.Snippet)
			s.Destination = sub(s.Destination)
			for i, pth := range s.PathAppend {
				s.PathAppend[i] = sub(pth)
			}
		case *FileStep:
			s.Dest = sub(s.Dest)
		case *ServiceCustomStep:
			// The systemd unit is pre-rendered at compile with {{.Home}} for
			// host/vm targets (see compileServiceSteps); resolve it — and the
			// user-scope unit install path — against the destination home here.
			s.UnitText = sub(s.UnitText)
			s.UnitPath = sub(s.UnitPath)
		case *OpStep:
			// Home-relative copy/download dest (tokenized at compile). The
			// Task body itself (cmd/content) is left alone — those shell-expand
			// $HOME at runtime as the deploy user.
			s.To = sub(s.To)
		}
	}
}

// StepsByVenue partitions the plan's steps by (Scope, Venue) tuple while
// preserving intra-partition order. Host target emission uses this to
// batch contiguous same-(scope, venue) runs into one heredoc. Not used
// by the OCI target (it walks Steps directly).
func (p *InstallPlan) StepsByVenue() []StepBatch {
	if len(p.Steps) == 0 {
		return nil
	}
	out := []StepBatch{}
	cur := StepBatch{Scope: p.Steps[0].Scope(), Venue: p.Steps[0].Venue()}
	for _, s := range p.Steps {
		if s.Scope() != cur.Scope || s.Venue() != cur.Venue {
			if len(cur.Steps) > 0 {
				out = append(out, cur)
			}
			cur = StepBatch{Scope: s.Scope(), Venue: s.Venue()}
		}
		cur.Steps = append(cur.Steps, s)
	}
	if len(cur.Steps) > 0 {
		out = append(out, cur)
	}
	return out
}

// StepBatch is a contiguous run of steps sharing the same (Scope, Venue).
// Emitted together: one sudo heredoc, one user heredoc, or one podman run
// per batch.
type StepBatch struct {
	Scope Scope
	Venue Venue
	Steps []InstallStep
}

// ---------------------------------------------------------------------------
// DeployTarget — what the emitters implement.
// ---------------------------------------------------------------------------

// EmitOpts carries cross-cutting toggles passed by command-line flags.
// Gates are checked per-step by the target; target-specific options (the
// container target's registry auth, the host target's --yes, --dry-run)
// are bundled here too.
type EmitOpts struct {
	DryRun               bool
	FormatJSON           bool // print IR as JSON on stdout instead of table
	AllowRepoChanges     bool
	AllowRootTasks       bool
	WithServices         bool
	SkipIncompatible     bool
	AssumeYes            bool // skip sudo preflight, confirmation prompts
	Verify               bool // run layer tests after install
	Pull                 bool // force re-fetch of remote refs / image pull
	BuilderImageOverride string

	// ParentExec is the DeployExecutor of the parent deployment in a
	// nested tree. Non-nil iff this target is dispatched as a child of
	// another — BundleAddCmd's tree walker builds the chain root-first
	// and passes the immediate ancestor's executor here. Targets that
	// support being nested (host, container, vm) compose their own
	// executor over ParentExec via NestedExecutor; leaf-only targets
	// (kubernetes) ignore it and error if non-nil.
	//
	// When nil, the target runs against its natural root venue
	// (ShellExecutor for host, a fresh SSHExecutor for vm, etc.)
	// — preserving the flat-schema behavior for v2 configs that happen
	// to have no `children:`.
	ParentExec DeployExecutor

	// ParentNode is the BundleNode above this target in the tree.
	// Useful for targets that need parent-level context beyond the
	// executor (e.g. a vm child wants to know its parent container's
	// name to wire network forwarding). nil at the root.
	ParentNode *BundleNode

	// Path is the dotted-path identifier of this node (e.g.
	// "stack.web.db"). Used for logging + ledger keying.
	Path string
}

// DeployTarget is the interface OCI + container-deploy + host-deploy
// emitters satisfy. Taking a slice of plans (rather than a single plan)
// lets whole-image deploys pass all per-candy plans at once and let the
// target merge them — useful because OCITarget may want to emit a single
// Containerfile for the image while the local deploy target may batch steps
// across candies.
type DeployTarget interface {
	Name() string
	Emit(plans []*InstallPlan, opts EmitOpts) error
}

// GateEnabled returns whether the given gate is permitted under opts.
// GateNone is always enabled; named gates require the corresponding
// opt-in flag.
func GateEnabled(g Gate, opts EmitOpts) bool {
	switch g {
	case GateNone:
		return true
	case GateAllowRepoChanges:
		return opts.AllowRepoChanges || opts.AssumeYes
	case GateAllowRootTasks:
		return opts.AllowRootTasks || opts.AssumeYes
	case GateWithServices:
		return opts.WithServices || opts.AssumeYes
	}
	return false
}

// ---------------------------------------------------------------------------
// Small helpers used by step types.
// ---------------------------------------------------------------------------

// extractStringSlice returns m[key] as []string or nil if absent.
// Accepts []string and []interface{} (as produced by yaml.v3) inputs.
func extractStringSlice(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		out := make([]string, len(t))
		copy(out, t)
		return out
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
