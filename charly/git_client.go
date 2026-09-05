package main

// git_client.go — the charly-core process-wide centralized git layer
// (spec/refs.GitClient). The sdk's loaderkit holds its own private singleton
// (loaderkit.gitClient); charly core cannot import sdk/loaderkit, so the host
// seams that resolve @github refs (host_build_remote_image_resolve,
// host_build_box_fetch_resolve) share THIS singleton instead of calling the raw
// git primitives (refs.GitLatestTag / refs.GitDefaultBranch) directly.
//
// The cache lives in the `cache:` section of the per-host charly.yml
// (~/.config/charly/charly.yml) — the single home for local system state
// (deployments, ledger, system, cache). NewGitClient("") resolves the default
// deploy config path (honoring the CHARLY_DEPLOY_CONFIG override, so a check
// bed's per-bed isolation applies uniformly).

import (
	"sync"

	"github.com/opencharly/spec/refs"
)

var gitClientOnce sync.Once
var gitClientInstance *refs.GitClient

// gitClient returns the process-wide centralized git layer. Constructed once and
// shared by every host seam that resolves a remote ref.
func gitClient() *refs.GitClient {
	gitClientOnce.Do(func() {
		// The singleton honors the PERSISTED bypass (refs.GitClient.SetBypass — the
		// `charly cache bypass` operator surface, owned by the plugin-cache command
		// candy): NewGitClient reads the cache: git: bypass flag at construction.
		gitClientInstance = refs.NewGitClient("")
	})
	return gitClientInstance
}
