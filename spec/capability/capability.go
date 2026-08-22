// Package capability (github.com/opencharly/spec/capability, #55 import-purity) holds the
// plugin capability DESCRIPTOR contract: the ProvidedCapability struct (one capability a
// plugin serves plus the CUE def that validates its plugin_input — the SDK-facing form of
// the proto ProvidedCapability), its StepContract (a class="step" plugin's declared
// install-step Scope/Venue/Gate/Emits), and CLISubcommand (the plain Name+Help authoring
// form of a class="command" capability's declared CLI child).
//
// It lives in spec — not the sdk root — so that charly core (the host) and every plugin
// (compiled-in or out-of-process) share ONE descriptor contract without either importing the
// other, and WITHOUT dragging cuelang/grpc/go-plugin into the descriptor slice (Rule 2:
// each heavy third-party dep lives only in the slice that needs it). The descriptor types
// are plain Go structs whose only non-stdlib reference is spec/spec (DeployTraits, CLIModel
// — themselves stdlib-only); spec/capability is a LEAF (nothing imports it back → no cycle).
//
// CLISubcommand was co-located with the cuelang-bearing ValidateGenerated helper in
// spec/climodel; it is relocated HERE so spec/capability stays cuelang-free while
// spec/climodel keeps cuelang ONLY for ValidateGenerated. spec/climodel re-exports
// CLISubcommand (`type CLISubcommand = capability.CLISubcommand`) so its existing consumers
// (sdk/schema.go's CLISubcommand alias, sdk/kong_reflect.go's KongSubcommands, and
// spec/climodel's own TestCLISubcommandFields) compile UNCHANGED. ProvidedCapability +
// StepContract relocate verbatim from github.com/opencharly/sdk/schema.go; the sdk root
// re-exports them (`type ProvidedCapability = capability.ProvidedCapability`,
// `type StepContract = capability.StepContract`) so the 92 plugin candy call sites
// compile UNCHANGED. The proto wire forms (pb.ProvidedCapability / pb.StepContract /
// pb.CLISubcommand) live in spec/proto and are NOT affected — BuildCapabilities (in the sdk
// root) marshals these authoring structs into the proto.
package capability

import (
	"github.com/opencharly/spec/spec"
)

