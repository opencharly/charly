package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/errors"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// runPluginKind decodes an EXTERNAL kind node out-of-process via its Provider's
// Invoke envelope (Op: OpLoad) — the kind-class analogue of runPluginVerb
// (provider_checkenv.go). A BUILT-IN kind uses the typed DecodeNode fast path (no
// JSON, provider_kind.go); an external plugin kind, which the core has no Go type
// for, validates the NAMELESS authored body against its SERVED .cue and returns its
// canonical entity JSON, stored in uf.PluginKinds[kind][name]. The entity NAME is
// the node KEY (gn.name) — never part of the validated body, so #<Kind>Input is
// untouched — threaded here from the node key into the storage key, so a consumer
// can look the entity up by name and the merge is root-wins override
// (mergePluginKindsMap). The split (typed builtin / serializable envelope external)
// keeps the per-entity decode hot path zero-JSON for builtins — the E3 envelope is
// paid only out-of-process. Transport-invisible above the registry.
func runPluginKind(prov Provider, gn *genericNode, uf *UnifiedFile) error {
	// C2-substrate: a substrate structural kind (pod/vm/k8s/local/android) is decoded
	// HOST-SIDE (its rich core-referencing value cannot ride op.Params nor a self-contained
	// plugin schema — see foldSubstrateKind) and folds into uf.Bundle (deploy) or the typed
	// template map (template). It does NOT use the op.Params + plugin-schema validation the
	// group-style / flat kinds below take — its value is validated host-side against the KEPT
	// #<Kind>Value def.
	if isStandaloneResourceKind(gn.disc) {
		return foldSubstrateKind(prov, gn, uf)
	}
	// C2-candy: the `candy` box⊻layer factory kind is decoded HOST-SIDE by the
	// bootstrap-critical candyIsImage + buildCandy (which STAY core — the discovered-candy
	// pre-check calls them directly), then folded into uf.Box (IMAGE) or uf.Candy (LAYER).
	// Like substrate, its rich core-referencing value can neither ride op.Params nor a
	// self-contained plugin schema, so it is host-validated against the KEPT #CandyValue def
	// and the plugin (candy/plugin-candy) is a pure ECHO. See foldCandyKind.
	if gn.disc == "candy" {
		return foldCandyKind(prov, gn, uf)
	}
	paramsJSON, err := entityBodyJSON(gn)
	if err != nil {
		return err
	}
	// A plugin KIND validates at LOAD time (inside the loader), BEFORE the
	// check/deploy paths gate plugin schemas (loadProjectPlugins). Ensure every
	// builtin plugin unit's served schema is loaded so validateAuthoredPluginInput
	// can find this kind's def; idempotent (sync.Once), local (no fetch).
	if err := loadBuiltinPluginUnits(); err != nil {
		return fmt.Errorf("node %q: builtin plugin schema gate: %w", gn.name, err)
	}
	// Validate the authored value against the plugin's served #Kind .cue BEFORE
	// dispatch — identical gate to the verb path (validateAuthoredPluginInput).
	if err := validateAuthoredPluginInput(ClassKind, gn.disc, paramsJSON); err != nil {
		return fmt.Errorf("node %q: %w", gn.name, err)
	}
	// F7/C8: a kind declaring Validates serves a DEEP OpValidate check BEYOND the static CUE
	// input-def gate above — the host dispatches it and surfaces error-severity Diagnostics as a
	// load failure. A kind that does not declare it pays nothing (no extra round-trip).
	if vc, ok := prov.(spec.ValidatingKindCarrier); ok && vc.IsValidatingKind() {
		vres, verr := prov.Invoke(context.Background(), &Operation{Reserved: gn.disc, Op: OpValidate, Params: paramsJSON})
		if verr != nil {
			return fmt.Errorf("node %q: plugin kind %q validate: %w", gn.name, gn.disc, verr)
		}
		var diags spec.Diagnostics
		if vres != nil && len(vres.JSON) > 0 {
			if err := json.Unmarshal(vres.JSON, &diags); err != nil {
				return fmt.Errorf("node %q: plugin kind %q validate: decode diagnostics: %w", gn.name, gn.disc, err)
			}
		}
		if diags.HasErrors() {
			return fmt.Errorf("node %q: kind %q validation failed: %s", gn.name, gn.disc, formatKindDiagnostics(diags))
		}
	}
	// F5 authored-member input-threading: a STRUCTURAL kind's authored RESOURCE-MEMBER
	// children are pre-decoded HOST-SIDE via the SAME core buildBundleNode recursion the
	// builtin path uses (buildResourceMemberChildren — one member-decode source of truth,
	// R3) and threaded to the plugin's OpLoad via op.Env, so the plugin reconstructs the
	// authored member tree into its spec.Deploy reply. They CANNOT ride op.Params: it is
	// unified against the plugin's CLOSED #<Kind>Input def, which the member subtree would
	// violate. A FLAT kind (F4) is not structural — no member env, opaque body only.
	structural := false
	if sc, ok := prov.(spec.StructuralKindCarrier); ok && sc.IsStructuralKind() {
		structural = true
	}
	var envJSON json.RawMessage
	if structural {
		members, merr := buildResourceMemberChildren(gn)
		if merr != nil {
			return fmt.Errorf("node %q: decode members: %w", gn.name, merr)
		}
		envJSON, err = json.Marshal(spec.StructuralKindLoadEnv{Members: members})
		if err != nil {
			return fmt.Errorf("node %q: marshal member env: %w", gn.name, err)
		}
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: gn.disc, Op: OpLoad, Params: paramsJSON, Env: envJSON})
	if err != nil {
		return fmt.Errorf("node %q: plugin kind %q: %w", gn.name, gn.disc, err)
	}
	// F5: a STRUCTURAL kind's OpLoad returns a spec.Deploy (BundleNode) member tree the host
	// folds into uf.Bundle — the SAME map a builtin structural kind's DecodeNode populates
	// (buildBundleNodeInto), so the entity participates in deploy/check exactly like a builtin
	// pod/group/candy. A FLAT kind (F4) lands its opaque body in uf.PluginKinds, unchanged.
	if structural {
		var dn spec.BundleNode
		if err := json.Unmarshal(out.JSON, &dn); err != nil {
			return fmt.Errorf("node %q: structural kind %q reply decode: %w", gn.name, gn.disc, err)
		}
		if uf.Bundle == nil {
			uf.Bundle = map[string]spec.BundleNode{}
		}
		uf.Bundle[gn.name] = dn
		return nil
	}
	// A FLAT (non-structural) kind's body is opaque (uf.PluginKinds) — it has NO member tree, and
	// assembleEntityBody skips entity children, so any authored resource-member child would be
	// SILENTLY DROPPED. Reject loudly instead (the parser admits members under any external kind;
	// this is where a flat kind's members are caught, F5 authored-member input-threading).
	for _, ch := range gn.children {
		if ch.discClass == "entity" {
			return fmt.Errorf("node %q: kind %q is not structural — it cannot nest resource-member children (%q); declare Structural:true to reconstruct authored members", gn.name, gn.disc, ch.name)
		}
	}
	if uf.PluginKinds == nil {
		uf.PluginKinds = map[string]map[string]json.RawMessage{}
	}
	if uf.PluginKinds[gn.disc] == nil {
		uf.PluginKinds[gn.disc] = map[string]json.RawMessage{}
	}
	uf.PluginKinds[gn.disc][gn.name] = out.JSON
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
// def (the closedness the removed #Node arm gave); (2) detects the shape (needs the
// genericNode tree); (3) pre-decodes the CANONICAL node via the core buildBundleNode (deploy)
// / decodeStandaloneTemplateJSON (template) — the SINGLE decode source of truth (R3); (4)
// threads it to the plugin's OpLoad via op.Env (spec.StructuralKindLoadEnv.Standalone); (5)
// folds the plugin's ECHO into uf.Bundle (deploy) or the typed template map uf.Pod/uf.VM/…
// (template — the C2-substrate TEMPLATE fold arm extending F5's deploy-only fold). RDD proved
// the canonical value round-trips through JSON byte-faithfully, so this is byte-equivalent to
// the former in-proc standaloneKind decode (buildBundleNodeInto / buildStandaloneResource).
func foldSubstrateKind(prov Provider, gn *genericNode, uf *UnifiedFile) error {
	if err := validateKindValueCUE(gn); err != nil {
		return fmt.Errorf("node %q: %w", gn.name, err)
	}
	deployShape := isDeployShape(gn) || len(resourceChildren(gn)) > 0
	var env spec.StructuralKindLoadEnv
	if deployShape {
		bn, err := buildBundleNode(gn)
		if err != nil {
			return fmt.Errorf("node %q: decode deploy: %w", gn.name, err)
		}
		env.Standalone = &spec.StandaloneLoad{Shape: "deploy", Deploy: bn}
	} else {
		tmpl, err := decodeStandaloneTemplateJSON(gn)
		if err != nil {
			return fmt.Errorf("node %q: decode template: %w", gn.name, err)
		}
		env.Standalone = &spec.StandaloneLoad{Shape: "template", Template: tmpl}
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("node %q: marshal substrate env: %w", gn.name, err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: gn.disc, Op: OpLoad, Env: envJSON})
	if err != nil {
		return fmt.Errorf("node %q: substrate kind %q: %w", gn.name, gn.disc, err)
	}
	if deployShape {
		var dn spec.BundleNode
		if err := json.Unmarshal(out.JSON, &dn); err != nil {
			return fmt.Errorf("node %q: substrate deploy reply decode: %w", gn.name, err)
		}
		ensureMap(&uf.Bundle)
		uf.Bundle[gn.name] = dn
		return nil
	}
	return foldStandaloneTemplateReply(gn.disc, gn.name, out.JSON, uf)
}

