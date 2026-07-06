package main

// node_build.go — the GENERIC entity-body access for the compact node-form
// model. An entity node's kind value IS its complete body (scalars, collections,
// and the desugared `plan:` list all inline — see node_parse.go/node_desugar.go),
// so "assembly" reduces to a defensive clone: the body decodes through the
// EXISTING per-kind CUE decoder (decodeEntityViaCUE) unchanged, and the strict
// typing comes from the COMPLETE per-kind def (#Candy/#Deploy/…). Sub-ENTITY
// children (bundle members, nested deployments) are handled by the per-kind
// constructor (node_bundle.go), never folded here. The former data/step
// child-node fold arms were DELETED with the child-node shape.

import (
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// assembleEntityBody returns the DOCUMENT-wrapped entity-body mapping to decode:
// a CLONE of the node's kind value (members are separate children, so the value
// needs no folding).
func assembleEntityBody(gn *genericNode) (*yaml.Node, error) {
	return entityBodyMapping(gn)
}

// entityBodyMapping returns a DOCUMENT-wrapped mapping CLONE of the node's kind
// value (an empty mapping when the value is null/absent or a scalar cross-ref
// like `vm: pg-vm`, which the constructor consumes separately).
func entityBodyMapping(gn *genericNode) (*yaml.Node, error) {
	if gn.discValue == nil || gn.discValue.Kind != yaml.MappingNode {
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}, nil
	}
	clone, err := cloneYAMLNode(gn.discValue)
	if err != nil {
		return nil, err
	}
	if mappingRoot(clone) == nil {
		return nil, fmt.Errorf("node %q: %q value must be a mapping", gn.name, gn.disc)
	}
	return clone, nil
}

// decodeNodeValue decodes gn's body via the shared CUE entity decoder into out
// (a *struct).
func decodeNodeValue(gn *genericNode, out any) error {
	body, err := assembleEntityBody(gn)
	if err != nil {
		return err
	}
	return decodeEntityViaCUE(body, reflect.TypeOf(out).Elem(), out, "node "+gn.name)
}
