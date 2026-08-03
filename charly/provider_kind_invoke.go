package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// runPluginKind decodes an EXTERNAL kind node out-of-process via its Provider's
// Invoke envelope (Op: OpLoad) — the kind-class analogue of runPluginVerb
// (provider_checkenv.go). A BUILT-IN kind uses the typed DecodeNode fast path (no
// JSON, provider_kind.go); an external plugin kind, which the core has no Go type
// for, validates the NAMELESS authored body against its SERVED .cue and returns its
// canonical entity JSON, stored in acc.PluginKinds[kind][name]. The entity NAME is
// the node KEY (gn.name) — never part of the validated body, so #<Kind>Input is
// untouched — threaded here from the node key into the storage key, so a consumer
// can look the entity up by name and the merge is root-wins override
// (mergePluginKindsMap). The split (typed builtin / serializable envelope external)
// keeps the per-entity decode hot path zero-JSON for builtins — the E3 envelope is
// paid only out-of-process. Transport-invisible above the registry.
//
// acc is the K1-unit-1 spec.MaterializedProject accumulator (the entity-map subset
// of *spec.UnifiedFile this dispatch ever touches — Box/Candy/VM/Pod/K8s/Local/Android/
// Bundle/PluginKinds), threaded from the MaterializeSeams.DecodeEntity callback
// (loader_threaded.go) rather than a full *spec.UnifiedFile — this dispatch never needed
// Import/Discover/Namespaces/etc, so the retype carries no behavior change.
//
// Takes pn spec.ParsedNode (K1 unit 3b) rather than charly's *genericNode: every tree-assembly
// helper this dispatch calls (entityBodyJSON/buildResourceMemberChildren/…) is now the relocated
// sdk/loaderkit mechanism, reached through the ProjectLoader seam directly on pn — no genericNode
// reconstruction anywhere in this function. genericNode survives ONLY where foldCandyKind needs it
// for the bootstrap-critical candyIsImage/buildCandy routing (clause B, permanently core).
func runPluginKind(prov Provider, pn spec.ParsedNode, acc *spec.MaterializedProject) error {
	// C2-substrate: a substrate structural kind (pod/vm/k8s/local/android) is decoded
	// HOST-SIDE (its rich core-referencing value cannot ride op.Params nor a self-contained
	// plugin schema — see foldSubstrateKind) and folds into acc.Bundle (deploy) or the typed
	// template map (template). It does NOT use the op.Params + plugin-schema validation the
	// group-style / flat kinds below take — its value is validated host-side against the KEPT
	// #<Kind>Value def.
	if isStandaloneResourceKind(pn.Disc) {
		return foldSubstrateKind(prov, pn, acc)
	}
	// C2-candy: the `candy` box⊻layer factory kind is decoded HOST-SIDE by the
	// bootstrap-critical candyIsImage + buildCandy (which STAY core — the discovered-candy
	// pre-check calls them directly), then folded into acc.Box (IMAGE) or acc.Candy (LAYER).
	// Like substrate, its rich core-referencing value can neither ride op.Params nor a
	// self-contained plugin schema, so it is host-validated against the KEPT #CandyValue def
	// and the plugin (candy/plugin-candy) is a pure ECHO. See foldCandyKind.
	if pn.Disc == "candy" {
		return foldCandyKind(prov, pn, acc)
	}
	paramsJSON, err := requireProjectLoader().EntityBodyJSON(pn)
	if err != nil {
		return err
	}
	// A plugin KIND validates at LOAD time (inside the loader), BEFORE the
	// check/deploy paths gate plugin schemas (loadProjectPlugins). Ensure every
	// builtin plugin unit's served schema is loaded so validateAuthoredPluginInput
	// can find this kind's def; idempotent (sync.Once), local (no fetch).
	if err := loadBuiltinPluginUnits(); err != nil {
		return fmt.Errorf("node %q: builtin plugin schema gate: %w", pn.Name, err)
	}
	// Validate the authored value against the plugin's served #Kind .cue BEFORE
	// dispatch — identical gate to the verb path (validateAuthoredPluginInput).
	if err := validateAuthoredPluginInput(ClassKind, pn.Disc, paramsJSON); err != nil {
		return fmt.Errorf("node %q: %w", pn.Name, err)
	}
	// F7/C8: a kind declaring Validates serves a DEEP OpValidate check BEYOND the static CUE
	// input-def gate above — the host dispatches it and surfaces error-severity Diagnostics as a
	// load failure. A kind that does not declare it pays nothing (no extra round-trip). Shared
	// with foldSubstrateKind (R3) — the SAME kind-blind dispatch, driven purely by the
	// capability's declared Validates flag, never a per-kind branch in host code.
	if err := dispatchKindOpValidate(prov, pn, paramsJSON); err != nil {
		return err
	}
	// F5 authored-member input-threading: a STRUCTURAL kind's authored RESOURCE-MEMBER
	// children are pre-decoded via the SAME loaderkit.BuildResourceMemberChildren recursion the
	// builtin path uses (one member-decode source of truth, R3) and threaded to the plugin's
	// OpLoad via op.Env, so the plugin reconstructs the authored member tree into its spec.Deploy
	// reply. They CANNOT ride op.Params: it is unified against the plugin's CLOSED #<Kind>Input
	// def, which the member subtree would violate. A FLAT kind (F4) is not structural — no member
	// env, opaque body only.
	structural := false
	if sc, ok := prov.(spec.StructuralKindCarrier); ok && sc.IsStructuralKind() {
		structural = true
	}
	var envJSON json.RawMessage
	if structural {
		members, merr := requireProjectLoader().BuildResourceMemberChildren(pn, loaderThreaded())
		if merr != nil {
			return fmt.Errorf("node %q: decode members: %w", pn.Name, merr)
		}
		envJSON, err = json.Marshal(spec.StructuralKindLoadEnv{Members: members})
		if err != nil {
			return fmt.Errorf("node %q: marshal member env: %w", pn.Name, err)
		}
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: pn.Disc, Op: OpLoad, Params: paramsJSON, Env: envJSON})
	if err != nil {
		return fmt.Errorf("node %q: plugin kind %q: %w", pn.Name, pn.Disc, err)
	}
	// F5: a STRUCTURAL kind's OpLoad returns a spec.Deploy (BundleNode) member tree the host
	// folds into acc.Bundle — the SAME map a builtin structural kind's DecodeNode populates
	// (BuildBundleNodeInto), so the entity participates in deploy/check exactly like a builtin
	// pod/group/candy. A FLAT kind (F4) lands its opaque body in acc.PluginKinds, unchanged.
	if structural {
		var dn spec.BundleNode
		if err := json.Unmarshal(out.JSON, &dn); err != nil {
			return fmt.Errorf("node %q: structural kind %q reply decode: %w", pn.Name, pn.Disc, err)
		}
		if acc.Bundle == nil {
			acc.Bundle = map[string]spec.BundleNode{}
		}
		acc.Bundle[pn.Name] = dn
		return nil
	}
	// A FLAT (non-structural) kind's body is opaque (acc.PluginKinds) — it has NO member tree, and
	// the entity-body assembler skips entity children, so any authored resource-member child would
	// be SILENTLY DROPPED. Reject loudly instead (the parser admits members under any external
	// kind; this is where a flat kind's members are caught, F5 authored-member input-threading).
	// Every pn.Children entry is an entity child by construction (the parse-time desugar already
	// separates step/data children into the plan/body fields before a spec.ParsedNode ever reaches
	// here — see node_parse.go), so a non-empty Children list alone is the complete check.
	if len(pn.Children) > 0 {
		return fmt.Errorf("node %q: kind %q is not structural — it cannot nest resource-member children (%q); declare Structural:true to reconstruct authored members", pn.Name, pn.Disc, pn.Children[0].Name)
	}
	if acc.PluginKinds == nil {
		acc.PluginKinds = map[string]map[string]json.RawMessage{}
	}
	if acc.PluginKinds[pn.Disc] == nil {
		acc.PluginKinds[pn.Disc] = map[string]json.RawMessage{}
	}
	acc.PluginKinds[pn.Disc][pn.Name] = out.JSON
	return nil
}

