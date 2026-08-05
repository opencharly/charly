package loader

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"
)

// refs_seams.go — the loader plugin builds its OWN remote-repo fetch legs (K-wave 2 cone R1).
//
// These four legs used to be spec.RefsCollectSeams: charly core resolved each one against the
// provider registry and threaded the struct down into the relocated mechanism. That put core in the
// middle of every fetch for no reason the boundary law recognises — core was not DEFINING a
// mechanism there, only CALLING three providers and reading one env var on the loader's behalf. The
// defines-vs-calls test says that is an R-item, so it moves here and RefsCollectSeams is deleted.
//
// Every leg now goes through the ONE dispatch any plugin uses to reach a peer — the host's
// InvokeProvider, retrieved from the in-proc executor the host threads onto ctx
// (sdk.ExecutorFromContext), exactly as ResolveMergedDeployTree already does. The host's own
// InvokeProvider lazily connects an unregistered target, so a peer that has not been reached yet in
// this process still resolves.

// refsSeams builds the fetch legs for one call. ctx MUST carry the host reverse channel; a caller
// without one is a contract bug, not a degraded mode, so it fails loudly rather than silently
// fetching nothing.
func refsSeams(ctx context.Context) (spec.RefsCollectSeams, error) {
	ex, ok := sdk.ExecutorFromContext(ctx)
	if !ok {
		return spec.RefsCollectSeams{}, fmt.Errorf("refs: no host reverse channel on context (loader not compiled-in?)")
	}
	return spec.RefsCollectSeams{
		Downloader:   peerDownloader{ctx: ctx, ex: ex},
		MigrateCache: func(path string) error { return migrateCacheViaPeer(ctx, ex, path) },
		ResolveLocal: func(body json.RawMessage) (*spec.ResolvedLocal, error) {
			return resolveLocalViaPeer(ctx, ex, body)
		},
		// The env var NAME lives in spec/proc (shared with candy/plugin-check's bed session, which
		// computes its own override independently); reading its VALUE is plain os.Getenv, never
		// something core had to do for us.
		OverrideEnvValue: os.Getenv(proc.RepoOverrideEnv),
	}, nil
}

// peerDownloader is the spec.RefsDownloader face of the registered refs BACKEND, reached over
// InvokeProvider instead of a typed in-proc handle. The backend itself is unchanged and unaware:
// candy/plugin-refs serves the same Download behind both its typed method and its OpResolve leg.
// Swapping the refs plugin still swaps the backend — that is the whole point of the seam, and it
// survives this move intact.
type peerDownloader struct {
	ctx context.Context
	ex  *sdk.Executor
}

func (d peerDownloader) Download(repoPath, version string) (string, error) {
	params, err := json.Marshal(spec.RefsDownloadInput{RepoPath: repoPath, Version: version})
	if err != nil {
		return "", fmt.Errorf("refs download: marshal input: %w", err)
	}
	resJSON, err := d.ex.InvokeProvider(d.ctx, "refs", "refs", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return "", err
	}
	var reply spec.RefsDownloadReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return "", fmt.Errorf("refs download: decode reply: %w", uerr)
		}
	}
	if reply.Dir == "" {
		return "", fmt.Errorf("refs download: backend returned no cache dir for %s@%s", repoPath, version)
	}
	return reply.Dir, nil
}

// migrateCacheViaPeer brings a freshly-fetched remote-repo cache's PROJECT files up to the head
// schema, via the compiled-in command:migrate plugin. `--project-only` never touches the per-host
// overlay (a remote fetch must not mutate the user's deploy state); `--quiet` discards progress
// output; `--dir` targets the cache tree. Byte-identical argv to the deleted core
// autoMigrateCacheProjectOnly.
func migrateCacheViaPeer(ctx context.Context, ex *sdk.Executor, path string) error {
	params, err := json.Marshal(map[string]any{"args": []string{"--project-only", "--quiet", "--dir", path}})
	if err != nil {
		return err
	}
	_, err = ex.InvokeProvider(ctx, "command", "migrate", sdk.OpRun, params, nil, sdk.InvokeProviderOpts{})
	return err
}

// resolveLocalViaPeer projects one opaque `kind:local` template body into a *spec.ResolvedLocal via
// candy/plugin-substrate's OpResolve leg — the same request envelope charly's own
// substrate_template_resolve.go builds.
func resolveLocalViaPeer(ctx context.Context, ex *sdk.Executor, body json.RawMessage) (*spec.ResolvedLocal, error) {
	params, err := json.Marshal(spec.SubstrateTemplateResolveRequest{Local: &spec.LocalResolveInput{Local: body}})
	if err != nil {
		return nil, fmt.Errorf("local resolve: marshal input: %w", err)
	}
	resJSON, err := ex.InvokeProvider(ctx, "kind", "local", sdk.OpResolve, params, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, err
	}
	var reply spec.LocalResolveReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return nil, fmt.Errorf("local resolve: decode reply: %w", uerr)
		}
	}
	return reply.Resolved, nil
}
