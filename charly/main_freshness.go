package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CheckBinaryFreshness verifies the running charly binary isn't stale relative
// to the source tree it's invoked from. Aborts the process with a clear,
// actionable error message if the source tree at (or above) cwd contains
// .go files newer than the running binary.
//
// Why this exists — the 2026-05-09 cuda-cudnn incident:
//
//	A user ran `charly box build versa` against the system /usr/bin/charly
//	binary that was 2 days old. The source tree at the same project root
//	had a freshly-committed cache-mount fix (commit 230c5d4) emitting
//	`--mount=type=cache,id=charly-var-cache-libdnf5,...`. The stale binary
//	emitted the old form WITHOUT `id=`, which broke buildah's cross-build
//	cache reuse, forcing a full re-download of cuda-cudnn (462 MiB) over
//	a 50 KiB/s mirror — 90 minutes burned. The bug was undetectable from
//	the build log alone; only `which charly` + `stat /usr/bin/charly` versus
//	`stat ./charly/charly` revealed the mismatch.
//
//	Per CLAUDE.md R9 ("After pushing code, explicitly rebuild on the
//	target and verify charly version. If the version is old, the fix under
//	test isn't really under test."), this guardrail makes the check
//	automatic at every invocation rather than relying on developer
//	discipline.
//
// Detection logic:
//  1. Walk up from cwd until we find a directory containing BOTH
//     charly/main.go AND charly.yml — this identifies an opencharly source
//     tree unambiguously (other projects might have a charly.yml, but
//     only opencharly has charly/main.go). If no source tree found, no
//     enforcement (the binary is being run against a non-opencharly
//     project; nothing to compare against).
//  2. Stat the running binary via os.Executable().
//  3. Walk every .go file under <sourceRoot>/charly/, find the newest mtime.
//  4. If newest source mtime > binary mtime + 60s slack, the binary is
//     stale. Emit a detailed error and exit 1.
//
// The 60-second slack absorbs same-clock-second builds (binary written,
// then a .go file touched within the same second by an editor save) and
// filesystem mtime rounding (some filesystems only resolve to the
// nearest second).
//
// Verb gating: info-only verbs (version, help, status, inspect, list)
// skip the check — these are read-only diagnostics that work correctly
// under any binary version, and being able to run `charly version` to
// confirm the mismatch is essential when debugging the freshness error
// itself. Heavyweight verbs (image build, image generate, deploy,
// rebuild, check, ...) enforce the check.
//
// Bypass: CHARLY_SKIP_FRESHNESS_CHECK=1 disables the check entirely. Use
// this for CI runs where the binary is intentionally pinned to a
// specific build, for testing pre-built images, or as an emergency
// override. NOT recommended for routine development.
func CheckBinaryFreshness(verbPath string) {
	if os.Getenv("CHARLY_SKIP_FRESHNESS_CHECK") != "" {
		return
	}
	if isFreshnessSafeVerb(verbPath) {
		return
	}

	binPath, err := os.Executable()
	if err != nil {
		return
	}
	binStat, err := os.Stat(binPath)
	if err != nil {
		return
	}

	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	sourceRoot := findCharlySourceRoot(cwd)
	if sourceRoot == "" {
		return
	}

	newestPath, newestModTime := newestGoFile(filepath.Join(sourceRoot, "charly"))
	if newestPath == "" {
		return
	}

	// 60-second slack: same-second builds + FS mtime rounding.
	if newestModTime.Before(binStat.ModTime().Add(60 * time.Second)) {
		return
	}

	rel, _ := filepath.Rel(sourceRoot, newestPath)
	fmt.Fprintf(os.Stderr, "charly: error: stale binary detected — refusing to run %q\n\n", verbPath)
	fmt.Fprintf(os.Stderr, "  running:        %s\n", binPath)
	fmt.Fprintf(os.Stderr, "  binary mtime:   %s\n", binStat.ModTime().Format(time.RFC3339))
	fmt.Fprintf(os.Stderr, "  newer source:   %s\n", rel)
	fmt.Fprintf(os.Stderr, "  source mtime:   %s\n", newestModTime.Format(time.RFC3339))
	fmt.Fprintf(os.Stderr, "  source root:    %s\n\n", sourceRoot)
	fmt.Fprintf(os.Stderr, "The source tree has been edited since this binary was built.\n")
	fmt.Fprintf(os.Stderr, "Running heavy operations against a stale binary leads to silent\n")
	fmt.Fprintf(os.Stderr, "miscompilation — e.g. the 2026-05-09 cuda-cudnn incident burned\n")
	fmt.Fprintf(os.Stderr, "90 minutes re-downloading 462 MiB because /usr/bin/charly predated\n")
	fmt.Fprintf(os.Stderr, "the cache-mount fix at commit 230c5d4.\n\n")
	fmt.Fprintf(os.Stderr, "Fix:    cd %s && task build:charly\n", sourceRoot)
	fmt.Fprintf(os.Stderr, "Bypass: export CHARLY_SKIP_FRESHNESS_CHECK=1   (NOT recommended)\n")
	os.Exit(1)
}

