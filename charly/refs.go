package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/opencharly/spec/spec"
)

// ParsedRef, IsRemoteImageRef, ParseRemoteRef, SplitRepoAndSubPath — pure remote-ref string
// parsing, relocated to sdk/spec (K1/W9) as generic vocab: their consumer set spans deploy/
// provider/command files with zero loader-cone character, so a MECHANISM kit (loaderkit) would be
// the wrong home — spec is the shared, always-legal-to-import vocabulary layer. charly/*.go call
// sites now reference spec.ParsedRef / spec.ParseRemoteRef / spec.IsRemoteImageRef directly
// (ZERO-ALIASES — no alias reintroduced here).
//
// EnsureRepoDownloaded / CollectRemoteRefs / CollectRemoteRefsOpts are now
// sdk/loaderkit.EnsureRepoDownloaded / CollectRemoteRefs / CollectRemoteRefsOpts (K1 unit 4) — this
// file keeps same-named/same-signature core wrapper functions (R3) that build the
// spec.RefsCollectSeams host-coupled legs (the resolved RefsDownloader, the registry-touching
// migrate-command dispatch, the registry-touching local-template substrate resolve, the raw
// CHARLY_REPO_OVERRIDE env value) and call through requireProjectLoader() — the many production
// call sites across the repo keep working unchanged. RepoOverrideEnv (the env var NAME) /
// selfSuperprojectOverridePair / mergeRepoOverrides stay core: they are shared with
// host_build_check_bed.go's + check_cmd.go's check-bed local-override wiring, a DIFFERENT domain
// from the loader/refs mechanism (RDD local-override plumbing, not candy-ref collection), so they
// are not K1 residue.

// RepoOverrideEnv configures RDD local-overrides: it points a remote `@github`
// repo ref at a LOCAL working tree (Go-`replace`-style), so an UNCOMMITTED
// candy / charly.yml change can be built and `charly check`'d by ANY
// consumer — across submodule boundaries — BEFORE it is committed and pushed.
// This is the supported "verify before you push to main" mechanism (no cache
// hacks, no producer-first tag churn).
//
// Value: a comma-separated list of `repoPath=localDir` pairs. repoPath matches
// the repo-root form every `@github` candy/namespace/image ref resolves through
// (`github.com/<org>/<repo>`); a bare `<org>/<repo>` is accepted too (auto
// `github.com/` prefix, same rule as `--repo`). Example:
//
//	CHARLY_REPO_OVERRIDE=opencharly/charly=/home/me/oc-charly \
//	    charly -C box/ubuntu box build ubuntu-coder
//
// The matched directory resolves verbatim (leading `~/` expanded); the ref's
// `:vTAG` is IGNORED — an override ALWAYS resolves to the dev's current tree.
const RepoOverrideEnv = "CHARLY_REPO_OVERRIDE"

// selfSuperprojectOverridePair returns a CHARLY_REPO_OVERRIDE pair
// (`<repo-identity>=<superproject-dir>`) that points a bed project's OWN
// superproject `@github` refs at the local working tree, or "" when projectDir
// is not a git submodule of a charly superproject. A check bed (a `disposable: true` bundle) living in
// a `box/<distro>` submodule references its parent repo's shared candies via
// `@github.com/<org>/<parent>/candy/<name>:<tag>`; without this override the bed
// would build the PINNED REMOTE candy and so test STALE code — the candy-ref
// analogue of why the bed runner builds the toolchain with `--dev-local-pkg`. The
// override IGNORES the ref's `:vTAG`, so the bed always tests the dev's current
// tree. Returns "" when projectDir is its own root (its candies already resolve
// from the local tree) or when git / the superproject identity is unavailable.
func selfSuperprojectOverridePair(projectDir string) string {
	out, err := exec.Command("git", "-C", projectDir, "rev-parse", "--show-superproject-working-tree").Output()
	if err != nil {
		return ""
	}
	superDir := strings.TrimSpace(string(out))
	if superDir == "" {
		return "" // not a submodule — its candies already resolve from the local tree
	}
	identity := spec.RootRepoIdentity(superDir)
	if identity == "" {
		return ""
	}
	return identity + "=" + superDir
}

