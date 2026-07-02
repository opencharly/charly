package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestFetchQcow2_ReusesUnpinnedCachedImage proves FU-4: an UNPINNED cloud_image
// (no Checksum.Value — e.g. box/arch's rolling images/latest/ URL, which cannot take
// a static pin) is reused from the URL-content-addressed cache via its recorded
// download-computed sum, instead of re-downloading the full image on every VM build
// (the re-download that trips mirror rate limits). Before the fix the cache-hit gate
// required a pinned expected sha256, so an unpinned URL ALWAYS re-fetched — which here
// (an unreachable .invalid URL) errors.
func TestFetchQcow2_ReusesUnpinnedCachedImage(t *testing.T) {
	cacheDir := t.TempDir()
	const url = "https://example.invalid/images/latest/Arch-Linux-x86_64-cloudimg.qcow2"
	urlHash := sha256.Sum256([]byte(url))
	cachePath := filepath.Join(cacheDir, hex.EncodeToString(urlHash[:])+".qcow2")
	if err := os.WriteFile(cachePath, []byte("fake qcow2 bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath+".sha256", []byte("deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Unpinned source (no Checksum.Value) pointing at an unreachable URL. With FU-4 the
	// recorded sum + cached file are reused (no download); without it, the fetcher would
	// fall through to downloading the unreachable URL and error.
	got, err := FetchQcow2(VmSource{URL: url, Cache: cacheDir})
	if err != nil {
		t.Fatalf("FetchQcow2 must reuse the cached unpinned image (no re-download), got error: %v", err)
	}
	if got.Path != cachePath {
		t.Fatalf("FetchQcow2: expected cached path %s, got %s", cachePath, got.Path)
	}
	if got.SHA256 != "deadbeef" {
		t.Fatalf("FetchQcow2: expected the recorded sum, got %q", got.SHA256)
	}
}
