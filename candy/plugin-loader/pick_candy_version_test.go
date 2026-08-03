package loader

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// pick_candy_version_test.go — relocated from charly/refs_test.go (#55 decoupling, Batch A,
// cross-batch file-ownership matrix: Batch A executes this move on Batch C's behalf).
// TestPickCandyVersion covers the per-entity-version arbiter (the sole candy-version resolver),
// asserting loaderkit.PickCandyVersion directly, zero charly dep.

// TestPickCandyVersion covers the per-entity-version arbiter (the sole
// candy-version resolver). Same per-entity `version:` across different git tags
// resolves with NO warning — the newest git tag wins for freshness — which is
// the Problem-B regression guard: a repo re-tag of an UNCHANGED candy must not
// warn. Different per-entity versions warn once and the newest version wins.
func TestPickCandyVersion(t *testing.T) {
	mk := func(ver, tag string) spec.CandyCandidate {
		return spec.CandyCandidate{
			Scanned: spec.ScannedCandy{Model: spec.CandyModel{Name: "x", Version: ver}},
			Version: ver,
			GitTag:  tag,
			Source:  "github.com/o/r@" + tag,
		}
	}
	capture := func(fn func() spec.CandyCandidate) (spec.CandyCandidate, string) {
		old := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w
		got := fn()
		_ = w.Close()
		os.Stderr = old
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		return got, buf.String()
	}

	// Same per-entity version, different git tags -> NO warning, newest tag wins.
	got, warn := capture(func() spec.CandyCandidate {
		return loaderkit.PickCandyVersion("github.com/o/r/layers/x", []spec.CandyCandidate{
			mk("2026.141.1600", "v2026.141.1600"),
			mk("2026.141.1600", "v2026.150.900"),
		})
	})
	if warn != "" {
		t.Errorf("same per-entity version must not warn, got: %q", warn)
	}
	if got.GitTag != "v2026.150.900" {
		t.Errorf("freshness tiebreak: want newest git tag v2026.150.900, got %q", got.GitTag)
	}

	// Different per-entity versions -> exactly one warning, newest version wins.
	got, warn = capture(func() spec.CandyCandidate {
		return loaderkit.PickCandyVersion("github.com/o/r/layers/x", []spec.CandyCandidate{
			mk("2026.141.1600", "v2026.141.1600"),
			mk("2026.144.0531", "v2026.144.531"),
		})
	})
	if got.Version != "2026.144.0531" {
		t.Errorf("newest per-entity version must win, got %q", got.Version)
	}
	if !strings.Contains(warn, "resolved to multiple versions") || !strings.Contains(warn, "2026.144.0531") {
		t.Errorf("expected one multi-version warning naming the winner, got: %q", warn)
	}
}