// mergeRepoOverrides combines an existing CHARLY_REPO_OVERRIDE value with an
// auto-added pair. The existing (operator-set) entries are placed FIRST so an
// explicit operator override for a repo WINS over the auto pair — repoOverrideDir
// returns the FIRST matching entry. Either argument may be empty.
func mergeRepoOverrides(existing, add string) string {
	existing = strings.TrimSpace(existing)
	add = strings.TrimSpace(add)
	switch {
	case existing == "":
		return add
	case add == "":
		return existing
	default:
		return existing + "," + add
	}
}

// refsCollectSeams builds the spec.RefsCollectSeams every EnsureRepoDownloaded/CollectRemoteRefsOpts
// seam call passes through — the registry-/host-coupled legs the relocated mechanism cannot do
// itself: the resolved RefsDownloader (P7), the registry-touching migrate-command dispatch
// (autoMigrateCacheProjectOnly), the registry-touching local-template substrate resolve
// (resolveLocalViaPlugin), and the raw CHARLY_REPO_OVERRIDE env value.
func refsCollectSeams() spec.RefsCollectSeams {
	return spec.RefsCollectSeams{
		Downloader:       requireRefsDownloader(),
		MigrateCache:     autoMigrateCacheProjectOnly,
		ResolveLocal:     resolveLocalViaPlugin,
		OverrideEnvValue: os.Getenv(RepoOverrideEnv),
	}
}

// EnsureRepoDownloaded downloads the repo if not already cached. Returns the cache path.
func EnsureRepoDownloaded(repoPath, version string) (string, error) {
	return requireProjectLoader().EnsureRepoDownloaded(repoPath, version, refsCollectSeams())
}

// autoMigrateCacheProjectOnly brings a remote-repo cache's PROJECT files up to the head schema
// via an in-proc Invoke of the compiled-in command:migrate plugin — the migration ENGINE lives
// in candy/plugin-migrate now (M15), not core. The `--project-only` flag never touches the
// per-host overlay (a remote fetch must not mutate the user's deploy state); `--quiet` discards
// the progress output; `--dir <cache>` targets the cache tree. command:migrate is compiled-in,
// so it resolves at init() — available here, deep in config loading, before LoadUnified completes.
// Registry-coupled (providerRegistry.resolve + Invoke), so it stays core and threads into the
// relocated EnsureRepoDownloaded mechanism as seams.MigrateCache.
func autoMigrateCacheProjectOnly(path string) error {
	prov, ok := providerRegistry.resolve(ClassCommand, "migrate")
	if !ok {
		return fmt.Errorf("migrate plugin (command:migrate) not registered — charly built without candy/plugin-migrate")
	}
	params, err := marshalJSON(map[string]any{"args": []string{"--project-only", "--quiet", "--dir", path}})
	if err != nil {
		return err
	}
	_, err = prov.Invoke(context.Background(), &Operation{Reserved: "migrate", Op: OpRun, Params: params})
	return err
}

// CollectRemoteRefs is the default-opts wrapper (enabled images only) around
// CollectRemoteRefsOpts. The overwhelming majority of call sites want
// enabled-only collection, so they keep this two-arg form.
func CollectRemoteRefs(cfg *spec.Config, layers map[string]spec.CandyReader) ([]spec.RemoteDownload, error) {
	return CollectRemoteRefsOpts(cfg, layers, spec.ResolveOpts{})
}

// CollectRemoteRefsOpts collects all unique remote refs from charly.yml candy
// lists and candy manifest depends/candy fields.
func CollectRemoteRefsOpts(cfg *spec.Config, layers map[string]spec.CandyReader, opts spec.ResolveOpts) ([]spec.RemoteDownload, error) {
	return requireProjectLoader().CollectRemoteRefsOpts(cfg, layers, opts, refsCollectSeams())
}