// dispatchKindOpValidate runs a kind's declared F7/C8 deep OpValidate check (Validates=true) and
// returns an error if the plugin reports any error-severity spec.Diagnostics item; a kind that
// does NOT declare Validates pays nothing (no extra round-trip, no error). Kind-blind by
// construction — entirely driven by the capability's own declared Validates flag (never a
// per-kind branch in host code, per the kernel/plugin boundary law) — so it is shared VERBATIM
// by the flat op.Params kind path (runPluginKind) and the rich-value host-pre-decoded substrate
// path (foldSubstrateKind), rather than each re-implementing the same dispatch (R3).
func dispatchKindOpValidate(prov Provider, pn spec.ParsedNode, paramsJSON json.RawMessage) error {
	vc, ok := prov.(spec.ValidatingKindCarrier)
	if !ok || !vc.IsValidatingKind() {
		return nil
	}
	vres, verr := prov.Invoke(context.Background(), &Operation{Reserved: pn.Disc, Op: OpValidate, Params: paramsJSON})
	if verr != nil {
		return fmt.Errorf("node %q: plugin kind %q validate: %w", pn.Name, pn.Disc, verr)
	}
	var diags spec.Diagnostics
	if vres != nil && len(vres.JSON) > 0 {
		if err := json.Unmarshal(vres.JSON, &diags); err != nil {
			return fmt.Errorf("node %q: plugin kind %q validate: decode diagnostics: %w", pn.Name, pn.Disc, err)
		}
	}
	if diags.HasErrors() {
		return fmt.Errorf("node %q: kind %q validation failed: %s", pn.Name, pn.Disc, formatKindDiagnostics(diags))
	}
	return nil
}

