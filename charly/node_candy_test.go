package main

import (
	"github.com/opencharly/sdk/spec"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// decodeCandyKindFirst decodes a kind-first candy BODY through the same CUE entity
// decoder the loader uses (handles PackageItem string-coercion + shorthand).
func decodeCandyKindFirst(t *testing.T, body string) spec.CandyYAML {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parsing kind-first candy: %v", err)
	}
	var c spec.CandyYAML
	if err := decodeEntityViaCUE(&doc, reflect.TypeOf(spec.CandyYAML{}), &c, "kind-first"); err != nil {
		t.Fatalf("CUE-decoding kind-first candy: %v", err)
	}
	return c
}

// candyKindFirst is the candy BODY in the internal (desugared, wire) shape —
// scalars + composition refs + env map + package list + service + plan as candy
// FIELDS, with the plan step carrying the internal plugin/plugin_input envelope
// (the form the parse-time desugar produces and #Op validates).
const candyKindFirst = `
version: "2026.150.0000"
description: in-memory store
status: working
candy: [supervisord]
require: [python]
env:
  REDIS_DATA: /var/lib/redis
  REDIS_PORT: "6379"
package:
  - redis
  - redis-cli
service:
  - name: redis
    exec: /usr/bin/redis-server
plan:
  - check: the binary exists
    plugin: file
    plugin_input:
      file: /usr/bin/redis-server
`

// candyNodeForm is the SAME candy in the COMPACT authored node-form: the FULL
// body lives inline in the `candy:` discriminator value, and the plan step
// authors the `file:` plugin sugar the parse-time desugar rewrites into the
// internal envelope above.
const candyNodeForm = `
redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
    status: working
    candy: [supervisord]
    require: [python]
    env:
      REDIS_DATA: /var/lib/redis
      REDIS_PORT: "6379"
    package:
      - redis
      - redis-cli
    service:
      - name: redis
        exec: /usr/bin/redis-server
    plan:
      - check: the binary exists
        file: /usr/bin/redis-server
`

// TestBuildCandy_RoundTrip proves the compact node-form constructor (parse +
// desugar + decode) produces EXACTLY the CandyYAML the direct body decode of the
// internal wire shape produces — the non-brittleness proof (RDD-1c) for the
// richest kind. Equal structs ⇒ every downstream consumer (generate / check /
// install-plan) sees identical input from the authored compact form.
func TestBuildCandy_RoundTrip(t *testing.T) {
	want := decodeCandyKindFirst(t, candyKindFirst)

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(candyNodeForm), &doc); err != nil {
		t.Fatalf("parsing node-form candy: %v", err)
	}
	nodes, err := genericNodesFromDoc(&doc)
	if err != nil {
		t.Fatalf("genericNodesFromDoc: %v", err)
	}
	if len(nodes) != 1 || nodes[0].name != "redis" {
		t.Fatalf("expected one node 'redis', got %d nodes", len(nodes))
	}
	name, ic, err := buildCandy(nodes[0])
	if err != nil {
		t.Fatalf("buildCandy: %v", err)
	}
	if name != "redis" {
		t.Fatalf("candy name = %q, want redis", name)
	}
	// In node-form the candy NAME is the top-level key (buildCandy stamps it);
	// the kind-first body fixture carries no `name:`, so reflect that here.
	want.Name = name
	if !reflect.DeepEqual(ic.CandyYAML, want) {
		t.Fatalf("node-form candy != kind-first candy\n node-form: %#v\n kind-first: %#v", ic.CandyYAML, want)
	}
}
