package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// qcow2RangeServer serves content honoring Range requests (206 + Content-Range),
// the behavior of real mirrors that makes a stale partial RESUME instead of
// restart — the precondition of the resume-across-rotation corruption.
func qcow2RangeServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rg := r.Header.Get("Range"); rg != "" {
			var off int
			if _, err := fmt.Sscanf(rg, "bytes=%d-", &off); err == nil && off > 0 && off < len(content) {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, len(content)-1, len(content)))
				w.Header().Set("Content-Length", strconv.Itoa(len(content)-off))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(content[off:])
				return
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = w.Write(content)
	}))
}

// TestFetchQcow2_StaleResumedPartialInvalidated proves the resume-invalidation
// rule: a pre-existing stale .part (left behind by an OLDER rotation of a
// mutable images/latest/ URL) Range-resumes into a checksum mismatch; the
// fetcher must treat the RESUMED partial as stale — invalidate it and refetch
// from zero ONCE — and succeed with the current upstream content. Before the
// fix this was a hard "checksum mismatch" failure (the check-k8s-deploy bed
// failure mode under the concurrent fan-out).
func TestFetchQcow2_StaleResumedPartialInvalidated(t *testing.T) {
	content := []byte(strings.Repeat("CURRENT-UPSTREAM-ROTATION ", 64))
	sum := sha256.Sum256(content)
	srv := qcow2RangeServer(t, content)
	defer srv.Close()

	cacheDir := t.TempDir()
	url := srv.URL + "/images/latest/test.qcow2"
	urlHash := sha256.Sum256([]byte(url))
	cachePath := filepath.Join(cacheDir, hex.EncodeToString(urlHash[:])+".qcow2")

	// A stale partial from the "previous rotation" — shorter than the current
	// content so the fetcher resumes mid-file and produces mixed bytes.
	if err := os.WriteFile(cachePath+".part", []byte("STALE-OLD-ROTATION-HEAD-BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := FetchQcow2(VmSource{
		URL:      url,
		Cache:    cacheDir,
		Checksum: spec.VmChecksum{Type: "sha256", Value: hex.EncodeToString(sum[:])},
	})
	if err != nil {
		t.Fatalf("FetchQcow2 must invalidate the stale resumed partial and refetch, got: %v", err)
	}
	if got.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha mismatch after refetch: %s", got.SHA256)
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(content) {
		t.Fatal("cached image is not the current upstream content")
	}
	if _, err := os.Stat(cachePath + ".part"); !os.IsNotExist(err) {
		t.Fatal("stale .part must be gone after promotion")
	}
}

// TestFetchQcow2_CleanFullMismatchIsHardError guards the loop's exit: a CLEAN
// full download (no resumed partial) that fails verification is a hard error,
// never an endless refetch.
func TestFetchQcow2_CleanFullMismatchIsHardError(t *testing.T) {
	content := []byte(strings.Repeat("SERVED-CONTENT ", 32))
	srv := qcow2RangeServer(t, content)
	defer srv.Close()

	cacheDir := t.TempDir()
	wrong := sha256.Sum256([]byte("something else entirely"))
	_, err := FetchQcow2(VmSource{
		URL:      srv.URL + "/img.qcow2",
		Cache:    cacheDir,
		Checksum: spec.VmChecksum{Type: "sha256", Value: hex.EncodeToString(wrong[:])},
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("clean full-download mismatch must be a hard checksum error, got: %v", err)
	}
}

// TestFetchLocks_SerializeByKey proves the two new per-concern lock wrappers
// actually serialize: while held, a non-blocking second acquire fails; after
// release it succeeds. Deterministic (trylock, no timing).
func TestFetchLocks_SerializeByKey(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "img.qcow2")
	rel1, err := acquireVmImageFetchLock(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if rel2, err := acquireFileLock(cachePath+".lock", false); err == nil {
		_ = rel2()
		t.Fatal("second acquire succeeded while the vm-image fetch lock was held")
	}
	if err := rel1(); err != nil {
		t.Fatal(err)
	}
	rel3, err := acquireFileLock(cachePath+".lock", false)
	if err != nil {
		t.Fatalf("acquire after release must succeed: %v", err)
	}
	_ = rel3()

	// The localpkg build lock keys by sha256(srcDir) under the user cache and
	// serializes the same way.
	srcDir := filepath.Join(t.TempDir(), "pkg", "arch")
	relA, err := acquireLocalPkgBuildLock(srcDir)
	if err != nil {
		t.Fatal(err)
	}
	relB := make(chan error, 1)
	go func() {
		rel, err := acquireLocalPkgBuildLock(srcDir) // blocking twin
		if err == nil {
			_ = rel()
		}
		relB <- err
	}()
	if err := relA(); err != nil {
		t.Fatal(err)
	}
	if err := <-relB; err != nil {
		t.Fatalf("blocking second localpkg acquire must succeed after release: %v", err)
	}
}
