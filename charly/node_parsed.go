package main

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// node_parsed.go — the host MATERIALIZE half of the P6/K1-unit-1 (#46) parse/materialize seam.
// The PARSE (yaml → spec.ParsedProject) runs in the loader plugin via sdk/loaderkit; here the
// host folds the ParsedProject back into the typed *loaderkit.UnifiedFile. parsedNodeToGeneric reconstructs
// the genericNode the TRUE clause-M dispatch (runPluginKind, provider_kind_invoke.go) reads from
// a ParsedNode's opaque JSON body; materializeProject / materializeNodeInto drive the registered
// spec.Materializer (candy/plugin-loader) per node — the former in-core not-found DISPATCH POLICY
// (normalizeNodeInto) moved out of core; this file keeps the accumulator seed/copy-back plumbing
// plus the genericNode reconstruction the DecodeEntity/BuildBundleEntity seam callbacks
// (loader_threaded.go) need.

// parsedNodeToGeneric reconstructs the genericNode the host-side kind DISPATCH (runPluginKind,
// buildBundleNodeInto — provider_kind_invoke.go/node_bundle.go, the TRUE clause-M mechanism) reads
// from a spec.ParsedNode: the JSON body becomes the discValue mapping node the fold + the
// substrate/candy host-pre-decode read (entityBodyMapping / buildBundleNode use gn.discValue
// only). Called from the DecodeEntity/BuildBundleEntity seam callbacks the registered
// spec.Materializer plugin calls back into — no re-entrancy back into the loader plugin.
func parsedNodeToGeneric(pn spec.ParsedNode) (*genericNode, error) {
	gn := &genericNode{name: pn.Name, disc: pn.Disc, discClass: "entity"}
	if len(pn.Body) > 0 {
		var asAny any
		if err := json.Unmarshal([]byte(pn.Body), &asAny); err != nil {
			return nil, fmt.Errorf("node %q: decode body: %w", pn.Name, err)
		}
		var dv yaml.Node
		if err := dv.Encode(asAny); err != nil {
			return nil, fmt.Errorf("node %q: encode body: %w", pn.Name, err)
		}
		gn.discValue = &dv
	}
	for _, ch := range pn.Children {
		cgn, err := parsedNodeToGeneric(*ch)
		if err != nil {
			return nil, err
		}
		gn.children = append(gn.children, cgn)
	}
	return gn, nil
}

// materializeNodeInto folds ONE parsed node into uf via the registered spec.Materializer plugin
// (K1 unit 1, #46) — the former in-core normalizeNodeInto's not-found policy now lives in
// candy/plugin-loader (loaderkit.Materialize), reached through requireMaterializer(); the actual
// registry resolve + provider dispatch stays host-side behind the DecodeEntity/BuildBundleEntity
// seam callbacks (hostMaterializeSeams, loader_threaded.go) — clause M never leaves charly/core.
//
// Pre-seeds the spec.MaterializedProject accumulator from uf's CURRENT maps (so repeated calls
// across a document's node list ACCUMULATE rather than reset — maps are reference types, so this
// is a cheap map-header copy, not a deep copy) and copies the result back after.
func materializeNodeInto(pn spec.ParsedNode, uf *loaderkit.UnifiedFile) error {
	acc := spec.MaterializedProject{
		Box: uf.Box, Candy: uf.Candy, Bundle: uf.Bundle, PluginKinds: uf.PluginKinds,
	}
	if err := requireMaterializer().MaterializeNode(pn, loaderThreaded(), hostMaterializeSeams(), &acc); err != nil {
		return err
	}
	uf.Box, uf.Candy, uf.Bundle, uf.PluginKinds = acc.Box, acc.Candy, acc.Bundle, acc.PluginKinds
	return nil
}

// genericToParsedNode is the INVERSE of parsedNodeToGeneric: it rebuilds the wire-safe
// spec.ParsedNode a *genericNode fixture represents. Production code never needs this (a real
// genericNode is always reconstructed FROM a spec.ParsedNode, never the other way around) — it
// exists solely so tests that build a *genericNode fixture by hand (rather than parsing real YAML)
// can still exercise the K1 unit-1 Materializer pipeline via normalizeNodeInto below.
func genericToParsedNode(gn *genericNode) (spec.ParsedNode, error) {
	pn := spec.ParsedNode{Name: gn.name, Disc: gn.disc}
	if gn.discValue != nil {
		var asAny any
		if err := gn.discValue.Decode(&asAny); err != nil {
			return spec.ParsedNode{}, fmt.Errorf("node %q: decode value: %w", gn.name, err)
		}
		b, err := json.Marshal(asAny)
		if err != nil {
			return spec.ParsedNode{}, fmt.Errorf("node %q: marshal value: %w", gn.name, err)
		}
		pn.Body = json.RawMessage(b)
	}
	for _, ch := range gn.children {
		cpn, err := genericToParsedNode(ch)
		if err != nil {
			return spec.ParsedNode{}, err
		}
		pn.Children = append(pn.Children, &cpn)
	}
	return pn, nil
}

// normalizeNodeInto is the TEST-FACING bridge into the K1 unit-1 Materializer pipeline: it
// converts gn back to a spec.ParsedNode (genericToParsedNode) and runs materializeNodeInto —
// preserving the pre-K1 call shape (*genericNode, *loaderkit.UnifiedFile) for tests that build a genericNode
// fixture directly. Production load paths (materializeProject / materializeDiscoveredNode) never
// call this; they already hold a genuine spec.ParsedNode from the registered DocParser/
// ProjectWalker and call materializeNodeInto directly.
func normalizeNodeInto(gn *genericNode, uf *loaderkit.UnifiedFile) error {
	pn, err := genericToParsedNode(gn)
	if err != nil {
		return err
	}
	return materializeNodeInto(pn, uf)
}

// materializeProject folds a whole spec.ParsedProject (the loader plugin's WalkProject reply — one
// document's decomposed nodes) into the typed loaderkit.UnifiedFile, node by node via materializeNodeInto.
// This is the exact host-side entry the plugin dispatch drops into: the registered loader plugin's
// spec.ProjectWalker (candy/plugin-loader, over sdk/loaderkit.Walk) builds the
// spec.LoadedProject/ParsedProject and loaderkit.MaterializeLoadedProject (via the MaterializeProject
// host seam, #48) calls this per document.
func materializeProject(pp *spec.ParsedProject, uf *loaderkit.UnifiedFile) error {
	if pp == nil {
		return nil
	}
	for i := range pp.Nodes {
		if err := materializeNodeInto(pp.Nodes[i], uf); err != nil {
			return err
		}
	}
	return nil
}
