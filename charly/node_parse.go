package main

// node_parse.go — the parsed node-form entity TYPE. The node-form PARSE itself (yaml → the
// generic spec.ParsedProject) moved to sdk/loaderkit (P6), served by the loader plugin candy;
// core reconstructs this genericNode from a ParsedNode via parsedNodeToGeneric (node_parsed.go)
// for the materialize half (normalizeNodeInto) + the genericNode consumers (candyIsImage,
// validateEntityNodeRec, buildCandy). Only the TYPE stays here.

import (
	"gopkg.in/yaml.v3"
)

// genericNode is one parsed node-form entity — the materialize-side reconstruction of a
// spec.ParsedNode (name + kind discriminator + the kind-value body + sub-entity members).
type genericNode struct {
	name      string         // the node's key (its name)
	disc      string         // the kind discriminator
	discClass string         // always "entity" (kept for downstream switches)
	discValue *yaml.Node     // the kind value: the COMPLETE entity body
	children  []*genericNode // sub-ENTITY members (deployable kinds only)
}
