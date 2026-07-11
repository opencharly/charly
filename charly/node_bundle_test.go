package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// bundleNodeForm is the COMPACT node-form (the only authoring surface): each
// member's inline checks live in the member's own `plan:` list INSIDE the kind
// value, and the deeply-nested pod-in-pod is a sub-ENTITY child. `image: coder`
// is a scalar cross-ref and stays in the value.
const bundleNodeForm = `
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

// TestBuildBundleNode_Structure proves the bundle builder turns the unified
// node-form into the correct BundleNode tree: a disposable group with two
// alongside pod members (Peer), an inline cross-member check in a member's Plan,
// and a deeply-nested pod-in-pod (Nested) with its own inline check.
func TestBuildBundleNode_Structure(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(bundleNodeForm), &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	nodes, err := genericNodesFromDoc(&doc)
	if err != nil {
		t.Fatalf("parseNodeTree: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 top node, got %d", len(nodes))
	}
	dn, err := buildBundleNode(nodes[0])
	if err != nil {
		t.Fatalf("buildBundleNode: %v", err)
	}
	if dn.Target != "" {
		t.Errorf("bundle group Target = %q, want empty (group)", dn.Target)
	}
	if dn.Disposable == nil || !*dn.Disposable {
		t.Errorf("bundle disposable = %v, want true", dn.Disposable)
	}
	if len(dn.Members) != 2 {
		t.Fatalf("want 2 alongside members, got %d", len(dn.Members))
	}
	web := dn.Members["web"]
	if web == nil || web.Target != "pod" || web.Image != "coder" {
		t.Fatalf("web member wrong: %+v", web)
	}
	if len(web.Plan) != 1 || web.Plan[0].Check == "" {
		t.Fatalf("web inline check missing: %+v", web.Plan)
	}
	cache := dn.Members["cache"]
	if cache == nil || cache.Children["migrate"] == nil {
		t.Fatalf("cache.migrate nested member missing: %+v", cache)
	}
	migrate := cache.Children["migrate"]
	if migrate.Target != "pod" || migrate.Image != "migrator" {
		t.Errorf("migrate member wrong: target=%q box=%q", migrate.Target, migrate.Image)
	}
	if len(migrate.Plan) != 1 || migrate.Plan[0].Check == "" {
		t.Errorf("migrate inline check missing: %+v", migrate.Plan)
	}
}
