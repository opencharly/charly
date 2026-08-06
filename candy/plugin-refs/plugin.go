// Package refs is the refs plugin candy — the swappable remote-repo FETCH BACKEND (P7). It serves
// the cache-miss DOWNLOAD via kit.RefsDownloader: the host resolves the registered refs provider to
// a kit.RefsDownloader and dispatches every remote-repo fetch through it, so an alternative refs
// plugin can serve a different backend (OCI/S3-hosted candies) by registering a different
// RefsDownloader. The DEFAULT (this candy) fetches via git through the shared sdk/kit primitives —
// the ONE copy of the git fetch (R3), the way candy/plugin-loader is the config front-end.
//
// The host keeps the fetch ORCHESTRATION (local-override resolution, cache-hit short-circuit, the
// post-fetch schema auto-migration via command:migrate); this plugin owns only the pluggable
// backend that turns a (repoPath, version) into a populated local cache tree. A compiled-in plugin
// candy is a SEPARATE Go module importing only sdk (never charly core), and the fetch runs deep in
// config loading — the compiled-in command:migrate already invokes at that point with no bootstrap
// cycle, so registering this at init() before the first load is likewise cycle-free.
//
// PLACEMENT — COMPILED-IN (in the embedded compiled_plugins:): the refs backend must ALWAYS resolve;
// every remote-candy fetch reaches it. Registered at init() before the first load; the host calls
// its typed Download (no wire envelope) compiled-in.
package refs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

const calver = "2026.192.0000"

// NewProvider returns the refs provider — a pb.ProviderServer that ALSO implements
// kit.RefsDownloader (the typed remote-repo download the host calls compiled-in).
func NewProvider() pb.ProviderServer { return &provider{} }

// NewMeta advertises the refs capability (Class "refs", word "refs").
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta(calver, []sdk.ProvidedCapability{
		{Class: "refs", Word: "refs"},
	}, nil)
}

type provider struct {
	pb.UnimplementedProviderServer
	kit.DefaultDownloader
}

// The embedded kit.DefaultDownloader supplies Download — the default git fetch backend
// (delegating to kit.DownloadRepo). An alternative refs plugin overrides Download for a
// different backend (OCI/S3).

// Invoke serves the WIRE face of that same backend: OpResolve turns a (repoPath, version) into a
// populated local cache dir. It exists because the fetch ORCHESTRATION moved out of charly core into
// candy/plugin-loader (K-wave 2 cone R1). While the host sat in the middle of every fetch it could
// resolve this provider to a typed kit.RefsDownloader and call Download in-proc; a peer PLUGIN
// cannot hold a typed handle to another plugin, so it reaches the backend the way every plugin
// reaches every peer — InvokeProvider(class:"refs", word:"refs", OpResolve).
//
// Both faces run the SAME Download (R3: one backend, two callers), so a refs plugin that overrides
// Download for an OCI/S3 backend serves both without knowing this method exists. The caller has
// already resolved local overrides and checked the cache — a backend only ever sees a genuine miss.
func (p *provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetOp() != sdk.OpResolve {
		return nil, fmt.Errorf("refs: unsupported op %q (only %q)", req.GetOp(), sdk.OpResolve)
	}
	var in spec.RefsDownloadInput
	if err := json.Unmarshal(req.GetParamsJson(), &in); err != nil {
		return nil, fmt.Errorf("refs: decode input: %w", err)
	}
	if in.RepoPath == "" || in.Version == "" {
		return nil, fmt.Errorf("refs: download needs both repo_path and version (got %q@%q)", in.RepoPath, in.Version)
	}
	dir, err := p.Download(in.RepoPath, in.Version)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(spec.RefsDownloadReply{Dir: dir})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: out}, nil
}
