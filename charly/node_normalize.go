package main

// node_normalize.go — kind-decode SUPPORT helpers consumed by the TRUE clause-M dispatch
// (provider_kind_invoke.go's runPluginKind/foldSubstrateKind) — the standalone-template shape
// detection + fold, plus the generic ensureMap helper. This file is the CORE-RESIDENT half of the
// node-form kind-decode split; its former per-node DISPATCH ORCHESTRATOR (normalizeNodeInto — the
// not-found policy: route to the fleet builder / defer-during-connect-pass / warn-and-skip / hard
// error) MOVED to candy/plugin-loader as the spec.Materializer seam (K1 unit 1, #46) — see
// charly/loader_threaded.go (hostMaterializeSeams/decodeEntityViaRegistry/
// buildFleetEntityViaRegistry) + sdk/loaderkit/materialize.go. The entity-body assembly +
// fleet/resource-member tree-builder mechanism (isDeployShape/decodeStandaloneTemplateJSON/
// resourceChildren, formerly here) is now sdk/loaderkit (K1 unit 3b), reached directly through
// requireProjectLoader() from provider_kind_invoke.go — no core wrapper survives them, since their
// only callers threaded pn straight into the seam.
//
// The legacy kind-keyed routing (the kind-first decode + per-kind document wrappers) was
// DELETED in the #NodeDoc-sole-gate cutover — a legacy kind-keyed / root-shape document is now
// hard-rejected at kit.ClassifyDoc with a `charly migrate` hint. Every kind flows through the
// ONE generic value-decoder (sdk/loaderkit.DecodeNodeValue), so node-form yields the exact same
// domain structs the kind-first decode produced (proven by the *_RoundTrip tests).

import (
	"encoding/json"

	"github.com/opencharly/spec/spec"
)

// isStandaloneResourceKind is now sdk/loaderkit.IsStandaloneResourceKind (K1 unit 3a) — a pure
// function of the registry-derived spec.Threaded snapshot. This file keeps a same-named/
// same-signature core wrapper (R3) since provider_kind_invoke.go calls it by this name.
func isStandaloneResourceKind(disc string) bool {
	return requireProjectLoader().IsStandaloneResourceKind(disc, loaderThreaded())
}

// foldStandaloneTemplateReply is now sdk/loaderkit.FoldStandaloneTemplateReply (K1 unit 3a) — the
// C2-substrate TEMPLATE fold arm (the standalone counterpart of runPluginKind's deploy fold into
// acc.Fleet). GENERIC by construction: no per-kind-word switch — every standalone-template kind
// (vm/pod/kubernetes/local/android) folds into the SAME map[disc][name] shape PluginKinds already uses
// for every other templated kind. This file keeps a same-named/same-signature core wrapper (R3)
// since provider_kind_invoke.go calls it by this name.
func foldStandaloneTemplateReply(disc, name string, replyJSON json.RawMessage, acc *spec.MaterializedProject) error {
	return requireProjectLoader().FoldStandaloneTemplateReply(disc, name, replyJSON, acc)
}

// ensureMap allocates a nil map[string]V in place.
func ensureMap[V any](m *map[string]V) {
	if *m == nil {
		*m = map[string]V{}
	}
}
