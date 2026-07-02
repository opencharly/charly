package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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
			return nil
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

// DownloadRepo downloads a remote repo to the cache.
// Returns the cache path where the repo was stored.
func DownloadRepo(repoPath string, version string) (string, error) {
	repoURL := RepoGitURL(repoPath)

	// Resolve the ref to a commit hash
	commit, err := GitResolveRef(repoURL, version)
	if err != nil {
		return "", fmt.Errorf("resolving %s:%s: %w", repoPath, version, err)
	}

	cachePath, err := RepoCachePath(repoPath, version)
	if err != nil {
		return "", err
	}

	// Check if already cached
	if _, err := os.Stat(cachePath); err == nil {
		return cachePath, nil
	}

	fmt.Fprintf(os.Stderr, "Downloading %s:%s...\n", repoPath, version)

	// Clone into cache
	if err := GitClone(repoURL, version, commit, cachePath); err != nil {
		return "", fmt.Errorf("downloading %s:%s: %w", repoPath, version, err)
	}

	// Remove .git directory to save space (cache is read-only)
	_ = os.RemoveAll(filepath.Join(cachePath, ".git"))

	return cachePath, nil
}

// DiscoverRemoteCandy returns the list of candy names in a remote repo directory
func DiscoverRemoteCandy(repoDir string) ([]string, error) {
	candiesDir := filepath.Join(repoDir, DefaultCandyDir)
	entries, err := os.ReadDir(candiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
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
		return compareSemver(tags[i], tags[j]) < 0
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

// compareSemver compares two semver-like version strings (e.g. "v1.2.3").
// Returns -1 if a < b, 0 if equal, 1 if a > b.
// Handles v-prefixed versions and falls back to string comparison for non-numeric parts.
func compareSemver(a, b string) int {
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
