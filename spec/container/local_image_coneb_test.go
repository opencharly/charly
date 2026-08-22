package container

// local_image_coneb_test.go — relocated from sdk/kit/local_image_test.go (#55 coneB build-render
// cone, Class A). The resolution family moved here (local_image_coneb.go); the tests exercise the
// same bodies, now in package container. The package-level var overrides (ListLocalImages) target
// container.ListLocalImages — the var container.ResolveLocalImageRef / container.ResolveShellImageRef
// READ — so the stubs take effect (the kit re-export vars are value-copies that no longer affect
// these bodies).

import (
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestParseLocalImagesJSON_DedupByID covers the root fix for the keep_images
// over-removal bug: podman emits ONE ROW PER TAG (each row's Names already lists
// every tag on that id), so the parser must collapse rows to ONE entry per
// distinct image id with the tag refs merged — not N near-identical entries.
func TestParseLocalImagesJSON_DedupByID(t *testing.T) {
	// Two rows for one id (id "ccc", two tags), each row carrying BOTH tags in
	// Names — exactly podman's row-per-tag shape. Plus a distinct id "ddd".
	js := []byte(`[
		{"Id":"ccc","Names":["ghcr/check-pod:2026.150.0916","ghcr/check-pod:2026.150.0836"],"Labels":{"ai.opencharly.image":"check-pod","ai.opencharly.version":"2026.155.1801"}},
		{"Id":"ccc","Names":["ghcr/check-pod:2026.150.0916","ghcr/check-pod:2026.150.0836"],"Labels":{"ai.opencharly.image":"check-pod","ai.opencharly.version":"2026.155.1801"}},
		{"Id":"ddd","Names":["ghcr/other:2026.001.0001"],"Labels":{"ai.opencharly.image":"other"}}
	]`)
	imgs, err := ParseLocalImagesJSON(js)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(imgs) != 2 {
		t.Fatalf("got %d entries, want 2 (one per distinct id): %+v", len(imgs), imgs)
	}
	// id ccc: the two duplicate rows collapse to one entry with BOTH tags
	// (deduped, not 4 copies), labels preserved.
	if imgs[0].ID != "ccc" || len(imgs[0].Names) != 2 {
		t.Fatalf("entry 0 = %+v, want id ccc with 2 merged tags", imgs[0])
	}
	if imgs[0].Labels["ai.opencharly.image"] != "check-pod" || imgs[0].Labels["ai.opencharly.version"] != "2026.155.1801" {
		t.Fatalf("entry 0 labels not preserved: %+v", imgs[0].Labels)
	}
	if imgs[1].ID != "ddd" || len(imgs[1].Names) != 1 {
		t.Fatalf("entry 1 = %+v, want id ddd with 1 tag", imgs[1])
	}
}

// TestParseLocalImagesJSON_DockerRepoTags covers the docker shape (RepoTags
// instead of Names) and that distinct untagged (empty-id) rows do NOT merge.
func TestParseLocalImagesJSON_DockerRepoTags(t *testing.T) {
	js := []byte(`[
		{"ID":"aaa","RepoTags":["ghcr/foo:2026.001.0001"],"Labels":{"ai.opencharly.image":"foo"}},
		{"Id":"","Names":["<none>:<none>"]},
		{"Id":"","Names":["<none>:<none>"]}
	]`)
	imgs, err := ParseLocalImagesJSON(js)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 1 foo (RepoTags) + 2 distinct empty-id rows kept separate = 3 entries.
	if len(imgs) != 3 {
		t.Fatalf("got %d entries, want 3 (docker RepoTags + 2 unmerged empty-id): %+v", len(imgs), imgs)
	}
	if imgs[0].ID != "aaa" || len(imgs[0].Names) != 1 || imgs[0].Names[0] != "ghcr/foo:2026.001.0001" {
		t.Fatalf("entry 0 = %+v, want id aaa with RepoTags ref", imgs[0])
	}
}

// TestShortNameMatchesRef — relocated from charly/checkrun_charly_verbs_test.go (it tests this
// package's unexported shortNameMatchesRef).
func TestShortNameMatchesRef(t *testing.T) {
	cases := []struct {
		fullRef string
		short   string
		want    bool
	}{
		{"ghcr.io/opencharly/jupyter:latest", "jupyter", true},
		{"ghcr.io/opencharly/jupyter", "jupyter", true}, // no tag
		{"localhost/jupyter:v2", "jupyter", true},
		{"jupyter:latest", "jupyter", true}, // no registry
		{"ghcr.io/opencharly/jupyter:latest", "filebrowser", false},
		{"ghcr.io/opencharly/something-jupyter:latest", "jupyter", false}, // not a trailing match
	}
	for _, tc := range cases {
		got := shortNameMatchesRef(tc.fullRef, tc.short)
		if got != tc.want {
			t.Errorf("shortNameMatchesRef(%q, %q) = %v, want %v", tc.fullRef, tc.short, got, tc.want)
		}
	}
}

func TestResolveLocalImageRefShortTaggedPinsExactBuild(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(engine string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			{ID: "old", Names: []string{"ghcr.io/opencharly/check-agent-box:check-agent-pod-2026.199.1646"}, Labels: map[string]string{spec.LabelBox: "check-agent-box", spec.LabelVersion: "2026.199.1330"}},
			{ID: "new", Names: []string{"ghcr.io/opencharly/check-agent-box:check-agent-pod-2026.199.1700"}, Labels: map[string]string{spec.LabelBox: "check-agent-box", spec.LabelVersion: "2026.199.1330"}},
		}, nil
	}
	got, err := ResolveLocalImageRef("podman", "check-agent-box:check-agent-pod-2026.199.1700")
	if err != nil {
		t.Fatalf("ResolveLocalImageRef(short:tag): %v", err)
	}
	if want := "ghcr.io/opencharly/check-agent-box:check-agent-pod-2026.199.1700"; got != want {
		t.Fatalf("ResolveLocalImageRef(short:tag) = %q, want %q", got, want)
	}
}

