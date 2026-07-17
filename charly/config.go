package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"

	"github.com/opencharly/sdk/deploykit"
)

// ErrNoCharlyYml is the sentinel wrapped by every "no charly.yml found in the
// project dir" load error. Callers that treat an absent project as EMPTY rather
// than a hard failure (the `charly box list …` read commands — an empty project
// has zero boxes, like `ls` in an empty dir) match it with errors.Is.
var ErrNoCharlyYml = errors.New("no charly.yml found in project directory")

// noCharlyYmlErr is the ONE construction of the absent-project load error
// (config.go + format_config.go), wrapping ErrNoCharlyYml for errors.Is.
func noCharlyYmlErr(dir string) error {
	return fmt.Errorf("no charly.yml found in %s (run `charly box new project .` to scaffold one): %w", dir, ErrNoCharlyYml)
}

// Config represents the charly.yml configuration projection
type Config struct {
	Defaults spec.BoxConfig `yaml:"defaults" json:"defaults"`
	Box      boxMap         `yaml:"box" json:"box"`
	// Local carries kind:local templates so remote-ref collection +
	// validation walk their candy: lists symmetrically with box candy
	// lists (kind:local templates compose remote @-ref candies too). Populated
	// from UnifiedFile.Local by ProjectConfig().
	Local map[string]json.RawMessage `yaml:"local,omitempty" json:"local,omitempty"`
	// Sidecar carries the project's sidecar-template library as OPAQUE bodies
	// (the raw PluginKinds["sidecar"] map). The kernel never reads their fields —
	// candy/plugin-sidecar's OpResolve owns all sidecar business logic (the sidecar
	// de-type, Cutover D). See /charly-automation:sidecar.
	Sidecar map[string]json.RawMessage `yaml:"sidecar,omitempty" json:"sidecar,omitempty"`
	// Namespaces carries child namespaces mounted by namespaced `import:`
	// entries (alias → projected sub-Config). Qualified refs like
	// `cachyos.cachyos` resolve through here. Populated from
	// UnifiedFile.Namespaces by ProjectConfig(). See namespace.go.
	Namespaces map[string]*Config `yaml:"-"`
}

// BuildFormats handles YAML unmarshal of the build: field.
// Package formats tied to the defined builders, installed in list order.
// Single string "rpm" becomes ["rpm"]. List ["pac", "aur"] stays as-is.
type BuildFormats []string

// LoadConfig reads charly.yml and returns the Config (defaults + images)
// projection. Mode purity preserved: this reads the PROJECT charly.yml only and
// never merges the per-host charly.yml overlay. Deploy-mode commands must call
// LoadBundleConfig + MergeDeployOntoMetadata explicitly.
func LoadConfig(dir string) (*Config, error) {
	return LoadConfigRaw(dir)
}

// LoadConfigRaw is an alias retained for call sites that previously
// distinguished raw-vs-merged loads. Both forms now read charly.yml via
// LoadUnified and return the Images projection.
func LoadConfigRaw(dir string) (*Config, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if !present {
		return nil, noCharlyYmlErr(dir)
	}
	cfg := uf.ProjectConfig()
	return cfg, nil
}

