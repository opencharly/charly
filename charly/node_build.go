package main

// node_build.go — the GENERIC entity assembler for the unified node-form model.
// An entity node's value holds its SCALAR config; its DATA children (package/env/
// service/route/…) and STEP children (run/check/agent-check/…) are folded back into
// the entity body — each data child appended to (or merged into) its matching
// collection field, each step child appended to `plan:` — which is then decoded
// through the EXISTING per-kind CUE decoder (decodeEntityViaCUE). The
// strict typing comes from the COMPLETE per-kind def (#Candy/#Deploy/…) on the
// assembled body, so there are no per-arm value gaps. Sub-ENTITY children (bundle
// members, nested deployments) are handled by the per-kind constructor, never
// folded here.

import (
	"fmt"
	"reflect"

	"gopkg.in/yaml.v3"
)

// assembleEntityBody clones an entity node's discriminator value (its scalar /
// object config) and folds every DATA + STEP child into the matching collection
// field, producing the DOCUMENT-wrapped entity-body mapping to decode. Sub-entity
// children are skipped (the constructor handles them).
func assembleEntityBody(gn *genericNode) (*yaml.Node, error) {
	body, err := entityBodyMapping(gn)
	if err != nil {
		return nil, err
	}
	bm := mappingRoot(body)
	for _, ch := range gn.children {
		switch ch.discClass {
		case "step":
			// Each step node (verb prose + inline Op fields) appends a plan step.
			appendSeqField(bm, "plan", ch.raw)
		case "data":
			// Each non-scalar field is one child carrying the WHOLE value (a list,
			// map, object, or composition ref-list) — set it onto the body field.
			setMapField(bm, ch.disc, ch.discValue)
		case "entity":
			// sub-entity (bundle member / nested deploy) — handled by node_bundle.go.
		}
	}
	return body, nil
}

// entityBodyMapping returns a DOCUMENT-wrapped mapping CLONE of the node's
// discriminator value (an empty mapping when the value is null/absent or a scalar
// cross-ref like `vm: pg-vm`, which the constructor consumes separately).
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

// decodeNodeValue assembles gn's body (value + folded data/step children) and
// decodes it via the shared CUE entity decoder into out (a *struct).
func decodeNodeValue(gn *genericNode, out any) error {
	body, err := assembleEntityBody(gn)
	if err != nil {
		return err
	}
	return decodeEntityViaCUE(body, reflect.TypeOf(out).Elem(), out, "node "+gn.name)
}

// appendSeqField appends item to the sequence stored under key in mapping m,
// creating the key (as an empty sequence) on first use.
func appendSeqField(m *yaml.Node, key string, item *yaml.Node) {
	seq := mapValue(m, key)
	if seq == nil {
		seq = &yaml.Node{Kind: yaml.SequenceNode}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, seq)
	}
	seq.Content = append(seq.Content, item)
}

// setMapField sets val under key in mapping m (replacing an existing value,
// appending the key on first use). One data child carries a field's whole value.
func setMapField(m *yaml.Node, key string, val *yaml.Node) {
	if existing := mapValue(m, key); existing != nil {
		*existing = *val
		return
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, val)
}
