package main

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// decodeCandyKindFirst decodes a kind-first candy BODY through the same CUE entity
// decoder the loader uses (handles PackageItem string-coercion + shorthand).
func decodeCandyKindFirst(t *testing.T, body string) CandyYAML {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("parsing kind-first candy: %v", err)
	}
	var c CandyYAML
	if err := decodeEntityViaCUE(&doc, reflect.TypeOf(CandyYAML{}), &c, "kind-first"); err != nil {
		t.Fatalf("CUE-decoding kind-first candy: %v", err)
	}
	return c
}

// candyKindFirst is the candy VALUE in the legacy kind-first shape (scalars +
// composition refs + env map + package list + service + plan as candy FIELDS).
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
    file: /usr/bin/redis-server
`

// candyNodeForm is the SAME candy in the unified node-form: ONLY scalars
// (version/description/status) stay in the `candy:` discriminator value; every
// non-scalar — the composition refs, env map, package list, service, and the
// check step — becomes a name-first CHILD node (`<name>-<datakey>` for data,
// `<name>-step-N` for a plan step).
const candyNodeForm = `
redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
    status: working
  redis-candy:
    candy: [supervisord]
  redis-require:
    require: [python]
  redis-env:
    env:
      REDIS_DATA: /var/lib/redis
      REDIS_PORT: "6379"
  redis-package:
    package:
      - redis
      - redis-cli
  redis-service:
    service:
      - name: redis
        exec: /usr/bin/redis-server
  redis-step-0:
    check: the binary exists
    file: /usr/bin/redis-server
`

// TestBuildCandy_RoundTrip proves the clean node-form constructor produces EXACTLY
// the CandyYAML the legacy kind-first decode produces — the non-brittleness proof
// (RDD-1c) for the richest kind. Equal structs ⇒ every downstream consumer
// (generate / check / install-plan) sees identical input from node-form.
func TestBuildCandy_RoundTrip(t *testing.T) {
	want := decodeCandyKindFirst(t, candyKindFirst)

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(candyNodeForm), &doc); err != nil {
		t.Fatalf("parsing node-form candy: %v", err)
	}
	_, nodes, err := parseNodeTree(&doc)
	if err != nil {
		t.Fatalf("parseNodeTree: %v", err)
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