// ResolveOpts carries optional knobs for ResolveBox. Zero value is the
// default behavior used by every code path EXCEPT the explicit operational
// overrides on `charly box build/inspect/validate --include-disabled` —
// those set IncludeDisabled to bypass the `enabled: false` gate without
// requiring the operator to flip authored config.
//
// IncludeDisabledNames scopes the override: when non-empty, ONLY images in
// the set bypass the disabled check; other disabled images stay filtered.
// Used by `charly box build <name> --include-disabled` so widening the
// working set doesn't surface unrelated disabled-image dep errors (e.g.
// images with remote candies that aren't fetched yet). Empty + IncludeDisabled
// = include every disabled image (the inspect/validate behavior).
type ResolveOpts struct {
	IncludeDisabled      bool            // skip the `enabled: false` check
	IncludeDisabledNames map[string]bool // when non-empty, scope IncludeDisabled to these names only
	// RequestedBoxes are the explicit build targets (`charly box build <name>`).
	// A qualified name here (e.g. `charly.arch-builder`) is pulled into the resolved
	// set even when it isn't reachable as a base/builder of a root image — so a
	// namespaced image can be an on-demand build target, not only a transitive
	// base. Bare names are ignored here (they resolve through the root loop).
	RequestedBoxes []string
	// ExtraCandyRefs are candy refs to collect IN ADDITION to the image/builder/
	// kind:local-template closure — specifically a DEPLOY's `add_candy:` candies.
	// The image-closure walk (collectBox) never reaches them (a deploy's add_candy
	// is not a base/builder/require edge of any image), so a bed that add_candy's a
	// host-side PLUGIN candy (e.g. plugin-spice for the `spice:` check verb) must
	// pass its add_candy refs here, or the plugin never enters the candy scan and
	// loadProjectPlugins can't build/connect it. Remote refs are fetched through the
	// SAME pipeline (per-entity-version arbitration + SourceDir population); a local
	// add_candy ref is already covered by ScanCandy and is a no-op here.
	ExtraCandyRefs []string
}

// shouldIncludeDisabled reports whether name's disabled gate should be
// bypassed under opts. Centralizes the IncludeDisabled + IncludeDisabledNames
// interaction so call sites stay simple.
func (opts ResolveOpts) shouldIncludeDisabled(name string) bool {
	if !opts.IncludeDisabled {
		return false
	}
	if len(opts.IncludeDisabledNames) == 0 {
		return true
	}
	return opts.IncludeDisabledNames[name]
}

