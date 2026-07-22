package main

import (
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestExternalDeployRecordVenueLedger_* retired here (S3b): recordVenueLedger moved to
// candy/plugin-bundle/deploy_target.go alongside the deploy-dispatch logic it belonged to (see
// CHANGELOG/2026.203.0212.md) — see candy/plugin-bundle/deploy_target_test.go for the ported
// tests (same behavior, same assertions).

// TestResolveDeployNodeByPath proves the dotted-path resolution that lets the deploy-plugin
// loader find a NESTED child deploy (the bed runner deploys arch-host via `charly bundle add
// check-arch-vm.arch-host` — a dotted name that is NOT a top-level tree key). Without this,
// deployNodePluginContext surfaced no plugin words for the nested child and its substrate
// word never loaded its provider (the "unknown target local" regression).
//
// The "vm:"-prefixed cases are the FINAL/K5 unit 6a RCA #8 live-probe-caught regression: a
// "vm:"-prefixed CLI address (the established convention for `charly bundle del vm:<name>` /
// `vm:<parent.child>`) used to resolve to NOTHING here, since the dotted-path split ran on the
// RAW name with the prefix still attached (`tree["vm:"+segment]` never matches — the tree is
// keyed by the plain name). deployNodePluginContext (this function's one caller) then collected
// zero referenced plugin words, so loadDeployPlugins never connected the substrate provider —
// resolveDelNode's OWN "vm:"-prefix shortcut (a synthetic Target-only placeholder that never
// touches the tree) masked the miss until the LATER actual dispatch needed the never-connected
// provider ("known substrate but its deploy provider is not connected").
func TestResolveDeployNodeByPath(t *testing.T) {
	tree := map[string]spec.BundleNode{
		"check-arch-vm": {
			Target: "vm",
			Children: map[string]*spec.BundleNode{
				"arch-host": {Target: "local"},
				"web":       {Target: "pod", Children: map[string]*spec.BundleNode{"db": {Target: "pod"}}},
			},
		},
		"pod-bed": {Target: "pod"},
	}
	cases := []struct {
		name       string
		wantOK     bool
		wantTarget string
	}{
		{"pod-bed", true, "pod"},                      // bare top-level
		{"check-arch-vm", true, "vm"},                 // bare root with children
		{"check-arch-vm.arch-host", true, "local"},    // dotted nested child — the failing case
		{"check-arch-vm.web.db", true, "pod"},         // deep dotted path
		{"nope", false, ""},                           // missing root
		{"check-arch-vm.nope", false, ""},             // missing child
		{"pod-bed.nope", false, ""},                   // child of a childless node
		{"vm:check-arch-vm", true, "vm"},              // RCA #8: "vm:"-prefixed top-level
		{"vm:check-arch-vm.arch-host", true, "local"}, // RCA #8: "vm:"-prefixed dotted nested child
		{"vm:check-arch-vm.web.db", true, "pod"},      // RCA #8: "vm:"-prefixed deep dotted path
		{"vm:does-not-exist", false, ""},              // RCA #8: prefix stays honest on a real miss
		{"vm:check-arch-vm.nope", false, ""},          // RCA #8: prefix stays honest on a missing child
	}
	for _, tc := range cases {
		n, ok := resolveDeployNodeByPath(tree, tc.name)
		if ok != tc.wantOK {
			t.Errorf("resolveDeployNodeByPath(%q) ok=%v, want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if ok && n.Target != tc.wantTarget {
			t.Errorf("resolveDeployNodeByPath(%q).Target = %q, want %q", tc.name, n.Target, tc.wantTarget)
		}
	}
}

// TestExternalDeploySubstratePluginRef proves the substrate→canonical-plugin-candy mapping a
// box/<distro> submodule auto-injects so an externalized substrate word resolves to its
// out-of-process provider (the main repo discovers it from candy/ directly; a submodule does
// not). A non-externalized substrate (pod) has NO ref. Kept in sync with
// externalizedDeploySubstrates by the startup checkDeployProviderBijection gate.
func TestExternalDeploySubstratePluginRef(t *testing.T) {
	want := map[string]string{
		"vm":      "@" + spec.DefaultProjectRepo + "/candy/plugin-deploy-vm",
		"pod":     "@" + spec.DefaultProjectRepo + "/candy/plugin-deploy-pod",
		"local":   "@" + spec.DefaultProjectRepo + "/candy/plugin-deploy-local",
		"android": "@" + spec.DefaultProjectRepo + "/candy/plugin-adb",
		"k8s":     "@" + spec.DefaultProjectRepo + "/candy/plugin-kube",
	}
	for word, exp := range want {
		got, ok := externalDeploySubstratePluginRef(word)
		if !ok || got != exp {
			t.Errorf("externalDeploySubstratePluginRef(%q) = %q ok=%v, want %q", word, got, ok, exp)
		}
	}
	// Every externalized substrate MUST have a plugin ref (else a submodule can't discover
	// it). ALL FIVE substrates are externalized now, so this covers the whole set.
	for word := range externalizedDeploySubstrates {
		if _, ok := externalDeploySubstratePluginRef(word); !ok {
			t.Errorf("externalized substrate %q has no plugin-candy ref", word)
		}
	}
}