// foldCandyKind decodes a `candy` box⊻layer node HOST-SIDE and folds candy/plugin-candy's echo
// into uf.Box (a full IMAGE — base:/from:) or uf.Candy (a LAYER fragment) (C2-candy). The candy
// value is rich + core-referencing (#Candy/#Box with host-canonicalized shorthand), so — like
// substrate — it can neither ride op.Params nor be validated by a self-contained plugin schema.
// So the host: (1) validates the authored value against the KEPT #CandyValue def; (2) runs the
// BOOTSTRAP-CRITICAL core box⊻layer routing candyIsImage + buildCandy (which STAY core — the
// discovered-candy pre-check in unified.go calls them DIRECTLY, so this is the SAME decode source,
// R3; the "bootstrap cycle" that blocked an EXTERNAL candy plugin does NOT exist for the
// COMPILED-IN plugin-candy, registered at init before any LoadUnified); (3) threads the canonical
// spec.Box (image) / spec.Candy (layer) to the plugin's OpLoad via op.Env; (4) folds the plugin's
// ECHO into uf.Box / uf.Candy. RDD proved a canonical spec.Box / spec.Candy round-trips through
// JSON byte-faithfully, so this is byte-equivalent to the former in-proc candyKind decode.
func foldCandyKind(prov Provider, gn *genericNode, uf *UnifiedFile) error {
	if err := validateKindValueCUE(gn); err != nil {
		return fmt.Errorf("node %q: %w", gn.name, err)
	}
	image := candyIsImage(gn)
	var env spec.StructuralKindLoadEnv
	if image {
		var b spec.BoxConfig
		if err := decodeNodeValue(gn, &b); err != nil {
			return fmt.Errorf("node %q: decode image: %w", gn.name, err)
		}
		env.Standalone = &spec.StandaloneLoad{Shape: "candy-image", Box: &b}
	} else {
		_, ic, berr := buildCandy(gn)
		if berr != nil {
			return fmt.Errorf("node %q: decode layer: %w", gn.name, berr)
		}
		env.Standalone = &spec.StandaloneLoad{Shape: "candy-layer", Candy: &ic.CandyYAML}
	}
	envJSON, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("node %q: marshal candy env: %w", gn.name, err)
	}
	out, err := prov.Invoke(context.Background(), &Operation{Reserved: gn.disc, Op: OpLoad, Env: envJSON})
	if err != nil {
		return fmt.Errorf("node %q: candy kind: %w", gn.name, err)
	}
	if image {
		var b spec.BoxConfig
		if err := json.Unmarshal(out.JSON, &b); err != nil {
			return fmt.Errorf("node %q: candy image reply decode: %w", gn.name, err)
		}
		uf.SetBox(gn.name, b)
		return nil
	}
	var c spec.CandyYAML
	if err := json.Unmarshal(out.JSON, &c); err != nil {
		return fmt.Errorf("node %q: candy layer reply decode: %w", gn.name, err)
	}
	uf.SetCandy(gn.name, &InlineCandy{CandyYAML: c})
	return nil
}