// ResolveShellImageRef branch coverage (sdk#68 review round — the helper shipped
// with none; these five cases FAIL without the function's branch logic).
// ListLocalImages is stubbed via its package-level var (same pattern as
// LocalImageExists/DetectGPU testability notes on the var itself).
func TestResolveShellImageRef_TagEmptyLocalHit(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(engine string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{{
			ID:    "sha256:aa",
			Names: []string{"localhost/jupyter:2026.190.1200", "localhost/jupyter:2026.191.0800"},
			Labels: map[string]string{
				spec.LabelBox:     "jupyter",
				spec.LabelVersion: "2026.185.0000",
			},
		}}, nil
	}
	got := ResolveShellImageRef("ghcr.io/opencharly", "jupyter", "")
	// Local CalVer resolution wins over the registry fallback; newest tag-CalVer picked.
	if got != "localhost/jupyter:2026.191.0800" {
		t.Errorf("tag-empty local-hit: got %q, want the newest local CalVer ref", got)
	}
}

func TestResolveShellImageRef_TagEmptyLocalMissWithRegistry(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(engine string) ([]LocalImageInfo, error) { return nil, nil }
	got := ResolveShellImageRef("ghcr.io/opencharly", "jupyter", "")
	if got != "ghcr.io/opencharly/jupyter" {
		t.Errorf("tag-empty local-miss + registry: got %q, want tagless registry/name", got)
	}
}

func TestResolveShellImageRef_TagEmptyLocalMissNoRegistry(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(engine string) ([]LocalImageInfo, error) { return nil, nil }
	got := ResolveShellImageRef("", "jupyter", "")
	if got != "jupyter" {
		t.Errorf("tag-empty local-miss no-registry: got %q, want bare short name", got)
	}
}

