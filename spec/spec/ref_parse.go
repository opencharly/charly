package spec

import "strings"

// ref_parse.go — the generic remote-ref VOCAB: a `@host/org/repo/sub/path:version` ref's shape,
// and the pure string parsing that recognizes/decomposes it. Lives in spec (not a mechanism kit)
// because its consumer set is wide and NOT loader-cone-specific — deploy/provider/command files
// that merely need to recognize or decompose a ref string, with zero registry/host coupling. The
// LOADER-MECHANISM consumers of this vocab (repo-identity cycle-break, --repo spec normalization)
// live in sdk/loaderkit and import this package, never the reverse.

// DefaultProjectRepo is the repo --repo defaults to when the spec is the literal string "default"
// (or when charly mcp serve auto-falls back).
const DefaultProjectRepo = "github.com/opencharly/charly"

// ParsedRef is a parsed remote reference: `@host/org/repo/sub/path:version`. Works for both candy
// refs and image refs.
type ParsedRef struct {
	Raw      string // original string, e.g. "@github.com/org/repo/candy/name:v1.0.0"
	RepoPath string // e.g. "github.com/org/repo"
	SubPath  string // e.g. "candy/name" (path within repo)
	Name     string // e.g. "name" (last segment)
	Version  string // e.g. "v1.0.0"
}

// IsRemoteImageRef returns true if a ref looks like a remote image reference (starts with @).
func IsRemoteImageRef(ref string) bool {
	return strings.HasPrefix(ref, "@")
}

// ParseRemoteRef parses a remote reference into repo path, sub-path, name, and version.
// e.g. "@github.com/org/repo/candy/name:v1.0.0" -> ParsedRef{RepoPath: "github.com/org/repo", SubPath: "candy/name", Name: "name", Version: "v1.0.0"}
func ParseRemoteRef(ref string) *ParsedRef {
	raw := ref

	// Strip @ prefix
	ref = strings.TrimPrefix(ref, "@")

	// Split version
	version := ""
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		version = ref[idx+1:]
		ref = ref[:idx]
	}

	// Split into repo path (first 3 segments) and sub-path (rest)
	repoPath, subPath, name := SplitRepoAndSubPath(ref)

	return &ParsedRef{
		Raw:      raw,
		RepoPath: repoPath,
		SubPath:  subPath,
		Name:     name,
		Version:  version,
	}
}

// SplitRepoAndSubPath splits a ref into repo path (host/org/repo), sub-path, and name.
// e.g. "github.com/org/repo/candy/name" -> ("github.com/org/repo", "candy/name", "name")
// For short refs like "pixi", returns ("", "", "pixi").
func SplitRepoAndSubPath(ref string) (repoPath, subPath, name string) {
	parts := strings.SplitN(ref, "/", 4) // [host, org, repo, sub/path]
	if len(parts) < 4 {
		// Not enough segments for a remote ref — treat as local name
		name = parts[len(parts)-1]
		if len(parts) <= 1 {
			return "", "", name
		}
		return strings.Join(parts, "/"), "", name
	}
	repoPath = strings.Join(parts[:3], "/")
	subPath = parts[3]
	if idx := strings.LastIndex(subPath, "/"); idx != -1 {
		name = subPath[idx+1:]
	} else {
		name = subPath
	}
	return repoPath, subPath, name
}
