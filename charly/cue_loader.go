package main

// cue_loader.go — the per-entity CUE decode path. A single entity node (a candy
// body, a kind entity, an assembled node-form body, a plan-step list) is
// canonicalized in place by NormalizeEntityNode against its Go type, re-marshaled,
// CUE-ingested, and Decoded into the target struct — making CUE the universal
// decode authority for the data model. The import:/discover: graph (which drives
// composition) is decoded by yaml.v3 and resolved in Go (sdk/loaderkit/repo_identity.go, since
// W9's ns_identity.go relocation), never here. See memory cue-loader-switch-design.

import (
	"fmt"
	"reflect"

	cueyaml "cuelang.org/go/encoding/yaml"
	"gopkg.in/yaml.v3"
)

// decodeEntityViaCUE normalizes a single entity node against its Go type, then
// CUE-ingests + Decodes it into out (a pointer). Does not mutate the input node.
// The node must BE the entity value (the candy body / a single kind entity /
// an assembled node-form body), not a kind-keyed wrapper. Used by the kind-keyed
// / candy / inline / node-form decode paths.
func decodeEntityViaCUE(node *yaml.Node, t reflect.Type, out any, label string) error {
	clone, err := cloneYAMLNode(node)
	if err != nil {
		return fmt.Errorf("%s: clone: %w", label, err)
	}
	// Normalize shorthand → canonical, then CUE-ingest + Decode into the struct.
	if err := NormalizeEntityNode(clone, t); err != nil {
		return fmt.Errorf("%s: normalize: %w", label, err)
	}
	b, err := yaml.Marshal(clone)
	if err != nil {
		return fmt.Errorf("%s: re-marshal: %w", label, err)
	}
	af, err := cueyaml.Extract(label, b)
	if err != nil {
		return fmt.Errorf("%s: cue yaml ingest: %w", label, err)
	}
	cv := cueSchemaCtx.BuildFile(af)
	if cv.Err() != nil {
		return fmt.Errorf("%s: cue build: %w", label, cv.Err())
	}
	if err := cv.Decode(out); err != nil {
		return fmt.Errorf("%s: cue decode: %w", label, err)
	}
	return nil
}

// cloneYAMLNode deep-copies a node by marshal+reparse (no cycles in raw input).
func cloneYAMLNode(node *yaml.Node) (*yaml.Node, error) {
	b, err := yaml.Marshal(node)
	if err != nil {
		return nil, err
	}
	var out yaml.Node
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// mappingRoot moved to kit (kit.MappingRoot, referenced directly) — shared with
// candy/plugin-migrate's engine (R3).