func TestResolveShellImageRef_TagSetWithRegistry(t *testing.T) {
	// tag-set paths must NOT consult local storage at all.
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(engine string) ([]LocalImageInfo, error) {
		t.Fatal("tag-set branch must not list local images")
		return nil, nil
	}
	got := ResolveShellImageRef("ghcr.io/opencharly", "jupyter", "2026.198.0001")
	if got != "ghcr.io/opencharly/jupyter:2026.198.0001" {
		t.Errorf("tag-set + registry: got %q", got)
	}
}

func TestResolveShellImageRef_TagSetNoRegistry(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(engine string) ([]LocalImageInfo, error) {
		t.Fatal("tag-set branch must not list local images")
		return nil, nil
	}
	got := ResolveShellImageRef("", "jupyter", "2026.198.0001")
	if got != "jupyter:2026.198.0001" {
		t.Errorf("tag-set no-registry: got %q", got)
	}
}

// TestResolveLocalImageRef_NeverReturnsSiblingDeployAlias is the regression witness for the
// cross-deployment image-crossing defect. The fixture is the REAL local store shape measured on
// the dev host (`charly box list tags check-pod`): several disposable beds share ONE base box, so
// every bed's `tagDeployAlias` alias (`<registry>/<deploy-name>:<calver>`) sits on an image
// carrying the BASE box's ai.opencharly.image label, and — because the label-CalVer is
// content-derived — they all share ONE label-CalVer. Pre-fix, the tag-CalVer tiebreak therefore
// elected whichever sibling bed was (re)deployed most recently: an untagged resolve of the BASE
// box name returned another deployment's image.
//
// The property: an untagged resolve of box B returns a ref NAMED B, never a sibling's alias.
func TestResolveLocalImageRef_NeverReturnsSiblingDeployAlias(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	const labelCV = "2026.209.1500" // one content-derived label-CalVer across the whole family
	ListLocalImages = func(engine string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			{ID: "base", Names: []string{"ghcr.io/opencharly/check-pod:2026.216.2119"},
				Labels: map[string]string{spec.LabelBox: "check-pod", spec.LabelVersion: labelCV}},
			// Sibling beds' aliases — NEWER tag-CalVers, same inherited base label.
			{ID: "sib1", Names: []string{"ghcr.io/opencharly/check-preempt-arbiter-pod:2026.216.2124"},
				Labels: map[string]string{spec.LabelBox: "check-pod", spec.LabelVersion: labelCV}},
			{ID: "sib2", Names: []string{"ghcr.io/opencharly/check-stepkind-emit-pod:2026.216.2120"},
				Labels: map[string]string{spec.LabelBox: "check-pod", spec.LabelVersion: labelCV}},
		}, nil
	}
	got, err := ResolveLocalImageRef("podman", "check-pod")
	if err != nil {
		t.Fatalf("ResolveLocalImageRef(check-pod): %v", err)
	}
	if want := "ghcr.io/opencharly/check-pod:2026.216.2119"; got != want {
		t.Fatalf("untagged base resolve = %q, want %q (a sibling deployment's alias must never win)", got, want)
	}
	// The aliases stay resolvable by their OWN deploy name via the name fallback — the alias's
	// entire purpose (deploy-name-keyed `charly config`/`charly start`).
	got, err = ResolveLocalImageRef("podman", "check-stepkind-emit-pod")
	if err != nil {
		t.Fatalf("ResolveLocalImageRef(check-stepkind-emit-pod): %v", err)
	}
	if want := "ghcr.io/opencharly/check-stepkind-emit-pod:2026.216.2120"; got != want {
		t.Fatalf("deploy-name resolve = %q, want %q", got, want)
	}
}

