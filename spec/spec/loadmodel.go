package spec

import (
	"encoding/json"
	"fmt"
	"strings"
)

// loadmodel.go — the loader-RESULT type family (#55 Phase B): the typed, in-memory
// DATA a project's charly.yml loads into, relocated from sdk/loaderkit (UnifiedFile,
// InlineCandy, ValidationError, CandyCandidate, the ProjectTemplates projection) so
// charly core and every loader-consuming plugin share ONE definition through the
// types-only spec module — dropping their sdk/loaderkit import where it was type-only.
//
// These are genuinely wire-shaped DATA carriers (spec.*/map/plain fields), NOT
// mechanism: the kind-blind WALK + registry-coupled MATERIALIZE that PRODUCE a
// UnifiedFile stay in sdk/loaderkit. The projection METHODS below (ProjectConfig,
// ProjectTemplates, the PluginKinds accessors, ResolvePluginKindViaPlugin/
// DecodePluginKindMap) need nothing beyond uf's own fields + other spec types, so
// they travel with the type. The ONE method that could NOT move — ProjectFleetConfig
// (returns *deploykit.FleetConfig, and spec must never import a mechanism kit) — is a
// deploykit FREE FUNCTION now: deploykit.ProjectFleetConfig(uf). loaderkit's
// ResolveOpts stays put too: it embeds *buildkit.{Init,Distro,Builder}Config mechanism
// config, so it is correctly-placed loader mechanism, not a wire type.

