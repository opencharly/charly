package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
)

// resolved_project_host.go — the THIN host FILL for the RESOLVED-project envelope (K5-Unit-0, the
// S-K5 keystone). It is a DATA PROJECTION over the resolve engines that already exist — ResolveBox
// (per enabled box) + ScanAllCandy + the folded uf.Bundle deploy tree — serialized into the generic
// spec.ResolvedProject (spec.#ResolvedProject, the third member of the envelope spine after
// spec.ParsedProject → spec.LoadedProject). It is NOT a new engine: it copies fields the existing
// engines populate, dropping the 6 host-only json:"-" compute-cache pointers of ResolvedBox that are
// never wire data (DistroConfig/DistroDef/BuilderConfig/InitSystem/InitDef/CandyCaps).
//
// It is exposed BOTH ways: as the generic action-noun HostBuild kind "resolved-project" (F11 — a
// plugin requests it over the established reverse channel via Executor.HostBuild) AND callable
// host-side directly (projectResolvedBox / projectCandyView / buildResolvedProjectFromDir). The
// per-concern config-resolve / status-substrate / check-config seams collapse into this one envelope
// at their consumers' later K5 units; this unit adds only the envelope + its fill.

// resolvedProjectBuilderKind is the F11 hostBuilders key — a generic action noun, never a provider word.
const resolvedProjectBuilderKind = "resolved-project"

// projectResolvedBox projects a resolved box (ResolvedBox = buildkit.ResolvedBox) into the wire-safe
// spec.ResolvedBoxView: EXACTLY the non-json:"-" fields `charly box inspect` already serializes
// (json.MarshalIndent(*ResolvedBox)), in declaration order. The 6 json:"-" host-only compute caches
// are DROPPED — they are re-derivable by a resolving plugin (or reached via RunHostStep), never wire
// data (S-K5 verdict, the design key).
func projectResolvedBox(b *ResolvedBox) spec.ResolvedBoxView {
	return spec.ResolvedBoxView{
		Name:                  b.Name,
		Version:               b.Version,
		EffectiveVersion:      b.EffectiveVersion,
		Status:                b.Status,
		Info:                  b.Info,
		CheckLevel:            b.CheckLevel,
		Base:                  b.Base,
		From:                  b.From,
		BootstrapBuilderImage: b.BootstrapBuilderImage,
		Platforms:             b.Platforms,
		Tag:                   b.Tag,
		Registry:              b.Registry,
		Pkg:                   b.Pkg,
		Distro:                b.Distro,
		BuildFormats:          b.BuildFormats,
		Tags:                  b.Tags,
		Candy:                 b.Candy,
		User:                  b.User,
		UID:                   int64(b.UID),
		GID:                   int64(b.GID),
		Home:                  b.Home,
		UserAdopted:           b.UserAdopted,
		Merge:                 b.Merge,
		Builder:               map[string]string(b.Builder),
		BuilderCapabilities:   b.BuilderCapabilities,
		Auto:                  b.Auto,
		Network:               b.Network,
		DataImage:             b.DataImage,
		IsExternalBase:        b.IsExternalBase,
		FullTag:               b.FullTag,
	}
}

// projectCandyView projects a scanned candy (the runtime *Candy) into the wire-safe spec.CandyView:
// identity + dep-graph (bare-ref form) + provides + ports/services. The Has* filesystem probes, the
// unexported package/service SECTIONS, and the *CandyPluginDecl stay host — this is NOT the candy
// BUILD model (that is the CandyModel / S-CM concern, K3-D), only the identity/graph surface
// inspect/list/status read.
func projectCandyView(c *Candy) spec.CandyView {
	v := spec.CandyView{
		Name:          c.Name,
		Version:       c.Version,
		Description:   c.Description,
		Status:        c.Status,
		Info:          c.Info,
		Remote:        c.Remote,
		RepoPath:      c.RepoPath,
		IsPlugin:      c.Plugin != nil,
		Require:       bareRefs(c.Require),
		IncludedCandy: bareRefs(c.IncludedCandy),
		EnvProvides:   c.EnvProvides(),
		MCPProvide:    c.MCPProvide(),
	}
	for _, p := range c.PortSpecs() {
		v.Ports = append(v.Ports, int64(p.Port))
	}
	for _, s := range c.Service() {
		v.ServiceNames = append(v.ServiceNames, s.Name)
	}
	return v
}