// TestRefRepoName covers the two shapes the promoted-to-filter predicate now has to get right
// (it gates candidacy, not just tie ordering): a registry PORT must not read as a tag, and a
// digest must be stripped.
func TestRefRepoName(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"ghcr.io/opencharly/jupyter:2026.216.2119", "jupyter"},
		{"ghcr.io/opencharly/jupyter", "jupyter"},
		{"localhost/jupyter:v2", "jupyter"},
		{"jupyter:latest", "jupyter"},
		{"jupyter", "jupyter"},
		{"localhost:5000/jupyter", "jupyter"},                             // registry port, no tag
		{"localhost:5000/jupyter:2026.216.2119", "jupyter"},               // registry port + tag
		{"ghcr.io/opencharly/jupyter@sha256:abc123", "jupyter"},           // digest
		{"ghcr.io/opencharly/versa/ecovoyage:2026.216.2119", "ecovoyage"}, // instance alias
	}
	for _, tc := range cases {
		if got := refRepoName(tc.ref); got != tc.want {
			t.Errorf("refRepoName(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

// --- the stale-build-election guard (charly#check-box-target-image) ---
//
// Creation times below are unix seconds; only their ORDER matters. Every fixture sets one on
// every image, because a candidate without one now makes the ordering unknown by design.

const (
	tAug14  = 1786000000 // oldest
	tAug15  = 1786100000
	tAug15b = 1786200000 // newest
)

// staleReproStorage reproduces the MEASURED local storage of the original incident (2026-08-15,
// day 227) that made `charly check box fedora-nonfree` print
// `Image: ghcr.io/opencharly/fedora-nonfree:2026.216.1908` and report `5 passed, 0 failed`
// against a plan that predated the candy edit under test.
//
// The two families split because the emitter labelled a namespaced build with the qualified box
// key while tagging the ref with the leaf name, so the fresh build fell into the NAME family —
// which the election discards wholesale when the label family is non-empty.
func staleReproStorage() []LocalImageInfo {
	return []LocalImageInfo{
		{ID: "old", Created: tAug14, Names: []string{
			"ghcr.io/opencharly/fedora-nonfree:2026.216.1516",
			"ghcr.io/opencharly/fedora-nonfree:2026.216.1908",
		}, Labels: map[string]string{spec.LabelBox: "fedora-nonfree", spec.LabelVersion: "2026.144.1443"}},
		{ID: "fresh", Created: tAug15, Names: []string{
			"ghcr.io/opencharly/fedora-nonfree:2026.227.0835",
			"ghcr.io/opencharly/fedora-nonfree:2026.227.0836",
		}, Labels: map[string]string{spec.LabelBox: "fedora.fedora-nonfree", spec.LabelVersion: "2026.227.0830"}},
	}
}

// TestResolveBuiltImageRef_RefusesStaleElection is the regression gate for the original incident:
// a verb that pronounces a verdict on a built artifact must REFUSE rather than certify an image
// older than the newest local build. The label-family split keeps the election on the OLD image
// while the newest build sits in the discarded family.
func TestResolveBuiltImageRef_RefusesStaleElection(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(string) ([]LocalImageInfo, error) { return staleReproStorage(), nil }

	got, err := ResolveBuiltImageRef("podman", "fedora-nonfree")
	if err == nil {
		t.Fatalf("ResolveBuiltImageRef(fedora-nonfree) = %q, nil — want a refusal", got)
	}
	if !errors.Is(err, spec.ErrStaleLocalImage) {
		t.Fatalf("error %v does not wrap spec.ErrStaleLocalImage", err)
	}
	// Both refs named, plus a runnable re-invocation. A refusal the operator cannot act on is only
	// marginally better than the silent pass it replaces.
	for _, want := range []string{
		"ghcr.io/opencharly/fedora-nonfree:2026.216.1908",
		"ghcr.io/opencharly/fedora-nonfree:2026.227.0836",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not mention %q", err.Error(), want)
		}
	}
}

// bedTagStorage is the shape that defeated the FIRST version of this guard, reproduced from the
// live host that exposed it: three distinct builds of one box, every one carrying a bed tag
// (`charly box build --tag` REPLACES the CalVer tag rather than adding to it), so every candidate
// has an EMPTY tag-CalVer and one content-stable label version.
//
// Ordering by the tag ties all three and falls through to a lexicographic last resort, which
// elects the OLDEST. Live consequence, measured: `check box docs-site-app` certified an image ~17
// hours old, green, exit 0 — the very incident this cutover exists to prevent, sailing past the
// guard because `newestBuildRef` returned "" and the guard read that as "nothing to worry about".
func bedTagStorage() []LocalImageInfo {
	lbl := map[string]string{spec.LabelBox: "docs-site-app", spec.LabelVersion: "2026.215.1207"}
	return []LocalImageInfo{
		{ID: "b", Created: tAug15, Names: []string{"ghcr.io/opencharly/docs-site-app:check-docs-2026.227.0846"}, Labels: lbl},
		{ID: "a", Created: tAug14, Names: []string{"ghcr.io/opencharly/docs-site-app:check-docs-2026.226.1543"}, Labels: lbl},
		{ID: "c", Created: tAug15b, Names: []string{"ghcr.io/opencharly/docs-site-app:check-docs-2026.227.1227"}, Labels: lbl},
	}
}

// TestResolveBuiltImageRef_BedTaggedElectsNewest is the gate for the blindness. Every candidate is
// bed-tagged, so NO tag-CalVer exists to order by; creation time does, and the election must land
// on the newest build rather than the lexicographically-first ref.
//
// Fails without the fix in BOTH halves at once: the election returns the 08-14 ref, and the guard
// does not refuse it either.
func TestResolveBuiltImageRef_BedTaggedElectsNewest(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(string) ([]LocalImageInfo, error) { return bedTagStorage(), nil }

	got, err := ResolveBuiltImageRef("podman", "docs-site-app")
	if err != nil {
		t.Fatalf("ResolveBuiltImageRef(docs-site-app): %v", err)
	}
	if want := "ghcr.io/opencharly/docs-site-app:check-docs-2026.227.1227"; got != want {
		t.Fatalf("resolve = %q, want %q (the newest build; every candidate is bed-tagged so only creation time orders them)", got, want)
	}
}

// TestResolveBuiltImageRef_BedTaggedFreshBuildIsNotRefused is the MIRROR case, and the one that
// made the first guard actively misdirect: a fresh bed-tagged build alongside an older plain-CalVer
// build. Ordering by the tag put the CalVer-tagged OLD image first (an empty tag-CalVer sorts
// last), so the guard refused a correct election and told the operator to re-run against the STALE
// image — worse than no guard at all.
func TestResolveBuiltImageRef_BedTaggedFreshBuildIsNotRefused(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	lbl := map[string]string{spec.LabelBox: "check-agent-box", spec.LabelVersion: "2026.199.1330"}
	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			{ID: "old", Created: tAug14, Names: []string{"ghcr.io/opencharly/check-agent-box:2026.216.1908"}, Labels: lbl},
			{ID: "fresh", Created: tAug15b, Names: []string{"ghcr.io/opencharly/check-agent-box:check-agent-pod-2026.227.1300"}, Labels: lbl},
		}, nil
	}

	got, err := ResolveBuiltImageRef("podman", "check-agent-box")
	if err != nil {
		t.Fatalf("ResolveBuiltImageRef(check-agent-box) refused a CORRECT election: %v", err)
	}
	if want := "ghcr.io/opencharly/check-agent-box:check-agent-pod-2026.227.1300"; got != want {
		t.Fatalf("resolve = %q, want %q (the fresh bed-tagged build)", got, want)
	}
}

