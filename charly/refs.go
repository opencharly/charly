package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/proc"
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
// SelfSuperprojectOverridePair / MergeRepoOverrides relocated to spec/proc (#55 W3 B2-full) — pure
// git-shelling + string manipulation with zero registry coupling, needed by BOTH this file's
// plugin_loader.go caller (deployNodePluginContext) AND candy/plugin-check's bed session (which
// computes its own repo-override before self-loading the project); callers here reference
// proc.RepoOverrideEnv / proc.SelfSuperprojectOverridePair directly (ZERO-ALIASES).

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
		OverrideEnvValue: os.Getenv(proc.RepoOverrideEnv),
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
	_, err = prov.Invoke(context.Background(), &Operation{Reserved: "migrate", Op: ops.OpRun, Params: params})
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
