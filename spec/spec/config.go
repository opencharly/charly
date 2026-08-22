package spec

import (
	"encoding/json"
	"sort"
)

// config.go — the Config PROJECTION type (FLOOR-SLIM Unit 5, relocated from charly/config.go +
// charly/uf_box_generic.go + charly/candy_chain.go + charly/namespace.go). Config is the
// resolved-but-not-yet-built view of a project's charly.yml: every method here is PURE over
// already-loaded spec types (BoxConfig / the opaque box map / the namespace tree) — no LoadUnified,
// no host I/O. charly's LoadConfig (the ONLY LoadUnified-coupled surface it still holds) lives
// beside the loader seam it forwards into, charly/loader_threaded.go — K-wave 2 cone R1 deleted
// charly/config.go and its LoadConfigRaw twin, which had become an alias of LoadConfig.
//
// Split rationale (why this file holds ONLY the spec-typed subset): a Config method whose
// signature touches a buildkit type (e.g. *buildkit.ResolvedBox) cannot be defined here — spec is
// the bottom of the sdk dependency graph and must never import buildkit or deploykit (both import
// spec; the reverse is a cycle). Those methods are FREE FUNCTIONS in sdk/buildkit (ResolveBox,
// ResolveAllBox, resolveNamespacedBases, pullNamespacedBox, …) and sdk/deploykit (BoxCandyChain /
// BoxDirectCandies — their real dependency, ResolveCandyOrder, lives in deploykit, not buildkit)
// taking *Config as their first parameter instead of a receiver. The builder-map VALUE-PRIMITIVE
// cluster (ResolveEffectiveBuilder/EffectiveBuilderForBox/distroBuilderMap/PickDistroBuilder +
// the DistroBuilderCandidate type) is pure value over spec types, so it lives HERE as spec free
// functions (builder_resolve.go) — buildkit's ResolveBox/ResolveAllBox call spec.ResolveEffectiveBuilder
// for the builder-map leg.

// BoxMap is the generic image map type — name → opaque marshaled BoxConfig. A plain alias of
// map[string]json.RawMessage, so it round-trips byte-identically with charly's own (still
// separately declared, for the UnifiedFile side) `boxMap` alias — both resolve to the SAME
// underlying type.
type BoxMap = map[string]json.RawMessage

// Config represents the charly.yml configuration projection.
type Config struct {
	Defaults BoxConfig `yaml:"defaults" json:"defaults"`
	Box      BoxMap    `yaml:"box" json:"box"`
	// Local carries kind:local templates so remote-ref collection + validation walk their candy:
	// lists symmetrically with box candy lists (kind:local templates compose remote @-ref
	// candies too). Populated from UnifiedFile.Local by UnifiedFile.ProjectConfig() (sdk/loaderkit).
	Local map[string]json.RawMessage `yaml:"local,omitempty" json:"local,omitempty"`
	// Sidecar carries the project's sidecar-template library as OPAQUE bodies (the raw
	// PluginKinds["sidecar"] map). The kernel never reads their fields — candy/plugin-sidecar's
	// OpResolve owns all sidecar business logic.
	Sidecar map[string]json.RawMessage `yaml:"sidecar,omitempty" json:"sidecar,omitempty"`
	// Skills carries the project's skill-kind library as OPAQUE bodies (the raw
	// PluginKinds["skill"] map). The kernel never reads their fields — CollectSkills
	// (deploykit) projects the composed candies' skills into the ai.opencharly.skill label
	// and candy/plugin-marketplace regenerates the plugins/ corpus from the same source.
	Skills map[string]json.RawMessage `yaml:"skills,omitempty" json:"skills,omitempty"`
	// Namespaces carries child namespaces mounted by namespaced `import:` entries (alias →
	// projected sub-Config). Qualified refs like `cachyos.cachyos` resolve through here.
	// Populated from UnifiedFile.Namespaces by ProjectConfig().
	Namespaces map[string]*Config `yaml:"-"`
}