// TestResolveBuiltImageRef_BedTaggedFreshBuildIsNotMisdirected is the OTHER face of the mirror
// case. Where the previous test shares one label version and the tag-ordered guard silently
// certified the OLD image, here the fresh build carries a HIGHER content version: the election
// correctly picks the fresh one, but a tag-ordered "newest build" can only see the old CalVer-
// tagged candidate — so the guard refused a CORRECT election and told the operator to re-run
// against the STALE image. A guard that points at the artifact it exists to keep you away from is
// worse than no guard, which is why both faces are gated.
func TestResolveBuiltImageRef_BedTaggedFreshBuildIsNotMisdirected(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			{ID: "old", Created: tAug14, Names: []string{"ghcr.io/opencharly/check-agent-box:2026.216.1908"},
				Labels: map[string]string{spec.LabelBox: "check-agent-box", spec.LabelVersion: "2026.199.1330"}},
			{ID: "fresh", Created: tAug15b, Names: []string{"ghcr.io/opencharly/check-agent-box:check-agent-pod-2026.227.1300"},
				Labels: map[string]string{spec.LabelBox: "check-agent-box", spec.LabelVersion: "2026.227.0830"}},
		}, nil
	}
	got, err := ResolveBuiltImageRef("podman", "check-agent-box")
	if err != nil {
		t.Fatalf("refused a CORRECT election (and would have pointed at the stale image): %v", err)
	}
	if want := "ghcr.io/opencharly/check-agent-box:check-agent-pod-2026.227.1300"; got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

