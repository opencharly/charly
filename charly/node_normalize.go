package main

// node_normalize.go — the normalizer dispatcher: turn a parsed genericNode into
// its domain struct and register it in the UnifiedFile. This is the node-form
// authoring surface's decode path, and the ONLY one: the legacy kind-keyed
// routing (the kind-first decode + per-kind document wrappers) was DELETED in the
// #NodeDoc-sole-gate cutover — a legacy kind-keyed / root-shape document is now
// hard-rejected at classifyDoc with a `charly migrate` hint. Every kind flows
// through the ONE generic value-decoder (node_build.go),
// so node-form yields the exact same domain structs the kind-first decode
// produced (proven by the *_RoundTrip tests).

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// normalizeNodeInto decodes one top-level entity node into uf's matching map by
// resolving the node's discriminator to its provider — the per-kind decode switch is
// gone (C2). The ONLY in-proc KindProvider left is candy (plugin_candy.go, the box⊻layer
// factory); every other kind routes to runPluginKind: the tier-1 kinds + group
// (candy/plugin-group) + the 5 substrate kinds pod/vm/k8s/local/android
// (candy/plugin-substrate — C2-substrate). runPluginKind folds a group-style reply into
// uf.Bundle and a substrate reply into uf.Bundle (deploy shape) or the typed template map
// uf.Pod/uf.VM/… (template shape — the C2-substrate TEMPLATE fold arm). buildBundleNode
// (node_bundle.go) / decodeStandaloneTemplateJSON (below) are the SINGLE host-side decode
// path both the deploy and template shapes share (R3).
func normalizeNodeInto(gn *genericNode, uf *UnifiedFile) error {
	prov, ok := providerRegistry.ResolveKind(gn.disc)
	if !ok {
		// An external DEPLOY substrate word (e.g. `exampledeploy`) at a deploy's edge
		// is not a KIND — it routes to the bundle builder, the same path the
		// deploy-shape kinds (pod/vm/k8s/local/android/group) take. Recognized via a
		// connected OR pre-scanned deploy provider (plugin_prescan.go), so the bed
		// parses before the out-of-process provider connects (loadProjectPlugins);
		// the actual Add still dispatches through the connected grpcProvider.
		if recognizedDeploySubstrate(gn.disc) {
			return buildBundleNodeInto(gn, uf)
		}
		// An external KIND (F4): declared by a project plugin candy whose out-of-process
		// provider has not registered. During the connect pre-pass's nested scan
		// (inKindConnectPass) the provider is not connected YET — DEFER (skip, no error) so the
		// nested ScanCandy/LoadUnified succeeds; the OUTER load decodes it after
		// connectDeclaredKindPlugins runs (depth-0, before this). OUTSIDE the pre-pass the
		// connect already ran, so a still-missing provider means the plugin FAILED to build or
		// connect (e.g. a minimal container with no Go toolchain). GRACEFULLY SKIP the node with
		// a loud warning — never a hard load error — so read-only commands (box list, validate)
		// still work in a degraded environment. The kind is warned, not silently dropped; a
		// deploy/build that actually USES this kind resolves the entity by name and fails loudly
		// there (the node is absent), so the missing provider is never masked where it matters.
		if recognizedKind(gn.disc) {
			if inKindConnectPass() {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Warning: node %q: kind %q is declared by a plugin whose provider did not connect (build/connect failed); skipping the node — any command that uses it will fail loudly at that point\n", gn.name, gn.disc)
			return nil
		}
		return fmt.Errorf("node %q: unsupported discriminator %q", gn.name, gn.disc)
	}
	// Built-in kind: the typed DecodeNode fast path (no JSON). External plugin kind:
	// the serializable Invoke envelope (runPluginKind) — the E3 generalization of the
	// verb dual-path to the kind class.
	if kp, ok := prov.(KindProvider); ok {
		return kp.DecodeNode(gn, uf)
	}
	return runPluginKind(prov, gn, uf)
}

// isStandaloneResourceKind reports whether disc names one of the 5 substrate kinds
// (pod/vm/k8s/local/android) — the kinds that are BOTH a standalone TEMPLATE (→ the typed
// map uf.Pod/uf.VM/…) and a deploy (→ uf.Bundle). C2-substrate externalized their decode
// PROVIDER to candy/plugin-substrate; this small explicit set is the core knowledge the
// template/deploy fold needs (the typed maps + #<Kind>Value defs are inherently core). It
// is the C2-substrate analogue of the former buildStandaloneResource switch (R5-deleted):
// group is a structural kind too but is NOT here (it has no per-substrate template map —
// it always folds to uf.Bundle). The SINGLE authority; keep in lockstep with
// decodeStandaloneTemplateJSON / foldStandaloneTemplateReply / substrateValueDef.
func isStandaloneResourceKind(disc string) bool {
	switch disc {
	case "pod", "vm", "k8s", "local", "android":
		return true
	}
	return false
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

// decodeStandaloneTemplateJSON decodes gn (a substrate TEMPLATE node — no cross-ref, no
// resource members) into its typed spec value via the core decodeNodeValue and returns the
// CANONICAL JSON the host threads to candy/plugin-substrate (op.Env). The per-substrate
// decode mirrors the former buildStandaloneResource (R3: one decode path, decodeNodeValue).
func decodeStandaloneTemplateJSON(gn *genericNode) (json.RawMessage, error) {
	switch gn.disc {
	case "vm":
		return decodeTemplateValueJSON[VmSpec](gn)
	case "pod":
		return decodeTemplateValueJSON[PodSpec](gn)
	case "k8s":
		return decodeTemplateValueJSON[K8sSpec](gn)
	case "local":
		return decodeTemplateValueJSON[LocalSpec](gn)
	case "android":
		return decodeTemplateValueJSON[AndroidSpec](gn)
	}
	return nil, fmt.Errorf("node %q: %q is not a standalone resource kind", gn.name, gn.disc)
}

// decodeTemplateValueJSON decodes gn's body into a fresh T (the typed template value) and
// marshals it to canonical JSON.
func decodeTemplateValueJSON[T any](gn *genericNode) (json.RawMessage, error) {
	var v T
	if err := decodeNodeValue(gn, &v); err != nil {
		return nil, err
	}
	return json.Marshal(&v)
}

// foldStandaloneTemplateReply folds candy/plugin-substrate's ECHOED template JSON into the
// right typed template map by kind — the C2-substrate TEMPLATE fold arm (the standalone
// counterpart of runPluginKind's deploy fold into uf.Bundle). The former in-proc path
// decoded straight into the map (buildStandaloneResource → decodePtrInto); here the
// canonical value round-trips through the plugin first (RDD-proven byte-faithful).
func foldStandaloneTemplateReply(disc, name string, replyJSON json.RawMessage, uf *UnifiedFile) error {
	switch disc {
	case "vm":
		return foldTemplateReply(name, replyJSON, &uf.VM)
	case "pod":
		return foldOpaqueTemplateReply(name, replyJSON, &uf.Pod)
	case "k8s":
		return foldOpaqueTemplateReply(name, replyJSON, &uf.K8s)
	case "local":
		return foldOpaqueTemplateReply(name, replyJSON, &uf.Local)
	case "android":
		return foldOpaqueTemplateReply(name, replyJSON, &uf.Android)
	}
	return fmt.Errorf("node %q: %q is not a standalone resource kind", name, disc)
}

// foldTemplateReply unmarshals the echoed template JSON into a fresh *T and stores it at
// name in *m (allocating on first use) — the typed-map counterpart of decodePtrInto.
func foldTemplateReply[T any](name string, replyJSON json.RawMessage, m *map[string]*T) error {
	var v T
	if err := json.Unmarshal(replyJSON, &v); err != nil {
		return err
	}
	if *m == nil {
		*m = map[string]*T{}
	}
	(*m)[name] = &v
	return nil
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