// UnifiedFile is the full schema of a single unified-format YAML document. Every field is
// optional — a file with only `distro:` is valid (typical for the embedded build vocabulary); a
// file with only `deploy:` is valid; etc.
type UnifiedFile struct {
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	// Repo is this project's canonical repo identity (e.g. "github.com/opencharly/charly").
	// Optional; only meaningful on the ROOT file.
	Repo string `yaml:"repo,omitempty" json:"repo,omitempty"`
	// Import is the SINGLE composition statement. A list whose items are either a bare string
	// (flat import into THIS root namespace) or a single-key map `alias: ref` (a namespaced
	// child import). See ImportList (load_directives.go).
	Import   ImportList     `yaml:"import,omitempty" json:"import,omitempty"`
	Discover DiscoverConfig `yaml:"discover,omitempty" json:"discover,omitempty"`
	// Defaults carries the `defaults:` block (BoxConfig-shaped inheritance defaults).
	Defaults BoxConfig `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	// Box is the generic kind-keyed IMAGE map (P6): name → opaque marshaled BoxConfig.
	Box BoxMap `yaml:"box,omitempty" json:"box,omitempty"`
	// Candy is the generic kind-keyed LAYER map: name → opaque marshaled InlineCandy.
	Candy map[string]json.RawMessage `yaml:"candy,omitempty" json:"candy,omitempty"`
	// Fleet is the flat name-keyed deploy map (the canonical `deploy:` surface).
	Fleet    map[string]FleetNode `yaml:"deploy,omitempty" json:"deploy,omitempty"`
	Provides *ProvidesConfig      `yaml:"provides,omitempty" json:"provides,omitempty"`

	// PluginKinds holds entities of KINDS contributed by plugins (a kind the core has no typed
	// map for). NAME-KEYED: kind word → entity NAME → canonical body (opaque JSON). Built-in
	// kinds decode into their typed maps above. Host-internal — never serialized.
	PluginKinds map[string]map[string]json.RawMessage `yaml:"-" json:"-"`

	// Namespaces holds child namespaces mounted by namespaced `import:` entries (alias →
	// fully-resolved isolated UnifiedFile). NOT authored directly — populated by the
	// materialize pass. Entries are referenced qualified, e.g. `base: cachyos.cachyos`.
	Namespaces map[string]*UnifiedFile `yaml:"-"`

	// RootDir is this UnifiedFile's OWN base directory — the dir its root document's SrcDir
	// names. Set once per materialize, for both the top-level project and each mounted
	// namespace. Empty for a project-less / synthetic UnifiedFile.
	RootDir string `yaml:"-"`
}

// InlineCandy is a candy declared inline in the unified file's `candy:` map. Mutually exclusive
// options: `from:` points at a directory to scan (the loader's CandyScanner seam), OR the inline
// body defines the candy (same fields as the candy manifest, flattened via yaml:",inline").
type InlineCandy struct {
	From      string `yaml:"from,omitempty" json:"from,omitempty"`
	CandyYAML `yaml:",inline"`
	// Manifest carries the discovery manifest filename for a `From:` directory so the candy
	// scan reads the right file. Not YAML-authored; carried through the opaque candy-map fold
	// via JSON, hence exported + json-tagged.
	Manifest string `yaml:"-" json:"__manifest,omitempty"`
}

// DecodeInlineCandy decodes one opaque layer body into the InlineCandy loader shape.
func DecodeInlineCandy(raw json.RawMessage) (*InlineCandy, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var il InlineCandy
	if err := json.Unmarshal(raw, &il); err != nil {
		return nil, false
	}
	return &il, true
}

// EncodeInlineCandy marshals a loader InlineCandy into its opaque body.
func EncodeInlineCandy(il *InlineCandy) json.RawMessage {
	raw, err := json.Marshal(il)
	if err != nil {
		// An InlineCandy always marshals (plain struct + generated spec fields); a failure is a
		// programming error.
		panic("EncodeInlineCandy: " + err.Error())
	}
	return raw
}

// BoxConfig decodes the authored image config for name; ok=false when absent.
func (uf *UnifiedFile) BoxConfig(name string) (BoxConfig, bool) {
	return BoxConfigFrom(uf.Box, name)
}

// HasBox reports whether an image named name is present.
func (uf *UnifiedFile) HasBox(name string) bool { _, ok := uf.Box[name]; return ok }

// BoxNames returns the image names, sorted.
func (uf *UnifiedFile) BoxNames() []string { return BoxNamesOf(uf.Box) }

// SetBox stores an authored image config under name (marshaling it opaque).
func (uf *UnifiedFile) SetBox(name string, b BoxConfig) {
	if uf.Box == nil {
		uf.Box = BoxMap{}
	}
	uf.Box[name] = EncodeBox(b)
}

// SetCandy stores a layer under name (marshaling it opaque).
func (uf *UnifiedFile) SetCandy(name string, il *InlineCandy) {
	if uf.Candy == nil {
		uf.Candy = map[string]json.RawMessage{}
	}
	uf.Candy[name] = EncodeInlineCandy(il)
}

// VM/Pod/Kubernetes/Local/Android are DERIVED accessors over uf.PluginKinds[disc] — the 5
// standalone-substrate-TEMPLATE kinds fold into PluginKinds generically (no per-kind-word
// switch). Each returns the opaque name→body map for its kind (nil when none configured); the
// kernel never decodes the bodies itself — consuming PLUGINS decode a body into the concrete
// kind they need.
func (uf *UnifiedFile) VM() map[string]json.RawMessage         { return uf.PluginKinds["vm"] }
func (uf *UnifiedFile) Pod() map[string]json.RawMessage        { return uf.PluginKinds["pod"] }
func (uf *UnifiedFile) Kubernetes() map[string]json.RawMessage { return uf.PluginKinds["kubernetes"] }
func (uf *UnifiedFile) Local() map[string]json.RawMessage      { return uf.PluginKinds["local"] }
func (uf *UnifiedFile) Android() map[string]json.RawMessage    { return uf.PluginKinds["android"] }

// CheckBeds returns the disposable R10 beds keyed by name. In the unified node-form model a bed
// IS a `disposable: true` fleet, so the bed set is derived directly from the disposable fleets
// in the Fleet map. Members are instruments (brought up alongside a driver), never standalone
// beds. Single enumeration source for `charly check run <bed>` (and the /verify-beds fan-out).
func (uf *UnifiedFile) CheckBeds() map[string]FleetNode {
	if uf == nil {
		return nil
	}
	beds := map[string]FleetNode{}
	for name, node := range uf.Fleet {
		if node.IsDisposable() && node.MemberOf == "" {
			beds[name] = node
		}
	}
	return beds
}

// ProjectConfig returns the *Config equivalent of uf (the box config view).
func (uf *UnifiedFile) ProjectConfig() *Config {
	return uf.projectConfigCached(map[*UnifiedFile]*Config{})
}

// projectConfigCached projects uf (and its import namespaces, recursively) into a *Config.
// The pointer-keyed cache breaks an intentional mutual-import cycle (a shared UnifiedFile node —
// e.g. a namespace two projects both import — is projected exactly once).
func (uf *UnifiedFile) projectConfigCached(cache map[*UnifiedFile]*Config) *Config {
	if c, ok := cache[uf]; ok {
		return c
	}
	images := uf.Box
	if images == nil {
		images = BoxMap{}
	}
	c := &Config{
		Defaults: uf.Defaults,
		Box:      images,
		Local:    uf.Local(),
		Sidecar:  uf.PluginKinds["sidecar"], // opaque bodies; candy/plugin-sidecar resolves them
		Skills:   uf.PluginKinds["skill"],   // opaque bodies; CollectSkills + plugin-marketplace resolve them
	}
	cache[uf] = c // cache BEFORE recursing (cycle break)
	if len(uf.Namespaces) > 0 {
		c.Namespaces = make(map[string]*Config, len(uf.Namespaces))
		for ns, sub := range uf.Namespaces {
			c.Namespaces[ns] = sub.projectConfigCached(cache)
		}
	}
	return c
}

// ProjectTemplates decodes the uf.Local/Kubernetes/Pod/VM/Android raw template maps (map[string]json.RawMessage)
// into the resolved kind-template maps validate/check-include/status read. Returns nil when no template
// kind is present. Recurses into uf.Namespaces, mirroring FillBoxPlans's prefix-accumulation pattern, so a
// namespace-qualified template ref (`local: <ns>.<tmpl>`, `kind:kubernetes` entity `<ns>.<name>`, …) is visible in
// the envelope too. Purely ADDITIVE (qualified keys never collide with a bare name, since a bare name can
// never contain "."), so every existing root-scoped consumer is unaffected.
//
// This is UnifiedFile's own projection (relocated from loaderkit.ProjectTemplates as a METHOD — the free
// function's name collided with the ProjectTemplates TYPE; it is the sibling of ProjectConfig above).
func (uf *UnifiedFile) ProjectTemplates() *ProjectTemplates {
	t := &ProjectTemplates{}
	fillNamespacedTemplates(uf, "", t, map[*UnifiedFile]bool{})
	if t.Local == nil && t.Kubernetes == nil && t.Pod == nil && t.VM == nil && t.Android == nil {
		return nil
	}
	return t
}

// ByKind returns t's namespace-qualified template map for kind (nil for an unknown kind or a nil
// receiver). This is the ONE place ProjectTemplates' named fields fold back to a kind-keyed lookup —
// kept here (spec, the shared vocabulary) rather than in the kernel: a kernel caller that needs "the
// template map for THIS kind" stays genuinely kind-blind by keying on the string it already carries
// instead of switching on it itself (host_build_deploy_entity_resolve.go, W0).
func (t *ProjectTemplates) ByKind(kind string) map[string]RawBody {
	if t == nil {
		return nil
	}
	switch kind {
	case "local":
		return t.Local
	case "kubernetes":
		return t.Kubernetes
	case "pod":
		return t.Pod
	case "vm":
		return t.VM
	case "android":
		return t.Android
	default:
		return nil
	}
}

// fillNamespacedTemplates recursively copies uf's OWN template maps (qualified by prefix) into t, then
// descends into uf.Namespaces with the accumulated prefix. The visited set guards the pointer-keyed
// namespace cache against a self-referential cycle (mirrors FillBoxPlans's own guard).
func fillNamespacedTemplates(uf *UnifiedFile, prefix string, t *ProjectTemplates, visited map[*UnifiedFile]bool) {
	if uf == nil || visited[uf] {
		return
	}
	visited[uf] = true
	// KIND-BLIND copy: the raw template bytes ride into the envelope verbatim as opaque RawBody. The
	// host NEVER decodes them into a concrete <Kind> (that would be per-kind knowledge in the kernel —
	// a boundary-law violation the TestNoConcreteKindInKernel gate catches). The consuming PLUGINS
	// decode a RawBody into the concrete kind they need.
	cp := func(src map[string]json.RawMessage, dst *map[string]RawBody) {
		for name, raw := range src {
			qualified := name
			if prefix != "" {
				qualified = prefix + "." + name
			}
			if *dst == nil {
				*dst = make(map[string]RawBody, len(src))
			}
			(*dst)[qualified] = raw
		}
	}
	cp(uf.Local(), &t.Local)
	cp(uf.Kubernetes(), &t.Kubernetes)
	cp(uf.Pod(), &t.Pod)
	cp(uf.VM(), &t.VM)
	cp(uf.Android(), &t.Android)
	for ns, sub := range uf.Namespaces {
		child := ns
		if prefix != "" {
			child = prefix + "." + ns
		}
		fillNamespacedTemplates(sub, child, t, visited)
	}
}

// ResolvePluginKindViaPlugin projects uf.PluginKinds[kind] (opaque bodies) into *T value
// envelopes via resolve, a per-body plugin OpResolve leg — the ONE shared loop every "resolve a
// plugin-kind catalog through its OpResolve leg" accessor uses (charly-core's Distros/Builders/
// resolveResources/resolveAndroids/resolveInits each supply their own registry-coupled resolve
// callback; this loop itself touches no registry). A bad entry is skipped rather than poisoning
// the whole vocabulary.
func ResolvePluginKindViaPlugin[T any](uf *UnifiedFile, kind string, resolve func(json.RawMessage) (*T, error)) map[string]*T {
	if uf == nil {
		return nil
	}
	bodies := uf.PluginKinds[kind]
	if len(bodies) == 0 {
		return nil
	}
	out := make(map[string]*T, len(bodies))
	for name, body := range bodies {
		v, err := resolve(body)
		if err != nil || v == nil {
			continue
		}
		out[name] = v
	}
	return out
}

// DecodePluginKindMap reconstructs the typed name-keyed map[string]*T for a plugin kind from
// uf.PluginKinds[kind] (each body the canonical T JSON the kind plugin's Invoke produced) via a
// PLAIN json.Unmarshal — no plugin OpResolve round-trip, for a kind whose body is already
// self-contained (Builder). Compare ResolvePluginKindViaPlugin above, its sibling for kinds that
// DO need a plugin-side resolve leg. A bad entry is skipped rather than poisoning the whole
// vocabulary. Returns nil when the kind has no entities.
func DecodePluginKindMap[T any](uf *UnifiedFile, kind string) map[string]*T {
	if uf == nil {
		return nil
	}
	bodies := uf.PluginKinds[kind]
	if len(bodies) == 0 {
		return nil
	}
	out := make(map[string]*T, len(bodies))
	for name, body := range bodies {
		var v T
		if err := json.Unmarshal(body, &v); err != nil {
			continue
		}
		out[name] = &v
	}
	return out
}

// ValidationError accumulates project/candy validation error messages (the loader validation
// accumulator, distinct from the richer Diagnostics — a plain []string collector). Relocated from
// loaderkit (resolve_opts.go) with UnifiedFile so the type-only loader-result consumers share ONE
// definition.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return fmt.Sprintf("validation error: %s", e.Errors[0])
	}
	return fmt.Sprintf("%d validation errors:\n\n  %s", len(e.Errors), strings.Join(e.Errors, "\n  "))
}

// Add adds an error to the collection.
func (e *ValidationError) Add(format string, args ...any) {
	e.Errors = append(e.Errors, fmt.Sprintf(format, args...))
}

// HasErrors returns true if there are any errors.
func (e *ValidationError) HasErrors() bool {
	return len(e.Errors) > 0
}

// CandyCandidate is one fetched materialization of a bare candy ref. The git tag is the fetch
// coordinate; Version is the candy's own per-entity `version:`. Relocated from loaderkit
// (candy_version.go) as loader-result DATA; the PickCandyVersion ARBITER that consumes it stays in
// loaderkit (it needs kit's semver/CalVer comparison — a mechanism).
type CandyCandidate struct {
	Scanned ScannedCandy
	Version string // per-entity version (Scanned.Model.Version) — mandatory, never ""
	GitTag  string // fetch coordinate (the @github :vTAG)
	Source  string // "<repo>@<git-tag>" for warning attribution
}

// EnvProvideEntry is a resolved env_provides entry in charly.yml. Relocated from deploykit
// (provides.go) so UnifiedFile.Provides (*ProvidesConfig) lives entirely in spec; deploykit keeps
// the provides PIPELINE (FilterOwnProvides/ResolveTemplate/…), consuming these spec types.
type EnvProvideEntry struct {
	Name   string `yaml:"name" json:"name"`
	Value  string `yaml:"value" json:"value"`
	Source string `yaml:"source" json:"source"`
}

func (e EnvProvideEntry) GetName() string   { return e.Name }
func (e EnvProvideEntry) GetSource() string { return e.Source }

// ProvidesConfig holds all resolved provides entries in charly.yml.
type ProvidesConfig struct {
	Env []EnvProvideEntry `yaml:"env,omitempty" json:"env,omitempty"`
	MCP []MCPProvideEntry `yaml:"mcp,omitempty" json:"mcp,omitempty"`
}
