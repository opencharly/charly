package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// remote_image.go — resolves an `@github.com/org/repo/box[:version]` REMOTE ref (NOT an
// OCI-registry concern despite the filename; the actual go-containerregistry engine lives in
// candy/plugin-oci) into a full build/run context: clone/cache the repo, load its charly.yml,
// resolve the box, scan its candies, and (BuildImage) build it.
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K1/K3 — the repo-fetch/cache
// machinery (EnsureRepoDownloaded, LoadConfig, ScanAllCandyWithConfig) is loader-cone (K1),
// and BuildImage's delegation to BuildCmd is build-cone (K3). Consumers span both cones —
// build.go, commands.go, ensure_image.go, image.go, shell.go, start.go, config_image.go
// (P14-rest trace, 2026-07) — so this moves together with the loader/build waves, not alone.

// RemoteImageContext holds the resolved state of a remote image reference.
// It contains everything needed to pull/build and run the image.
type RemoteImageContext struct {
	Ref      spec.ParsedRef
	CacheDir string
	Config   *Config
	Resolved *buildkit.ResolvedBox
	Candies  map[string]*Candy
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
	resolved, err := cfg.ResolveBox(parsed.Name, calverTag, cachePath, ResolveOpts{})
	if err != nil {
		return nil, fmt.Errorf("resolving image %q in %s: %w", parsed.Name, parsed.RepoPath, err)
	}

	// Scan candies from the cached repo
	layers, err := ScanAllCandyWithConfig(cachePath, cfg)
	if err != nil {
		return nil, fmt.Errorf("scanning candies in %s: %w", parsed.RepoPath, err)
	}

	// Build the registry image ref for pulling
	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, tag)

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

// BuildImage builds the image locally from the cached source.
func (ctx *RemoteImageContext) BuildImage(_ *kit.ResolvedRuntime, tag string) error {
	// The generate+build both run inside buildCmd.Run() now that box build dispatches through the
	// compiled-in candy/plugin-build DRIVE (build:box) — which resolves + renders the .build/ tree
	// host-side over the build-prep seam, then drives podman — from ctx.CacheDir after the chdir
	// below. A standalone NewGenerator+Generate preflight here would be redundant work whose .build/
	// output the candy build drive immediately regenerates.
	buildCmd := &BuildCmd{
		Boxes: []string{ctx.BoxName},
		Tag:   tag,
	}
	origDir, _ := os.Getwd()
	if err := os.Chdir(ctx.CacheDir); err != nil {
		return fmt.Errorf("changing to cache dir: %w", err)
	}
	defer os.Chdir(origDir) //nolint:errcheck

	return buildCmd.Run()
}

// StripURLScheme removes http:// or https:// from a remote ref if present.
func StripURLScheme(ref string) string {
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimPrefix(ref, "http://")
	return ref
}