// validateKindValueCUE validates an externalized structural kind node's authored VALUE against
// the KEPT #<Kind>Value def — the host-side replacement for the removed #Node arm's closedness
// (a typo'd field in a `vm:`/`pod:`/`candy:` value is a hard load error, exactly as before).
// Only a MAPPING value is gated: a SCALAR value is a cross-ref (`pod: coder`), which carries no
// authored fields to typo-check (it is resolved at deploy). The RAW authored value is validated
// (shorthand intact) since #<Kind>Value accepts the same shorthand the arm did. Covers the 5
// substrate kinds (#<Kind>Value) AND candy (#CandyValue).
// entityBodyJSON returns a node's kind-value mapping as canonical JSON, generically
// (yaml.v3 → map[string]any → JSON) — with NO concrete-kind Go type. It is the single
// body→wire mechanism for BOTH the op.Params plugin-kind path (runPluginKind) and the
// substrate TEMPLATE thread (decodeStandaloneTemplateJSON), so the kernel never types a
// spec.<Kind> merely to canonicalize a value. The value is validated kind-blind
// (validateKindValueCUE against the data-driven #<Kind>Value def, or the load-time gate),
// and every plugin OpLoad re-decodes + defaults + echoes, so no host-side typed decode is
// needed.
func entityBodyJSON(gn *genericNode) (json.RawMessage, error) {
	body, err := assembleEntityBody(gn)
	if err != nil {
		return nil, fmt.Errorf("node %q: assemble: %w", gn.name, err)
	}
	yamlBytes, err := yaml.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("node %q: marshal: %w", gn.name, err)
	}
	// YAML → JSON for the wire envelope (yaml.v3 decodes mappings as
	// map[string]any, which marshals to JSON cleanly).
	var asMap any
	if err := yaml.Unmarshal(yamlBytes, &asMap); err != nil {
		return nil, fmt.Errorf("node %q: reparse: %w", gn.name, err)
	}
	j, err := json.Marshal(asMap)
	if err != nil {
		return nil, fmt.Errorf("node %q: to json: %w", gn.name, err)
	}
	return j, nil
}

func validateKindValueCUE(gn *genericNode) error {
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
	// KNOWN GAP (RCA'd during the dead-code-radical-removal batch, NOT introduced by it — the
	// now-deleted validateEntityCUE was the only concrete check and it already had zero
	// production callers before this batch touched anything): non-concrete validation here
	// lets a required-but-unset field slip through (e.g. a vm PCI hostdev's slot/function).
	// A blanket cue.Concrete(true) fix was attempted and REVERTED — it broke 9+ real cases
	// across vm/pod/local/k8s/candy (TestCueKinds_Corpus, TestBundleCompileParity_*,
	// TestPreresolveActiveInitInto_*, TestCompileServiceSteps_*, TestBuildDeployPlan*,
	// TestInvokeProvider_LazyConnectFallback*), not just candy's known base⊻from disjunction
	// issue — RDD-verified live via go test ./... catching each one. The correct fix needs
	// per-field concreteness (or a narrower, kind/field-scoped check) rather than a blanket
	// flip; tracked as its own named, registered program exit — the VALIDATION-CORRECTNESS
	// BATCH — not silently dropped (see the batch's CHANGELOG and the program register).
	if verr := entity.Unify(def).Validate(); verr != nil {
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