// isFreshnessSafeVerb returns true for read-only diagnostic verbs that
// produce correct output under any binary version. The principle: if a
// verb only reads state and emits text, it's safe; if it writes files,
// builds artifacts, or deploys, it must run on the current source.
func isFreshnessSafeVerb(verbPath string) bool {
	// Kong's ctx.Command() returns space-joined paths like "box build",
	// "secrets gpg env", "version". Match by exact name OR by prefix
	// (so "box inspect ..." matches "box inspect").
	safePrefixes := []string{
		"version",
		"help",
		"status",        // reads container state
		"box inspect",   // reads the project config + emits JSON
		"box list",      // reads the project config + emits text
		"box validate",  // reads the project config + emits warnings (no writes)
		"bundle show",   // reads charly.yml
		"bundle status", // reads deploy state
		"bundle path",   // prints a path
		"secrets list",  // reads credential store
		"secrets get",
		"secrets path",
		"settings show",
	}
	for _, p := range safePrefixes {
		if verbPath == p || strings.HasPrefix(verbPath, p+" ") {
			return true
		}
	}
	return false
}

// CheckBinaryStamped refuses to run the disposable-bed RUNNER (`charly check run <bed>`) when the
// executing binary reports an UNSTAMPED version ("unknown"), pointing at `task build:binary`. It is
// the version-identity analog of CheckBinaryFreshness: a plain `go build -o bin/charly ./charly`
// leaves BuildCalVer empty → CharlyVersion()=="unknown", and the bed runner then fails MINUTES later
// inside an eval-VM plan step (image tags, plugin-cache keys, and bed stamp assertions all key off the
// version) — or passes vacuously. This makes the precondition mechanical instead of a behavioral
// "remember to task build:binary" rule the gate defect twice slipped past (R4: a precondition, never a
// retry). Scope is ONLY `charly check run`: ctx.Command() collapses the whole check family to "check"
// (the subcommand is a passthrough arg), so "run" is read from os.Args; check box/live and every other
// verb are unaffected. The CHARLY_SKIP_FRESHNESS_CHECK bypass disables this too (CI / pinned builds).
func CheckBinaryStamped(verbPath string) {
	if !shouldRefuseUnstamped(verbPath) {
		return
	}
	binPath, _ := os.Executable()
	fmt.Fprintf(os.Stderr, `charly: error: refusing to run "check run" with an UNSTAMPED binary (version "unknown")

  running:  %s

A plain `+"`go build`"+` produces an unstamped binary. `+"`charly check run`"+` bakes the version into
image tags, plugin-cache keys, and bed stamp assertions, so a bed run against an
unstamped binary fails minutes later inside a plan step (or passes vacuously) — the
twice-recurred gate defect.

Fix:    task build:binary                        # stamps the CalVer identity
Bypass: export CHARLY_SKIP_FRESHNESS_CHECK=1      (NOT recommended)
`, binPath)
	os.Exit(1)
}

// checkSubcommandIsRun reports whether the check family's passthrough subcommand is "run" — the token
// immediately after the "check" command word in os.Args. externalCommandHolder forwards everything
// after the command word as passthrough Args, so ctx.Command() collapses to "check" for run/box/live
// alike, and the subcommand can only be recovered from os.Args.
func checkSubcommandIsRun() bool {
	for i, a := range os.Args {
		if a == "check" {
			return i+1 < len(os.Args) && os.Args[i+1] == "run"
		}
	}
	return false
}

// shouldRefuseUnstamped is CheckBinaryStamped's pure decision: refuse iff the verb is `check run`
// (the bed runner) AND the binary is unstamped, and the CHARLY_SKIP_FRESHNESS_CHECK bypass is unset.
// verbPath is normalized via commandPathKey because Kong renders the check family's passthrough as
// `check <args>` (NOT `check`), so an exact "check" compare silently misses every real invocation —
// the bug the isolated os.Args unit test could not catch.
func shouldRefuseUnstamped(verbPath string) bool {
	if os.Getenv("CHARLY_SKIP_FRESHNESS_CHECK") != "" {
		return false
	}
	if commandPathKey(verbPath) != "check" || !checkSubcommandIsRun() {
		return false
	}
	return CharlyVersion() == "unknown"
}

// findCharlySourceRoot walks up from start looking for a directory that
// contains BOTH charly/main.go AND charly.yml. Returns the path to that
// directory, or empty string if no such ancestor exists within 12 levels.
//
// The dual-marker requirement (charly/main.go + charly.yml) is what makes
// this unambiguous: many projects have a charly.yml; only opencharly has
// charly/main.go alongside it.
func findCharlySourceRoot(start string) string {
	cur := start
	for range 12 {
		if statExists(filepath.Join(cur, "charly", "main.go")) &&
			statExists(filepath.Join(cur, UnifiedFileName)) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
	return ""
}

// statExists is a tiny helper that returns true iff os.Stat succeeds.
func statExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// newestGoFile walks dir recursively (skipping vendor / node_modules /
// .git) and returns the path + mtime of the .go file with the latest
// mtime. Returns "", zero-time if no .go files found or dir doesn't
// exist.
func newestGoFile(dir string) (string, time.Time) {
	var newestPath string
	var newestModTime time.Time

	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == "vendor" || base == "node_modules" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		// _test.go files never enter the built binary (go build excludes
		// them), so an edited test cannot make the installed charly stale —
		// counting them produced false-positive staleness during test-only
		// work.
		if strings.HasSuffix(p, "_test.go") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(newestModTime) {
			newestModTime = info.ModTime()
			newestPath = p
		}
		return nil
	})
	return newestPath, newestModTime
}
