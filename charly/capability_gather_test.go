package main

// capability_gather_test.go — A-0's R7 proof: the D-CAP-3 vocabulary end to end on `env` (the one
// capability type with both an authored want surface and an authored provide surface), plus the
// REFUSAL for a type that has none.
//
// The five cases, and what each one is for:
//   1. bound          — a need binds the one visible provide, and the recorded map names it;
//   2. ErrUnbound     — a need with no provide FAILS the load (D-CAP-1: "a `need:` must be bound
//                       and fails the load when it is not");
//   3. ErrAmbiguous   — two visible provides of one capability error and name BOTH (D-CAP-3);
//   4. visibility     — a provide that EXISTS but is not visible from the needing node does not
//                       bind: §8.2 is load-bearing, not decoration;
//   5. ErrNoProvideSurface — a `secret` need raises the REFUSAL, never ErrUnbound: the search
//                       cannot happen, so reporting "searched, found none" would be a lie.

import (
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

func envNeed(names ...string) *spec.CandyNeed {
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return &spec.CandyNeed{Env: m}
}

func envProvides(m map[string]string) map[string]string { return m }

// TestGatherCapabilitiesVocabulary is the four-case vocabulary of D-CAP-3 on `env`.
func TestGatherCapabilitiesVocabulary(t *testing.T) {
	app := spec.Candy{Name: "app"}
	app.Need = envNeed("FOO")
	prov := spec.Candy{Name: "prov", EnvProvides: envProvides(map[string]string{"FOO": "1"})}
	visible := map[string]bool{"app": true, "prov": true}
	models := map[string]spec.Candy{"app": app, "prov": prov}

	t.Run("bound", func(t *testing.T) {
		bindings, err := GatherStackCapabilities("app", models, visible)
		if err != nil {
			t.Fatalf("a visible provide must bind: %v", err)
		}
		if len(bindings) != 1 || bindings[0].Bound != "candy:prov" {
			t.Fatalf("the recorded map must name the provide, got %+v", bindings)
		}
		if bindings[0].Capability.Name != "FOO" || bindings[0].Capability.Type != "env" {
			t.Fatalf("the binding names the wrong capability: %+v", bindings[0].Capability)
		}
	})

	t.Run("ErrUnbound", func(t *testing.T) {
		lonely := spec.Candy{Name: "app"}
		lonely.Need = envNeed("NOBODY_OFFERS_THIS")
		_, err := GatherStackCapabilities("app", map[string]spec.Candy{"app": lonely}, map[string]bool{"app": true})
		if err == nil {
			t.Fatal("an unbound need must fail the load (D-CAP-1)")
		}
		if !strings.Contains(err.Error(), "NOBODY_OFFERS_THIS") {
			t.Fatalf("the error must name what was searched: %v", err)
		}
	})

	t.Run("ErrAmbiguous", func(t *testing.T) {
		two := spec.Candy{Name: "prov2", EnvProvides: envProvides(map[string]string{"FOO": "2"})}
		_, err := GatherStackCapabilities("app", map[string]spec.Candy{"app": app, "prov": prov, "prov2": two},
			map[string]bool{"app": true, "prov": true, "prov2": true})
		if err == nil {
			t.Fatal("two visible provides of one capability must error (D-CAP-3)")
		}
		msg := err.Error()
		if !strings.Contains(msg, "candy:prov") || !strings.Contains(msg, "candy:prov2") {
			t.Fatalf("the ambiguity must name BOTH provides: %v", msg)
		}
	})

	t.Run("visibility: an invisible provide does not bind", func(t *testing.T) {
		// `prov` exists in the resolved stack but is NOT a member this node can see (§8.2), so the
		// same tree that bound it above must now fail: the gatherer respects the tree.
		_, err := GatherStackCapabilities("app", models, map[string]bool{"app": true})
		if err == nil {
			t.Fatal("a provide outside the needing node's visible set must NOT bind — §8.2 is load-bearing")
		}
		if !strings.Contains(err.Error(), "FOO") {
			t.Fatalf("the unbound error must name the capability: %v", err)
		}
	})
}

// TestGatherRefusesATypeWithNoProvideSurface is the refusal: `secret` has needs and uses in the
// capability table but no authored provide surface (spec#368), so a secret need must raise
// ErrNoProvideSurface — NOT ErrUnbound, which would claim a search happened.
func TestGatherRefusesATypeWithNoProvideSurface(t *testing.T) {
	app := spec.Candy{Name: "app"}
	app.Need = &spec.CandyNeed{Secret: map[string]struct{}{"lab-key": {}}}
	// A lookalike: another candy in the tree that DOES carry an env provide, so the refusal cannot be
	// an accident of an empty tree.
	prov := spec.Candy{Name: "prov", EnvProvides: envProvides(map[string]string{"FOO": "1"})}

	_, err := GatherStackCapabilities("app",
		map[string]spec.Candy{"app": app, "prov": prov},
		map[string]bool{"app": true, "prov": true})
	if err == nil {
		t.Fatal("a secret need must be refused: no authored provide surface exists for it")
	}
	var refusal *ErrNoProvideSurface
	if !errors.As(err, &refusal) {
		t.Fatalf("want ErrNoProvideSurface, got %T: %v — a search that never happened must not be reported as ErrUnbound", err, err)
	}
	if refusal.Type != "secret" {
		t.Fatalf("the refusal must name the capability type, got %q", refusal.Type)
	}
	if strings.Contains(err.Error(), "searched") && !strings.Contains(err.Error(), "cannot be searched") {
		t.Fatalf("the refusal must distinguish itself from a search that found nothing: %v", err)
	}
}

// TestGatherUsesFallBackWhenUnbound is the `use:` half, which the first revision never
// exercised. D-CAP-1 draws the line: a `need:` "must be bound and FAILS THE LOAD when it is
// not", while a `use:` "is bound when a provide exists and takes its `default:`, or goes
// without, otherwise." So the same tree that errors for a need must SUCCEED for a use -- and
// nothing else in this file proves it, because every other fixture authors only `.Need`.
//
// The plant that makes it load-bearing: project `use:` as needsolver.Need instead of
// needsolver.Use and the unbound case below fails the load it must survive.
func TestGatherUsesFallBackWhenUnbound(t *testing.T) {
	app := spec.Candy{Name: "app"}
	app.Use = &spec.CandyUse{Env: map[string]struct {
		Default spec.InitNeutral `yaml:"default,omitempty" json:"default,omitempty"`
	}{"NOBODY_OFFERS_THIS": {}}}

	// No visible member provides env:NOBODY_OFFERS_THIS. A NEED here would be ErrUnbound; a
	// USE falls back, so the gather must return no error and record no binding.
	got, err := GatherStackCapabilities("app", map[string]spec.Candy{"app": app}, map[string]bool{"app": true})
	if err != nil {
		t.Fatalf("a `use:` with no visible provide must fall back, not fail the load: %v", err)
	}
	// D-CAP-3: "a `use:` is bound when a provide exists and takes its `default:`, or goes
	// without, otherwise" -- and an unbound use is RECORDED, not dropped. So exactly one
	// binding comes back with Bound EMPTY, which is the state a deployment reads to report the
	// capability it went without. An empty slice would lose that, and the absence of an error
	// is what distinguishes a `use:` here from a `need:`.
	if len(got) != 1 {
		t.Fatalf("an unbound use must record exactly one binding, got %d: %#v", len(got), got)
	}
	if got[0].Bound != "" {
		t.Fatalf("an unbound use must record Bound=\"\" (it went without), got %q", got[0].Bound)
	}
	if got[0].Want.Name != "NOBODY_OFFERS_THIS" {
		t.Fatalf("the recorded binding must name the use, got %#v", got[0])
	}

	// The bound half: the same use, now with a visible provider, binds to it.
	prov := spec.Candy{Name: "prov"}
	prov.EnvProvides = map[string]string{"NOBODY_OFFERS_THIS": "http://prov:80"}
	models := map[string]spec.Candy{"app": app, "prov": prov}
	got, err = GatherStackCapabilities("app", models, map[string]bool{"app": true, "prov": true})
	if err != nil {
		t.Fatalf("a `use:` with a visible provide must bind: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a bound use must record one binding, got %d: %#v", len(got), got)
	}
	if got[0].Want.Name != "NOBODY_OFFERS_THIS" || got[0].Bound != "candy:prov" {
		t.Fatalf("the binding must name the use and the provide's Origin, got %#v", got[0])
	}
}

// TestGatherCollectsAProvidersProvidesOncePerType is the dedupe's own coverage, and it is the
// test whose ABSENCE let a false error ship. The gatherer collects a member's provides once per
// distinct provide TYPE; when it collected them inside the WANT loop instead, a member's env
// provides were appended once per env want, so two env needs against ONE provider counted that
// provider twice and `needsolver.Resolve` reported a FALSE ErrAmbiguous naming
// "(candy:prov, candy:prov)" -- an error blaming the tree for the gatherer's arithmetic.
//
// The fixture must have TWO wants of one type. Every other case here has one, which is exactly
// why the defect was invisible: a test with one item per collection cannot see a double-count.
func TestGatherCollectsAProvidersProvidesOncePerType(t *testing.T) {
	app := spec.Candy{Name: "app"}
	app.Need = &spec.CandyNeed{Env: map[string]struct{}{"FOO": {}, "BAR": {}}}

	prov := spec.Candy{Name: "prov"}
	prov.EnvProvides = map[string]string{"FOO": "http://prov:80", "BAR": "http://prov:81"}
	models := map[string]spec.Candy{"app": app, "prov": prov}

	got, err := GatherStackCapabilities("app", models, map[string]bool{"app": true, "prov": true})
	if err != nil {
		// A double-count surfaces HERE, as ErrAmbiguous naming the same Origin twice.
		t.Fatalf("two env needs against ONE provider must not be ambiguous: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("both env needs must bind, got %d: %#v", len(got), got)
	}
	for _, b := range got {
		if b.Bound != "candy:prov" {
			t.Fatalf("%q must bind to candy:prov, got %q", b.Want.Name, b.Bound)
		}
	}
}
