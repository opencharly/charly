package main

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// substrateNodeFromYAML parses a single-entity node-form doc and returns its top-level
// genericNode (for the C2-substrate byte-equivalence proof).
func substrateNodeFromYAML(t *testing.T, doc string) *genericNode {
	t.Helper()
	var ydoc yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &ydoc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, nodes, err := parseNodeTree(&ydoc)
	if err != nil {
		t.Fatalf("parseNodeTree: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("want 1 node, got %d", len(nodes))
	}
	return nodes[0]
}

// TestSubstrateKind_BothShapesByteEquivalent is the C2-substrate acceptance proof: the
// COMPILED-IN candy/plugin-substrate structural-kind seam (foldSubstrateKind → host pre-decode
// → plugin ECHO → fold) produces a result BYTE-EQUIVALENT to a DIRECT core decode of the same
// node, for BOTH substrate shapes — isolating the plugin round-trip from LoadUnified's later
// member-plan hoisting:
//
//   - a DEPLOY-shape node (`pod:` with image + nested children) folds into uf.Bundle,
//     byte-identical to buildBundleNode(gn) (the former in-proc standaloneKind →
//     buildBundleNodeInto path); and
//   - a standalone TEMPLATE-shape node (a bare `vm:` — the PRIMARY VM authoring form) folds
//     into uf.VM, byte-identical to decodeNodeValue(gn, &VmSpec) (the former
//     buildStandaloneResource path) — the C2-substrate TEMPLATE fold arm that extends F5's
//     deploy-only fold.
//
// RDD proved a canonical spec.Deploy / spec.Vm round-trips through JSON byte-faithfully; this
// locks it through the REAL compiled-in plugin provider (providerRegistry.ResolveKind). Compiled-in,
// so NOT -short-gated (no external build).
func TestSubstrateKind_BothShapesByteEquivalent(t *testing.T) {
	// --- DEPLOY shape (folds uf.Bundle) ---
	depDoc := `substrate-dep:
    pod:
        image: coder
        disposable: true
        tunnel: tailscale
    web:
        pod:
            image: web
    cache:
        pod:
            image: cache
        migrate:
            pod:
                image: migrator
`
	depGn := substrateNodeFromYAML(t, depDoc)
	prov, ok := providerRegistry.ResolveKind("pod")
	if !ok {
		t.Fatal("pod kind must resolve to the compiled-in candy/plugin-substrate provider")
	}
	var uf UnifiedFile
	if err := foldSubstrateKind(prov, depGn, &uf); err != nil {
		t.Fatalf("foldSubstrateKind (deploy): %v", err)
	}
	bn, ok := uf.Bundle["substrate-dep"]
	if !ok {
		t.Fatalf("deploy shape not folded into uf.Bundle; keys %v", bundleKeysFor(&uf))
	}
	if uf.Pod["substrate-dep"] != nil {
		t.Fatal("deploy shape also landed in uf.Pod — must be uf.Bundle ONLY")
	}
	baseBn, err := buildBundleNode(depGn)
	if err != nil {
		t.Fatalf("baseline buildBundleNode: %v", err)
	}
	if got, want := mustJSON(t, bn), mustJSON(t, *baseBn); got != want {
		t.Fatalf("DEPLOY-shape plugin fold != direct core decode\n plugin: %s\n core:   %s", got, want)
	}

	// --- TEMPLATE shape (folds uf.VM) — the PRIMARY VM authoring form ---
	tmplDoc := `substrate-tmpl:
    vm:
        source:
            kind: cloud_image
            url: https://example.invalid/img.qcow2
            base_user: arch
        disk_size: 20 GiB
        ram: 4G
        cpu: 2
        firmware: uefi-insecure
`
	tmplGn := substrateNodeFromYAML(t, tmplDoc)
	vprov, ok := providerRegistry.ResolveKind("vm")
	if !ok {
		t.Fatal("vm kind must resolve to the compiled-in candy/plugin-substrate provider")
	}
	var uf2 UnifiedFile
	if err := foldSubstrateKind(vprov, tmplGn, &uf2); err != nil {
		t.Fatalf("foldSubstrateKind (template): %v", err)
	}
	vm, ok := uf2.VM["substrate-tmpl"]
	if !ok {
		t.Fatalf("template shape not folded into uf.VM; VM is %+v", uf2.VM)
	}
	if _, dup := uf2.Bundle["substrate-tmpl"]; dup {
		t.Fatal("template shape also landed in uf.Bundle — must be uf.VM ONLY")
	}
	// The template canonicalizes GENERICALLY (entityBodyJSON — no concrete-kind type,
	// Cutover N); the plugin echoes it byte-faithfully. Baseline against the same generic
	// pre-decode, and cross-check that both decode to the SAME ResolvedVm (the byte form
	// differs from the old typed decode only by non-omitempty empties + key order — the
	// consumer, resolveVmViaPlugin, is unaffected).
	baseTmpl, err := entityBodyJSON(tmplGn)
	if err != nil {
		t.Fatalf("baseline entityBodyJSON: %v", err)
	}
	if got, want := string(vm), string(baseTmpl); got != want {
		t.Fatalf("TEMPLATE-shape plugin fold != generic pre-decode\n plugin: %s\n core:   %s", got, want)
	}
	var fromFold, fromBase spec.ResolvedVm
	if err := json.Unmarshal(vm, &fromFold); err != nil {
		t.Fatalf("decode fold: %v", err)
	}
	if err := json.Unmarshal(baseTmpl, &fromBase); err != nil {
		t.Fatalf("decode baseline: %v", err)
	}
	if mustJSON(t, &fromFold) != mustJSON(t, &fromBase) {
		t.Fatal("TEMPLATE fold and generic pre-decode must decode to the same ResolvedVm")
	}
}
