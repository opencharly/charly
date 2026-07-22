package main

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestReservedWordRegistry_KindBijection proves every CUE kind (spec.KindWords)
// has a registered in-proc KindProvider (the init() gate enforces this at process
// start; this re-runs it as a test) AND that the completeness check FAILS for a
// spec kind with no provider. The decode behaviour itself is covered by the
// *_RoundTrip tests (which now route through normalizeNodeInto → the registry).
func TestReservedWordRegistry_KindBijection(t *testing.T) {
	// Positive: every spec.KindWord resolves to a registered KindProvider.
	if err := checkKindProviderBijection(spec.KindWords); err != nil {
		t.Fatalf("live kind registry is not complete: %v", err)
	}

	// Negative — a CUE kind with NO provider: add a ghost kind word; the
	// completeness check must report it as missing. (Extra ClassKind providers —
	// out-of-tree plugin kinds — are intentionally allowed, so there is no
	// extra-provider failure case.)
	kindsPlusGhost := append(append([]string{}, spec.KindWords...), "ghostkind")
	err := checkKindProviderBijection(kindsPlusGhost)
	if err == nil {
		t.Fatal("expected kind bijection to FAIL for a spec kind with no provider, got nil")
	}
	if !strings.Contains(err.Error(), "ghostkind") {
		t.Errorf("missing-provider error must name the unhandled kind; got: %v", err)
	}
}

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

// TestReservedWordRegistry_KindsDispatchable proves every registered authoring
// kind is ACTUALLY handled by the loader's normalizeNodeInto dispatch — so the
// registry can never claim a handler that the dispatch switch lacks (the
// anti-drift link between the registry and the real code path).
func TestReservedWordRegistry_KindsDispatchable(t *testing.T) {
	for _, kind := range spec.KindWords {
		gn := &genericNode{name: "probe-" + kind, disc: kind, discClass: "entity"}
		uf := &UnifiedFile{}
		err := normalizeNodeInto(gn, uf)
		// A real handler arm may return a decode error on the empty probe node,
		// but it must NEVER return the "unsupported discriminator" sentinel — that
		// is the no-handler signal.
		if err != nil && strings.Contains(err.Error(), "unsupported discriminator") {
			t.Errorf("kind %q is registered but normalizeNodeInto has no handler: %v", kind, err)
		}
	}
}

// TestReservedWordRegistry_DeployBijection proves the F1 substrate-kind-plugin dispatch
// seam: the deploy-target bijection ACCEPTS every canonical substrate (ALL FIVE now
// externalized — android, k8s, local, pod, vm) having NO in-proc DeployTargetProvider —
// served out-of-process by candy/plugin-adb (android) / candy/plugin-kube (k8s) /
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
	for _, w := range []string{"android", "k8s", "local", "pod", "vm"} {
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
