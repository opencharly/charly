package main

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/refs"
	"github.com/opencharly/spec/spec"
)

// remote_image.go — resolves an `@github.com/org/repo/box[:version]` REMOTE ref (NOT an
// OCI-registry concern despite the filename; the actual go-containerregistry engine lives in
// candy/plugin-oci) into a full build/run context: clone/cache the repo, load its charly.yml,
// resolve the box, and scan its candies.
//
// MIGRATION INVENTORY (north-star §4.4): this file is UNTIL-K1 — the repo-fetch/cache
// machinery (EnsureRepoDownloaded, LoadConfig, ScanAllCandyWithConfig) is loader-cone (K1)
// and CANNOT leave core (EnsureRepoDownloaded is the host git clone/cache, K1/B; ResolveRemoteImage
// is ALSO the backing of the "remote-image-resolve" HostBuild seam, reached BOTH by
// candy/plugin-build's ensure fallback AND by candy/plugin-box's dispatchBuild for
// `charly box build @ref`). The build-DRIVE half is GONE (K3 #39, P8b): the remote-ref pivot now
// runs in candy/plugin-box's dispatchBuild — it DETECTS the pivot purely (buildkit.DetectRemoteBuildRef,
// a shallow charly.yml peek, sdk-side), reaches ResolveRemoteImage over the thin
// HostBuild("remote-image-resolve") seam (host_build_remote_image_resolve.go), then re-dispatches
// build:box (candy/plugin-build) against the cached source dir; the former RemoteImageContext.BuildImage
// indirection is deleted. Consumers — commands.go, host_build_box_ref_resolve.go,
// host_build_remote_image_resolve.go, image.go — reach the resolve half only, so this
// moves together with the loader wave, not alone.

// RemoteImageContext holds the resolved state of a remote image reference.
// It contains everything needed to pull/build and run the image.
type RemoteImageContext struct {
	Ref      spec.ParsedRef
	CacheDir string
	Config   *Config
	Resolved *spec.ResolvedBox
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
		repoURL := refs.RepoGitURL(parsed.RepoPath)
		tag, err := refs.GitLatestTag(repoURL)
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
	vopts, err := resolveVocabOpts(cachePath, spec.ResolveOpts{})
	if err != nil {
		return nil, fmt.Errorf("resolving image %q in %s: %w", parsed.Name, parsed.RepoPath, err)
	}
	resolved, err := deploykit.ResolveSpecBox(cfg, parsed.Name, calverTag, cachePath, vopts)
	if err != nil {
		return nil, fmt.Errorf("resolving image %q in %s: %w", parsed.Name, parsed.RepoPath, err)
	}

	// Scan candies from the cached repo
	layers, err := ScanAllCandyWithConfig(cachePath, cfg)
	if err != nil {
		return nil, fmt.Errorf("scanning candies in %s: %w", parsed.RepoPath, err)
	}

	// Build the registry image ref for pulling
	imageRef := container.ResolveShellImageRef(resolved.Registry, resolved.Name, tag)

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
