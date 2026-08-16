package clean

import (
	"testing"

	"github.com/opencharly/sdk/kit"
)

// TestPruneImagesByRetention_MultiTagGroupSurvivesIntact is the discriminating guard for the
// two-ordinal `keep_images` ranking: a group holding ONE image that wears several tags plus
// DISTINCT siblings behind it is the only shape on which the two-ordinal ranking and the
// pre-fix single tag-row ordinal choose differently. Every other shape — N distinct images
// each wearing one tag — ranks identically under both, so no assertion over it can fail on
// the pre-fix code.
//
// PROVENANCE. The table below is not invented: it is the `fedora-nonfree` group as OBSERVED on
// a live host on 2026-08-16 at 04:4xZ, post-prune (that store had been pruned at 06:18:45 local,
// so it is a contaminated baseline and is recorded as such). The shape is ordinary rather than
// exotic — eight groups on that host carried an id wearing 2-3 tags, because a content-stable
// box rebuilt with a fresh `--tag` shares its image id and ACCUMULATES the tag (confirmed at
// run time: a `--tag rt-x2` rebuild of unchanged content re-reported the pre-existing CalVer tag
// alongside the new one). The literal is frozen deliberately: reading the live store here would
// trade determinism back for host state, and the group WILL drift.
//
// Ordering note: all four images carry an identical ai.opencharly.version label, so the
// comparator falls through the label CalVer and labelled-ness keys to CREATION TIME — which is
// why the Created values, not the tag strings, establish the ranks. The tags are bed-shaped
// (`check-<bed>-<calver>`), which ExtractCalVerTag reports as EMPTY, so tag CalVer orders
// nothing here. That is the ordering the engine documents and the reason creation time precedes
// the tag.
//
// Pairs with TestPruneImagesByRetention_SharedID, which proves the shared-id ranking; this one
// proves a multi-tag image survives INTACT while the distinct sibling past the budget does not.
// Neither subsumes the other.
func TestPruneImagesByRetention_MultiTagGroupSurvivesIntact(t *testing.T) {
	origList, origCtr, origFloor := kit.ListLocalImages, listContainerImageRefs, liveBuildFloor
	defer func() { kit.ListLocalImages, listContainerImageRefs, liveBuildFloor = origList, origCtr, origFloor }()

	// No live build: the host-global build-activity lock is the seam this stub exists for. With a
	// live build the `lastTag` guard protects every single-tagged image and the expected pruning
	// silently does not happen — which is exactly why a LIVE bed cannot make this assertion.
	liveBuildFloor = func() (kit.CalVer, bool, int) { return kit.CalVer{}, false, 0 }
	listContainerImageRefs = func(string) (map[string]bool, map[string]bool, error) {
		return map[string]bool{}, map[string]bool{}, nil
	}

	const (
		multiTagA = "ghcr.io/opencharly/fedora-nonfree:check-docs-2026.227.2301"
		multiTagB = "ghcr.io/opencharly/fedora-nonfree:check-marketplace-2026.228.0010"
		multiTagC = "ghcr.io/opencharly/fedora-nonfree:check-sidecar-pod-2026.228.0221"
		rankTail  = "ghcr.io/opencharly/fedora-nonfree:check-docs-2026.227.1835"
	)
	lbl := func() map[string]string {
		return map[string]string{
			"ai.opencharly.box":     "fedora-nonfree",
			"ai.opencharly.version": "2026.227.0830", // identical across all four, as observed
		}
	}
	group := []kit.LocalImageInfo{
		{ID: "57a3efe70a68", Created: 1786850392, Labels: lbl(), Names: []string{
			"ghcr.io/opencharly/fedora-nonfree:check-pod-2026.228.0319"}},
		{ID: "d39f559add13", Created: 1786850024, Labels: lbl(), Names: []string{
			"ghcr.io/opencharly/fedora-nonfree:check-pod-overlay-2026.228.0312"}},
		// The multi-tag image: rank 2 of 4, inside a keep_images: 3 budget, wearing three tags
		// whose own ordinals (0,1,2) are all inside the budget too. BOTH ordinals inside => all
		// three survive. The pre-fix ranking counted TAG ROWS, so these sat at rows 2,3,4 and the
		// last two fell outside the budget.
		{ID: "e91486ab32f7", Created: 1786825413, Labels: lbl(), Names: []string{
			multiTagA, multiTagB, multiTagC}},
		// The distinct sibling past the budget: rank 3, removable on BOTH code paths. Present so
		// the fixture cannot pass by pruning nothing at all.
		{ID: "8241fa641a54", Created: 1786818957, Labels: lbl(), Names: []string{rankTail}},
	}
	kit.ListLocalImages = func(string) ([]kit.LocalImageInfo, error) { return group, nil }

	removed, err := pruneImagesByRetention("podman", 3, true /* dryRun: selection only, no rmi */)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	got := map[string]bool{}
	for _, r := range removed {
		got[r] = true
	}

	// The DISCRIMINATING half: the multi-tag image keeps every tag. Pre-fix this fails — the
	// tag-row ranking selects multiTagB and multiTagC.
	for _, keep := range []string{multiTagA, multiTagB, multiTagC} {
		if got[keep] {
			t.Errorf("multi-tag image lost a tag it must keep: %s\n"+
				"  all three tags of e91486ab32f7 are inside BOTH ordinals at keep_images=3;\n"+
				"  selecting one is the pre-fix tag-row ranking", keep)
		}
	}

	// The NON-discriminating half, kept as the vacuity check: true on both code paths, so it
	// proves nothing on its own — but it fails if the fixture ever stops pruning at all (a guard
	// engaging, keepN mis-resolved, the group mis-keyed), which is what would otherwise let the
	// assertions above pass for the wrong reason.
	if !got[rankTail] {
		t.Errorf("the distinct sibling past the budget was NOT selected: %s\n"+
			"  it is rank 3 at keep_images=3, datable, and unreferenced;\n"+
			"  if this stops being selected the test above is passing vacuously", rankTail)
	}

	if len(removed) != 1 {
		t.Errorf("expected exactly 1 selected ref, got %d: %v", len(removed), removed)
	}
}

