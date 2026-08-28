package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
)

// refs_sweep_test.go — the candy-cutover ref sweep, THE gate for the ref-advance
// PRs (bare candy names → @github.com/opencharly/<kind>-<name>:v<tag> pins).
//
// Why this test exists (RCA 2026-08-25): the batch-2.3-2.6 PR body claimed "the
// resolver test exercised the changed refs at the pinned tags" — FALSE. The only
// resolution test in the suite, TestCandySourceDirs_OverrideAnchorsRemoteApk,
// scans box/cachyos ONLY; its remote-ref closure reaches a couple of batch-2.2
// candies and NONE of the batch-2.3-2.6 refs, and a fresh-cache full-suite run
// downloads ZERO remote repos. There was NO automated gate asserting that every
// @github.com/opencharly/... pin in the tree resolves at its pinned tag, nor
// that no bare candy-list name survives for a moved candy. The missed
// `- container-nesting` bare ref (validator finding 1) is exactly the class
// this sweep exists to catch.
//
// The rule (ONE canonical sweep, no external table): a candy is "moved" iff a
// KIND-PREFIXED standalone pin @github.com/opencharly/<kind>-<name> exists
// anywhere in the tree. Once moved, every require:/candy: list ref to that
// candy MUST be the @github pin — a bare `- <name>` is a cutover defect (R5
// same-commit sweep). In-repo refs of the form
// @github.com/opencharly/charly/candy/<name> (the check-* test-harness
// fixtures) are NOT moved-candy pins and stay bare by design.

var (
	// remoteRefRe matches @github.com/opencharly/<path>:v<calver> pins.
	remoteRefRe = regexp.MustCompile(`@github\.com/opencharly/([a-zA-Z0-9._/-]+):v[0-9]+\.[0-9]+\.[0-9]+`)
	// movedPinRe matches KIND-PREFIXED standalone repo pins (layer-|pod-|vm-|box-).
	movedPinRe = regexp.MustCompile(`@github\.com/opencharly/(layer|pod|vm|box)-([a-z0-9-]+):v[0-9]+\.[0-9]+\.[0-9]+`)
	// bareListItemRe matches a bare `- <name>` list item (no @, no /, no :).
	bareListItemRe = regexp.MustCompile(`^-\s+([a-z][a-z0-9-]*)\s*(?:#.*)?$`)
	// candyListKeyRe matches a require:/candy: list header line.
	candyListKeyRe = regexp.MustCompile(`^\s*(require|candy):\s*$`)
)

// collectRefs walks every charly.yml under the repo root (candy/**, box/**,
// box/*/box/** and the root manifest) and returns:
//   - movedBare: bare `- <name>` candy-list items whose candy has a standalone
//     kind-prefixed pin elsewhere in the tree (cutover sweep defects), and
//   - pins: every remote @github.com/opencharly/... ref with its repo+version.
func collectRefs(repoRoot string) (movedBare []string, pins []string) {
	var ymls []string
	ymls = append(ymls, filepath.Join(repoRoot, "charly.yml"))
	for _, pat := range []string{
		filepath.Join(repoRoot, "candy", "*", "charly.yml"),
		filepath.Join(repoRoot, "box", "*", "charly.yml"),
		filepath.Join(repoRoot, "box", "*", "box", "*", "charly.yml"),
	} {
		m, _ := filepath.Glob(pat)
		ymls = append(ymls, m...)
	}

	// Pass 1: collect every kind-prefixed standalone pin in the tree → the moved set.
	moved := map[string]bool{} // bare candy name → moved
	for _, f := range ymls {
		b, err := os.ReadFile(f)
		if err != nil {
			continue // box submodule placeholder dirs glob fine but read as dirs
		}
		for _, m := range movedPinRe.FindAllStringSubmatch(string(b), -1) {
			moved[m[2]] = true // kind in m[1], name in m[2]
		}
	}

	// Pass 2: walk require:/candy: lists; flag bare items for moved candies,
	// collect every remote pin.
	for _, f := range ymls {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(string(b), "\n")
		inList := false
		for _, ln := range lines {
			trimmed := strings.TrimSpace(ln)
			if candyListKeyRe.MatchString(trimmed) {
				inList = true
				continue
			}
			if inList {
				// A non-empty non-list line ends the list block.
				if trimmed != "" && !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "#") {
					inList = false
				} else if m := bareListItemRe.FindStringSubmatch(trimmed); m != nil {
					if moved[m[1]] {
						movedBare = append(movedBare, fmt.Sprintf("%s: - %s", f, m[1]))
					}
				}
			}
		}
		pins = append(pins, remoteRefRe.FindAllString(string(b), -1)...)
	}
	slices.Sort(movedBare)
	movedBare = slices.Compact(movedBare)
	slices.Sort(pins)
	pins = slices.Compact(pins)
	return movedBare, pins
}

// TestCandyCutoverSweep_NoBareMovedRefs asserts the R5 same-commit sweep: no bare
// candy-list name survives for any candy that has moved to a standalone repo.
// FAILS on the missed-sweep class (the `- container-nesting` defect) — the
// exact gap the batch-2.3-2.6 validator blocked on. Requires only the tree;
// no network.
func TestCandyCutoverSweep_NoBareMovedRefs(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(wd)
	movedBare, _ := collectRefs(repoRoot)
	if len(movedBare) > 0 {
		t.Fatalf("bare refs to moved candies survive the cutover sweep (R5):\n  %s",
			strings.Join(movedBare, "\n  "))
	}
	t.Logf("sweep: zero bare refs to moved candies across %s", repoRoot)
}