// ResolveBox resolves a single box's configuration by applying defaults
func (c *Config) ResolveBox(name string, calverTag string, dir string, opts ResolveOpts) (*buildkit.ResolvedBox, error) {
	// Namespace-aware entry: a qualified name (e.g. `charly.arch-builder`,
	// `cachyos.cachyos`) resolves inside the Config of the namespace that
	// owns it, where its base:/builder: refs are relative. This mirrors
	// resolveBoxRef's descent (namespace.go) so that EVERY ResolveBox
	// caller — `charly box inspect/generate/merge/pull/validate`,
	// ensure-image's build-fallback, `charly bundle add`/`charly update` — is
	// namespace-aware through this single chokepoint instead of each
	// re-implementing (or omitting) the descent. Additive: a bare name
	// takes the flat tail below exactly as before, so existing behaviour
	// is unchanged; only qualified names (which previously hard-errored
	// "not found") gain resolution.
	if ns, rest, ok := spec.SplitNamespaceRef(name); ok {
		sub, found := c.Namespaces[ns]
		if !found {
			return nil, fmt.Errorf("import namespace %q not found (resolving image %q)", ns, name)
		}
		return sub.ResolveBox(rest, calverTag, dir, opts)
	}
	img, ok := c.BoxConfig(name)
	if !ok {
		return nil, fmt.Errorf("box %q not found in charly.yml", name)
	}
	if !img.IsEnabled() && !opts.shouldIncludeDisabled(name) {
		return nil, fmt.Errorf("image %q is disabled (pass --include-disabled to operate on it without flipping authored config)", name)
	}

	resolved := &buildkit.ResolvedBox{
		Name:       name,
		Version:    img.Version,
		Status:     resolveStatus(""), // boxes author no status; the effective rung (worst-of-candy-chain) is computed at generate time for the ai.opencharly.status label
		Info:       deploykit.DescriptionInfo(img.Description),
		CheckLevel: kit.ResolveCheckLevel(img.CheckLevel),
	}

	if err := c.resolveBase(resolved, img, name); err != nil {
		return nil, err
	}

	c.resolvePlatforms(resolved, img)

	c.resolveTag(resolved, img, calverTag)

	// Resolve registry: image -> defaults -> ""
	resolved.Registry = img.Registry
	if resolved.Registry == "" {
		resolved.Registry = c.Defaults.Registry
	}

	c.resolveDistro(resolved, img)

	if err := c.resolveBuild(resolved, img, name); err != nil {
		return nil, err
	}

	// Build unified Tags for task matching: ["all"] + Distro + BuildFormats
	resolved.Tags = append([]string{"all"}, resolved.Distro...)
	resolved.Tags = append(resolved.Tags, resolved.BuildFormats...)

	// Candies are not inherited, they're image-specific
	// Strip @ prefix and :version suffixes — candy map keys use bare refs
	resolved.Candy = make([]string, len(img.Candy))
	for i, ref := range img.Candy {
		resolved.Candy[i] = deploykit.BareRef(ref)
	}

	// Ports are NOT resolved here — they are inherited from the candy chain
	// (CollectBoxPorts) at OCI-label emission + inspect time, since ResolveBox
	// has no candy map. Boxes no longer declare ports.

	// Resolve user: image -> defaults -> "user"
	resolved.User = img.User
	if resolved.User == "" {
		resolved.User = c.Defaults.User
	}
	if resolved.User == "" {
		resolved.User = "user"
	}

	// Resolve UID: image -> defaults -> 1000
	resolved.UID = resolveIntPtr(img.UID, c.Defaults.UID, 1000)

	// Resolve GID: image -> defaults -> 1000
	resolved.GID = resolveIntPtr(img.GID, c.Defaults.GID, 1000)

	// Resolve merge config: image -> defaults -> nil
	if img.Merge != nil {
		resolved.Merge = img.Merge
	} else if c.Defaults.Merge != nil {
		resolved.Merge = c.Defaults.Merge
	}

	// Builder resolution flows through the ONE canonical method so it can't
	// diverge across commands (build/generate/inspect via ResolveBox,
	// `charly bundle add`'s synthetic host/VM image, and the remote-ref fetch walk
	// via effectiveBuilderForBox all call resolveEffectiveBuilder).
	resolved.Builder = c.resolveEffectiveBuilder(name, resolved.Distro, resolved.Base, resolved.IsExternalBase, img.Builder)

	// BuilderCapabilities: image-specific capability declaration, NOT inherited
	resolved.BuilderCapabilities = img.Produce

	// Schema v4: DNS / AcmeEmail / Tunnel / Engine no longer resolve from
	// image config — they are deployment choices and flow through
	// MergeDeployOntoMetadata → BoxMetadata directly.

	// VM configuration (disk_size, ram, cpus, firmware, libvirt, …) lives
	// on `kind: vm` entities in vm.yml, NOT on box: entries. The
	// legacy BoxConfig.Vm / .Libvirt fields were removed in the VM
	// hard-cutover; `bootc: true` on an image now only declares that the
	// container image is bootc-bootable (for `charly vm build` to produce a
	// qcow2 via `bootc install to-disk`). To run that bootc image as a
	// VM, declare a paired `kind: vm` entity with `source.kind: bootc`
	// in vm.yml (see `charly migrate`).

	// Resolve network: image -> defaults -> ""
	resolved.Network = img.Network
	if resolved.Network == "" {
		resolved.Network = c.Defaults.Network
	}

	// Data image flag (not inherited from defaults)
	resolved.DataImage = img.DataImage

	// Home directory will be resolved later (after inspecting base image)
	if resolved.User == "root" {
		resolved.Home = "/root"
	} else {
		resolved.Home = fmt.Sprintf("/home/%s", resolved.User)
	}

	// Compute full tag
	if resolved.Registry != "" {
		resolved.FullTag = fmt.Sprintf("%s/%s:%s", resolved.Registry, name, resolved.Tag)
	} else {
		resolved.FullTag = fmt.Sprintf("%s:%s", name, resolved.Tag)
	}

	// Resolve build config from charly.yml. Unconditional — caller must
	// supply a project dir containing charly.yml. Tests that need
	// in-memory-only resolution use testProjectDir(t).
	distroCfg, builderCfg, _, err := LoadBuildConfigForBox(dir)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", name, err)
	}
	resolved.DistroConfig = distroCfg
	resolved.BuilderConfig = builderCfg
	if distroCfg != nil {
		// Expand the package-cascade chain with any inherit_packages: ancestor
		// (cachyos → [cachyos, arch]) so an `arch:` candy block reaches cachyos.
		// Idempotent when the box already authored the ancestor explicitly.
		resolved.Distro = distroCfg.ExpandPackageInheritance(resolved.Distro)
		resolved.DistroDef = distroCfg.ResolveDistro(resolved.Distro)
	}

	// Reconcile user_policy against the distro's base_user declaration.
	// Must run after DistroDef is resolved. Updates resolved.User/UID/GID/
	// Home when adopting so all downstream substitution (${USER}, ${HOME},
	// COPY --chown, sudoers writes) sees the adopted identity.
	policy := img.UserPolicy
	if policy == "" {
		policy = c.Defaults.UserPolicy
	}
	if policy == "" {
		policy = "auto"
	}
	baseUser := (*BaseUserDef)(nil)
	if resolved.DistroDef != nil {
		baseUser = resolved.DistroDef.BaseUser
	}
	userExplicitlySet := img.User != "" || c.Defaults.User != ""
	switch policy {
	case "adopt":
		if baseUser == nil {
			return nil, fmt.Errorf("image %s: user_policy: adopt requires distro %v to declare base_user in the embedded vocabulary (charly/charly.yml)", name, resolved.Distro)
		}
		resolved.User = baseUser.Name
		resolved.UID = baseUser.UID
		resolved.GID = baseUser.GID
		resolved.Home = baseUser.Home
		resolved.UserAdopted = true
	case "auto":
		if baseUser != nil && !userExplicitlySet {
			resolved.User = baseUser.Name
			resolved.UID = baseUser.UID
			resolved.GID = baseUser.GID
			resolved.Home = baseUser.Home
			resolved.UserAdopted = true
		}
	case "create":
		// no-op — resolved.User/UID/GID/Home already reflect image config +
		// defaults + hardcoded fallback, and the bootstrap preamble
		// (deploykit.Generator.WriteBootstrap) will useradd.
	default:
		return nil, fmt.Errorf("image %s: unknown user_policy %q (expected auto, adopt, or create)", name, policy)
	}

	return resolved, nil
}

