package sdk

import (
	"fmt"
	"io/fs"
	"strings"

	"cuelang.org/go/cue/cuecontext"

	"github.com/opencharly/charly/charly/internal/schemaconcat"
	pb "github.com/opencharly/charly/charly/plugin/proto"
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
	// its OpLoad returns a spec.Deploy member tree the host folds into uf.Bundle — rather than
	// a FLAT body landed opaquely in uf.PluginKinds (F4). false for every other class/kind.
	Structural bool
	// Lifecycle is set ONLY for Class=="deploy" (F6): the substrate brings its OWN host-side
	// venue lifecycle (PrepareVenue/Start/Stop/Status/Rebuild/...) served over the lifecycle Ops,
	// so the host registers a wire-backed substrateLifecycle for it. false for every other
	// class/deploy (local/android/k8s keep the generic host-venue behaviour).
	Lifecycle bool
	// Preresolve is set ONLY for Class=="deploy" (F6): the substrate declares a host-side
	// PRERESOLVE step (OpPreresolve) the host runs before apply, shipping the opaque result in
	// DeployVenue.Substrate — the wire-backed generalization of the in-core k8s/android
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

// BuildCapabilities is the serve-side half of the "every plugin ships its own CUE
// schema" contract. It concatenates the plugin's embedded schema/*.cue via the SAME
// schemaconcat contract charly uses for its base (R3 — one concat loop, no
// duplicate), compiles it STANDALONE to fail loudly on a broken or empty schema
// (a self-contained schema must compile alone — the same property that lets
// `cue exp gengotypes` generate the plugin's Go params), and assembles the Describe
// reply carrying the raw .cue source the host splices onto its base.
//
// schemaFS is the plugin's `//go:embed schema/*.cue` FS; dir is the embedded
// subdirectory ("schema"). Both the SDK and charly's base reach the same internal
// schemaconcat because the SDK lives under charly/ — an external module imports
// only this SDK, never charly/internal directly.
func BuildCapabilities(calver string, provided []ProvidedCapability, schemaFS fs.FS, dir string) (*pb.Capabilities, error) {
	body, _, err := schemaconcat.ConcatSchema(schemaFS, dir, nil)
	if err != nil {
		return nil, fmt.Errorf("plugin schema: %w", err)
	}
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("plugin ships no CUE schema (every plugin MUST ship its own schema)")
	}
	if v := cuecontext.New().CompileString(body); v.Err() != nil {
		return nil, fmt.Errorf("plugin schema does not compile: %w", v.Err())
	}
	out := make([]*pb.ProvidedCapability, 0, len(provided))
	for _, c := range provided {
		pc := &pb.ProvidedCapability{Class: c.Class, Word: c.Word, InputDef: c.InputDef, Structural: c.Structural, Lifecycle: c.Lifecycle, Preresolve: c.Preresolve, Validates: c.Validates, Phase: c.Phase}
		if c.StepContract != nil {
			pc.StepContract = &pb.StepContract{Scope: c.StepContract.Scope, Venue: int32(c.StepContract.Venue), Gate: c.StepContract.Gate, Emits: c.StepContract.Emits}
		}
		out = append(out, pc)
	}
	return &pb.Capabilities{
		Calver:          calver,
		ProtocolVersion: ProtocolVersion,
		Provided:        out,
		SchemaCue:       body,
	}, nil
}
