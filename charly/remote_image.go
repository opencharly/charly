package main

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// remote_image.go — resolves an `@github.com/org/repo/box[:version]` REMOTE ref (NOT an
// OCI-registry concern despite the filename; the actual go-containerregistry engine lives in
// candy/plugin-oci) into a full build/run context: clone/cache the repo, load its charly.yml,
// resolve the box, and scan its candies.
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K1 — the repo-fetch/cache
// machinery (EnsureRepoDownloaded, LoadConfig, ScanAllCandyWithConfig) is loader-cone (K1)
// and CANNOT leave core (EnsureRepoDownloaded is the host git clone/cache, K1/B; ResolveRemoteImage
// is ALSO the backing of the "remote-image-resolve" HostBuild seam plugin-build's ensure fallback
// reaches). The build-DRIVE half is GONE (K3 #39): buildRemote (build.go) now runs the cached
// source through the full BuildCmd.Run() pipeline (build:box in candy/plugin-build) directly — the
// former RemoteImageContext.BuildImage indirection is deleted. Consumers — build.go, commands.go,
// host_build_box_ref_resolve.go, image.go, config_image.go (P14-rest trace, 2026-07) — reach the
// resolve half only, so this moves together with the loader wave, not alone.

// RemoteImageContext holds the resolved state of a remote image reference.
// It contains everything needed to pull/build and run the image.
type RemoteImageContext struct {
	Ref      spec.ParsedRef
	CacheDir string
	Config   *Config
	Resolved *buildkit.ResolvedBox
	Candies  map[string]spec.CandyReader
	ImageRef string // registry/name:tag for pull
	BoxName  string // short name (e.g. "openclaw-browser")
}

// ResolveRemoteImage resolves a remote image reference to a full context.
// Format: @github.com/org/repo/image:version
func ResolveRemoteImage(ref string, tag string) (*RemoteImageContext, error) {
	parsed := spec.ParseRemoteRef(ref)
	if parsed.RepoPath == "" || parsed.Name == "" {
		return nil, fmt.Errorf("invalid remote image ref %q: expected @github.com/org/repo/image:version", ref)
	}

	version := parsed.Version
	if version == "" {
		repoURL := kit.RepoGitURL(parsed.RepoPath)
		tag, err := kit.GitLatestTag(repoURL)
		if err != nil {
			return nil, fmt.Errorf("resolving latest version for %s: %w", parsed.RepoPath, err)
		}
		version = tag
		fmt.Fprintf(os.Stderr, "Resolved @%s -> %s\n", parsed.RepoPath, version)
	}

	// Download/cache the repo
	cachePath, err := EnsureRepoDownloaded(parsed.RepoPath, version)
	if err != nil {
		return nil, fmt.Errorf("downloading %s:%s: %w", parsed.RepoPath, version, err)
	}

	// Load the remote charly.yml
	cfg, err := LoadConfig(cachePath)
	if err != nil {
		return nil, fmt.Errorf("loading config from %s: %w", parsed.RepoPath, err)
	}

	// Resolve the image
	calverTag := ComputeCalVer()
	bkopts, err := buildkitOptsWithVocab(cachePath, ResolveOpts{})
	if err != nil {
		return nil, fmt.Errorf("resolving image %q in %s: %w", parsed.Name, parsed.RepoPath, err)
	}
	resolved, err := buildkit.ResolveBox(cfg, parsed.Name, calverTag, cachePath, bkopts)
	if err != nil {
		return nil, fmt.Errorf("resolving image %q in %s: %w", parsed.Name, parsed.RepoPath, err)
	}

	// Scan candies from the cached repo
	layers, err := ScanAllCandyWithConfig(cachePath, cfg)
	if err != nil {
		return nil, fmt.Errorf("scanning candies in %s: %w", parsed.RepoPath, err)
	}

	// Build the registry image ref for pulling
	imageRef := kit.ResolveShellImageRef(resolved.Registry, resolved.Name, tag)

	return &RemoteImageContext{
		Ref:      *parsed,
		CacheDir: cachePath,
		Config:   cfg,
		Resolved: resolved,
		Candies:  layers,
		ImageRef: imageRef,
		BoxName:  parsed.Name,
	}, nil
}