// resolveBase resolves a box's base image (from-builder, data-image, or
// base:/defaults chain) and sets IsExternalBase. Split out of ResolveBox.
func (c *Config) resolveBase(resolved *buildkit.ResolvedBox, img spec.BoxConfig, name string) error {
	// `from: builder:<name>` — non-registry base via a kind: bootstrap
	// builder. Mutually exclusive with base:; pre-build phase produces
	// a rootfs tarball, generator emits FROM scratch + ADD.
	switch {
	case img.From != "":
		if img.HasBaseFromConflict() {
			return fmt.Errorf("image %s: from: and base: are mutually exclusive", name)
		}
		resolved.From = img.From
		resolved.BootstrapBuilderImage = img.BootstrapBuilderImage
		resolved.Base = "scratch"
		resolved.IsExternalBase = true
	case img.DataImage:
		resolved.Base = "scratch"
		resolved.IsExternalBase = true
	default:
		// Resolve base: image -> defaults -> "quay.io/fedora/fedora:43"
		resolved.Base = img.Base
		if resolved.Base == "" {
			resolved.Base = c.Defaults.Base
		}
		if resolved.Base == "" {
			resolved.Base = "quay.io/fedora/fedora:43"
		}

		// Check if base is internal (another enabled image — local OR resolved
		// through an import namespace, e.g. `cachyos.cachyos`) or external.
		if baseImg, _, isInternal := c.resolveBoxRef(resolved.Base); isInternal && baseImg.IsEnabled() {
			resolved.IsExternalBase = false
		} else {
			resolved.IsExternalBase = true
		}
	}
	return nil
}

