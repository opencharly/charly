package main

import (
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// node_parsed.go — the host MATERIALIZE half of the P6 parse/materialize seam. The PARSE
// (yaml → spec.ParsedProject) runs in the loader plugin via sdk/loaderkit; here the host folds
// the ParsedProject back into the typed *UnifiedFile. parsedNodeToGeneric reconstructs the
// genericNode the kind fold reads from a ParsedNode's opaque JSON body, and materializeProject /
// materializeParsedNode drive normalizeNodeInto per node.

// parsedNodeToGeneric reconstructs the genericNode the host MATERIALIZER folds (via
// normalizeNodeInto) from a spec.ParsedNode: the JSON body becomes the discValue mapping node
// the fold + the substrate/candy host-pre-decode read (entityBodyMapping / buildBundleNode use
// gn.discValue only). The materialize runs entirely host-side on this reconstruction — no
// re-entrancy back into the loader plugin.
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

// materializeParsedNode folds one spec.ParsedNode (a loader-plugin parse output) into the typed
// UnifiedFile: it reconstructs the genericNode the kind fold reads and runs the SAME
// normalizeNodeInto the direct in-core parse used (R3). The host MATERIALIZE half of the seam —
// when the parse relocates to candy/plugin-loader, the plugin's ParsedProject nodes reach this
// unchanged.
func materializeParsedNode(pn spec.ParsedNode, uf *UnifiedFile) error {
	gn, err := parsedNodeToGeneric(pn)
	if err != nil {
		return err
	}
	return normalizeNodeInto(gn, uf)
}

// materializeProject folds a whole spec.ParsedProject (the loader plugin's OpLoad reply — one
// document's decomposed nodes) into the typed UnifiedFile, node by node. This is the exact
// host-side entry the plugin dispatch drops into: today mergeUnifiedDocs builds the
// ParsedProject in-core then calls this; when the parse relocates, the same ParsedProject
// arrives from candy/plugin-loader unchanged.
func materializeProject(pp *spec.ParsedProject, uf *UnifiedFile) error {
	if pp == nil {
		return nil
	}
	for i := range pp.Nodes {
		if err := materializeParsedNode(pp.Nodes[i], uf); err != nil {
			return err
		}
	}
	return nil
}