// DecodeBox decodes one opaque image body into the authored BoxConfig.
func DecodeBox(raw json.RawMessage) (BoxConfig, bool) {
	if len(raw) == 0 {
		return BoxConfig{}, false
	}
	var b BoxConfig
	if err := json.Unmarshal(raw, &b); err != nil {
		return BoxConfig{}, false
	}
	return b, true
}

// EncodeBox marshals an authored BoxConfig into its opaque body.
func EncodeBox(b BoxConfig) json.RawMessage {
	raw, err := json.Marshal(b)
	if err != nil {
		// A BoxConfig always marshals (plain struct); a failure is a programming error.
		panic("EncodeBox: " + err.Error())
	}
	return raw
}

// BoxConfigFrom decodes name's image config from a generic image map.
func BoxConfigFrom(m BoxMap, name string) (BoxConfig, bool) {
	raw, ok := m[name]
	if !ok {
		return BoxConfig{}, false
	}
	return DecodeBox(raw)
}

// BoxNamesOf returns the image names in a generic image map, sorted for determinism.
func BoxNamesOf(m BoxMap) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// BoxConfig decodes the authored image config for name; ok=false when absent.
func (c *Config) BoxConfig(name string) (BoxConfig, bool) { return BoxConfigFrom(c.Box, name) }

// HasBox reports whether an image named name is present.
func (c *Config) HasBox(name string) bool { _, ok := c.Box[name]; return ok }

// SetBox stores an authored image config under name (marshaling it opaque).
func (c *Config) SetBox(name string, b BoxConfig) {
	if c.Box == nil {
		c.Box = BoxMap{}
	}
	c.Box[name] = EncodeBox(b)
}

// AllBoxNames returns every image name (enabled or not), sorted — the raw-map view BoxNames
// filters. Used by the map-killing accessors where the enabled filter is applied separately.
// Exported: called cross-package from several charly consumers (host_build_feature.go,
// resolved_project_host.go, refs.go) and in-package by spec's own distroBuilderMap
// (builder_resolve.go).
func (c *Config) AllBoxNames() []string { return BoxNamesOf(c.Box) }

// EachBox iterates every image as (name, decoded BoxConfig) in sorted name order — the
// decode-on-iterate view consumers walk INSTEAD of the kernel holding a typed map. Exported: used
// cross-package as a range-over-func iterator by charly's validate.go / validate_project_host.go.
func (c *Config) EachBox(yield func(string, BoxConfig) bool) {
	for _, name := range BoxNamesOf(c.Box) {
		b, _ := DecodeBox(c.Box[name])
		if !yield(name, b) {
			return
		}
	}
}