// TestRetentionUndatableGuardIsAnAND pins the exemption's exact width. The guard is
// `!OkLabel && !OkTag`, so a row is protected only when NEITHER key can date it. A `:latest` on an
// image that carries a datable ai.opencharly.version label has OkLabel == true and is therefore
// RECLAIMABLE by tag ordinal — the opposite of what a comment in this file used to claim
// ("can never be elected for removal however many tags its image wears", the OR reading).
//
// This matters more than it sounds: the version label is a DECLARED version
// (deploykit.ComputeEffectiveVersions — the box's version:, else the highest candy version:, else
// the base's), not a content hash, so nearly every charly-built image carries one. The exemption
// protects far fewer rows than its name suggests, and `latest` on a managed image is not among
// them. Perturbing the guard to `||` makes the labelled case pass and this test fail.
func TestRetentionUndatableGuardIsAnAND(t *testing.T) {
	origList, origCtr, origFloor := kit.ListLocalImages, listContainerImageRefs, liveBuildFloor
	defer func() { kit.ListLocalImages, listContainerImageRefs, liveBuildFloor = origList, origCtr, origFloor }()
	liveBuildFloor = func() (kit.CalVer, bool, int) { return kit.CalVer{}, false, 0 }
	listContainerImageRefs = func(string) (map[string]bool, map[string]bool, error) {
		return map[string]bool{}, map[string]bool{}, nil
	}

	// One image, five undatable `:latest`-style tags, so tag ordinals 3 and 4 fall outside
	// keep_images=3. The ONLY difference between the two cases is the version label.
	names := []string{"ghcr/x:latest", "ghcr/x:dev", "ghcr/x:stable", "ghcr/x:edge", "ghcr/x:main"}
	run := func(labels map[string]string) []string {
		rows := make([]kit.LocalImageInfo, len(names))
		for i := range names {
			rows[i] = kit.LocalImageInfo{ID: "aaa", Created: 100, Labels: labels, Names: names}
		}
		kit.ListLocalImages = func(string) ([]kit.LocalImageInfo, error) { return rows, nil }
		removed, err := pruneImagesByRetention("podman", 3, true)
		if err != nil {
			t.Fatalf("prune: %v", err)
		}
		return removed
	}

	// Datable LABEL present -> OkLabel true -> the AND does NOT protect -> surplus tags removable.
	labelled := run(map[string]string{"ai.opencharly.box": "x", "ai.opencharly.version": "2026.100.0000"})
	if len(labelled) == 0 {
		t.Errorf("a datable ai.opencharly.version label must defeat the undatable exemption, "+
			"leaving surplus undatable tags reclaimable by tag ordinal — got %d removals", len(labelled))
	}

	// NEITHER key datable -> the exemption applies -> nothing removable, however many tags.
	unlabelled := run(map[string]string{"ai.opencharly.box": "x"})
	if len(unlabelled) != 0 {
		t.Errorf("with neither a datable label nor a datable tag the exemption must protect every "+
			"row however many tags the image wears — got %d removals: %v", len(unlabelled), unlabelled)
	}
}
