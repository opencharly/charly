package check

// live_image_test.go — the regression gate for the live-mode image identity (live_image.go).
//
// The store these tests fabricate is the exact shape that produced the defect on the check-docs bed
// (RCA 2026-08-05): several builds of ONE box, every one carrying the SAME content-derived
// `ai.opencharly.version` (it tracks the candy's declared version, not the build), every one tagged
// `check-<bed>-<YYYY.DDD.HHMM>` — a tag `ExtractCalVerTag` reports as EMPTY because it is not three
// numeric dot-parts. With both sort keys tied across every candidate, the short-name resolver's
// lexicographic last resort elects the OLDEST build. Reading the plan off THAT image made a check
// step assert the previous build's baked literal, so no check literal could pass its own R10 bed.
//
// The assertion below is therefore not "the helper reads a label" but "the live plan comes from the
// image the container is RUNNING even when the local store's own ranking prefers another build of
// the same box".

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/spec"
)

const (
	liveTestBox     = "docs-site-app"
	liveTestOldest  = "ghcr.io/opencharly/docs-site-app:check-docs-2026.216.1150"
	liveTestMiddle  = "ghcr.io/opencharly/docs-site-app:check-docs-2026.217.0854"
	liveTestRunning = "ghcr.io/opencharly/docs-site-app:check-docs-2026.217.0938"
	// One content-stable label CalVer shared by every build of this box — the reason the
	// resolver's primary sort key cannot separate them.
	liveTestLabelCalVer = "2026.215.1207"
)

// liveTestLabels builds the OCI label map of one build, with a step whose prose names the build so
// a caller can tell which image's baked plan it got.
func liveTestLabels(t *testing.T, ref string) map[string]string {
	t.Helper()
	set := spec.LabelDescriptionSet{
		Candy: []spec.LabeledDescription{{
			Origin: "candy:docs-site",
			Plan:   []spec.Step{{Check: "the cloned site is the pinned commit, not a stale cached layer (" + ref + ")"}},
		}},
	}
	body, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("marshaling description for %s: %v", ref, err)
	}
	return map[string]string{
		spec.LabelBox:         liveTestBox,
		spec.LabelVersion:     liveTestLabelCalVer,
		spec.LabelDescription: string(body),
	}
}

// stubLiveImageStore installs a local store holding three builds of one box (oldest → running) and
// points the container inspector at running. Every stub is restored by t.Cleanup.
func stubLiveImageStore(t *testing.T, running string) {
	t.Helper()
	labels := map[string]map[string]string{
		liveTestOldest:  liveTestLabels(t, liveTestOldest),
		liveTestMiddle:  liveTestLabels(t, liveTestMiddle),
		liveTestRunning: liveTestLabels(t, liveTestRunning),
	}

	origInspect := container.InspectLabels
	origList := container.ListLocalImages
	origExists := container.LocalImageExists
	origRef := containerImageRef
	t.Cleanup(func() {
		container.InspectLabels = origInspect
		container.ListLocalImages = origList
		container.LocalImageExists = origExists
		containerImageRef = origRef
	})

	container.InspectLabels = func(_, imageRef string) (map[string]string, error) {
		if l, ok := labels[imageRef]; ok {
			return l, nil
		}
		return nil, nil
	}
	container.ListLocalImages = func(string) ([]container.LocalImageInfo, error) {
		out := make([]container.LocalImageInfo, 0, len(labels))
		for ref, l := range labels {
			out = append(out, container.LocalImageInfo{ID: ref, Names: []string{ref}, Labels: l})
		}
		return out, nil
	}
	container.LocalImageExists = func(_, imageRef string) bool {
		_, ok := labels[imageRef]
		return ok
	}
	containerImageRef = func(_, containerName string) (string, error) {
		if containerName != "charly-check-docs" {
			t.Fatalf("liveDeployMetadata inspected container %q, want charly-check-docs", containerName)
			return "", nil
		}
		return running, nil
	}
}

// planOrigin returns the prose of the single baked candy step, which names the build it came from.
func planOrigin(t *testing.T, meta *spec.BoxMetadata) string {
	t.Helper()
	if meta == nil || meta.Description == nil || len(meta.Description.Candy) != 1 || len(meta.Description.Candy[0].Plan) != 1 {
		t.Fatalf("metadata carries no single baked candy step: %+v", meta)
	}
	return meta.Description.Candy[0].Plan[0].Check
}

// TestLiveDeployMetadataReadsTheRunningContainersImage is the defect's coverage: the plan MUST come
// from the image the container runs, even though the local store holds newer-named and
// older-named builds of the same box that a short-name resolution would elect instead.
func TestLiveDeployMetadataReadsTheRunningContainersImage(t *testing.T) {
	stubLiveImageStore(t, liveTestRunning)

	// Guard the fixture itself: the store must still be adversarial, i.e. resolving the box's
	// SHORT NAME against it must answer with some build OTHER than the running one. If this ever
	// stops holding, the test below would pass for the wrong reason.
	stale, rerr := container.ResolveLocalImageRef("podman", liveTestBox)
	if rerr != nil {
		t.Fatalf("short-name resolution over the fixture store: %v", rerr)
	}
	if stale == liveTestRunning {
		t.Fatalf("fixture no longer reproduces the defect: short-name resolution returned the running image %s", stale)
	}

	meta, err := liveDeployMetadata("podman", "charly-check-docs")
	if err != nil {
		t.Fatalf("liveDeployMetadata: %v", err)
	}
	got := planOrigin(t, meta)
	if want := "the cloned site is the pinned commit, not a stale cached layer (" + liveTestRunning + ")"; got != want {
		t.Errorf("live plan came from the wrong build (short-name resolution answers %s)\n got: %s\nwant: %s", stale, got, want)
	}
}

// TestLiveDeployMetadataRequiresARunningContainer pins the failure mode: a deployment whose
// container cannot be inspected is an ERROR, never a silent fallback to some other local image of
// the same box.
func TestLiveDeployMetadataRequiresARunningContainer(t *testing.T) {
	stubLiveImageStore(t, liveTestRunning)
	containerImageRef = func(string, string) (string, error) {
		return "", errNoSuchContainer{}
	}

	meta, err := liveDeployMetadata("podman", "charly-check-docs")
	if err == nil {
		t.Fatalf("liveDeployMetadata returned metadata %+v for an uninspectable container; want an error", meta)
	}
	if meta != nil {
		t.Errorf("liveDeployMetadata returned metadata alongside its error: %+v", meta)
	}
}

// TestLiveDeployMetadataRejectsAnEmptyImageRef covers the other uninspectable shape: an inspect that
// succeeds but reports no image.
func TestLiveDeployMetadataRejectsAnEmptyImageRef(t *testing.T) {
	stubLiveImageStore(t, "")

	if meta, err := liveDeployMetadata("podman", "charly-check-docs"); err == nil {
		t.Fatalf("liveDeployMetadata accepted an empty image ref, returning %+v; want an error", meta)
	}
}

type errNoSuchContainer struct{}

func (errNoSuchContainer) Error() string { return "no such container" }