// TestResolveBuiltImageRef_SameArtifactNeverRefuses pins the simplification creation time made
// possible: many tags on ONE image id are one artifact, so there is no older/newer to arbitrate
// and the guard must stay silent. Without the id check this refuses a box against itself.
func TestResolveBuiltImageRef_SameArtifactNeverRefuses(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			{ID: "one", Created: tAug15, Names: []string{
				"ghcr.io/opencharly/docs-site-app:check-docs-2026.227.1227",
				"ghcr.io/opencharly/docs-site-app:check-docs-2026.227.1242",
			}, Labels: map[string]string{spec.LabelBox: "docs-site-app", spec.LabelVersion: "2026.215.1207"}},
		}, nil
	}
	if _, err := ResolveBuiltImageRef("podman", "docs-site-app"); err != nil {
		t.Fatalf("refused two tags on ONE image id — they are the same artifact: %v", err)
	}
}

// TestResolveBuiltImageRef_SingleFamilyStaleElectionRefuses is the scenario the guard's own doc
// comment names, and the one no other test covered: ONE label family, no name/label split, where
// the election's PRIMARY key (the content-derived ai.opencharly.version) elects an image that is
// NOT the most recently built.
//
// That gap let a regression through green. When the election and newestBuild were refactored to
// share ONE comparator, they shared its PRIMARY key too — so cands[0] became identically the max
// newestBuild returns, NewestBuildRef == Ref always, and RefuseIfStale had nothing to compare. The
// guard became a tautology. Every existing refusal test survived because they all use the
// two-family split, where newestBuild scans the WIDER union and can still differ.
//
// The two orderings must therefore stay DIFFERENT in their primary key — the election answers
// "which artifact does this name mean" (content version), newestBuild answers "which was built
// most recently" (creation time) — and only their deterministic tail is shared.
func TestResolveBuiltImageRef_SingleFamilyStaleElectionRefuses(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	// ONE family: both carry the same ai.opencharly.box, so no split is doing the work here.
	// The HIGHER content version is the OLDER build — a sibling worktree on a newer source tree
	// built first, then this tree built something newer from older sources.
	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			{ID: "highlabel-older", Created: tAug14, Names: []string{"ghcr.io/opencharly/web:2026.227.0830"},
				Labels: map[string]string{spec.LabelBox: "web", spec.LabelVersion: "2026.227.0830"}},
			{ID: "lowlabel-newer", Created: tAug15b, Names: []string{"ghcr.io/opencharly/web:2026.144.1443"},
				Labels: map[string]string{spec.LabelBox: "web", spec.LabelVersion: "2026.144.1443"}},
		}, nil
	}

	got, err := ResolveBuiltImageRef("podman", "web")
	if err == nil {
		t.Fatalf("ResolveBuiltImageRef(web) = %q, nil — want a REFUSAL. The election picked the higher "+
			"CONTENT version while a NEWER BUILD exists; if newestBuild shares the election's primary key "+
			"it agrees by construction and the guard can never fire", got)
	}
	if !errors.Is(err, spec.ErrStaleLocalImage) {
		t.Fatalf("error %v does not wrap spec.ErrStaleLocalImage", err)
	}
	// It must name the NEWER BUILD as the artifact to re-run against, not the higher content version.
	if !strings.Contains(err.Error(), "ghcr.io/opencharly/web:2026.144.1443") {
		t.Fatalf("refusal %q does not name the newest BUILD", err.Error())
	}
}