// TestCandyCutoverSweep_AllRemotePinsResolve asserts every @github.com/opencharly/...
// pin in the tree resolves at its pinned tag via the CANONICAL loader path
// (requireProjectLoader().EnsureRepoDownloaded → loaderkit.EnsureRepoDownloaded),
// the same path runtime resolution uses. FAILS on any dangling pin — the
// double-v-tag bug class. The repo cache makes repeat runs fast; a cold cache
// downloads each unique repo+tag once.
func TestCandyCutoverSweep_AllRemotePinsResolve(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(wd)
	_, pins := collectRefs(repoRoot)
	if len(pins) == 0 {
		t.Skip("no remote pins in tree — cutover not started")
	}

	ctx := hostInProcCtx()
	loader := requireProjectLoader()
	unique := map[string]bool{}
	var failures []string

	// Deduplicate FIRST, then resolve concurrently. Each miss is one network round trip,
	// and the cutover left 226 unique pins: resolving them one after another cost ~7
	// minutes of the package's 10-minute default timeout on a cold CI cache, so the
	// package ran with no headroom and ANY test added to it tipped the whole package over.
	// A warm cache hides this completely (0.04s locally), which is why it surfaced as an
	// unrelated PR's CI failure rather than as this test's.
	type job struct{ pin, repo, version string }
	var jobs []job
	for _, pin := range pins {
		parsed := spec.ParseRemoteRef(pin)
		if parsed.RepoPath == "" || parsed.Version == "" {
			failures = append(failures, fmt.Sprintf("unparseable pin %q", pin))
			continue
		}
		key := parsed.RepoPath + ":" + parsed.Version
		if unique[key] {
			continue
		}
		unique[key] = true
		jobs = append(jobs, job{pin: pin, repo: parsed.RepoPath, version: parsed.Version})
	}

	// Bounded: the point is to overlap network latency, not to open 226 sockets at a
	// remote we do not own.
	const workers = 12
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	ch := make(chan job)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				path, err := loader.EnsureRepoDownloaded(ctx, j.repo, j.version)
				mu.Lock()
				if err != nil {
					failures = append(failures, fmt.Sprintf("%s: %v", j.pin, err))
				} else {
					t.Logf("resolved %s -> %s", j.pin, path)
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	wg.Wait()

	if len(failures) > 0 {
		t.Fatalf("%d remote pins failed to resolve at their pinned tag:\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
	t.Logf("all %d unique remote pins resolved at their pinned tags (cold-cache downloads shown above)", len(unique))
}

// TestCandyCutoverSweep_NoCharlyCandyRefs asserts the cutover end-state: no
// require:/candy: list in CHARLY'S OWN tree (root charly.yml + candy/**) may
// reference a candy via the OLD in-repo @github.com/opencharly/charly/candy/<name>
// form EXCEPT the check-* test fixtures, which are charly's own R10 bed candies
// composed as LOCAL members (fleet add rejects remote primary candy refs — the
// named gap). A non-check-* charly/candy ref is a stale pre-cutover pin. The
// box/* trees are SUBMODULE repos with their own cutover timelines and are
// deliberately excluded.
func TestCandyCutoverSweep_NoCharlyCandyRefs(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Dir(wd)
	ymls := append([]string{filepath.Join(repoRoot, "charly.yml")}, func() (r []string) {
		m, _ := filepath.Glob(filepath.Join(repoRoot, "candy", "*", "charly.yml"))
		return m
	}()...)
	re := regexp.MustCompile(`-\s+'?@github\.com/opencharly/charly/candy/([a-z0-9-]+)`)
	var stale []string
	for _, f := range ymls {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			name := m[1]
			if strings.HasPrefix(name, "check-") {
				continue // check-* R10 bed fixtures stay in-repo (fleet-add gap)
			}
			stale = append(stale, fmt.Sprintf("%s: %s", f, m[0]))
		}
	}
	if len(stale) > 0 {
		t.Fatalf("stale in-repo candy pins (charly/candy/<name>) survive for non-fixture candies:\n  %s",
			strings.Join(stale, "\n  "))
	}
	t.Logf("no stale in-repo charly/candy pins outside the check-* fixtures")
}

// TestCandyCutoverSweep_BoxLessSkips mirrors the box/cachyos test's graceful
// box-less checkout handling: the resolve sweep may run against a tree whose
// box/* submodules are gitlink placeholders (empty dirs). The walker already
// tolerates unreadable files; this test just proves the walker terminates on a
// minimal tree (a smoke of collectRefs's error tolerance).
func TestCandyCutoverSweep_WalkerToleratesBoxLess(t *testing.T) {
	repoRoot := t.TempDir()
	// The fixture IS the precondition: a write that silently failed would make the
	// walker terminate on an empty tree and pass for the wrong reason.
	mustWrite(t, filepath.Join(repoRoot, "charly.yml"),
		"discover:\n  - path: candy\n    recursive: true\n")
	if err := os.MkdirAll(filepath.Join(repoRoot, "candy", "keepalive"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repoRoot, "candy", "keepalive", "charly.yml"),
		"keepalive:\n  candy:\n    - base: quay.io/fedora/fedora:43\n    - '@github.com/opencharly/pod-keepalive:v2026.237.100'\n")
	movedBare, pins := collectRefs(repoRoot)
	if len(movedBare) != 0 {
		t.Fatalf("bare moved ref: %v", movedBare)
	}
	if len(pins) != 1 {
		t.Fatalf("expected 1 pin, got %d", len(pins))
	}
	_ = proc.RepoOverrideEnv // keep the import alive for parity with the resolver test
}
