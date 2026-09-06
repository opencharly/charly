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
  group:
    disposable: true
  web:
    pod:
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
// node-form into the correct FleetNode tree: a disposable group with two
// alongside pod members (Peer), an inline cross-member check in a member's Plan,
// and a deeply-nested pod-in-pod (Nested) with its own inline check.
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
	if dn.Target != "" {
		t.Errorf("fleet group Target = %q, want empty (group)", dn.Target)
	}
	if dn.Disposable == nil || !*dn.Disposable {
		t.Errorf("fleet disposable = %v, want true", dn.Disposable)
	}
	if len(dn.Member) != 2 {
		t.Fatalf("want 2 members, got %d", len(dn.Member))
	}
	web := dn.MemberByName("web")
	if web == nil || web.Node == nil || web.Node.Target != "pod" || web.Node.Image != "coder" {
		t.Fatalf("web member wrong: %+v", web)
	}
	if len(web.Node.Plan) != 1 || web.Node.Plan[0].Check == "" {
		t.Fatalf("web inline check missing: %+v", web.Node.Plan)
	}
	cache := dn.MemberByName("cache")
	if cache == nil || cache.Node == nil || cache.Node.MemberByName("migrate") == nil {
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
