package spec

import (
	"encoding/json"
	"testing"
)

func ptr[T any](v T) *T { return &v }

// TestMergeRawTemplateMap verifies the root-wins merge semantics for opaque
// substrate-template maps (local/android after Cutover I). Relocated with
// MergeUnified from sdk/loaderkit/merge.go (#55 C3b), using raw JSON bodies so
// the test carries no charly-core type dependency.
func TestMergeRawTemplateMap(t *testing.T) {
	keep := json.RawMessage(`{"box":"keep"}`)
	drop := json.RawMessage(`{"box":"drop"}`)
	add := json.RawMessage(`{"box":"add"}`)
	dst := map[string]json.RawMessage{"a": keep}
	src := map[string]json.RawMessage{"a": drop, "b": add}
	mergeRawTemplateMap(&dst, src)
	if string(dst["a"]) != string(keep) {
		t.Errorf("existing entry should win: got %s", dst["a"])
	}
	if string(dst["b"]) != string(add) {
		t.Errorf("new entry should be added: %s", dst["b"])
	}
}

// TestMergeBoxConfig_BuildTunables guards the regression where new
// BoxConfig fields are silently dropped during the unified loader's
// defaults: merge because mergeBoxConfig is a hand-maintained field-by-field
// merger. The build-speed tunables (jobs / podman_jobs / podman_jobs_cap /
// context_ignore / cache) MUST survive the merge, or defaults.context_ignore
// authored in charly.yml never reaches the generator. Relocated with
// MergeUnified from sdk/loaderkit/merge.go (#55 C3b).
func TestMergeBoxConfig_BuildTunables(t *testing.T) {
	// dst empty → fills from src (the path that dropped these fields).
	dst := &BoxConfig{}
	src := &BoxConfig{
		Jobs:          ptr(4),
		PodmanJobs:    ptr(0),
		PodmanJobsCap: ptr(8),
		ContextIgnore: []string{"image", ".check"},
		Cache:         "image",
		KeepImages:    ptr(5),
		KeepCheckRuns: ptr(10),
	}
	mergeBoxConfig(dst, src)
	if dst.KeepImages == nil || *dst.KeepImages != 5 {
		t.Errorf("KeepImages not merged from src: %v", dst.KeepImages)
	}
	if dst.KeepCheckRuns == nil || *dst.KeepCheckRuns != 10 {
		t.Errorf("KeepCheckRuns not merged from src: %v", dst.KeepCheckRuns)
	}
	if dst.Jobs == nil || *dst.Jobs != 4 {
		t.Errorf("Jobs not merged from src: %v", dst.Jobs)
	}
	if dst.PodmanJobs == nil || *dst.PodmanJobs != 0 {
		t.Errorf("PodmanJobs (explicit 0) not merged from src: %v", dst.PodmanJobs)
	}
	if dst.PodmanJobsCap == nil || *dst.PodmanJobsCap != 8 {
		t.Errorf("PodmanJobsCap not merged from src: %v", dst.PodmanJobsCap)
	}
	if len(dst.ContextIgnore) != 2 {
		t.Errorf("ContextIgnore not merged from src: %v", dst.ContextIgnore)
	}
	if dst.Cache != "image" {
		t.Errorf("Cache not merged from src: %q", dst.Cache)
	}

	// dst already set → src must NOT override (per-field "dst wins if set").
	dst2 := &BoxConfig{Jobs: ptr(2), Cache: "registry"}
	mergeBoxConfig(dst2, &BoxConfig{Jobs: ptr(9), Cache: "image"})
	if dst2.Jobs == nil || *dst2.Jobs != 2 {
		t.Errorf("dst Jobs should win, got %v", dst2.Jobs)
	}
	if dst2.Cache != "registry" {
		t.Errorf("dst Cache should win, got %q", dst2.Cache)
	}
}

// TestMergePluginKindsMap_NameKeyedOverride proves Cutover A's root-wins override on
// the merge itself: uf.PluginKinds is kind→name→body, and merging a source that
// authors the SAME kind+name as the destination yields ONE entry — the destination
// (root/project) wins and the source (embedded/import) is dropped — exactly the
// build-vocab map merge (mergeDistroMap) rule. A new name in the source is gap-filled. (The
// pre-cutover append semantics would have produced two entries for the shared name.)
// Relocated with MergeUnified from sdk/loaderkit/merge_test.go (#55 C3b); exercises
// the MergePluginKindsMap already resident in this spec module.
func TestMergePluginKindsMap_NameKeyedOverride(t *testing.T) {
	dst := map[string]map[string]json.RawMessage{
		"sidecar": {"tailscale": json.RawMessage(`{"image":"project"}`)},
	}
	src := map[string]map[string]json.RawMessage{
		"sidecar": {
			"tailscale": json.RawMessage(`{"image":"embedded"}`), // same name — must NOT override dst
			"redis":     json.RawMessage(`{"image":"embedded"}`), // new name — must be gap-filled
		},
	}
	MergePluginKindsMap(&dst, src)

	sc := dst["sidecar"]
	if len(sc) != 2 {
		t.Fatalf("expected 2 sidecar entries (tailscale override + redis gap-fill), got %d (%v)", len(sc), sc)
	}
	if got := string(sc["tailscale"]); got != `{"image":"project"}` {
		t.Errorf("tailscale not root-wins: got %q, want the project (dst) body", got)
	}
	if got := string(sc["redis"]); got != `{"image":"embedded"}` {
		t.Errorf("redis gap-fill missing/wrong: got %q", got)
	}
}