// resolvePlatforms resolves a box's target platforms
// (image -> defaults -> linux/amd64+arm64). Split out of ResolveBox.
func (c *Config) resolvePlatforms(resolved *buildkit.ResolvedBox, img spec.BoxConfig) {
	resolved.Platforms = img.Platforms
	if len(resolved.Platforms) == 0 {
		resolved.Platforms = c.Defaults.Platforms
	}
	if len(resolved.Platforms) == 0 {
		resolved.Platforms = []string{"linux/amd64", "linux/arm64"}
	}
}

// resolveTag resolves a box's tag (image -> defaults -> "auto"), substituting
// the computed calver when "auto". Split out of ResolveBox.
func (c *Config) resolveTag(resolved *buildkit.ResolvedBox, img spec.BoxConfig, calverTag string) {
	resolved.Tag = img.Tag
	if resolved.Tag == "" {
		resolved.Tag = c.Defaults.Tag
	}
	if resolved.Tag == "" {
		resolved.Tag = "auto"
	}
	// If tag is "auto", use the computed calver
	if resolved.Tag == "auto" {
		resolved.Tag = calverTag
	}
}

// resolveDistro resolves a box's distro tags
// (image -> base-chain walk -> defaults). Split out of ResolveBox.
func (c *Config) resolveDistro(resolved *buildkit.ResolvedBox, img spec.BoxConfig) {
	resolved.Distro = img.Distro
	if len(resolved.Distro) == 0 {
		resolved.Distro = c.walkBaseChainDistro(resolved.Base)
	}
	if len(resolved.Distro) == 0 {
		resolved.Distro = c.Defaults.Distro
	}
}

// resolveBuild resolves a box's build formats (image -> base-chain walk ->
// defaults; required unless a data image) and the primary cache-mount format.
// Split out of ResolveBox.
func (c *Config) resolveBuild(resolved *buildkit.ResolvedBox, img spec.BoxConfig, name string) error {
	buildFmts := img.Build
	if len(buildFmts) == 0 {
		buildFmts = c.walkBaseChainBuild(resolved.Base)
	}
	if len(buildFmts) == 0 {
		buildFmts = c.Defaults.Build
	}
	if len(buildFmts) == 0 && !img.DataImage {
		return fmt.Errorf("image %s: build: field required (set in image, base, or defaults)", name)
	}
	resolved.BuildFormats = buildFmts
	if len(buildFmts) > 0 {
		resolved.Pkg = buildFmts[0] // primary format for cache mounts
	}
	return nil
}

