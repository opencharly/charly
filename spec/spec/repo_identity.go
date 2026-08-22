package spec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// repo_identity.go — repo identity for the import-namespace cycle-break, and --repo spec
// normalization. Pure fs/git/yaml logic with no *Config/*Candy dependency, relocated to the
// dedicated spec module (#55 2b Class A) so charly core + the loader-consuming plugins reach it
// without importing loaderkit. Consumed by the WalkProject entry point (candy/plugin-loader defaults
// the WalkSeams.RepoIdentity + rootIdentity) and candy/plugin-build's superproject resolve. charly's
// own loader-cone consumers (refs.go, main_repo.go) are GONE — K-wave 2 cone R1 moved the fetch
// orchestration and the --repo resolve into candy/plugin-loader. loaderkit re-exports these as
// forwarders for its own callers.
//
// The namespaced-import loader breaks mutual-import cycles by REPO IDENTITY (the `host/owner/repo`
// path), NOT by the pinned `:version`. This makes the importing project's namespace pins
// authoritative: when a transitively-imported release of some repo imports THIS repo back (the
// intentional main <-> cachyos mutual import) at a DIFFERENT pinned version, the back-reference
// resolves to the node already in progress up the load stack instead of fetching a divergent — and
// possibly stale-schema — snapshot.

// NormalizeRepoSpec turns a user-supplied --repo spec into a (repoPath, version) pair suitable for
// a repo-cache download. Spec formats:
//
//	"default"               → (DefaultProjectRepo, "")
//	"owner/repo"            → ("github.com/owner/repo", "")
//	"owner/repo@ref"        → ("github.com/owner/repo", "ref")
//	"host/owner/repo[@ref]" → used literally
//
// An empty version means "resolve to default branch at lookup time".
func NormalizeRepoSpec(spc string) (repoPath, version string) {
	spc = strings.TrimSpace(spc)
	if spc == "default" {
		return DefaultProjectRepo, ""
	}
	if before, after, ok := strings.Cut(spc, "@"); ok {
		repoPath, version = before, after
	} else {
		repoPath = spc
	}
	// Bare owner/repo (exactly one slash, no dots in the first segment) → auto-prefix github.com.
	// The dot-check distinguishes "github.com/foo" (already host-qualified) from "owner/repo".
	if slashes := strings.Count(repoPath, "/"); slashes == 1 {
		first, _, _ := strings.Cut(repoPath, "/")
		if !strings.Contains(first, ".") {
			repoPath = "github.com/" + repoPath
		}
	}
	return repoPath, version
}

// RepoIdentity returns the canonical repo identity of an import ref, or "" when it can't be
// determined (in which case the loader degrades to version-keyed behavior). A remote
// `@host/org/repo[/sub]:ver` ref yields `host/org/repo` directly (no fetch, no git); a local path
// yields the git `origin` identity of the target directory.
func RepoIdentity(ref, baseDir string) string {
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
	return GitRemoteIdentity(dir)
}

// RootRepoIdentity determines the local root project's own repo identity for cycle-break
// registration. An explicit `repo:` field in charly.yml is authoritative; otherwise it falls back
// to the `git remote origin` identity of the working tree. Returns "" when neither is available
// (the loader then behaves exactly as before — version-keyed, no self-identity short-circuit).
func RootRepoIdentity(dir string) string {
	if data, err := os.ReadFile(filepath.Join(dir, UnifiedFileName)); err == nil {
		var head struct {
			Repo string `yaml:"repo" json:"repo"`
		}
		if yaml.Unmarshal(data, &head) == nil && head.Repo != "" {
			return NormalizeRepoIdentity(head.Repo)
		}
	}
	return GitRemoteIdentity(dir)
}

// GitRemoteIdentity returns the normalized `host/owner/repo` identity of dir's git `origin`
// remote, or "" when dir is not a git repo / has no origin / git is unavailable.
func GitRemoteIdentity(dir string) string {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return NormalizeGitRemoteURL(strings.TrimSpace(string(out)))
}

// NormalizeRepoIdentity normalizes an explicit `repo:` value (which may be a full git URL, an
// scp-style ref, or a bare `owner/repo`) to the `host/owner/repo` form ParseRemoteRef produces —
// so an explicit declaration and a remote `@`-ref compare equal. Reuses NormalizeRepoSpec's
// bare-`owner/repo` → github.com rule.
func NormalizeRepoIdentity(s string) string {
	repoPath, _ := NormalizeRepoSpec(NormalizeGitRemoteURL(s))
	return strings.TrimSuffix(repoPath, ".git")
}

// NormalizeGitRemoteURL strips the scheme / `git@` / `.git` decorations from a git remote URL,
// leaving `host/owner/repo`. scp-style (`git@host:owner/repo`) and scheme URLs (`https://`,
// `ssh://`, `git://`, with optional `user@`) are both handled. A value already in `host/owner/repo`
// (or bare `owner/repo`) form passes through unchanged.
func NormalizeGitRemoteURL(raw string) string {
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
