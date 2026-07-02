package main

import (
	"fmt"
	"os"
	"strings"
)

// RemoteImageContext holds the resolved state of a remote image reference.
// It contains everything needed to pull/build and run the image.
type RemoteImageContext struct {
	Ref      ParsedRef
	CacheDir string
	Config   *Config
	Resolved *ResolvedBox
	Candies  map[string]*Candy
	ImageRef string // registry/name:tag for pull
	BoxName  string // short name (e.g. "openclaw-browser")
}

// ResolveRemoteImage resolves a remote image reference to a full context.
// Format: @github.com/org/repo/image:version
func ResolveRemoteImage(ref string, tag string) (*RemoteImageContext, error) {
	parsed := ParseRemoteRef(ref)
	if parsed.RepoPath == "" || parsed.Name == "" {
		return nil, fmt.Errorf("invalid remote image ref %q: expected @github.com/org/repo/image:version", ref)
	}

	version := parsed.Version
	if version == "" {
		repoURL := RepoGitURL(parsed.RepoPath)
		tag, err := GitLatestTag(repoURL)
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
func (ctx *RemoteImageContext) BuildImage(_ *ResolvedRuntime, tag string) error {
	// The generate+build both run inside buildCmd.Run() now that box build dispatches
	// through candy/plugin-build → HostBuild("image") → runBoxBuild (NewGenerator + Generate +
	// buildImages), from ctx.CacheDir after the chdir below. A standalone NewGenerator+Generate
	// preflight here would be redundant work whose .build/ output runBoxBuild immediately regenerates.
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

// ContainerName returns the container name for a remote image.
func (ctx *RemoteImageContext) ContainerName() string {
	return containerName(ctx.BoxName)
}

// CollectVolumes collects volumes for the remote image.
func (ctx *RemoteImageContext) CollectVolumes() ([]VolumeMount, error) {
	return CollectBoxVolume(
		ctx.Config, ctx.Candies, ctx.BoxName,
		ctx.Resolved.Home,
		nil,
	)
}

// RemoteContainerName returns the container name for a remote ref.
func RemoteContainerName(ref string) string {
	parsed := ParseRemoteRef(ref)
	return containerName(parsed.Name)
}

// StripURLScheme removes http:// or https:// from a remote ref if present.
func StripURLScheme(ref string) string {
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimPrefix(ref, "http://")
	return ref
}