// ResolveAllBox resolves all enabled images in the config. opts.IncludeDisabled
// extends the working set to images marked enabled: false (the build verb's
// `--include-disabled` flag flips this for one-off operational rebuilds
// without modifying authored config).
func (c *Config) ResolveAllBox(calverTag string, dir string, opts ResolveOpts) (map[string]*buildkit.ResolvedBox, error) {
	resolved := make(map[string]*buildkit.ResolvedBox)
	for _, name := range c.allBoxNames() {
		img, _ := c.BoxConfig(name)
		if !img.IsEnabled() && !opts.shouldIncludeDisabled(name) {
			continue
		}
		ri, err := c.ResolveBox(name, calverTag, dir, opts)
		if err != nil {
			return nil, err
		}
		resolved[name] = ri
	}
	// Pull in any namespace-qualified base images (e.g. versa's
	// `base: cachyos.cachyos`) resolved within their own namespace context,
	// keyed by the fully-qualified name, so the build graph + generator can
	// reference them. See namespace.go.
	// Pull in any explicitly-requested namespace-qualified targets BEFORE base
	// resolution. resolveNamespacedBases is reachability-scoped (it only follows
	// bases/builders of root images); an on-demand target like
	// `charly box build charly.arch-builder` — or ensure-image's build-fallback for a
	// namespaced builder — must be pulled explicitly so it lands in the resolved
	// map under its fully-qualified key. Pulling it FIRST lets the
	// resolveNamespacedBases fixpoint below also collect the target's own
	// transitive bases AND builders (it iterates the resolved set), so the build
	// graph + filterBox have every dependency. Uses the SAME
	// pullNamespacedBox path as the base pull.
	for _, name := range opts.RequestedBoxes {
		if _, _, qualified := spec.SplitNamespaceRef(name); !qualified {
			continue
		}
		if _, done := resolved[name]; done {
			continue
		}
		if err := c.pullNamespacedBox(c, name, "", calverTag, dir, opts, resolved); err != nil {
			return nil, err
		}
	}
	if err := c.resolveNamespacedBases(resolved, calverTag, dir, opts); err != nil {
		return nil, err
	}
	return resolved, nil
}

// BoxNames returns a sorted list of enabled box names
func (c *Config) BoxNames() []string {
	names := make([]string, 0, len(c.Box))
	for _, name := range c.allBoxNames() {
		img, _ := c.BoxConfig(name)
		if !img.IsEnabled() {
			continue
		}
		names = append(names, name)
	}
	// Sort for deterministic output
	kit.SortStrings(names)
	return names
}

// resolveIntPtr resolves a *int value through fallback chain: value -> fallback -> defaultVal
func resolveIntPtr(value, fallback *int, defaultVal int) int {
	if value != nil {
		return *value
	}
	if fallback != nil {
		return *fallback
	}
	return defaultVal
}

// resolveEffectiveBuilder computes an image's effective builder map via the
// SINGLE canonical precedence, lowest→highest:
//
//	defaults.builder       (the project-wide baseline)
//	→ distro-keyed default (the root image whose distro: matches THIS image's
//	                        resolved distro — so an arch/cachyos image
//	                        auto-selects arch-builder, a fedora image
//	                        fedora-builder, with NO per-image builder: map)
//	→ direct local base    (a same-namespace base's builder map)
//	→ per-image override    (img.Builder)
//
// then self-references are filtered (a builder image must not use itself).
//
// Why distro-keyed and not base-inherited: a builder map holds
// namespace-relative REFS, so it can't be copied across an import-namespace
// boundary (a base's `charly.arch-builder` would dangle in a consumer where `charly.`
// doesn't resolve — see charly/namespace.go). `distro:` IS a value and DOES cross
// the boundary, so we key off the resolved distro and source the builder map
// from a root-namespace image whose bare refs resolve HERE.
//
// EVERY builder-consuming path calls this — ResolveBox (box: images), the
// synthetic host/VM image in `charly bundle add` (deploy_add_cmd.go), and the
// remote-ref FETCH walk (effectiveBuilderForBox → CollectRemoteRefsOpts) — so
// the resolution can never drift between commands and the fetch set stays in
// lockstep with the resolve set.
func (c *Config) resolveEffectiveBuilder(name string, distro []string, base string, isExternalBase bool, imgBuilder buildkit.BuilderMap) buildkit.BuilderMap {
	out := make(buildkit.BuilderMap)
	maps.Copy(out, c.Defaults.Builder)
	maps.Copy(out, c.distroBuilderMap(distro))
	if !isExternalBase {
		// DELIBERATELY flat (not resolveBoxRef): a base's builder map is only
		// inherited when the base is ROOT-local. A namespace-qualified base
		// (e.g. `cachyos.cachyos`) intentionally does NOT contribute its builder
		// map here — builder: is a map of namespace-relative refs that would
		// dangle in this consumer's namespace; the consumer instead gets its
		// builder distro-keyed via distroBuilderMap above. So the qualified-base
		// miss is correct, not a divergence bug. See namespace.go's header.
		if baseImg, ok := c.BoxConfig(base); ok {
			maps.Copy(out, baseImg.Builder)
		}
	}
	maps.Copy(out, imgBuilder)
	for typ, b := range out {
		if b == name {
			delete(out, typ)
		}
	}
	return out
}