// projectResolvedProject assembles the spec.ResolvedProject from already-loaded resolve-engine
// outputs — a pure DATA projection, no resolution logic of its own. boxes come from ResolveBox
// (enabled boxes, sorted), candies from ScanAllCandy (sorted), deploy from the folded uf.Bundle tree.
func projectResolvedProject(cfg *Config, layers map[string]*Candy, bundle map[string]BundleNode, dir, version string, opts ResolveOpts) (*spec.ResolvedProject, error) {
	rp := &spec.ResolvedProject{Version: version}

	calver := ComputeCalVer()
	for _, name := range cfg.allBoxNames() {
		img, ok := cfg.BoxConfig(name)
		if !ok {
			continue
		}
		if !img.IsEnabled() && !opts.shouldIncludeDisabled(name) {
			continue
		}
		resolved, err := cfg.ResolveBox(name, calver, dir, opts)
		if err != nil {
			return nil, fmt.Errorf("resolving box %q: %w", name, err)
		}
		if rp.Boxes == nil {
			rp.Boxes = make(map[string]spec.ResolvedBoxView, len(cfg.Box))
		}
		rp.Boxes[name] = projectResolvedBox(resolved)
	}

	for name, c := range layers {
		if c == nil {
			continue
		}
		if rp.Candies == nil {
			rp.Candies = make(map[string]spec.CandyView, len(layers))
		}
		rp.Candies[name] = projectCandyView(c)
	}

	if len(bundle) > 0 {
		// BundleNode is a type alias for spec.Deploy, so the folded deploy tree projects into the
		// envelope's map[string]*Deploy directly (a per-iteration copy, addressed).
		rp.Deploy = make(map[string]*spec.Deploy, len(bundle))
		for k, v := range bundle {
			node := v
			rp.Deploy[k] = &node
		}
	}

	return rp, nil
}

// buildResolvedProjectFromDir is the load+project entry the "resolved-project" host-builder wraps and
// host-side callers use directly. It registers the embedded build vocabulary (so ResolveBox resolves
// distro/builder reliably, the box-list/validate path), loads the project, and projects it.
func buildResolvedProjectFromDir(dir string, opts ResolveOpts) (*spec.ResolvedProject, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}
	if distroCfg, _, _, verr := LoadDefaultBuildConfig(dir); verr == nil {
		RegisterBuildVocabulary(distroCfg)
	}
	layers, err := ScanAllCandyWithConfigOpts(dir, cfg, opts)
	if err != nil {
		return nil, err
	}
	var bundle map[string]BundleNode
	version := ""
	if uf, present, uerr := LoadUnified(dir); uerr != nil {
		return nil, uerr
	} else if present {
		if derr := uf.ApplyDiscover(dir); derr != nil {
			return nil, derr
		}
		bundle = uf.Bundle
		version = uf.Version
	}
	return projectResolvedProject(cfg, layers, bundle, dir, version, opts)
}

// hostBuildResolvedProject is the "resolved-project" host-builder (F11): resolve the project at
// req.Dir (empty = cwd) and return the generic spec.ResolvedProject envelope. Registered idempotently
// at package-var init, like every other hostBuilders entry.
func hostBuildResolvedProject(_ context.Context, req spec.ResolvedProjectRequest, _ buildEngineContext) (spec.ResolvedProject, error) {
	dir := req.Dir
	if dir == "" {
		d, err := os.Getwd()
		if err != nil {
			return spec.ResolvedProject{}, err
		}
		dir = d
	}
	rp, err := buildResolvedProjectFromDir(dir, ResolveOpts{IncludeDisabled: req.IncludeDisabled})
	if err != nil {
		return spec.ResolvedProject{}, err
	}
	return *rp, nil
}

var _ = func() bool {
	registerHostBuilder(resolvedProjectBuilderKind, typedHostBuilder(resolvedProjectBuilderKind, hostBuildResolvedProject))
	return true
}()