// TestResolveBuiltImageRef_TiedKeysDoNotRefuse pins the election/newest-build tiebreak SYMMETRY.
// Two DISTINCT images sharing a creation second with no CalVer tag to separate them tie on every
// recency key. The election's last resort is ascending ref; if newestBuild picked descending, the
// two would name different refs, and RefuseIfStale — which compares refs, not keys — would refuse
// a pair where neither is newer than the other. Distinct builds do share a second under parallel
// load, so this was reachable rather than theoretical.
//
// Fails with the descending tiebreak: the guard refuses with two refs it cannot rank.
func TestResolveBuiltImageRef_TiedKeysDoNotRefuse(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	lbl := map[string]string{spec.LabelBox: "twin-app", spec.LabelVersion: "2026.215.1207"}
	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			{ID: "one", Created: tAug15, Names: []string{"ghcr.io/opencharly/twin-app:check-a-2026.227.1000"}, Labels: lbl},
			{ID: "two", Created: tAug15, Names: []string{"ghcr.io/opencharly/twin-app:check-b-2026.227.1000"}, Labels: lbl},
		}, nil
	}
	got, err := ResolveBuiltImageRef("podman", "twin-app")
	if err != nil {
		t.Fatalf("refused two artifacts that tie on every recency key — neither is newer: %v", err)
	}
	if want := "ghcr.io/opencharly/twin-app:check-a-2026.227.1000"; got != want {
		t.Fatalf("resolve = %q, want %q (the election's ascending-ref last resort)", got, want)
	}
}

// TestResolveBuiltImageRef_RefusesWhenOrderUnknown pins the deliberate answer to "what if the
// ordering cannot be established": refuse, do not pass. Permissive-on-unknown is exactly what made
// the first guard blind, so an engine that reports no creation time gets an explicit error naming
// the reason rather than a silent verdict.
func TestResolveBuiltImageRef_RefusesWhenOrderUnknown(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	lbl := map[string]string{spec.LabelBox: "docs-site-app", spec.LabelVersion: "2026.215.1207"}
	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			{ID: "a", Created: tAug14, Names: []string{"ghcr.io/opencharly/docs-site-app:check-docs-2026.226.1543"}, Labels: lbl},
			{ID: "b", Created: 0, Names: []string{"ghcr.io/opencharly/docs-site-app:check-docs-2026.227.0846"}, Labels: lbl},
		}, nil
	}
	err := ResolveBuiltImageRef2Err(t)
	if err == nil {
		t.Fatal("no refusal when the ordering could not be established")
	}
	if !errors.Is(err, spec.ErrStaleLocalImage) {
		t.Fatalf("error %v does not wrap spec.ErrStaleLocalImage", err)
	}
	if !strings.Contains(err.Error(), "could not establish") {
		t.Fatalf("refusal %q does not say the ordering is unknown — the operator cannot tell this apart from a staleness refusal", err.Error())
	}
}

// ResolveBuiltImageRef2Err is a tiny helper keeping the unknown-order test readable.
func ResolveBuiltImageRef2Err(t *testing.T) error {
	t.Helper()
	_, err := ResolveBuiltImageRef("podman", "docs-site-app")
	return err
}

// TestResolveLocalImageRef_LenientFormStillElects pins the deliberate split: the lenient resolver
// (every consumption path — deploy, vm build, builder bootstrap) shares the election, including its
// creation-time ordering, but never refuses.
func TestResolveLocalImageRef_LenientFormStillElects(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(string) ([]LocalImageInfo, error) { return staleReproStorage(), nil }

	got, err := ResolveLocalImageRef("podman", "fedora-nonfree")
	if err != nil {
		t.Fatalf("ResolveLocalImageRef(fedora-nonfree): %v", err)
	}
	if want := "ghcr.io/opencharly/fedora-nonfree:2026.216.1908"; got != want {
		t.Fatalf("lenient resolve = %q, want %q (unchanged election, no refusal)", got, want)
	}
}