// effectiveBuilderForBox computes the builder image refs an image will build
// against, from a RAW BoxConfig — the FETCH-path counterpart to ResolveBox's
// resolved-value path. Both end at the ONE canonical resolveEffectiveBuilder;
// this helper just supplies its inputs (base, is-external-base, distro) using the
// SAME precedence + canonical helpers ResolveBox uses (Defaults fallback,
// resolveBoxRef for the internal/external line, walkBaseChainDistro for distro
// inheritance). It exists because CollectRemoteRefsOpts.collectBox runs during
// the remote-ref FETCH phase, before any ResolvedBox exists (and without the
// dir/tag ResolveBox needs); reading the raw per-image img.Builder there
// under-collected builders supplied by defaults.builder / the distro-keyed
// default (whose per-image map is empty — e.g. bazzite/aurora -> charly.fedora-builder),
// surfacing as "unknown layer" at generate time. Routing through this keeps the
// FETCH set's builder edges in lockstep with the RESOLVE set's (resolveNamespacedBases).
func (c *Config) effectiveBuilderForBox(name string, img spec.BoxConfig) buildkit.BuilderMap {
	base := "scratch"
	isExternalBase := true
	if img.From == "" && !img.DataImage {
		base = img.Base
		if base == "" {
			base = c.Defaults.Base
		}
		if base == "" {
			base = "quay.io/fedora/fedora:43"
		}
		if baseImg, _, isInternal := c.resolveBoxRef(base); isInternal && baseImg.IsEnabled() {
			isExternalBase = false
		}
	}
	distro := img.Distro
	if len(distro) == 0 {
		distro = c.walkBaseChainDistro(base)
	}
	if len(distro) == 0 {
		distro = c.Defaults.Distro
	}
	return c.resolveEffectiveBuilder(name, distro, base, isExternalBase, img.Builder)
}

// distroBuilderMap returns the builder map of the root-namespace image that
// owns the given distro — the distro-keyed builder default. This is what lets
// a cachyos/Arch image auto-select `arch-builder` (and a Fedora image
// `fedora-builder`) WITHOUT a per-image `builder:` declaration: the matching
// source image (e.g. base.yml's `arch`, distro [arch], builder arch-builder)
// lives in THIS root namespace, so its bare builder refs resolve here — unlike
// a base's namespace-relative builder map, which must NOT be copied across an
// import-namespace boundary (see charly/namespace.go).
//
// distroTags is the image's resolved distro in priority order (most-specific
// first, e.g. ["cachyos","arch"] or ["fedora:43","fedora"]); the first tag with
// a matching root image wins, so a cachyos image with no root `cachyos` image
// correctly falls through to its `arch` tag → arch-builder. Only root images
// that actually declare a non-empty builder map are considered. Root-image
// iteration is name-sorted so the result is deterministic when more than one
// image shares a distro tag.
func (c *Config) distroBuilderMap(distroTags []string) buildkit.BuilderMap {
	if len(distroTags) == 0 {
		return nil
	}
	names := c.allBoxNames()
	for _, tag := range distroTags {
		for _, name := range names {
			img, _ := c.BoxConfig(name)
			if len(img.Builder) == 0 {
				continue
			}
			if slices.Contains(img.Distro, tag) {
				return img.Builder
			}
		}
	}
	return nil
}

