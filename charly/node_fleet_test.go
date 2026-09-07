package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// fleetNodeForm is the COMPACT node-form (the only authoring surface): each
// member's inline checks live in the member's own `plan:` list INSIDE the kind
// value, and the deeply-nested pod-in-pod is a sub-ENTITY child. `image: coder`
// is a scalar cross-ref and stays in the value.
const fleetNodeForm = `
shop:
  pod:
    disposable: true
    image: coder
    plan:
      - check: web reaches the cache
        command: "redis-cli -h ${HOST:cache} ping"
  cache:
    pod:
      image: coder
    migrate:
      pod:
        image: migrator
        plan:
          - check: migration ran
            command: "test -f /done"
`

// TestBuildFleetNode_Structure proves the fleet builder turns the unified
// node-form into the correct FleetNode tree: a disposable pod PRIMARY (the
// post-migrate member-tree spelling — the former group bed's first member
// promoted, Cutover C task 1) with an inline cross-member check in its own
// Plan, one deploy-level pod sibling (Peer), and a deeply-nested pod-in-pod
// (Nested) with its own inline check.
func TestBuildFleetNode_Structure(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(fleetNodeForm), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, err := genericNodesFromDoc(&doc)
	if err != nil {
		t.Fatalf("genericNodesFromDoc: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 top node, got %d", len(nodes))
	}
	pn, err := genericToParsedNode(nodes[0])
	if err != nil {
		t.Fatalf("genericToParsedNode: %v", err)
	}
	dn, err := requireProjectLoader().BuildFleetNode(pn, loaderThreaded())
	if err != nil {
		t.Fatalf("BuildFleetNode: %v", err)
	}
	if dn.Target != "pod" {
		t.Errorf("fleet primary Target = %q, want pod", dn.Target)
	}
	if dn.Image != "coder" {
		t.Errorf("fleet primary image = %q, want coder (the promoted first member's body)", dn.Image)
	}
	if dn.Disposable == nil || !*dn.Disposable {
		t.Errorf("fleet disposable = %v, want true", dn.Disposable)
	}
	if len(dn.Plan) != 1 || dn.Plan[0].Check == "" {
		t.Fatalf("primary inline check missing: %+v", dn.Plan)
	}
	if len(dn.Member) != 1 {
		t.Fatalf("want 1 deploy-level member, got %d", len(dn.Member))
	}
	cache := dn.MemberByName("cache")
	if cache == nil || cache.Node == nil || cache.Node.Target != "pod" || cache.Node.Image != "coder" {
		t.Fatalf("cache member wrong: %+v", cache)
	}
	if cache.Node.MemberByName("migrate") == nil {
		t.Fatalf("cache.migrate nested member missing: %+v", cache)
	}
	migrate := cache.Node.MemberByName("migrate")
	if migrate.Node.Target != "pod" || migrate.Node.Image != "migrator" {
		t.Errorf("migrate member wrong: target=%q box=%q", migrate.Node.Target, migrate.Node.Image)
	}
	if len(migrate.Node.Plan) != 1 || migrate.Node.Plan[0].Check == "" {
		t.Errorf("migrate inline check missing: %+v", migrate.Node.Plan)
	}
}
