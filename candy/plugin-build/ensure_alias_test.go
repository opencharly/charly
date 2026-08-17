package build

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
)

// ensure_alias_test.go — pins the post-build alias contract in aliasBuiltImageOntoRequestedRef.
//
// The defect this covers was ORDERING, not arithmetic: the produced ref used to be resolved
// BEFORE the build, and every resolver for it bottoms out in ResolveShellImageRef with an empty
// tag, which reads the local image store. On a cold store that degraded to the bare
// `<registry>/<name>` — equal to the requested ref — so the `produced != image` guard silently
// skipped the alias and the requested ref was never created. The bed then failed ~1000 lines
// later with podman's `image not known`, and only on the first run against a fresh store.
//
// So these tests drive the resolve through a CALLBACK whose return value differs between the
// cold-store and warm-store answers, which is the only way to exercise the ordering. A test that
// passed the produced ref in as a plain string could not distinguish the two orders at all.

// aliasOutcome runs the alias helper with a scripted produced-ref resolve, reporting whether the
// resolve was consulted and what error came back.
func aliasOutcome(t *testing.T, image, produced string) (consulted bool, err error) {
	t.Helper()
	err = aliasBuiltImageOntoRequestedRef(context.Background(), image, func() string {
		consulted = true
		return produced
	})
	return consulted, err
}

// The cold-store answer. This is the exact shape the old pre-build resolve produced for the arch
// builder: the requested ref back, verbatim. Aliasing a ref onto itself is a no-op, so returning
// nil here would leave the caller's --pull=never run to fail on a missing image — the helper must
// refuse instead of reporting success.
func TestAliasBuiltImage_ColdStoreResolveIsRejectedNotSilentlySkipped(t *testing.T) {
	const image = "ghcr.io/opencharly/arch-builder"

	consulted, err := aliasOutcome(t, image, image)
	if !consulted {
		t.Fatal("the produced-ref resolve was never consulted — the helper cannot have checked " +
			"whether the build satisfied the requested ref")
	}
	if err == nil {
		t.Fatalf("aliasing %q onto itself reported SUCCESS; the requested ref is still absent from "+
			"local storage, so this is the cold-store defect the ordering fix removes", image)
	}
	// The message must name the ref the caller asked for and the useless ref that came back,
	// because the downstream symptom (podman's "image not known") names neither.
	for _, want := range []string{image, "still absent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// An empty resolve is the other unusable answer and must fail the same way.
func TestAliasBuiltImage_EmptyProducedRefIsRejected(t *testing.T) {
	if _, err := aliasOutcome(t, "ghcr.io/opencharly/arch-builder", ""); err == nil {
		t.Fatal("an empty produced ref reported success — nothing was aliased and the requested " +
			"ref is absent")
	}
}

// The presence control. A short name carries no tag to satisfy, so the helper must return nil
// WITHOUT consulting the store at all. Without this test, an implementation that rejected every
// input would satisfy both rejection tests above and still be broken.
func TestAliasBuiltImage_ShortNameNeedsNoAlias(t *testing.T) {
	consulted, err := aliasOutcome(t, "arch-builder", "ghcr.io/opencharly/arch-builder:2026.229.1423")
	if err != nil {
		t.Fatalf("a short-name input must need no alias, got %v", err)
	}
	if consulted {
		t.Error("a short-name input resolved the produced ref; there is no tag to satisfy, so the " +
			"store should not be consulted")
	}
}

// Guards the predicate the helper branches on. `LooksLikeFullRef` is what separates the two cases
// above, so a change in its answer for these two inputs silently redirects the whole helper.
func TestAliasBuiltImage_FullRefPredicateSeparatesTheCases(t *testing.T) {
	if !kit.LooksLikeFullRef("ghcr.io/opencharly/arch-builder") {
		t.Error("the tagless registry ref must read as a full ref — it is the case that needs an alias")
	}
	if kit.LooksLikeFullRef("arch-builder") {
		t.Error("a bare short name must NOT read as a full ref")
	}
}