// walkBaseChainDistro walks the base chain through box: entries to find
// the first ancestor with a distro: field set. Returns nil if no ancestor
// defines distro tags or the chain reaches an external base image.
func (c *Config) walkBaseChainDistro(baseName string) []string {
	seen := make(map[string]bool)
	cur := c
	current := baseName
	for {
		if seen[current] {
			return nil // cycle detected
		}
		seen[current] = true
		// resolveBoxRef crosses import namespaces (`cachyos.cachyos`); distro
		// is a VALUE so inheriting it across a namespace boundary is correct.
		baseImg, sub, ok := cur.resolveBoxRef(current)
		if !ok || !baseImg.IsEnabled() {
			return nil // external base or disabled
		}
		if len(baseImg.Distro) > 0 {
			return baseImg.Distro
		}
		if baseImg.Base == "" {
			return nil
		}
		cur = sub
		current = baseImg.Base
	}
}

// walkBaseChainBuild walks the base chain through box: entries to find
// the first ancestor with a build: field set. Returns nil if no ancestor
// defines build formats or the chain reaches an external base image.
func (c *Config) walkBaseChainBuild(baseName string) []string {
	seen := make(map[string]bool)
	cur := c
	current := baseName
	for {
		if seen[current] {
			return nil // cycle detected
		}
		seen[current] = true
		// Crosses import namespaces; build: is a VALUE (format list), inherited
		// across a namespace boundary like distro:.
		baseImg, sub, ok := cur.resolveBoxRef(current)
		if !ok || !baseImg.IsEnabled() {
			return nil // external base or disabled
		}
		if len(baseImg.Build) > 0 {
			return baseImg.Build
		}
		if baseImg.Base == "" {
			return nil
		}
		cur = sub
		current = baseImg.Base
	}
}

// baseChainNode is one image visited while walking an internal base chain.
// Name is the ref as it was reached (bare for a root image, namespace-qualified
// for a base reached across an import boundary, e.g. `cachyos.cachyos`).
type baseChainNode struct {
	Name string
	Img  spec.BoxConfig
}

// walkBaseChain walks boxName's ROOT-INTERNAL base-image chain and returns
// the images in walk order (self first, then each internal base). It is the ONE
// shared base-chain traversal used by every chain-walking collector
// (CollectHooks / CollectShell / CollectDescription /
// CollectBoxVolume) — each previously re-implemented the identical
// `for { img := cfg.Box[current]; ...; current = img.Base }` loop (R3: one
// implementation, no divergent copies), now cycle-safe for all of them.
//
// It deliberately does NOT descend import namespaces. A namespace-qualified
// base (e.g. `selkies.selkies-labwc`) is a SEPARATELY-BUILT image that owns
// its own baked check / hooks / shell / volume labels; re-collecting its candies
// into the consumer would DOUBLE-COUNT every candy the consumer also lists
// directly (the same candy reached bare here and via its `@github…` ref in the
// base), which the per-section id-uniqueness validator correctly rejects.
// Stopping at the namespace boundary (and at external / disabled / missing
// bases) is the long-standing, semantically-correct per-image collection
// behaviour — preserved here byte-for-byte. Namespace-AWARENESS belongs to
// NAME resolution (ResolveBox / resolveBoxRef / findBoxByLeaf), not to
// this per-image candy-collection walk; the distro/build VALUE walkers
// (walkBaseChainDistro / walkBaseChainBuild) cross namespaces precisely because
// those are inherited values, whereas candy contributions are not.
func (c *Config) walkBaseChain(boxName string) []baseChainNode {
	var out []baseChainNode
	seen := make(map[string]bool)
	current := boxName
	for current != "" && !seen[current] {
		seen[current] = true
		img, ok := c.BoxConfig(current)
		if !ok {
			break
		}
		out = append(out, baseChainNode{Name: current, Img: img})
		baseImg, isInternal := c.BoxConfig(img.Base)
		if !isInternal || !baseImg.IsEnabled() {
			break
		}
		current = img.Base
	}
	return out
}
