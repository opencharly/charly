package main

// node_normalize.go — kind-decode SUPPORT helpers consumed by the TRUE clause-M dispatch
// (provider_kind_invoke.go's runPluginKind/foldSubstrateKind, node_bundle.go's
// buildBundleNodeInto) — the standalone-template shape detection + fold, plus the generic
// ensureMap helper. This file is the CORE-RESIDENT half of the node-form kind-decode split;
// its former per-node DISPATCH ORCHESTRATOR (normalizeNodeInto — the not-found policy: route
// to the bundle builder / defer-during-connect-pass / warn-and-skip / hard error) MOVED to
// candy/plugin-loader as the spec.Materializer seam (K1 unit 1, #46) — see
// charly/loader_threaded.go (hostMaterializeSeams/decodeEntityViaRegistry/
// buildBundleEntityViaRegistry) + sdk/loaderkit/materialize.go. The former in-proc KindProvider
// fast path that normalizeNodeInto also carried was DEAD code (spec.KindWords has been
// permanently empty since every authoring kind externalized — see provider_kind.go) and was NOT
// ported; the plugin's DecodeEntity seam call always dispatches via runPluginKind now, exactly
// as it already did in production (R1/R2 dead-code cleanup folded into this cutover).
//
// The legacy kind-keyed routing (the kind-first decode + per-kind document wrappers) was
// DELETED in the #NodeDoc-sole-gate cutover — a legacy kind-keyed / root-shape document is now
// hard-rejected at kit.ClassifyDoc with a `charly migrate` hint. Every kind flows through the
// ONE generic value-decoder (node_build.go), so node-form yields the exact same domain structs
// the kind-first decode produced (proven by the *_RoundTrip tests).

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// isStandaloneResourceKind reports whether disc names one of the 5 substrate kinds
// (pod/vm/k8s/local/android) — the kinds that are BOTH a standalone TEMPLATE (→ the typed
// map uf.Pod/uf.VM/…) and a deploy (→ uf.Bundle). DATA-driven via deployTraitsFor (P9's
// plugin-declared #DeployTraits) — the SAME kind-blind fact bundleTargetForDisc/setBundleCrossRef
// (node_bundle.go) already resolve against — rather than a hand-kept kind-word switch (the
// boundary law's self-test; this switch previously reincarnated the R5-deleted
// buildStandaloneResource switch verbatim, a regression this fix closes for good). group is a
// structural kind too but resolves false here — it declares no #DeployTraits (it has no
// per-substrate template map; it always folds to uf.Bundle). Keep in lockstep with
// decodeStandaloneTemplateJSON / foldStandaloneTemplateReply / substrateValueDef.
func isStandaloneResourceKind(disc string) bool {
	return deployTraitsFor(disc) != nil
}

// isDeployShape reports whether a substrate node is a DEPLOY (vs a standalone template): a
// scalar discriminator value (`vm: pg-vm` / `pod: img`) is a cross-ref deploy, and a mapping
// value carrying `from:` or `image:` is a deploy. (Moved from the R5-deleted plugin_substrate.go
// when standaloneKind externalized to candy/plugin-substrate.)
func isDeployShape(gn *genericNode) bool {
	dv := gn.discValue
	if dv == nil {
		return false
	}
	if dv.Kind == yaml.ScalarNode {
		return dv.Value != ""
	}
	if dv.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(dv.Content); i += 2 {
			if k := dv.Content[i].Value; k == "from" || k == "image" {
				return true
			}
		}
	}
	return false
}

// decodeStandaloneTemplateJSON canonicalizes gn (a substrate TEMPLATE node — no cross-ref,
// no resource members) to the JSON the host threads to candy/plugin-substrate (op.Env),
// GENERICALLY via entityBodyJSON — with NO concrete-kind Go type. The value is validated
// kind-blind (validateKindValueCUE against the data-driven #<Kind>Value def, plus the load
// gate); the plugin OpLoad re-decodes + defaults + echoes. Shares entityBodyJSON with
// runPluginKind's op.Params path (R3: one body→wire mechanism).
func decodeStandaloneTemplateJSON(gn *genericNode) (json.RawMessage, error) {
	if !isStandaloneResourceKind(gn.disc) {
		return nil, fmt.Errorf("node %q: %q is not a standalone resource kind", gn.name, gn.disc)
	}
	// Generic canonicalization — no concrete-kind Go type. The value was already
	// validated kind-blind (validateKindValueCUE / load gate); the plugin OpLoad
	// re-decodes + defaults + echoes. See entityBodyJSON.
	return entityBodyJSON(gn)
}

// foldStandaloneTemplateReply folds candy/plugin-substrate's ECHOED template JSON into the
// right typed template map by kind — the C2-substrate TEMPLATE fold arm (the standalone
// counterpart of runPluginKind's deploy fold into acc.Bundle). The former in-proc path
// decoded straight into the map (buildStandaloneResource → decodePtrInto); here the
// canonical value round-trips through the plugin first (RDD-proven byte-faithful). acc is the
// K1-unit-1 spec.MaterializedProject accumulator (see runPluginKind's doc comment).
func foldStandaloneTemplateReply(disc, name string, replyJSON json.RawMessage, acc *spec.MaterializedProject) error {
	switch disc {
	case "vm":
		return foldOpaqueTemplateReply(name, replyJSON, &acc.VM)
	case "pod":
		return foldOpaqueTemplateReply(name, replyJSON, &acc.Pod)
	case "k8s":
		return foldOpaqueTemplateReply(name, replyJSON, &acc.K8s)
	case "local":
		return foldOpaqueTemplateReply(name, replyJSON, &acc.Local)
	case "android":
		return foldOpaqueTemplateReply(name, replyJSON, &acc.Android)
	}
	return fmt.Errorf("node %q: %q is not a standalone resource kind", name, disc)
}

// foldOpaqueTemplateReply stores the echoed template JSON VERBATIM at name in *m —
// the opaque counterpart for de-typed substrate templates (local/android, Cutover I):
// the kernel keeps the body opaque and resolves it via candy/plugin-substrate on read.
func foldOpaqueTemplateReply(name string, replyJSON json.RawMessage, m *map[string]json.RawMessage) error {
	if *m == nil {
		*m = map[string]json.RawMessage{}
	}
	(*m)[name] = replyJSON
	return nil
}

// resourceChildren returns gn's children whose discriminator is itself a
// resource/bundle kind (the markers of a bundle-shaped node). The deployable set
// is the CUE-derived resourceKindSet (#ResourceKind).
func resourceChildren(gn *genericNode) []*genericNode {
	var out []*genericNode
	for _, ch := range gn.children {
		if resourceKindSet[ch.disc] {
			out = append(out, ch)
		}
	}
	return out
}

// ensureMap allocates a nil map[string]V in place.
func ensureMap[V any](m *map[string]V) {
	if *m == nil {
		*m = map[string]V{}
	}
}
