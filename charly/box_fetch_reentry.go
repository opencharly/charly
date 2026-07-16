package main

import (
	"fmt"
	"os"
)

// box_fetch_reentry.go — the hidden `charly __box-fetch` / `__box-refresh` core reentry points
// behind the COMPILED-IN candy/plugin-authoring command:fetch / command:refresh verbs (P14b).
// The repo resolver (ResolveProjectRepo → EnsureRepoDownloaded) is HOST-COUPLED: it applies
// CHARLY_REPO_OVERRIDE, dispatches the cache-miss download through the registered refs backend
// (candy/plugin-refs), and auto-migrates the cache to the head schema via the command:migrate
// plugin registry — none of which a sdk-only plugin can reach. So the authoring plugin's
// dispatchFetch/dispatchRefresh re-run these hidden core commands over the generic HostBuild("cli")
// reverse channel (the SAME seam candy/plugin-box's `pkg` verb uses for `__box-pkg`), and the
// host-coupled resolution stays in core. main() registers each as a hidden Kong command; the
// reentry subprocess inherits charly's stdio (prints the cache path to stdout / its error to
// stderr) and its exit code rides the CliReply.
//
// K5-doomed: both reentries (and this file) die when ResolveProjectRepo/EnsureRepoDownloaded move
// into the plugin over sdk kits (a `HostBuild("refs-resolve")` seam) — the SAME tracked-residue
// pattern as the sibling __box-pkg / __box-inspect-overlay / __box-list-tags reentries (K5
// seam-death sweep). The fetch/refresh handlers were already core pre-P14b (they do not ADD core
// LOC; they stay as the K5-exited hidden reentry, not a permanent facade).

// BoxFetchCmd is the hidden `charly __box-fetch [<spec>]` reentry: pre-prime the remote-repo cache
// (default spec: 'default' → opencharly/charly) and print the cache path.
type BoxFetchCmd struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → opencharly/charly)"`
}

func (c *BoxFetchCmd) Run() error {
	spec := c.Spec
	if spec == "" {
		spec = "default"
	}
	path, err := ResolveProjectRepo(spec)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// BoxRefreshCmd is the hidden `charly __box-refresh [<spec>]` reentry: force re-clone of a remote
// project repo (remove its cache entry, then re-resolve) and print the cache path.
type BoxRefreshCmd struct {
	Spec string `arg:"" optional:"" help:"Repo spec (default: 'default' → opencharly/charly)"`
}

func (c *BoxRefreshCmd) Run() error {
	spec := c.Spec
	if spec == "" {
		spec = "default"
	}
	repoPath, version := normalizeRepoSpec(spec)
	if version == "" {
		branch, err := GitDefaultBranch(RepoGitURL(repoPath))
		if err != nil {
			return fmt.Errorf("resolving default branch for %s: %w", repoPath, err)
		}
		version = branch
	}
	cachePath, err := RepoCachePath(repoPath, version)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(cachePath); err != nil {
		return fmt.Errorf("removing cache %s: %w", cachePath, err)
	}
	path, err := ResolveProjectRepo(spec)
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
