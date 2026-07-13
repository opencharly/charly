package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Repo identity for the import-namespace cycle-break.
//
// The namespaced-import loader breaks mutual-import cycles by REPO IDENTITY (the
// `host/owner/repo` path), NOT by the pinned `:version`. This makes the importing
// project's namespace pins authoritative: when a transitively-imported release of
// some repo imports THIS repo back (the intentional main <-> cachyos mutual
// import) at a DIFFERENT pinned version, the back-reference resolves to the node
// already in progress up the load stack (above all the local root) instead of
// fetching a divergent — and possibly stale-schema — snapshot. The cycle-break
// itself now lives in loaderkit.Walk (its namespace-mount recursion), consulting
// this function through the WalkSeams.RepoIdentity seam; the root's own identity
// is seeded into the walk by hostWalkProject (loader_driver.go).

// nsRepoIdentity returns the canonical repo identity of an import ref, or "" when
// it can't be determined (in which case the loader degrades to version-keyed
// behavior). A remote `@host/org/repo[/sub]:ver` ref yields `host/org/repo`
// directly (no fetch, no git); a local path yields the git `origin` identity of
// the target directory.
func nsRepoIdentity(ref, baseDir string) string {
	if strings.HasPrefix(ref, "@") {
		if pr := ParseRemoteRef(ref); pr != nil {
			return pr.RepoPath
		}
		return ""
	}
	p := ref
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, ref)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return ""
	}
	dir := abs
	if info, statErr := os.Stat(abs); statErr == nil && !info.IsDir() {
		dir = filepath.Dir(abs)
	}
	return gitRemoteIdentity(dir)
}

// rootRepoIdentity determines the local root project's own repo identity for
// cycle-break registration. An explicit `repo:` field in charly.yml is
// authoritative; otherwise it falls back to the `git remote origin` identity of
// the working tree. Returns "" when neither is available (the loader then behaves
// exactly as before — version-keyed, no self-identity short-circuit).
func rootRepoIdentity(dir string) string {
	if data, err := os.ReadFile(filepath.Join(dir, UnifiedFileName)); err == nil {
		var head struct {
			Repo string `yaml:"repo" json:"repo"`
		}
		if yaml.Unmarshal(data, &head) == nil && head.Repo != "" {
			return normalizeRepoIdentity(head.Repo)
		}
	}
	return gitRemoteIdentity(dir)
}

// gitRemoteIdentity returns the normalized `host/owner/repo` identity of dir's
// git `origin` remote, or "" when dir is not a git repo / has no origin / git is
// unavailable.
func gitRemoteIdentity(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return normalizeGitRemoteURL(strings.TrimSpace(string(out)))
}

// normalizeRepoIdentity normalizes an explicit `repo:` value (which may be a full
// git URL, an scp-style ref, or a bare `owner/repo`) to the `host/owner/repo`
// form ParseRemoteRef produces — so an explicit declaration and a remote `@`-ref
// compare equal. Reuses normalizeRepoSpec's bare-`owner/repo` → github.com rule.
func normalizeRepoIdentity(s string) string {
	repoPath, _ := normalizeRepoSpec(normalizeGitRemoteURL(s))
	return strings.TrimSuffix(repoPath, ".git")
}

// normalizeGitRemoteURL strips the scheme / `git@` / `.git` decorations from a
// git remote URL, leaving `host/owner/repo`. scp-style (`git@host:owner/repo`)
// and scheme URLs (`https://`, `ssh://`, `git://`, with optional `user@`) are
// both handled. A value already in `host/owner/repo` (or bare `owner/repo`) form
// passes through unchanged.
func normalizeGitRemoteURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	if after, ok := strings.CutPrefix(s, "git@"); ok {
		s = after
		return strings.Replace(s, ":", "/", 1)
	}
	for _, sch := range []string{"https://", "http://", "ssh://", "git://"} {
		if after, ok := strings.CutPrefix(s, sch); ok {
			s = after
			if slash := strings.Index(s, "/"); slash >= 0 {
				if at := strings.Index(s[:slash], "@"); at >= 0 {
					s = s[at+1:]
				}
			}
			return s
		}
	}
	return s
}
