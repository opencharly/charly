package main

import (
	"strings"
	"testing"
)

// TestProviderManifest_EnforcedBijection proves the providers: manifest in the
// embedded charly.yml is the AUTHORITY for built-in provider registration: it must
// agree exactly with the compiled-in instances, it actually DROVE registration, and
// the bijection check catches BOTH a manifest entry with no instance AND an instance
// the manifest omits. The init() panics on any break (so the whole suite depends on
// agreement); this asserts the enforcement directly, including with doctored inputs.
func TestProviderManifest_EnforcedBijection(t *testing.T) {
	byKey := builtinInstanceMap()
	real := parseEmbeddedProviderManifest()

	// 1. The shipped manifest agrees exactly with the compiled-in instances.
	if p := manifestInstanceProblems(real, byKey); len(p) != 0 {
		t.Fatalf("embedded providers: manifest disagrees with compiled-in instances: %v", p)
	}

	// 2. Every manifest provider is actually registered — the manifest drove it.
	total := 0
	for class, words := range real {
		for _, w := range words {
			total++
			if _, ok := providerRegistry.resolve(ProviderClass(class), w); !ok {
				t.Fatalf("manifest provider %s:%s is not registered — manifest must drive registration", class, w)
			}
		}
	}
	if total != len(builtinProviderInstances) {
		t.Fatalf("manifest lists %d providers, %d instances compiled in", total, len(builtinProviderInstances))
	}

	// 3. A manifest that OMITS a shipped provider → caught as an orphan instance.
	missing := providerManifest{}
	for c, ws := range real {
		missing[c] = append([]string{}, ws...)
	}
	missing["verb"] = missing["verb"][:len(missing["verb"])-1]
	if p := manifestInstanceProblems(missing, byKey); len(p) == 0 ||
		!strings.Contains(strings.Join(p, " "), "absent from the providers: manifest") {
		t.Fatalf("expected an omitted-manifest-entry to be caught, got: %v", p)
	}

	// 4. A manifest naming a provider the binary does NOT ship → caught as a stray.
	stray := providerManifest{}
	for c, ws := range real {
		stray[c] = append([]string{}, ws...)
	}
	stray["verb"] = append(stray["verb"], "ghostverb")
	if p := manifestInstanceProblems(stray, byKey); len(p) == 0 ||
		!strings.Contains(strings.Join(p, " "), "no compiled-in instance") {
		t.Fatalf("expected a stray-manifest-entry to be caught, got: %v", p)
	}
}
