package main

import "testing"

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
	imgs, err := parseLocalImagesJSON(js)
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
	imgs, err := parseLocalImagesJSON(js)
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
