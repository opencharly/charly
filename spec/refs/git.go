// Package refs is the spec fabric slice for the remote-repo GIT primitives — pure git-exec +
// string parsing + cache-path computation, host primitives a plugin composes but the kernel
// itself needs (reconcile's --remote tag query, the collection walk). RELOCATED from sdk/kit
// (#55 fabric-primitive extraction), so charly core reaches them via spec/refs directly and the
// remaining sdk/candy callers keep a thin kit re-export. The FETCH ORCHESTRATION (override →
// cache → clone → migrate) still lives in the refs plugin (candy/plugin-refs); these are the
// reusable mechanisms it composes. The only non-stdlib dependency is the advisory file lock
// (spec/lock) DownloadRepo serializes concurrent cache refresh with.
package refs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/spec/lock"
)

// GitResolveRef resolves a git reference (tag, branch, or commit) to a full commit hash.
// Uses git ls-remote for tags/branches; for commit hashes, validates length and returns as-is.
func GitResolveRef(repoURL string, ref string) (string, error) {
	// If ref looks like a full commit hash (40 hex chars), return as-is
	if len(ref) == 40 && isHex(ref) {
		return ref, nil
	}

	// Query the ref AND its peeled ^{} form so an ANNOTATED tag resolves to the
	// underlying COMMIT (refs/tags/X^{}), not the tag object (refs/tags/X).
	cmd := exec.Command("git", "ls-remote", repoURL, ref, "refs/tags/"+ref, "refs/tags/"+ref+"^{}", "refs/heads/"+ref)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s %s: %w", repoURL, ref, err)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if commit := pickResolvedCommit(lines, ref); commit != "" {
		return commit, nil
	}

	// If nothing matched but ref is a short hex, it might be a short commit
	if len(ref) >= 7 && isHex(ref) {
		return ref, nil
	}

	return "", fmt.Errorf("could not resolve ref %q in %s", ref, repoURL)
}

// pickResolvedCommit selects the commit hash for ref from `git ls-remote`
// output lines. An ANNOTATED tag exposes two refs — `refs/tags/<ref>` (the tag
// OBJECT) and `refs/tags/<ref>^{}` (the COMMIT it points at) — so the peeled
// form is preferred; a lightweight tag or branch has only its direct ref.
// Returns "" when ref isn't present. Keeping this pure makes the peel-preference
// unit-testable without a network round-trip.
func pickResolvedCommit(lines []string, ref string) string {
	peeled := "refs/tags/" + ref + "^{}"
	tagRef := "refs/tags/" + ref
	headRef := "refs/heads/" + ref
	// 1. Peeled annotated-tag commit wins (never the tag object).
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == peeled {
			return parts[0]
		}
	}
	// 2. Exact lightweight-tag / branch / ref match.
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 && (parts[1] == tagRef || parts[1] == headRef || parts[1] == ref) {
			return parts[0]
		}
	}
	// 3. Defensive: any other peeled ref present.
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) >= 2 && strings.HasSuffix(parts[1], "^{}") {
			return parts[0]
		}
	}
	return ""
}

// GitClone clones a git repository at a specific ref into the target directory.
// Uses shallow clone for efficiency.
func GitClone(repoURL string, ref string, commit string, targetDir string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(targetDir), 0755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}

	// Primary: shallow-clone the resolved COMMIT directly. `commit` is already
	// peeled to a real commit by GitResolveRef, and GitHub/GitLab allow fetching
	// a reachable commit by sha. This avoids git's
	// "refs/tags/<tag> <sha> is not a commit!" warning that
	// `git clone --depth 1 --branch <annotated-tag>` emits (the annotated tag
	// ref is a tag object, not a commit).
	if len(commit) >= 7 && isHex(commit) {
		if err := gitCloneByCommit(repoURL, commit, targetDir); err == nil {
			return populateSubmodules(targetDir)
		}
		_ = os.RemoveAll(targetDir) // clean up partial clone before falling back
	}

	// Fallback: clone by ref name (servers that don't allow fetch-by-sha).
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", ref, repoURL, targetDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(targetDir) // clean up partial clone
		return fmt.Errorf("git clone --branch %s %s: %w", ref, repoURL, err)
	}

	return populateSubmodules(targetDir)
}

