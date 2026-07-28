package main

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
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
// coneB-ROUTED RESIDUE: this file is the box-fetch/refresh COMMAND reentry, and it relocates to
// coneB's command:fetch (candy/plugin-box) — the SAME box-command relocation as pkg_cmd→build:pkg.
// The ENGINE it drives, EnsureRepoDownloaded (via ResolveProjectRepo), STAYS FLOOR (the #118
// boundary-law ruling: the host git clone/cache ResolveRef-closure orchestration is heavily
// host-coupled — CHARLY_REPO_OVERRIDE + the refs backend + the command:migrate registry — and is
// reached by loaderkit via the injected ResolveRef seam; it never moves to loaderkit OR a plugin).
// So the plugin's dispatchFetch/dispatchRefresh keep driving the FLOOR engine over the
// HostBuild("cli") reverse channel — only the COMMAND moves. The fetch/refresh handlers were
// already core pre-P14b (they do not ADD core LOC; they stay as the coneB-exited hidden reentry).

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
	repoPath, version := loaderkit.NormalizeRepoSpec(spec)
	if version == "" {
		branch, err := kit.GitDefaultBranch(kit.RepoGitURL(repoPath))
		if err != nil {
			return fmt.Errorf("resolving default branch for %s: %w", repoPath, err)
		}
		version = branch
	}
	cachePath, err := kit.RepoCachePath(repoPath, version)
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