// The word→#<Kind>Value CUE-def map the host value gate consults is spec.KindValueDefs
// — CUE-DERIVED from the #<X>Value defs (schema/node.cue) by schemagen (clause D of the
// kernel/plugin boundary law: kind-recognition data loaded from CUE, NOT a compiled-in
// per-kind Go map). It is the HOST-SIDE closedness gate replacing the removed #Node arm:
// a plugin cannot serve a self-contained schema for these rich core-referencing values,
// so the host validates the authored value against the KEPT def in-core. Covers the 5
// substrate kinds (C2-substrate, #<Kind> | #DeployValue) AND candy (C2-candy, #CandyValue
// = *#Candy | #Image); keep the #<Kind>Value defs in lockstep with isStandaloneResourceKind
// + the foldSubstrateKind/foldCandyKind branches.

// foldSubstrateKind decodes a SUBSTRATE structural kind node (pod/vm/k8s/local/android)
// HOST-SIDE and folds candy/plugin-substrate's echo into the right map (C2-substrate). The
// value is rich + core-referencing (#Vm/#Deploy/#LibvirtDomain/… with host-canonicalized
// shorthand like tunnel:/port:), so — unlike group's scalar #GroupInput value — it cannot be
// re-decoded soundly from the raw op.Params by a plugin nor validated by a self-contained
// plugin schema. So the host: (1) validates the authored value against the KEPT #<Kind>Value
// def (the closedness the removed #Node arm gave); (2) detects the shape via
// sdk/loaderkit.IsDeployShape/ResourceChildren (reached through the ProjectLoader seam on pn
// directly, K1 unit 3b — no genericNode reconstruction needed here); (3) pre-decodes the
// CANONICAL node via the SAME relocated loaderkit.BuildBundleNode (deploy) /
// DecodeStandaloneTemplateJSON (template) — the SINGLE decode source of truth (R3); (4)
// threads it to the plugin's OpLoad via op.Env (spec.StructuralKindLoadEnv.Standalone); (5)
// folds the plugin's ECHO into acc.Bundle (deploy) or the typed template map acc.Pod/acc.VM/…
// (template — the C2-substrate TEMPLATE fold arm extending F5's deploy-only fold). RDD proved
// the canonical value round-trips through JSON byte-faithfully, so this is byte-equivalent to
// the former in-proc standaloneKind decode (buildBundleNodeInto / buildStandaloneResource).
func foldSubstrateKind(prov Provider, pn spec.ParsedNode, acc *spec.MaterializedProject) error {
	if err := validateKindValueCUE(pn); err != nil {
		return fmt.Errorf("node %q: %w", pn.Name, err)
	}
	// F7/C8: dispatch the substrate's declared deep OpValidate check (today ONLY the "vm"
	// capability declares Validates:true, for the PCI-hostdev-concreteness check
	// validateKindValueCUE's closedness-only gate cannot express — see that function's
	// comment). Kind-blind: driven purely by the resolved provider's own Validates flag, so
	// this call is a complete no-op for pod/k8s/local/android (and would be for vm too, if it
	// ever stopped declaring Validates). The RAW authored body is the natural input — the
	// SAME body the flat op.Params path validates against — since the concreteness check
	// only needs to see which fields were actually authored, not the canonicalized value.
	rawBody, err := requireProjectLoader().EntityBodyJSON(pn)
	if err != nil {
		return err
	}
	if err := dispatchKindOpValidate(prov, pn, rawBody); err != nil {
		return err
	}
	t := loaderThreaded()
	pl := requireProjectLoader()
	deployShape := pl.IsDeployShape(pn) || len(pl.ResourceChildren(pn)) > 0
	var env spec.StructuralKindLoadEnv
	if deployShape {
		bn, err := pl.BuildBundleNode(pn, t)
		if err != nil {
			return fmt.Errorf("node %q: decode deploy: %w", pn.Name, err)
		}
		env.Standalone = &spec.StandaloneLoad{Shape: "deploy", Deploy: bn}
	} else {
		tmpl, err := pl.DecodeStandaloneTemplateJSON(pn, t)
		if err != nil {
			return fmt.Errorf("node %q: decode template: %w", pn.Name, err)
		}
		env.Standalone = &spec.StandaloneLoad{Shape: "template", Template: tmpl}
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("node %q: marshal substrate env: %w", pn.Name, err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: pn.Disc, Op: OpLoad, Env: envJSON})
	if err != nil {
		return fmt.Errorf("node %q: substrate kind %q: %w", pn.Name, pn.Disc, err)
	}
	if deployShape {
		var dn spec.BundleNode
		if err := json.Unmarshal(out.JSON, &dn); err != nil {
			return fmt.Errorf("node %q: substrate deploy reply decode: %w", pn.Name, err)
		}
		ensureMap(&acc.Bundle)
		acc.Bundle[pn.Name] = dn
		return nil
	}
	return foldStandaloneTemplateReply(pn.Disc, pn.Name, out.JSON, acc)
}