// populateSubmodules initializes EVERY submodule a freshly-fetched repo declares.
// The raw clone/fetch above populates none of them, and a `--repo` cache is a
// WHOLE PROJECT a user drives with nothing but the charly binary — so any
// submodule may be load-bearing for a documented command:
//
//   - sdk + spec — every out-of-process plugin candy builds STANDALONE in its own
//     module (GOWORK=off) against `replace … => ../../sdk` and `=> ../../spec`.
//     ALL 86 candy go.mod files carry BOTH replaces, so populating only `sdk`
//     left every such build failing on `../../spec/go.mod: no such file`, and no
//     out-of-process verb could be reached through --repo at all.
//   - box/<distro> — the box definitions themselves; main owns none.
//   - plugins, docs, pkg/* — skills, the docs site, and the packaging sources.
//
// This deliberately replaces an `sdk`-only special case whose stated rationale
// ("the ONLY submodule a plugin build depends on … never the heavy box/* ones")
// was wrong on both counts: spec is equally required, and a shallow init of all
// twelve costs ~8s and adds 24MB to a 36MB clone (60MB total) — the cost that
// "heavy" was guarding against does not exist at --depth 1. A repo declaring no
// submodules is a clean no-op.
//
// The insteadOf rewrite forces the .gitmodules SSH URL (git@github.com:) to
// HTTPS — matching how the parent repo is cloned — so no SSH key is needed in a
// headless/CI run. Non-recursive, matching the configuration proven to build.
//
// A SUBMODULE THAT CANNOT BE FETCHED FAILS THE WHOLE CLONE, deliberately. This
// is a real widening: the old code was a silent no-op for any repo not
// declaring `sdk`, so a third-party project with a private or dead submodule
// used to fetch "fine" and now errors here. That is the correct trade, because a
// SILENTLY PARTIAL cache is the exact defect this function exists to fix — it
// cost a full debugging session, surfacing three layers down as
// `../../spec/go.mod: no such file` with nothing pointing back at the fetch. A
// caller that cannot reach a declared submodule has an incomplete project and is
// told so HERE, naming the submodule, rather than at some later build whose
// error does not mention fetching at all. git's own stderr is wrapped in, so the
// specific submodule and cause survive into the message.
//
// The blast radius is narrower than .gitmodules suggests: `submodule update
// --init` walks the INDEX, so an entry declared in .gitmodules with no gitlink
// (mode 160000) recorded is ignored and cannot fail a fetch. Only submodules
// genuinely committed into the tree are attempted.
func populateSubmodules(targetDir string) error {
	gm := filepath.Join(targetDir, ".gitmodules")
	if _, err := os.Stat(gm); err != nil {
		return nil // no submodules declared — nothing to populate
	}
	cmd := exec.Command("git",
		"-c", "url.https://github.com/.insteadOf=git@github.com:",
		"-c", "advice.detachedHead=false",
		"submodule", "update", "--init", "--depth", "1", "-q")
	cmd.Dir = targetDir
	// Capture rather than pass through: git names the offending submodule and the
	// reason (auth, missing ref, unreachable host) on stderr, and that is the only
	// thing that makes this failure actionable. Passing it to os.Stderr would print
	// it detached from the error the caller reports.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("populating submodules in %s: %w\n%s", targetDir, err, detail)
		}
		return fmt.Errorf("populating submodules in %s: %w", targetDir, err)
	}
	return nil
}

// gitCloneByCommit clones a repo and checks out a specific commit
func gitCloneByCommit(repoURL string, commit string, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	cmds := [][]string{
		// Keep the throwaway clone SILENT on success (zero-warnings gate):
		// -c init.defaultBranch suppresses git's "using 'master' ... suppress
		// this warning" hint; -q + advice.detachedHead=false silence the
		// remaining init / fetch / detached-checkout chatter.
		{"git", "-c", "init.defaultBranch=main", "init", "-q"},
		{"git", "remote", "add", "origin", repoURL},
		{"git", "fetch", "--depth", "1", "-q", "origin", commit},
		{"git", "-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = targetDir
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(targetDir) // clean up on failure
			return fmt.Errorf("git %s: %w", strings.Join(args[1:], " "), err)
		}
	}

	return nil
}

// RepoGitURL converts a repo path to a git clone URL.
// e.g. "github.com/opencharly/ml-layers" -> "https://github.com/opencharly/ml-layers.git"
func RepoGitURL(repoPath string) string {
	return "https://" + repoPath + ".git"
}

// IsMutableRef reports whether a repo ref version can ADVANCE upstream (a
// branch name such as main, or an empty version that resolves to the default
// branch). Immutable coordinates — CalVer/semver tags (v…) and full commit
// SHAs — never change what they point at; the project's tags are add-only.
// Mutable refs must re-resolve on every access: a cache hit on a branch
// otherwise freezes it at first-download content forever (the pre-#146 @main
// protocol skew — a stale main export served protocol-v1 plugin sources
// indefinitely).
func IsMutableRef(version string) bool {
	if version == "" {
		return true
	}
	if strings.HasPrefix(version, "v") {
		return false
	}
	if len(version) == 40 && isHex(version) {
		return false
	}
	return true
}

// refProvenancePath is the sidecar recording the commit a cache export was
// cloned from.
func refProvenancePath(cachePath string) string {
	return cachePath + ".ref"
}

