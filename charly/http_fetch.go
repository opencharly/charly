package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// FetchedImage is the result of FetchQcow2: the absolute path to the
// cached file plus the resolved sha256 (useful for logging / audit).
type FetchedImage struct {
	Path   string
	SHA256 string
}

// FetchQcow2 downloads (or reuses a cached copy of) a qcow2 URL,
// verifying the sha256 checksum. Resumable: partial downloads with a
// matching Content-Length are continued via Range: bytes=.
//
// If the checksum value is empty, the fetcher attempts to auto-resolve
// a sidecar file at <url>.SHA256 / .sha256 / .sha256sum (Arch convention
// is .SHA256). The first one that returns HTTP 200 wins.
func FetchQcow2(src VmSource) (FetchedImage, error) {
	if src.URL == "" {
		return FetchedImage{}, fmt.Errorf("FetchQcow2: source.url is empty")
	}

	cacheDir := src.Cache
	if cacheDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return FetchedImage{}, fmt.Errorf("resolving home dir: %w", err)
		}
		cacheDir = filepath.Join(home, ".cache", "charly", "vm-images")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return FetchedImage{}, fmt.Errorf("creating cache dir: %w", err)
	}

	// Content-addressed by sha256(url). Stable across URL query params.
	urlHash := sha256.Sum256([]byte(src.URL))
	cachePath := filepath.Join(cacheDir, hex.EncodeToString(urlHash[:])+".qcow2")
	cacheSumPath := cachePath + ".sha256"

	// Serialize concurrent fetchers of this image (flock, cross-process): two beds
	// building VMs from the same cloud image otherwise race on the shared .part —
	// one renames it away under the other, and interleaved Range-resumes mix bytes.
	// The cache-hit check runs UNDER the lock (double-checked): a waiter usually
	// finds the image the first fetcher just promoted.
	releaseLock, err := acquireVmImageFetchLock(cachePath)
	if err != nil {
		return FetchedImage{}, fmt.Errorf("vm-image fetch lock: %w", err)
	}
	defer func() { _ = releaseLock() }()

	// Resolve expected sha256.
	expected := resolveExpectedSHA256(src)

	// Cache hit: reuse the cached image when a recorded sidecar sum + the cached file
	// both exist AND either (a) it matches the pinned expected sha256, or (b) nothing
	// is pinned (expected == ""). Case (b) lets a rolling/unpinned cloud_image (e.g.
	// box/arch's images/latest/ URL, which cannot take a static pin) be reused via its
	// own download-computed sum instead of re-fetching the full image on EVERY VM build
	// — the cache is content-addressed by URL and self-consistent via the recorded sum.
	// This stops unpinned rolling URLs from re-downloading hundreds of MiB per build and
	// tripping mirror rate limits (the failure mode that took down check-fedora-vm before
	// fedora-vm was pinned). Pinned images keep the stronger expected==recorded check. To
	// avoid re-hashing large qcow2 files on every build, the recorded sidecar sum is trusted.
	if existing := readRecordedSum(cacheSumPath); existing != "" && (expected == "" || existing == expected) {
		if _, err := os.Stat(cachePath); err == nil {
			return FetchedImage{Path: cachePath, SHA256: existing}, nil
		}
	}

	// Download (resumable) to a .part file, then rename. A RESUMED partial that
	// fails verification is treated as STALE — a mutable upstream URL (an
	// images/latest/ rotation) changed under it — so it is invalidated and the
	// fetch restarts ONCE from zero (deterministic cache invalidation, not a
	// retry: a clean full download that still mismatches is a hard error).
	partPath := cachePath + ".part"
	var actual string
	for {
		resumed := false
		if st, err := os.Stat(partPath); err == nil && st.Size() > 0 {
			resumed = true
		}
		if err := downloadResumable(src.URL, partPath); err != nil {
			return FetchedImage{}, err
		}
		actual, err = fileSHA256(partPath)
		if err != nil {
			return FetchedImage{}, fmt.Errorf("sha256 of %s: %w", partPath, err)
		}
		if expected == "" || actual == expected {
			break
		}
		_ = os.Remove(partPath)
		if resumed {
			fmt.Fprintf(os.Stderr, "fetch qcow2: resumed partial failed verification (upstream rotated?) — refetching %s from zero\n", src.URL)
			continue
		}
		// Hard failure — a clean full download mismatched.
		return FetchedImage{}, fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
	}

	// Atomic rename into place + record the sum.
	if err := os.Rename(partPath, cachePath); err != nil {
		return FetchedImage{}, fmt.Errorf("promoting %s to %s: %w", partPath, cachePath, err)
	}
	_ = os.WriteFile(cacheSumPath, []byte(actual+"\n"), 0o644)

	return FetchedImage{Path: cachePath, SHA256: actual}, nil
}

