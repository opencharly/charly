package main

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The host walk stamps the embedded default vocabulary's digest on the walk envelope, because the
// vocabulary is a materialize input and the materialized-tree cache keys on the envelope. Without
// the stamp a binary whose embedded charly.yml changed was served the previous vocabulary's cached
// tree whenever the spec and sdk versions stayed put.
func TestHostWalkStampsTheEmbeddedDefaultsDigest(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "charly.yml", "discover: []\n")
	lp, err := hostWalkProject(dir, []byte("discover: []\n"))
	if err != nil {
		t.Fatalf("hostWalkProject: %v", err)
	}
	sum := sha256.Sum256(embeddedCharlyDefaults)
	if want := "sha256:" + hex.EncodeToString(sum[:]); lp.EmbeddedDefaultsDigest != want {
		t.Fatalf("walk envelope digest = %q, want %q (the embedded defaults' content digest)", lp.EmbeddedDefaultsDigest, want)
	}
}