// writeRefProvenance records the clone's resolved commit next to the export.
func writeRefProvenance(cachePath, commit string) error {
	return os.WriteFile(refProvenancePath(cachePath), []byte(commit+"\n"), 0o644)
}

// repoCacheFresh reports whether the cache at cachePath is a complete export
// cloned from exactly commit. A missing export, a missing provenance sidecar
// (a cache written before this contract), a sidecar naming a different commit
// (the ref moved upstream), or an INCOMPLETE export all count as stale.
//
// Completeness is checked, not assumed. This function has always claimed to
// verify "a complete export" and never did — it compared only the commit — so a
// cache whose CONTENT was wrong stayed a permanent hit as long as the ref did
// not move. That is not hypothetical: every cache written before
// populateSubmodules holds one of twelve submodules, records the correct commit,
// and is therefore served forever. Without this check the submodule fix would
// only ever help caches created AFTER it, and every existing user would stay
// broken until main happened to advance — with no CLI verb to invalidate a repo
// cache entry (`charly clean --invalidate` targets image tags), leaving hand-
// deleting a cache directory as the only remedy. Verifying content instead makes
// every such cache self-heal on next access.
func repoCacheFresh(cachePath, commit string) bool {
	if commit == "" {
		return false
	}
	if st, err := os.Stat(cachePath); err != nil || !st.IsDir() {
		return false
	}
	recorded, err := os.ReadFile(refProvenancePath(cachePath))
	if err != nil {
		return false
	}
	if strings.TrimSpace(string(recorded)) != commit {
		return false
	}
	return submodulesPopulated(cachePath)
}

// submodulesPopulated reports whether every submodule the export declares has
// content on disk. An export declaring none is trivially complete.
//
// The export has had its .git removed (see downloadRepoFrom), so this reads
// .gitmodules directly — via `git config -f`, the same parser git itself uses,
// rather than a second hand-rolled INI reader that could disagree with it. An
// unreadable or absent .gitmodules means nothing to verify, which keeps a
// non-submodule repo on the pure cache-hit path.
func submodulesPopulated(cachePath string) bool {
	gm := filepath.Join(cachePath, ".gitmodules")
	if _, err := os.Stat(gm); err != nil {
		return true
	}
	out, err := exec.Command("git", "config", "-f", gm, "--get-regexp", `submodule\..*\.path`).Output()
	if err != nil {
		return true // no submodule.*.path entries — nothing to verify
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(cachePath, fields[1]))
		if err != nil {
			// ABSENT is not incomplete. git materializes an empty placeholder
			// directory for every GITLINK it clones, so a path missing entirely
			// was never gitlinked — a .gitmodules entry with no index entry, which
			// populateSubmodules cannot fetch either (it walks the index). Treating
			// that as incomplete would make the export permanently unfresh and
			// re-clone the repo on EVERY command, forever. Only an existing-but-
			// empty directory is the real unpopulated-gitlink case.
			continue
		}
		if len(entries) == 0 {
			return false
		}
	}
	return true
}

// DownloadRepo downloads a remote repo to the cache.
// Returns the cache path where the repo was stored.
//
// Freshness contract: the cache is reused ONLY when its recorded provenance
// (the commit the export was cloned from) equals the ref's CURRENTLY resolved
// commit — so a branch that advanced upstream (main) is re-downloaded instead
// of silently serving stale content. The check costs the one ls-remote
// GitResolveRef already performs; for immutable tags the provenance always
// matches, so their cache-hit behavior is unchanged. A cache written before
// this contract has no provenance and is re-downloaded once, then self-heals.
func DownloadRepo(repoPath string, version string) (string, error) {
	return downloadRepoFrom(RepoGitURL(repoPath), repoPath, version)
}