// resolveExpectedSHA256 returns the sha256 to verify against. Tries in
// order: explicit src.Checksum.Value, then .SHA256 / .sha256 / .sha256sum
// sidecar files at the same directory as src.URL. Returns "" when none
// resolve — in that case, FetchQcow2 proceeds without verification
// (and records whatever sha256 the downloaded bytes compute to).
func resolveExpectedSHA256(src VmSource) string {
	if src.Checksum.Value != "" {
		return normalizeSum(src.Checksum.Value)
	}
	// Auto-resolve sidecar.
	for _, suffix := range []string{".SHA256", ".sha256", ".sha256sum"} {
		sidecarURL := src.URL + suffix
		body, ok := fetchSidecar(sidecarURL)
		if !ok {
			continue
		}
		// Sidecar formats:
		//   "<hex>  <filename>\n"  (sha256sum output)
		//   "<hex>\n"               (bare digest)
		sum := extractBareSHA256(string(body), baseNameFromURL(src.URL))
		if sum != "" {
			return sum
		}
	}
	return ""
}

func fetchSidecar(u string) ([]byte, bool) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(u)
	if err != nil || resp.StatusCode != 200 {
		if resp != nil {
			resp.Body.Close()
		}
		return nil, false
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, false
	}
	return body, true
}

func baseNameFromURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return filepath.Base(parsed.Path)
}

// extractBareSHA256 parses a sha256sum-style sidecar body, returning
// the bare 64-char hex digest that corresponds to filename (or the
// first line when filename is empty or doesn't match any row).
func extractBareSHA256(body, filename string) string {
	lines := strings.Split(body, "\n")
	// First pass: try to match by filename.
	if filename != "" {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 && strings.HasSuffix(fields[1], filename) {
				return normalizeSum(fields[0])
			}
		}
	}
	// Second pass: bare-hex single-line.
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 1 && len(fields[0]) == 64 {
			return normalizeSum(fields[0])
		}
		if len(fields) >= 1 && len(fields[0]) == 64 {
			// "<sum>  filename" with only one filename line — take the hash.
			return normalizeSum(fields[0])
		}
	}
	return ""
}

// normalizeSum strips a "sha256:" prefix and lower-cases.
func normalizeSum(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "sha256:")
	return strings.ToLower(s)
}

// downloadResumable GETs u into dst. If dst already exists (from a
// previous partial download), sends a Range: bytes=<offset>- header to
// resume. Updates dst in place.
func downloadResumable(u, dst string) error {
	var offset int64
	if fi, err := os.Stat(dst); err == nil {
		offset = fi.Size()
	}

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return fmt.Errorf("building GET %s: %w", u, err)
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	client := &http.Client{Timeout: 0} // no timeout — qcow2s can be large
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// Server ignored our Range header (or we had no offset) — start
		// from scratch.
		offset = 0
	case http.StatusPartialContent:
		// Range honored — resume from offset.
	default:
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}

	flag := os.O_WRONLY | os.O_CREATE
	if offset == 0 {
		flag |= os.O_TRUNC
	} else {
		flag |= os.O_APPEND
	}
	f, err := os.OpenFile(dst, flag, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", dst, err)
	}
	defer f.Close() //nolint:errcheck

	total := offset + resp.ContentLength
	fmt.Fprintf(os.Stderr, "Fetching %s", u)
	if total > 0 {
		fmt.Fprintf(os.Stderr, " (%s)", humanBytes(total))
	}
	if offset > 0 {
		fmt.Fprintf(os.Stderr, " resuming at %s", humanBytes(offset))
	}
	fmt.Fprintln(os.Stderr)

	if _, err := io.Copy(f, resp.Body); err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	return nil
}

func readRecordedSum(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// fileSHA256 streams a file's sha256 without loading it fully.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// humanBytes renders a byte count as a compact human-readable string.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB",
		float64(n)/float64(div), "KMGTP"[exp])
}

// _ suppresses go vet unused-import concerns for strconv in some
// build-tag configurations; retained for parity with sibling files.
var _ = strconv.Itoa
