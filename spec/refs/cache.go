package refs

import (
	"fmt"
	"os"
	"path/filepath"
)

// cache.go — the remote-repo cache LOCATION helpers. Pure path computation over
// $CHARLY_REPO_CACHE / ~/.cache/charly/repos, shared by core (reconcile / the collection walk)
// and the refs fetch backend (candy/plugin-refs) that clones into these paths. RELOCATED from
// sdk/kit alongside the git primitives (#55 fabric-primitive extraction).

// RepoCacheDir returns the cache directory for remote repos.
// Uses $CHARLY_REPO_CACHE env var if set, otherwise ~/.cache/charly/repos/.
func RepoCacheDir() (string, error) {
	if envDir := os.Getenv("CHARLY_REPO_CACHE"); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	return filepath.Join(home, ".cache", "charly", "repos"), nil
}

// RepoCachePath returns the cache path for a specific repo version.
// e.g. ~/.cache/charly/repos/github.com/org/repo@v1.0.0/
func RepoCachePath(repoPath, version string) (string, error) {
	cacheDir, err := RepoCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, repoPath+"@"+version), nil
}

// IsRepoCached reports whether a repo version is already in the cache AS A
// USABLE EXPORT — the directory exists AND every submodule it declares has
// content.
//
// The completeness half is load-bearing for IMMUTABLE refs. EnsureRepoDownloaded
// short-circuits on `cached && !IsMutableRef(version)`, returning RepoCachePath
// directly and never entering DownloadRepo — so the repoCacheFresh completeness
// check cannot see a tag's cache at all. Directory-exists alone therefore pinned
// every incomplete TAG export permanently: a tag never moves, so nothing would
// ever re-fetch it. On the machine this was found, `charly@v2026.183.1359` held
// 0 of its 9 declared submodules and `charly@v2026.201.0706` held 1 of 10, both
// unrepairable for the life of the cache. Checking content here routes an
// incomplete export down the download branch instead, so tag and branch caches
// self-heal by the SAME predicate rather than one growing its own copy.
func IsRepoCached(repoPath, version string) (bool, error) {
	cachePath, err := RepoCachePath(repoPath, version)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(cachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return submodulesPopulated(cachePath), nil
}