// downloadRepoFrom is DownloadRepo with the clone URL explicit, so the
// freshness contract is testable against a local file:// remote.
func downloadRepoFrom(repoURL, repoPath, version string) (string, error) {
	// Resolve the ref to a commit hash
	commit, err := GitResolveRef(repoURL, version)
	if err != nil {
		return "", fmt.Errorf("resolving %s:%s: %w", repoPath, version, err)
	}

	cachePath, err := RepoCachePath(repoPath, version)
	if err != nil {
		return "", err
	}

	// Serialize concurrent download/refresh of the SAME cache path (a blocking
	// per-path file lock, the plugin build-cache pattern): the loser re-checks
	// provenance under the lock and reuses the winner's fresh export instead of
	// cloning over it.
	release, err := lock.AcquireFileLock(cachePath+".lock", true)
	if err != nil {
		return "", fmt.Errorf("acquiring repo cache lock %s: %w", cachePath, err)
	}
	defer func() { _ = release() }()

	if repoCacheFresh(cachePath, commit) {
		return cachePath, nil
	}

	fmt.Fprintf(os.Stderr, "Downloading %s:%s...\n", repoPath, version)

	// Clone into a sibling temp dir first, then swap: readers see either the
	// complete old export or the complete new one, never a half-cloned tree.
	tmpPath := cachePath + ".download"
	_ = os.RemoveAll(tmpPath) // leftover from an interrupted earlier download
	if err := GitClone(repoURL, version, commit, tmpPath); err != nil {
		_ = os.RemoveAll(tmpPath)
		return "", fmt.Errorf("downloading %s:%s: %w", repoPath, version, err)
	}

	// Remove .git directory to save space (cache is read-only)
	_ = os.RemoveAll(filepath.Join(tmpPath, ".git"))

	// Publish: swing any old export aside, move the fresh one in, then drop the
	// old — the no-tree window is the single rename between them, and a publish
	// failure restores the old export.
	gcPath := cachePath + ".gc"
	_ = os.RemoveAll(gcPath)
	hadOld := false
	if _, err := os.Stat(cachePath); err == nil {
		hadOld = true
		if err := os.Rename(cachePath, gcPath); err != nil {
			_ = os.RemoveAll(tmpPath)
			return "", fmt.Errorf("parking old repo cache %s: %w", cachePath, err)
		}
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		if hadOld {
			_ = os.Rename(gcPath, cachePath)
		}
		_ = os.RemoveAll(tmpPath)
		return "", fmt.Errorf("publishing repo cache %s: %w", cachePath, err)
	}
	_ = os.RemoveAll(gcPath)

	return cachePath, writeRefProvenance(cachePath, commit)
}

// GitDefaultBranch detects the default branch of a remote repository.
// Uses git ls-remote --symref to find what HEAD points to.
// Returns the branch name (e.g., "main", "master").
func GitDefaultBranch(repoURL string) (string, error) {
	cmd := exec.Command("git", "ls-remote", "--symref", repoURL, "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote --symref %s HEAD: %w", repoURL, err)
	}
	branch := parseDefaultBranch(string(out))
	if branch == "" {
		return "", fmt.Errorf("could not determine default branch for %s", repoURL)
	}
	return branch, nil
}

// parseDefaultBranch extracts the branch name from git ls-remote --symref output.
// Example line: "ref: refs/heads/main\tHEAD"
func parseDefaultBranch(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			// "ref: refs/heads/main\tHEAD" -> "main"
			ref := after
			if before, _, ok := strings.Cut(ref, "\t"); ok {
				return before
			}
		}
	}
	return ""
}

// GitLatestTag queries a remote repo for tags and returns the highest semver tag.
// Looks for tags matching v* pattern, sorts by semver, returns the highest.
// Returns an error if no version tags are found.
func GitLatestTag(repoURL string) (string, error) {
	cmd := exec.Command("git", "ls-remote", "--tags", repoURL)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote --tags %s: %w", repoURL, err)
	}

	tags := parseTagRefs(string(out))
	if len(tags) == 0 {
		return "", fmt.Errorf("no version tags found in %s", repoURL)
	}

	sort.Slice(tags, func(i, j int) bool {
		return CompareSemver(tags[i], tags[j]) < 0
	})

	return tags[len(tags)-1], nil
}

// parseTagRefs extracts tag names from git ls-remote --tags output.
// Filters for v* tags and excludes peeled refs (^{}).
func parseTagRefs(output string) []string {
	var tags []string
	seen := make(map[string]bool)
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		ref := parts[1]
		// Skip peeled refs
		if strings.HasSuffix(ref, "^{}") {
			continue
		}
		tag := strings.TrimPrefix(ref, "refs/tags/")
		if !strings.HasPrefix(tag, "v") {
			continue
		}
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
}

// CompareSemver compares two semver-like version strings (e.g. "v1.2.3").
// Returns -1 if a < b, 0 if equal, 1 if a > b.
// Handles v-prefixed versions and falls back to string comparison for non-numeric parts.
func CompareSemver(a, b string) int {
	aParts := parseSemverParts(a)
	bParts := parseSemverParts(b)

	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		var av, bv int
		if i < len(aParts) {
			av = aParts[i]
		}
		if i < len(bParts) {
			bv = bParts[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// parseSemverParts extracts numeric parts from a version string like "v1.2.3".
func parseSemverParts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	// Strip pre-release suffix (e.g. "-rc1")
	if idx := strings.IndexByte(v, '-'); idx != -1 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n := 0
		for _, c := range p {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			} else {
				break
			}
		}
		nums = append(nums, n)
	}
	return nums
}

// isHex returns true if s contains only hexadecimal characters
func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return len(s) > 0
}
