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

// TestReservedWordRegistry_DeploySubstrates proves the F1 substrate-kind-plugin dispatch
// seam under the OPEN deploy-provider model: the deploy provider set is the generated
// provider-ref index (every deploy word a plugin in the corpus declares), NOT a closed
// vocabulary and NOT an in-proc provider. The canonical substrates present in the corpus
// (android, kindcluster, kubernetes, local, pod, vm) are served out-of-process by
// candy/plugin-adb / candy/plugin-kube / candy/plugin-deploy-local / candy/plugin-deploy-pod
// / candy/plugin-deploy-vm, whose providers register at plugin-load time; a plugin-ONLY word
// (exampledeploy / examplelifecycle, declared by the example plugins and NOT in
// spec.ResourceKinds) is a first-class deploy substrate too — that is what "anyone can create
// any kind of plugin with zero core changes" means, and why the former
// checkDeployProviderBijection (which validated against the closed spec.ResourceKinds
// vocabulary) is GONE.
//
// `kubevirt` is deliberately NOT in the asserted set. It remains a CUE resource kind (so
// unresolvedDeployTargetError still classifies it as a KNOWN substrate via resourceKindSet),
// but no plugin in `charly/plugin_corpus.txt` declares `deploy:kubevirt`: the server,
// github.com/opencharly/plugin-kubevirt, is owned by the KubeVirt venue leg (a separate
// in-flight session) and cannot join the corpus yet because it ALSO declares `kind:kubevirt`,
// which the compiled-in candy/plugin-substrate declares too — a duplicate the generator
// rejects ("a word has one canonical provider"). When that leg lands plugin-kubevirt into the
// corpus and retires the overlap, `deploy:kubevirt` returns to this set with no charly change.
func TestReservedWordRegistry_DeploySubstrates(t *testing.T) {
	t.Cleanup(snapshotProviderState())

	// Every canonical corpus substrate is externalized: recognized as a deploy substrate AND
	// INTENTIONALLY without an in-proc DeployTargetProvider. pluginDeployTarget (S3b) reads
	// gp.lifecycle/gp.preresolve directly off the resolved *grpcProvider — there is no separate
	// per-substrate lifecycle registry left to assert against.
	for _, w := range []string{"android", "kindcluster", "kubernetes", "local", "pod", "vm"} {
		if !externalizedDeploySubstrates[w] {
			t.Fatalf("%s must be in externalizedDeploySubstrates (the generated deploy-provider set)", w)
		}
		if _, ok := providerRegistry.resolve(ClassDeployTarget, w); ok {
			t.Fatalf("%s must NOT have an in-proc DeployTargetProvider — it is externalized", w)
		}
	}

	// `kubevirt` is a CUE resource kind but is NOT in the index-derived set (its server,
	// plugin-kubevirt, is not in the corpus yet — see the KubeVirt venue leg above). This pins
	// that the set is a projection of the index, not of spec.ResourceKinds: a ResourceKind with
	// no corpus plugin is absent here while still being a KNOWN word to the diagnostics.
	if externalizedDeploySubstrates["kubevirt"] {
		t.Fatalf("kubevirt must NOT be in the index-derived deploy set while plugin-kubevirt is absent from the corpus")
	}
	if !resourceKindSet["kubevirt"] {
		t.Fatalf("kubevirt must remain a CUE resource kind (it is a known substrate word)")
	}

	// The set is OPEN: a plugin-declared word outside spec.ResourceKinds is a deploy substrate
	// with no core edit. This FAILS on the pre-change code, whose externalizedDeploySubstrates
	// was derived from the closed spec.ResourceKinds list.
	for _, w := range []string{"exampledeploy", "examplelifecycle"} {
		if !externalizedDeploySubstrates[w] {
			t.Errorf("%s (a plugin-only deploy word) must be a recognized deploy substrate — the set is plugin-declared, not a closed vocabulary", w)
		}
	}

	// A deploy word is recognized WITHOUT an in-proc provider by its plugin declaration
	// (recognizedDeploySubstrate's declared-before-connected leniency) — the third-party path.
	if recognizedDeploySubstrate("some-third-party-deploy-word") {
		t.Fatal("an undeclared/undeclared word must NOT be recognized")
	}
	registerDeclaredDeploySubstrate("some-third-party-deploy-word")
	defer func() {
		declaredDeployMu.Lock()
		delete(declaredDeploySubstrate, "some-third-party-deploy-word")
		declaredDeployMu.Unlock()
	}()
	if !recognizedDeploySubstrate("some-third-party-deploy-word") {
		t.Fatal("a project-declared deploy word must be recognized with no core change")
	}
}