// ProvidedCapability is one capability a plugin serves plus the CUE def that
// validates its plugin_input — the SDK-facing form of the proto ProvidedCapability.
// An external plugin lists these in its Describe; the host validates authored
// plugin_input for each word against its def in the served schema.
type ProvidedCapability struct {
	Class    string // "verb" / "kind" / "deploy" / "step" / "builder"
	Word     string // the reserved word, e.g. "externalprobe"
	InputDef string // the CUE def for this word's plugin_input, e.g. "#ExternalprobeInput"
	// StepContract is set ONLY for Class=="step" (F3): the plugin-declared install-step
	// contract (Scope/Venue/Gate) the host applies to the external step via the open default
	// arm — no compiled-in case. nil for every other class.
	StepContract *StepContract
	// Structural is set ONLY for Class=="kind" (F5): the kind decodes a STRUCTURAL entity —
	// its OpLoad returns a spec.Deploy member tree the host folds into uf.Fleet — rather than
	// a FLAT body landed opaquely in uf.PluginKinds (F4). false for every other class/kind.
	Structural bool
	// Lifecycle is set ONLY for Class=="deploy" (F6): the substrate brings its OWN host-side
	// venue lifecycle (PrepareVenue/Start/Stop/Status/Rebuild/...) served over the lifecycle Ops,
	// so the host registers a wire-backed substrateLifecycle for it. false for every other
	// class/deploy (local/android/kubernetes keep the generic host-venue behaviour).
	Lifecycle bool
	// Preresolve is set ONLY for Class=="deploy" (F6): the substrate declares a host-side
	// PRERESOLVE step (OpPreresolve) the host runs before apply, shipping the opaque result in
	// DeployVenue.Substrate — the wire-backed generalization of the in-core kubernetes/android
	// preresolvers. false for every other class/deploy.
	Preresolve bool
	// Validates is set ONLY for Class=="kind" (F7/C8): the kind serves a deep OpValidate check
	// (returns spec.Diagnostics) the host dispatches at load, BEYOND the static CUE input-def
	// gate. false → only the static gate runs (every other class/kind).
	Validates bool
	// Phase is the plugin lifecycle PHASE (F9): one of the sdk.Phase* constants. "" → the kernel
	// treats it as PhaseRuntime (the default). PhaseBootstrap runs BEFORE config validation —
	// declare it for a capability that must load/run early (migrate, egress). The kernel loads +
	// invokes plugins in PhaseOrder.
	Phase string
	// Primary is set ONLY for Class=="verb": the input field the scalar sugar
	// shorthand targets (`file: /x` → plugin_input: {<Primary>: "/x"}). "" → the
	// verb takes a map input only. The host registers it into the parse-time
	// desugar's primary registry (compiled-in at init; an EXTERNAL plugin
	// additionally declares it in its candy manifest's plugin.primary map so the
	// byte-gated prescan knows it BEFORE the provider connects).
	Primary string
	// DeployTraits is set ONLY for Class=="kind" on a SUBSTRATE kind (P9): the kind's
	// DECLARED deploy behaviour (venue + image_backed/image_context/machine_venue/
	// exclusive_venue/leaf_only). kit.StampDescent stamps it onto every node's
	// spec.DescentDescriptor so the kernel consults the substrate behaviour BY TRAIT
	// (off node.Descent) — never by switching on the kind word. nil for every other
	// capability (the zero-value → external-in-place semantics).
	DeployTraits *spec.DeployTraits
	// Subcommands is set ONLY for Class=="command" (F-CLI-NEST): the plugin's DECLARED
	// one-level-deep CLI subcommand catalog (name+help). The host uses it to build a REAL
	// nested Kong grammar — a named `cmd:""` child per entry, restoring `--help` fidelity
	// and CLI-model (MCP) leaf discoverability — in place of the opaque `[<args>...]`
	// pass-through holder every command-class capability otherwise gets. Empty (the
	// default) preserves today's flat pass-through behavior byte-for-byte; use
	// KongSubcommands to derive the catalog from an existing Kong-tagged struct instead of
	// hand-duplicating it.
	Subcommands []CLISubcommand
	// CommandModel is set ONLY for Class=="command". Its generated #CLIModel
	// describes the plugin-owned leaf grammar for host and MCP reflection.
	CommandModel *spec.CLIModel
}

// CLISubcommand is one DECLARED child of a class="command" capability's own CLI word — the
// SDK-facing authoring form (a Name+Help pair). The proto wire form is pb.CLISubcommand
// (in spec/proto); this struct is the authoring shape a plugin constructs in its
// ProvidedCapability.Subcommands list, which BuildCapabilities marshals into the proto.
//
// Relocated from spec/climodel (#55 import-purity, Rule 2): the plain authoring struct has
// no cuelang dependency, so it lives in this cuelang-free slice rather than the
// cuelang-bearing spec/climodel (whose only heavy dep is ValidateGenerated's CUE validation).
type CLISubcommand struct {
	Name string
	Help string
	// Hidden marks a DECLARED subcommand as HIDDEN-BUT-REACHABLE (F-CLI-NEST): the host still
	// renders it as a real Kong `cmd:""` child — tagged `hidden:""` — so it DISPATCHES (e.g.
	// the iterate harness's `charly check run-local` re-exec) but stays invisible to `--help`
	// and the CLI model (MCP tool surface), mirroring the `hidden:""` tag on the plugin's own
	// grammar struct field. KongSubcommands (sdk) derives it from that tag; a plugin declaring
	// a catalog by hand sets it directly.
	Hidden bool
}

// StepContract is the SDK-facing form of the proto StepContract — a class="step" plugin's
// declared install-step Scope/Venue/Gate. Reverse is NOT declared (an external step's
// teardown ops are recorded dynamically from its OpExecute reply).
type StepContract struct {
	Scope string // "system" | "user" | "user-profile"
	Venue int    // 0=host-native, 1=container-builder, 2=skip
	Gate  string // "" | "allow-repo-changes" | "allow-root-tasks" | "with-services"
	// Emits declares that the step produces a build-context Containerfile FRAGMENT
	// (the plugin serves Invoke(OpEmit) → EmitReply.Fragment). The pod-overlay OCITarget
	// bakes it; false => a deploy-only step (no build fragment — OCITarget skips it, like
	// apk on an image build). F-STEP-EMIT: the BUILD leg C1 needs to externalize a step
	// kind whose EmitOCI produces a Containerfile fragment.
	Emits bool
}
