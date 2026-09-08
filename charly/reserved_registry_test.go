package main

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestReservedWordRegistry_VerbBijection proves VerbCatalog ⇄ spec.OpVerbs is a
// bijection and that every verb is an authorable Op field, plus the failure paths.
func TestReservedWordRegistry_VerbBijection(t *testing.T) {
	if err := checkVerbBijection(spec.VerbCatalog, spec.OpVerbs, spec.AuthoringVerbs); err != nil {
		t.Fatalf("live verb registry is not a bijection: %v", err)
	}

	// A CUE verb with no VerbCatalog handler must be reported.
	verbsPlusGhost := append(append([]string{}, spec.OpVerbs...), "ghostverb")
	// ghostverb is also not an authorable field, so it surfaces in two buckets;
	// the missing-handler bucket is what we assert on.
	if err := checkVerbBijection(spec.VerbCatalog, verbsPlusGhost, spec.AuthoringVerbs); err == nil ||
		!strings.Contains(err.Error(), "ghostverb") {
		t.Fatalf("expected verb bijection to FAIL for a spec verb with no handler, got: %v", err)
	}

	// A verb absent from spec.AuthoringVerbs (i.e. CUE doesn't know it as an Op
	// field) must be reported as not-authorable.
	if err := checkVerbBijection(spec.VerbCatalog, spec.OpVerbs, []string{"file"}); err == nil ||
		!strings.Contains(err.Error(), "not in spec.AuthoringVerbs") {
		t.Fatalf("expected verb bijection to FAIL when verbs are not authorable, got: %v", err)
	}
}

// TestReservedWordRegistry_KindsDispatchable proves every registered ClassKind provider is
// ACTUALLY handled by the loader's materializeNodeInto dispatch — so the registry can never
// claim a handler that the kind-blind dispatch lacks (the anti-drift link between the registry
// and the real code path; rewritten from the former spec.KindWords loop + genericNode probes —
// spec.KindWords is EMPTY and the KindProvider interface died with the genericNode bridge,
// parser consolidation F2.1).
func TestReservedWordRegistry_KindsDispatchable(t *testing.T) {
	words := make([]string, 0, 8)
	for _, p := range providerRegistry.allProviders() {
		if p.Class() == ClassKind {
			words = append(words, p.Reserved())
		}
	}
	for _, disc := range words {
		pn := spec.ParsedNode{Name: "probe-" + disc, Disc: disc}
		uf := &spec.UnifiedFile{}
		err := materializeNodeInto(pn, uf)
		// A real handler arm may return a decode error on the empty probe node,
		// but it must NEVER return the "unsupported discriminator" sentinel — that
		// is the no-handler signal.
		if err != nil && strings.Contains(err.Error(), "unsupported discriminator") {
			t.Errorf("kind %q is registered but materializeNodeInto has no handler: %v", disc, err)
		}
	}
}

// TestReservedWordRegistry_DeployBijection proves the F1 substrate-kind-plugin dispatch
// seam: the deploy-target bijection ACCEPTS every canonical substrate (ALL FIVE now
// externalized — android, kubernetes, local, pod, vm) having NO in-proc DeployTargetProvider —
// served out-of-process by candy/plugin-adb (android) / candy/plugin-kube (kubernetes) /
// candy/plugin-deploy-local (local) / candy/plugin-deploy-pod (pod) / candy/plugin-deploy-vm
// (vm), whose grpcProvider connects at plugin-load time; and FAILS when a word is NEITHER
// builtin NOR externalized (the in-proc XOR externalized invariant — never neither).
func TestReservedWordRegistry_DeployBijection(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	// Positive: the live registry (all five externalized, none in-proc) passes — the same
	// gate the init() bijection runs at process start.
	if err := checkDeployProviderBijection(); err != nil {
		t.Fatalf("live deploy-target bijection is broken: %v", err)
	}

	// ALL FIVE are externalized substrates: in externalizedDeploySubstrates AND
	// INTENTIONALLY without an in-proc DeployTargetProvider. pluginDeployTarget (S3b) reads
	// gp.lifecycle/gp.preresolve directly off the resolved *grpcProvider — there is no separate
	// per-substrate lifecycle registry left to assert against.
	for _, w := range []string{"android", "kubernetes", "local", "pod", "vm"} {
		if !externalizedDeploySubstrates[w] {
			t.Fatalf("%s must be in externalizedDeploySubstrates (the F1 source of truth)", w)
		}
		if _, ok := providerRegistry.resolve(ClassDeployTarget, w); ok {
			t.Fatalf("%s must NOT have an in-proc DeployTargetProvider — it is externalized", w)
		}
	}

	// Negative: a substrate that is NEITHER externalized NOR backed by an in-proc provider
	// violates the bijection and must FAIL the gate. Temporarily de-list pod (which has no
	// in-proc deploy-target provider) → it is now neither → fail.
	delete(externalizedDeploySubstrates, "pod")
	defer func() { externalizedDeploySubstrates["pod"] = true }()
	if err := checkDeployProviderBijection(); err == nil {
		t.Fatal("expected bijection to FAIL when a substrate is NEITHER externalized NOR an in-proc provider")
	}
}