// foldCandyKind decodes a `candy` box⊻layer node HOST-SIDE and folds candy/plugin-candy's echo
// into acc.Box (a full IMAGE — base:/from:) or acc.Candy (a LAYER fragment) (C2-candy). The candy
// value is rich + core-referencing (#Candy/#Box with host-canonicalized shorthand), so — like
// substrate — it can neither ride op.Params nor be validated by a self-contained plugin schema.
// So the host: (1) validates the authored value against the KEPT #CandyValue def; (2) runs the
// BOOTSTRAP-CRITICAL core box⊻layer routing candyIsImage + buildCandy (which STAY core — the
// discovered-candy pre-check in unified.go calls them DIRECTLY, so this is the SAME decode source,
// R3; the "bootstrap cycle" that blocked an EXTERNAL candy plugin does NOT exist for the
// COMPILED-IN plugin-candy, registered at init before any LoadUnified); (3) threads the canonical
// spec.Box (image) / spec.Candy (layer) to the plugin's OpLoad via op.Env; (4) folds the plugin's
// ECHO into acc.Box / acc.Candy. RDD proved a canonical spec.Box / spec.Candy round-trips through
// JSON byte-faithfully, so this is byte-equivalent to the former in-proc candyKind decode.
// gn is reconstructed LOCALLY from pn (parsedNodeToGeneric) solely to reach candyIsImage/
// buildCandy — the ONE remaining genericNode use in this dispatch, because those two are
// BOOTSTRAP-CRITICAL (clause B: the discovered-candy pre-check in unified.go calls them directly,
// so they cannot themselves move or be reformulated without breaking that pre-load-time call).
// Every OTHER call here (validateKindValueCUE, DecodeNodeValue) threads pn straight through.
func foldCandyKind(prov Provider, pn spec.ParsedNode, acc *spec.MaterializedProject) error {
	if err := validateKindValueCUE(pn); err != nil {
		return fmt.Errorf("node %q: %w", pn.Name, err)
	}
	gn, err := parsedNodeToGeneric(pn)
	if err != nil {
		return fmt.Errorf("node %q: %w", pn.Name, err)
	}
	image := candyIsImage(gn)
	var env spec.StructuralKindLoadEnv
	if image {
		var b spec.BoxConfig
		if err := requireProjectLoader().DecodeNodeValue(pn, &b); err != nil {
			return fmt.Errorf("node %q: decode image: %w", pn.Name, err)
		}
		env.Standalone = &spec.StandaloneLoad{Shape: "candy-image", Box: &b}
	} else {
		_, ic, berr := buildCandy(gn)
		if berr != nil {
			return fmt.Errorf("node %q: decode layer: %w", pn.Name, berr)
		}
		env.Standalone = &spec.StandaloneLoad{Shape: "candy-layer", Candy: &ic.CandyYAML}
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("node %q: marshal candy env: %w", pn.Name, err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: pn.Disc, Op: OpLoad, Env: envJSON})
	if err != nil {
		return fmt.Errorf("node %q: candy kind: %w", pn.Name, err)
	}
	if image {
		var b spec.BoxConfig
		if err := json.Unmarshal(out.JSON, &b); err != nil {
			return fmt.Errorf("node %q: candy image reply decode: %w", pn.Name, err)
		}
		// The acc.Box[name]=EncodeBox(b) inline write below is exactly what spec.UnifiedFile.SetBox
		// does (uf_box_generic.go) — that method lives on *spec.UnifiedFile, which acc (a
		// spec.MaterializedProject) is not, so this dispatch inlines the SAME spec.EncodeBox call.
		ensureMap(&acc.Box)
		acc.Box[pn.Name] = spec.EncodeBox(b)
		return nil
	}
	var c spec.CandyYAML
	if err := json.Unmarshal(out.JSON, &c); err != nil {
		return fmt.Errorf("node %q: candy layer reply decode: %w", pn.Name, err)
	}
	// Mirrors spec.UnifiedFile.SetCandy (uf_candy_generic.go) — spec.EncodeInlineCandy(*spec.InlineCandy) stays
	// core-private (spec.InlineCandy embeds spec.CandyYAML), reused verbatim.
	ensureMap(&acc.Candy)
	acc.Candy[pn.Name] = spec.EncodeInlineCandy(&spec.InlineCandy{CandyYAML: c})
	return nil
}