// BoxNames returns a sorted list of enabled box names.
func (c *Config) BoxNames() []string {
	names := make([]string, 0, len(c.Box))
	for _, name := range c.AllBoxNames() {
		img, _ := c.BoxConfig(name)
		if !img.IsEnabled() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ResolveBoxRef resolves a (possibly qualified) box name to its BoxConfig and the Config
// (namespace context) it lives in. Bare names resolve in c; `ns.name` descends into
// c.Namespaces[ns] recursively. Exported: called cross-package from charly's deploy_ref.go,
// check_bed_run.go, host_build_box_ref_resolve.go, validate.go, refs.go, and from sdk/buildkit's
// resolveBase (via the *Config it's given).
func (c *Config) ResolveBoxRef(ref string) (BoxConfig, *Config, bool) {
	if ns, rest, ok := SplitNamespaceRef(ref); ok {
		sub, ok := c.Namespaces[ns]
		if !ok {
			return BoxConfig{}, nil, false
		}
		return sub.ResolveBoxRef(rest)
	}
	img, ok := c.BoxConfig(ref)
	if !ok {
		return BoxConfig{}, nil, false
	}
	return img, c, true
}

// FindBoxByLeaf searches for a box whose leaf (unqualified) name equals `leaf` — first in c's own
// root box map, then recursively in each imported namespace (deterministic alias order). Returns
// the fully-qualified ref under which the box is reachable from c (bare for a root hit, `ns.<...>`
// for a namespaced hit), or ok=false when no box with that leaf exists anywhere in the namespace
// tree.
//
// This is the DISCOVERY dual of ResolveBoxRef: ResolveBoxRef resolves a KNOWN qualified ref by
// descent, whereas FindBoxByLeaf discovers the qualification for a bare leaf that may live in a
// namespace. ensure-image's build-fallback needs it because it only has the basename of a full
// registry ref (e.g. `arch-builder` extracted from `ghcr.io/opencharly/arch-builder:<tag>`) and
// must find that the box lives under the `charly` namespace to build it locally.
func (c *Config) FindBoxByLeaf(leaf string) (string, bool) {
	if leaf == "" {
		return "", false
	}
	if _, ok := c.Box[leaf]; ok {
		return leaf, true
	}
	aliases := make([]string, 0, len(c.Namespaces))
	for ns := range c.Namespaces {
		aliases = append(aliases, ns)
	}
	sort.Strings(aliases)
	for _, ns := range aliases {
		if q, ok := c.Namespaces[ns].FindBoxByLeaf(leaf); ok {
			return ns + "." + q, true
		}
	}
	return "", false
}

// BaseChainNode is one image visited while walking an internal base chain. Name is the ref as it
// was reached (bare for a root image, namespace-qualified for a base reached across an import
// boundary, e.g. `cachyos.cachyos`).
type BaseChainNode struct {
	Name string
	Img  BoxConfig
}

// WalkBaseChain walks boxName's ROOT-INTERNAL base-image chain and returns the images in walk
// order (self first, then each internal base). It is the ONE shared base-chain traversal used by
// every chain-walking collector (CollectHooks / CollectShell / CollectDescription /
// CollectBoxVolume) — each previously re-implemented the identical
// `for { img := cfg.Box[current]; ...; current = img.Base }` loop (R3: one implementation, no
// divergent copies), now cycle-safe for all of them.
//
// It deliberately does NOT descend import namespaces. A namespace-qualified base (e.g.
// `selkies.selkies-labwc`) is a SEPARATELY-BUILT image that owns its own baked check / hooks /
// shell / volume labels; re-collecting its candies into the consumer would DOUBLE-COUNT every
// candy the consumer also lists directly. Namespace-AWARENESS belongs to NAME resolution
// (ResolveBoxRef / FindBoxByLeaf), not to this per-image candy-collection walk; the distro/build
// VALUE walkers (WalkBaseChainDistro / WalkBaseChainBuild) cross namespaces precisely because
// those are inherited values, whereas candy contributions are not.
//
// Exported: called cross-package from sdk/deploykit's BoxCandyChain.
func (c *Config) WalkBaseChain(boxName string) []BaseChainNode {
	var out []BaseChainNode
	seen := make(map[string]bool)
	current := boxName
	for current != "" && !seen[current] {
		seen[current] = true
		img, ok := c.BoxConfig(current)
		if !ok {
			break
		}
		out = append(out, BaseChainNode{Name: current, Img: img})
		baseImg, isInternal := c.BoxConfig(img.Base)
		if !isInternal || !baseImg.IsEnabled() {
			break
		}
		current = img.Base
	}
	return out
}

// WalkBaseChainDistro walks the base chain through box: entries to find the first ancestor with a
// distro: field set. Returns nil if no ancestor defines distro tags or the chain reaches an
// external base image. Exported: called cross-package from sdk/buildkit's resolveDistro /
// effectiveBuilderForBox.
func (c *Config) WalkBaseChainDistro(baseName string) []string {
	seen := make(map[string]bool)
	cur := c
	current := baseName
	for {
		if seen[current] {
			return nil // cycle detected
		}
		seen[current] = true
		// ResolveBoxRef crosses import namespaces (`cachyos.cachyos`); distro
		// is a VALUE so inheriting it across a namespace boundary is correct.
		baseImg, sub, ok := cur.ResolveBoxRef(current)
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

// WalkBaseChainBuild walks the base chain through box: entries to find the first ancestor with a
// build: field set. Returns nil if no ancestor defines build formats or the chain reaches an
// external base image. Exported: called cross-package from sdk/buildkit's resolveBuild.
func (c *Config) WalkBaseChainBuild(baseName string) []string {
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
		baseImg, sub, ok := cur.ResolveBoxRef(current)
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
