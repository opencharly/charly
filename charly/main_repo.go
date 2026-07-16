package main

import (
	"fmt"
	"strings"

	"github.com/opencharly/sdk/kit"
)

// DefaultProjectRepo is the repo --repo defaults to when the spec is the
// literal string "default" (or when charly mcp serve auto-falls back).
const DefaultProjectRepo = "github.com/opencharly/charly"

// normalizeRepoSpec turns a user-supplied --repo spec into a (repoPath, version)
// pair suitable for EnsureRepoDownloaded. Spec formats:
//
//	"default"               → (DefaultProjectRepo, "")
//	"owner/repo"            → ("github.com/owner/repo", "")
//	"owner/repo@ref"        → ("github.com/owner/repo", "ref")
//	"host/owner/repo[@ref]" → used literally
//
// An empty version means "resolve to default branch at lookup time".
func normalizeRepoSpec(spec string) (repoPath, version string) {
	spec = strings.TrimSpace(spec)
	if spec == "default" {
		return DefaultProjectRepo, ""
	}
	if before, after, ok := strings.Cut(spec, "@"); ok {
		repoPath, version = before, after
	} else {
		repoPath = spec
	}
	// Bare owner/repo (exactly one slash, no dots in the first segment) →
	// auto-prefix github.com. The dot-check distinguishes "github.com/foo"
	// (already host-qualified) from "owner/repo".
	if slashes := strings.Count(repoPath, "/"); slashes == 1 {
		first, _, _ := strings.Cut(repoPath, "/")
		if !strings.Contains(first, ".") {
			repoPath = "github.com/" + repoPath
		}
	}
	return repoPath, version
}

// ResolveProjectRepo turns a --repo spec into a local cache path that can
// be passed to os.Chdir. Reuses the existing remote-candy cache machinery
// (RepoCacheDir, EnsureRepoDownloaded) so we don't have a second copy of
// "clone-and-cache".
func ResolveProjectRepo(spec string) (string, error) {
	if spec == "" {
		return "", fmt.Errorf("empty --repo spec")
	}
	repoPath, version := normalizeRepoSpec(spec)
	if repoPath == "" {
		return "", fmt.Errorf("invalid --repo spec %q", spec)
	}
	if version == "" {
		branch, err := kit.GitDefaultBranch(kit.RepoGitURL(repoPath))
		if err != nil {
			return "", fmt.Errorf("resolving default branch for %s: %w", repoPath, err)
		}
		version = branch
	}
	return EnsureRepoDownloaded(repoPath, version)
}