// TestResolveBuiltImageRef_PinnedInputPassesThrough covers the escape hatch AND the reason the R10
// bed sequence is untouched by the guard: the bed builds `<image> --tag <run-tag>` and then checks
// `<image>:<run-tag>`, an explicit pin, so the guard never fires there.
func TestResolveBuiltImageRef_PinnedInputPassesThrough(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(string) ([]LocalImageInfo, error) { return staleReproStorage(), nil }

	got, err := ResolveBuiltImageRef("podman", "fedora-nonfree:2026.216.1908")
	if err != nil {
		t.Fatalf("ResolveBuiltImageRef(pinned older tag): %v", err)
	}
	if want := "ghcr.io/opencharly/fedora-nonfree:2026.216.1908"; got != want {
		t.Fatalf("pinned resolve = %q, want %q", got, want)
	}
}

// TestResolveBuiltImageRef_ConsistentLabelsElectNewestBuild proves the guard does NOT fire once the
// emitter labels every build with the box's LEAF name (the sdk render fix): one family, the fresh
// build's higher content-derived label-CalVer wins outright.
func TestResolveBuiltImageRef_ConsistentLabelsElectNewestBuild(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		imgs := staleReproStorage()
		imgs[1].Labels[spec.LabelBox] = "fedora-nonfree" // what the fixed render emits
		return imgs, nil
	}

	got, err := ResolveBuiltImageRef("podman", "fedora-nonfree")
	if err != nil {
		t.Fatalf("ResolveBuiltImageRef(fedora-nonfree) with consistent labels: %v", err)
	}
	if want := "ghcr.io/opencharly/fedora-nonfree:2026.227.0836"; got != want {
		t.Fatalf("resolve = %q, want %q (the newest build)", got, want)
	}
}

// TestResolveBuiltImageRef_SiblingAliasIsNotANewerBuild guards the guard: a sibling deployment's
// `<deploy-name>:<calver>` alias must not read as "a newer build of this box". The alias is named
// for the OTHER deployment, so it is never a candidate for this short name and cannot trigger a
// refusal — otherwise every bed host would refuse every untagged verdict.
func TestResolveBuiltImageRef_SiblingAliasIsNotANewerBuild(t *testing.T) {
	orig := ListLocalImages
	defer func() { ListLocalImages = orig }()
	const labelCV = "2026.209.1500"
	ListLocalImages = func(string) ([]LocalImageInfo, error) {
		return []LocalImageInfo{
			{ID: "base", Created: tAug14, Names: []string{"ghcr.io/opencharly/check-pod:2026.216.2119"},
				Labels: map[string]string{spec.LabelBox: "check-pod", spec.LabelVersion: labelCV}},
			{ID: "sib", Created: tAug15b, Names: []string{"ghcr.io/opencharly/check-preempt-arbiter-pod:2026.216.2124"},
				Labels: map[string]string{spec.LabelBox: "check-pod", spec.LabelVersion: labelCV}},
		}, nil
	}
	got, err := ResolveBuiltImageRef("podman", "check-pod")
	if err != nil {
		t.Fatalf("ResolveBuiltImageRef(check-pod): %v", err)
	}
	if want := "ghcr.io/opencharly/check-pod:2026.216.2119"; got != want {
		t.Fatalf("resolve = %q, want %q", got, want)
	}
}

// TestParseLocalImagesJSON_Created proves the ordering key is actually READ off the engine rows —
// the whole fix rests on it, and a silently-absent field would make every resolve "order unknown".
func TestParseLocalImagesJSON_Created(t *testing.T) {
	js := []byte(`[{"Id":"aaa","Created":1786797320,"Names":["ghcr/foo:2026.001.0001"],"Labels":{}}]`)
	imgs, err := ParseLocalImagesJSON(js)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(imgs) != 1 || imgs[0].Created != 1786797320 {
		t.Fatalf("Created not parsed: %+v", imgs)
	}
}