// validateKindValueCUE validates an externalized structural kind node's authored VALUE against
// the KEPT #<Kind>Value def — the host-side replacement for the removed #Node arm's closedness
// (a typo'd field in a `vm:`/`pod:`/`candy:` value is a hard load error, exactly as before).
// Only a MAPPING value is gated: a SCALAR value is a cross-ref (`pod: coder`), which carries no
// authored fields to typo-check (it is resolved at deploy). The RAW authored value is validated
// (shorthand intact) since #<Kind>Value accepts the same shorthand the arm did. Covers the 5
// substrate kinds (#<Kind>Value) AND candy (#CandyValue).
//
// entityBodyJSON (the generic body→wire mechanism BOTH the op.Params plugin-kind path and the
// substrate TEMPLATE thread used) is now sdk/loaderkit.EntityBodyJSON (K1 unit 3b), reached
// directly through requireProjectLoader() at each call site — no core wrapper survives it (its
// former callers here are all pn-based now).
//
// gn is reconstructed LOCALLY from pn (parsedNodeToGeneric, node_parsed.go) for the RAW discValue
// shape check below (a scalar cross-ref carries no authored fields to typo-check) — the SAME
// reconstruction idiom foldCandyKind uses, kept local rather than adding a dedicated seam method
// for a single small consumer.
func validateKindValueCUE(pn spec.ParsedNode) error {
	gn, err := parsedNodeToGeneric(pn)
	if err != nil {
		return err
	}
	if gn.discValue == nil || gn.discValue.Kind != yaml.MappingNode {
		return nil
	}
	defPath, ok := spec.KindValueDefs[gn.disc]
	if !ok {
		return nil
	}
	def := sharedCueSchema.LookupPath(cue.ParsePath(defPath))
	if def.Err() != nil {
		return fmt.Errorf("kind value def %s not found: %w", defPath, def.Err())
	}
	b, err := yaml.Marshal(gn.discValue)
	if err != nil {
		return fmt.Errorf("%s value: marshal: %w", gn.disc, err)
	}
	entity, err := cueDocFromYAML("node "+gn.name, b)
	if err != nil {
		return err
	}
	// KNOWN GAP, CLOSED via a PLUGIN, not a kernel patch (RCA'd during the
	// dead-code-radical-removal batch, NOT introduced by it — the now-deleted
	// validateEntityCUE was the only concrete check and it already had zero production
	// callers before that batch touched anything): this gate is CLOSEDNESS-only (no
	// cue.Concrete(true)), so a required-but-unset field slips through silently (e.g. a vm
	// PCI hostdev's slot/function). A blanket cue.Concrete(true) fix was attempted and
	// REVERTED — it broke 9 real cases across vm/pod/local/k8s/candy (TestCueKinds_Corpus,
	// TestBundleCompileParity_*, TestPreresolveActiveInitInto_*, TestCompileServiceSteps_*,
	// TestBuildDeployPlan*, TestInvokeProvider_LazyConnectFallback*) because those
	// transitively load the REAL repo-root charly.yml, which legitimately carries
	// non-concrete/disjunctive constructs elsewhere in the document (candy's base⊻from
	// disjunction and others) that a document-wide concrete check trips on. A first attempt
	// at a NARROW, kind/field-scoped fix landed it here in the kernel (a hand `if kind !=
	// "vm"` branch + a hardcoded field path) — an architecture-review finding overruled
	// that placement: per the kernel/plugin boundary law, ANY kernel branch/switch encoding
	// concrete-kind semantic knowledge (which fields a `type: pci` hostdev needs) is BY
	// DEFINITION an R-item that leaked into core, never a kept exception, regardless of how
	// narrowly it's scoped. The CORRECT placement is the F7/C8 deep OpValidate capability
	// (`ProvidedCapability.Validates`) exactly the "checks CUE cannot express" case it
	// exists for — see candy/plugin-substrate's "vm" capability (validate_vm.go), dispatched
	// kind-blindly from foldSubstrateKind via dispatchKindOpValidate. This gate therefore
	// stays closedness-only, unchanged, forever — concreteness lives in the plugin.
	merged := entity.Unify(def)
	if verr := merged.Validate(); verr != nil {
		return fmt.Errorf("%s: %s", gn.disc, errors.Details(verr, nil))
	}
	return nil
}

// formatKindDiagnostics renders the error-severity items of an OpValidate reply into one
// semicolon-joined string (path-prefixed when a path is set) for the load error message.
func formatKindDiagnostics(d spec.Diagnostics) string {
	msgs := make([]string, 0, len(d.Items))
	for _, it := range d.Items {
		if it.Severity == "warning" {
			continue
		}
		if it.Path != "" {
			msgs = append(msgs, it.Path+": "+it.Message)
		} else {
			msgs = append(msgs, it.Message)
		}
	}
	return strings.Join(msgs, "; ")
}
