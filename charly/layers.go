package main

import (
	"errors"
	"fmt"
	"github.com/opencharly/sdk/spec"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/opencharly/sdk/kit"
	"gopkg.in/yaml.v3"
)

// PortSpec shorthand (scalar `8080` / `"tcp:5900"`) is canonicalized to the
// {port, protocol} struct form by the CUE loader's normalizer (cue_normalize.go,
// expandPortSpecNode); the custom UnmarshalYAML was deleted in the CUE loader
// switch (Cutover 1).

// ShellAllowlist enumerates valid per-shell sub-block keys inside `shell:`.
// Adding a new shell here is a renderer change (new managed-block / drop-in
// destination); keep in sync with deploy_host_helpers.go shell-detection
// probe and the shell-snippet destination table (deploykit.CompileShellSnippetSteps).

// sortedEnvDeps returns a deterministic slice from a name-keyed map, sorted by Name.
func sortedEnvDeps(m map[string]spec.EnvDependency) []spec.EnvDependency {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]spec.EnvDependency, 0, len(m))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// candyYAMLKnownFields lists non-format top-level keys in the candy manifest.
// Unknown keys are routed to FormatSections (if matching an embedded distro format)
// or TagSections (otherwise).
//
// `directory`, `info` deleted in the 2026-05 Calamares cutover (0 YAML files
// used either; `description:` carries the metadata `info:` previously held).
// `depends` renamed to `requires`. Calamares-shaped `packages` + `distros`
// added as the unified package surface; per-format `rpm:`/`deb:`/`pac:`/
// `aur:` and per-distro tag sections (debian:13: etc.) collapse into them
// via `charly migrate`.
var candyYAMLKnownFields = map[string]bool{
	"description": true, "version": true, "status": true,
	"name": true, "from": true,
	"candy": true, "require": true, "engine": true, "env": true,
	"path_append": true, "port": true, "route": true, "service": true,
	"volume": true, "alias": true, "extract": true, "security": true,
	"libvirt": true, "hook": true,
	"port_relay": true, "secret": true, "data": true,
	"env_provide": true, "env_require": true, "env_accept": true,
	"secret_accept": true, "secret_require": true,
	"mcp_provide": true, "mcp_require": true, "mcp_accept": true,
	"var": true, "plan": true,
	"plugin":     true,
	"artifact":   true,
	"capability": true, "requires_capability": true,
	"package": true, "distro": true,
	"apk":      true,
	"shell":    true,
	"localpkg": true, "reboot": true,
}

// The build vocabulary — the set of distro names and package-format names — is
// NOT hardcoded in Go. It is DERIVED at load time from the embedded build
// vocabulary (plus any project build.yml override) — the `distro:` section (the
// DistroConfig) — by RegisterBuildVocabulary, which every entry point calls
// before scanning candies. Adding a new distro or package format is therefore
// purely an embedded-vocabulary (or project-override) edit, with no code change.
//
// These caches are consumed ONLY by the candy-manifest shape guard
// (looksLikeDistroOrFormatKey / rejectLegacyCandyKeys) to recognize a
// package-format or per-distro section mistakenly placed at the candy root. The
// FORWARD package parser (derivePackageSectionsFromCalamares) needs no
// vocabulary at all — it routes every `distro:` sub-key structurally and lets
// the cascade resolver match on the image's real img.Distro/img.Pkg.
var (
	// candyYAMLFormatNames = the union of every distro's declared package
	// formats (rpm/deb/pac/aur/…), inherited chains resolved.
	candyYAMLFormatNames map[string]bool
	// candyYAMLDistroNames = every distro name declared in the embedded build vocabulary.
	candyYAMLDistroNames map[string]bool
)

// RegisterBuildVocabulary derives the distro/format vocabulary from a
// DistroConfig and caches it for the duration of the process. Sourced entirely
// from the embedded build vocabulary (plus any project build.yml override),
// never from a Go constant. Safe to call repeatedly; a nil
// config clears the caches (the shape guard then fails open — no false
// positives).
func RegisterBuildVocabulary(dc *DistroConfig) {
	candyYAMLFormatNames = make(map[string]bool)
	candyYAMLDistroNames = make(map[string]bool)
	if dc == nil {
		return
	}
	for _, name := range dc.AllFormatNames() {
		candyYAMLFormatNames[name] = true
	}
	for name := range dc.Distro {
		candyYAMLDistroNames[name] = true
	}
}

// derivePackageSectionsFromCalamares is the SOLE populator of a candy's package
// surface from the Calamares-aligned top-level `package:` + `distro:` map. Every
// `distro:` key — bare (`debian`), versioned (`debian-13`), or compound
// (`debian,ubuntu` / `debian-13,ubuntu-24.04`) — routes to one or more per-distro
// TAG sections keyed in colon form (`debian`, `debian:13`). NO distro key ever
// feeds a shared FORMAT section: that collapse (debian + ubuntu both → the one
// `deb` format section) is exactly what made repo selection non-deterministic
// (first-writer-wins over Go's randomized map order) and unioned divergent
// package lists across distros. With per-distro tag sections, each distro owns
// its own section, so two distros that share a package format can never race.
//
// The top-level `package:` list is the always-included BASE; it is recorded on
// layer.topPackages and folded at RESOLVE time (compileSystemPackageSteps), NOT
// here — folding it into every section at parse time is what cross-contaminated
// debian/ubuntu before this cutover.
//
// An `aur:` sub-block (under ANY key — `arch:`, `cachyos:`, or the `pac:`
// family) routes to its own dedicated `aur` FORMAT section: aur is a real
// secondary BUILD format the aur builder consumes, not a distro tag. It is only
// ever built when the image's config-derived build formats include `aur`.
//
// Parsing is purely STRUCTURAL — NO Go-side distro/format vocabulary is
// consulted. A package-format family key (`pac:`/`deb:`/`rpm:`) routes to a tag
// section matched at resolve time via the image's img.Pkg; a bare/versioned
// distro key matches via img.Distro. Correctness of a key is the validator's
// job; selection is the cascade resolver's.
//
// Distro keys are iterated in sorted order so a pathological double-definition of
// the same tag (e.g. a `debian,ubuntu` compound plus a standalone `debian`)
// resolves deterministically; the validator rejects genuinely conflicting extras.
//
// Mapping:
//
//	distro.fedora.*          → tagSections["fedora"]
//	distro.debian.*          → tagSections["debian"]
//	distro.pac.*             → tagSections["pac"]   (family key, matched via img.Pkg)
//	distro.arch.*            → tagSections["arch"]   (+ any .aur.* → formatSections["aur"])
//	distro.debian-13.*       → tagSections["debian:13"]   (dash → colon)
//	distro."debian,ubuntu".* → tagSections["debian"] + tagSections["ubuntu"]
func derivePackageSectionsFromCalamares(layer *Candy, ly *spec.CandyYAML) {
	layer.topPackages = PackageNames(ly.Package)

	ensureTag := func(tagKey string) *TagPkgConfig {
		if layer.tagSections == nil {
			layer.tagSections = map[string]*TagPkgConfig{}
		}
		cfg := layer.tagSections[tagKey]
		if cfg == nil {
			cfg = &TagPkgConfig{Raw: map[string]any{}}
			layer.tagSections[tagKey] = cfg
		}
		if cfg.Raw == nil {
			cfg.Raw = map[string]any{}
		}
		return cfg
	}
	ensureFormat := func(fmtName string) *PackageSection {
		if layer.formatSections == nil {
			layer.formatSections = map[string]*PackageSection{}
		}
		ps := layer.formatSections[fmtName]
		if ps == nil {
			ps = &PackageSection{FormatName: fmtName, Raw: map[string]any{}}
			layer.formatSections[fmtName] = ps
		}
		if ps.Raw == nil {
			ps.Raw = map[string]any{}
		}
		return ps
	}
	// addPackages unions pkgs into *dst (dedup, first-seen order).
	addPackages := func(dst *[]string, pkgs []string) {
		seen := map[string]bool{}
		for _, p := range *dst {
			seen[p] = true
		}
		for _, p := range pkgs {
			if !seen[p] {
				*dst = append(*dst, p)
				seen[p] = true
			}
		}
	}
	// setRaw records a non-nil extra (repo/copr/options/exclude/module) into a
	// section's Raw. Within ONE distro level it's a plain assign; cross-level
	// most-specific-wins is the resolver's job (compileSystemPackageSteps).
	//
	// A nil slice wrapped in `any` is `!= nil` under Go interface rules (the
	// interface carries a non-nil type), so a bare `val != nil` would let a nil
	// `dp.Copr` through and record `copr: any(nil []string)` — which JSON-marshals
	// as `null`. The K4-B deploy-compile slice re-hydrates the candy from the
	// resolved-project envelope (a JSON round-trip), where that `null` comes back
	// as a bare `nil` interface that the cascade resolver then EXCLUDES — so the
	// live and re-hydrated paths would disagree on whether the entry is present.
	// Treating an interface-wrapped nil slice as absent normalizes the opaque
	// raw-install-context carry-through across the plugin boundary (the entry is
	// semantically empty either way; the step's typed Copr/Exclude/Module fields
	// are extracted separately and are unaffected).
	setRaw := func(raw map[string]any, key string, val any) {
		if val == nil {
			return
		}
		if rv := reflect.ValueOf(val); rv.Kind() == reflect.Slice && rv.IsNil() {
			return
		}
		raw[key] = val
	}

	// Sorted iteration → deterministic regardless of Go map order.
	distroKeys := make([]string, 0, len(ly.Distro))
	for k := range ly.Distro {
		distroKeys = append(distroKeys, k)
	}
	sortStrings(distroKeys)

	for _, distroKey := range distroKeys {
		dp := ly.Distro[distroKey]
		if dp == nil {
			continue
		}
		// Split compound keys (`debian,ubuntu` / `debian-13,ubuntu-24.04`); each
		// part becomes its own tag section carrying this entry's shared content.
		for part := range strings.SplitSeq(distroKey, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			// Canonicalize: bare `debian` stays `debian`; versioned `debian-13`
			// → colon-form tag key `debian:13` (matches img.Distro tags).
			tagKey := part
			if i := strings.IndexByte(part, '-'); i > 0 {
				tagKey = part[:i] + ":" + part[i+1:]
			}
			// Every key under `distro:` is, by the author's placement, a distro
			// or package-format tag — so parsing is purely STRUCTURAL: each key
			// becomes a tag section, with NO Go-side vocabulary list consulted.
			// Correctness (is this a real distro/format?) is the validator's job,
			// and SELECTION (which sections apply to THIS image) is the cascade
			// resolver's, keyed on the image's real img.Distro + img.Pkg — e.g. a
			// `pac:` family key is matched via img.Pkg, a bare `debian:` via
			// img.Distro, a typo matches nothing and contributes nothing.
			cfg := ensureTag(tagKey)
			addPackages(&cfg.Package, PackageNames(dp.Package))
			cfg.Raw["package"] = cfg.Package
			setRaw(cfg.Raw, "repo", dp.Repo)
			setRaw(cfg.Raw, "copr", dp.Copr)
			setRaw(cfg.Raw, "options", dp.Options)
			setRaw(cfg.Raw, "exclude", dp.Exclude)
			setRaw(cfg.Raw, "module", dp.Module)
			// A secondary build sub-block (an `aur:` block) routes to its own
			// image-global FORMAT section, regardless of which distro/family key
			// it sits under — pure structure, no `bare == "arch"` hardcode. The
			// aur section is only ever BUILT when the image's config-derived
			// build formats include `aur` (img.BuildFormats, from the DistroConfig
			// — so a non-pac image silently ignores a stray aur block); the
			// validator flags an aur block placed under a non-pac distro.
			if dp.AUR != nil {
				aurPS := ensureFormat("aur")
				addPackages(&aurPS.Packages, PackageNames(dp.AUR.Package))
				aurPS.Raw["package"] = aurPS.Packages
				setRaw(aurPS.Raw, "options", dp.AUR.Options)
				setRaw(aurPS.Raw, "replaces", dp.AUR.Replaces)
			}
		}
	}
}

// Format-specific structs (RpmConfig, DebConfig, PacConfig, AurConfig) removed.
// All format sections are now parsed dynamically as PackageSection via the embedded distro format names.
// See PackageSection type and CandyYAML.UnmarshalYAML for the generic parsing.

// Candy represents a candy directory and its contents
type Candy struct {
	Name        string
	Path        string // directory containing the candy manifest
	SourceDir   string // anchor for relative file lookups (run: copy/download, data.src, install files); defaults to Path, overridden by the candy manifest's `directory:`
	Version     string // CalVer version from the candy manifest
	Description string // plain-string self-description; first line = summary
	Status      string // maturity rung: working | testing | broken (default testing)
	// Plugin is the candy's `plugin:` block (nil = an ordinary candy). Its presence
	// makes the candy a PLUGIN — it provides reserved-word Providers (built-in or
	// out-of-tree). See provider.go / validatePluginCandy.
	Plugin *spec.Plugin
	Info   string // the description's first line — summary shown in listings
	// Parse-time filesystem-probe caches: each caches a single fileExists /
	// dirExists check against SourceDir performed once at scan time. These stay
	// fields (a remote candy's cache dir may be evicted before they're read,
	// and re-probing on every access would re-do I/O) — they are NOT redundant.
	// Every OTHER Has* predicate (HasEnv/HasPorts/HasVolumes/HasData/…) is a
	// derived method computed from the populated field, not a stored bool.
	HasPixiToml       bool
	HasPyprojectToml  bool
	HasEnvironmentYml bool
	HasPackageJson    bool
	HasCargoToml      bool
	HasSrcDir         bool
	HasPixiLock       bool // the candy manifest has a non-empty tasks: list

	// Init system detection (populated by PopulateCandyInitSystem)
	InitSystems    map[string]bool // set of init system names this candy triggers
	PortRelayPorts []int           // port_relay: field (init-agnostic)

	// Candy references. Each CandyRef carries the original ref string; the bare
	// map-key form (.Bare()) and pinned version (.Version()) are derived. One
	// list per concern — no parallel bare/raw arrays (the duplication that
	// split version off the ref and enabled the silent version-collision bug).
	Require       []CandyRef // require: deps (ordering + resolution)
	IncludedCandy []CandyRef // candy: composition refs (splicing)
	BakePlugin    []CandyRef // bake_plugin: out-of-tree plugin candies whose pre-built provider binary is baked into composing images (generate.go emitBakedPlugins)

	// Remote candy metadata
	Remote        bool   // true if from a remote repo
	RepoPath      string // e.g. "github.com/opencharly/charly" (empty for local)
	SubPathPrefix string // e.g. "candy/" — parent directory within the repo for sibling resolution

	// Pre-populated from the candy manifest
	formatSections  map[string]*PackageSection // generic format sections (only `aur` now — the secondary AUR build format)
	tagSections     map[string]*TagPkgConfig   // per-distro/version package sections (debian, ubuntu, debian:13, …) — the sole package surface
	topPackages     []string                   // top-level package: — the always-included BASE, folded at RESOLVE time (never at parse — that cross-contaminated debian/ubuntu)
	ports           []string
	portSpecs       []spec.PortSpec // full PortSpec data with protocol info
	envConfig       *EnvConfig
	route           *RouteConfig
	serviceFiles    []string            // paths to *.service files in candy dir (systemd user-level, file_copy model)
	service         []spec.ServiceEntry // unified service: list (the only service schema)
	volumes         []VolumeYAML
	aliases         []AliasYAML
	extract         []ExtractYAML
	data            []DataYAML
	security        *SecurityConfig
	libvirt         []string
	hooks           *HooksConfig
	secrets         []SecretYAML
	envProvides     map[string]string    // env vars provided to other containers (service discovery)
	envRequires     []spec.EnvDependency // env vars this candy must have
	envAccepts      []spec.EnvDependency // env vars this candy can optionally use
	secretAccepts   []spec.EnvDependency // credential-store-backed env vars this candy can optionally use
	secretRequires  []spec.EnvDependency // credential-store-backed env vars this candy must have
	mcpProvides     []spec.MCPServerYAML // MCP servers provided to other containers
	mcpRequires     []spec.EnvDependency // MCP servers this candy must have
	mcpAccepts      []spec.EnvDependency // MCP servers this candy can optionally use
	engine          string               // required run engine from the candy manifest ("docker", "podman", or "")
	vars            map[string]string    // candy-local variables (from the candy manifest vars:)
	apk             []ApkPackageSpec     // Android apps to install on a kind:android device (from the candy manifest apk:)
	localpkg        map[string]string    // per-format native-package source dirs (pac/rpm/deb → dir) from the candy manifest localpkg:
	reboot          bool                 // reboot the deploy target after this candy (from the candy manifest reboot:)
	ExternalBuilder string               // reserved word of an EXTERNAL builder plugin this candy selects (from the candy manifest external_builder:); resolved at build via OpResolve — see deploykit EmitExternalBuilderStages
	plan            []spec.Step          // unified ordered plan (from the candy manifest plan:): run:/check:/agent-*/include:
	artifacts       []CandyArtifact      // files to retrieve after setup (from the candy manifest artifacts:)
	shell           *spec.Shell          // shell-init declarations (from the candy manifest shell:)

	// Candy-contributed image-level facts (capabilities: block in the candy manifest)
	// and cross-candy requirement declarations (requires_capabilities:).
	capabilities         *CandyCapabilities
	requiresCapabilities []string
}

// ScanCandy returns all candies for the project at dir. Post-unified-cutover
// this loads charly.yml via LoadUnified, applies discover:, and projects
// the candies map. Legacy `candy/` directory scan remains as a fallback when
// charly.yml is absent (e.g., transitional test fixtures).
// DefaultCandyDir is the single source of truth for the on-disk directory that
// holds candy definitions. The discover: block overrides it per project
// for discovery; write/resolve paths fall back to this default. Renaming the
// candy directory project-wide is a one-line change here.
// The value lives in kit (the importable host-engine shared with out-of-tree
// plugin candies); these are the in-core aliases.
const DefaultCandyDir = kit.DefaultCandyDir

// DefaultBoxDir is the on-disk directory that holds box definitions,
// discovered per-box as <DefaultBoxDir>/<name>/<UnifiedFileName>. Symmetric with
// DefaultCandyDir; the discover: block overrides it per project.
const DefaultBoxDir = kit.DefaultBoxDir

// The per-directory discovery manifest filename is the ONE filename the code
// knows — UnifiedFileName ("charly.yml", defined in unified.go). There is no
// separate manifest constant: a project's root file, every discovered box, and
// every discovered candy all use the single charly.yml name. Each `discover[]`
// spec may still override it via `manifest:` in charly.yml.

func ScanCandy(dir string) (map[string]*Candy, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if present {
		if err := uf.ApplyDiscover(dir); err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}
		return uf.ProjectCandies(dir)
	}
	return legacyScanCandiesDir(dir)
}

// legacyScanCandiesDir is the pre-unified filesystem walk. Kept for test
// fixtures (and the migration tool) that don't yet have an charly.yml.
func legacyScanCandiesDir(dir string) (map[string]*Candy, error) {
	candiesDir := filepath.Join(dir, DefaultCandyDir)
	entries, err := os.ReadDir(candiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*Candy), nil
		}
		return nil, fmt.Errorf("reading candy directory: %w", err)
	}
	layers := make(map[string]*Candy)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		layer, err := scanCandy(filepath.Join(candiesDir, name), name, UnifiedFileName)
		if err != nil {
			return nil, fmt.Errorf("scanning candy %s: %w", name, err)
		}
		layers[name] = layer
	}
	return layers, nil
}

// parseCandyYAML reads and unmarshals a candy manifest file. Strict schema:
//   - Empty / comment-only file → zero-value CandyYAML.
//   - Single top-level `candy:` key → decode its body as CandyYAML (canonical form).
//   - `candy:` + other top-level keys → error (ambiguous shape).
//   - Multi-document stream → error (the candy manifest is not a bundle file).
//   - Flat form (no `candy:` wrapper) → error with migration hint.
func parseCandyYAML(path string) (*spec.CandyYAML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Empty / comment-only guard.
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return &spec.CandyYAML{}, nil
	}

	// Parse the stream down to its single top-level mapping node (or nil for an
	// all-comment/null file → zero-value CandyYAML).
	inner, err := singleCandyMappingNode(path, data)
	if err != nil {
		return nil, err
	}
	if inner == nil {
		return &spec.CandyYAML{}, nil
	}

	// Unified node-form: a single name-first node `<name>: {candy: …, <children>}`.
	// (The `candy` discriminator is NESTED under the node name, so the kind-keyed
	// branch below — which looks for a TOP-LEVEL `candy:` key — won't match.)
	if len(inner.Content) == 2 && !kindWordSet[inner.Content[0].Value] {
		// The ONE node-form parse is the registered config front-end (P6, sdk/loaderkit); the
		// candy genericNode buildCandy consumes is reconstructed from the parsed node.
		if _, pp, perr := requireLoaderParser().ParseDoc(inner, loaderThreaded()); perr == nil && len(pp.Nodes) == 1 && pp.Nodes[0].Disc == "candy" {
			gn, gerr := parsedNodeToGeneric(pp.Nodes[0])
			if gerr != nil {
				return nil, fmt.Errorf("%s: %w", path, gerr)
			}
			_, ic, berr := buildCandy(gn)
			if berr != nil {
				return nil, fmt.Errorf("%s: %w", path, berr)
			}
			return &ic.CandyYAML, nil
		}
	}

	// Collect top-level keys.
	var keys []string
	var candyIdx = -1
	for i := 0; i < len(inner.Content); i += 2 {
		k := inner.Content[i].Value
		keys = append(keys, k)
		if k == "candy" {
			candyIdx = i + 1
		}
	}

	if candyIdx >= 0 {
		// Canonical kind-keyed form — `candy:` must be the only top-level key.
		if len(keys) != 1 {
			var other []string
			for _, k := range keys {
				if k != "candy" {
					other = append(other, k)
				}
			}
			return nil, fmt.Errorf("%s: ambiguous — `candy:` wrapper present AND other top-level keys %v (pick one form)", path, other)
		}
		// 2026-05 Calamares cutover: hard-fail on legacy field shapes.
		// Every legacy form has a one-shot remediation via `charly migrate`.
		body := inner.Content[candyIdx]
		if body != nil && body.Kind == yaml.MappingNode {
			if err := rejectLegacyCandyKeys(path, body); err != nil {
				return nil, err
			}
			// Load-time top-level typo-detection (CUE-decode is lenient and would
			// silently drop a plural/singular typo; full closed-schema validation
			// is `charly box validate`'s job).
			if err := rejectUnknownCandyTopLevelKeys(path, body); err != nil {
				return nil, err
			}
		}
		// Load is decode-only (fast, runs on every invocation). Full closed-schema
		// CUE validation (unknown-key rejection + value constraints like the CalVer
		// regex/enums) runs at `charly box validate` (validateCandyManifestCUE) on
		// the AUTHORED form — not at load, where it would reject minimal in-tree
		// fixtures and slow the hot path. See cue-loader-switch-design.
		var ly spec.CandyYAML
		if err := decodeEntityViaCUE(body, reflect.TypeOf(spec.CandyYAML{}), &ly, path); err != nil {
			return nil, err
		}
		return &ly, nil
	}

	// Neither node-form nor the `candy:` kind-keyed form — an unrecognized manifest.
	return nil, fmt.Errorf("%s: unrecognized candy manifest shape — expected node-form `<name>: {candy: …}` (or the `candy:` kind-keyed form)", path)
}

// singleCandyMappingNode parses a candy manifest's bytes as a YAML multi-document
// stream and returns the single top-level mapping node (DocumentNode unwrapped). It
// returns (nil, nil) when the stream holds no non-empty document (an all-comment /
// null file → zero-value CandyYAML), and errors on a multi-document stream or a
// non-mapping top level.
func singleCandyMappingNode(path string, data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	var docs []yaml.Node
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		// Skip empty (null-valued) docs.
		if node.Kind == 0 || (node.Kind == yaml.DocumentNode && (len(node.Content) == 0 || (len(node.Content) == 1 && node.Content[0].Tag == "!!null"))) {
			continue
		}
		docs = append(docs, node)
	}
	if len(docs) == 0 {
		return nil, nil
	}
	if len(docs) > 1 {
		return nil, fmt.Errorf("%s: the candy manifest is not a multi-document stream; bundle files belong in the unified charly.yml", path)
	}
	// Unwrap the DocumentNode wrapper.
	inner := &docs[0]
	if inner.Kind == yaml.DocumentNode && len(inner.Content) > 0 {
		inner = inner.Content[0]
	}
	if inner.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: top level must be a mapping (got kind=%v)", path, inner.Kind)
	}
	return inner, nil
}

// rejectLegacyCandyKeys is the candy-manifest shape guard: a removed field name
// (`depends`/`directory`/`info`) or a misplaced package-format / per-distro
// section at the candy root produces a clear error describing the current
// schema. Runs before standard YAML decoding so the user sees a precise message,
// not a generic "field not found". The format/distro vocabulary it recognizes is
// the DYNAMIC build vocabulary sourced from the embedded build vocabulary (RegisterBuildVocabulary) —
// no hardcoded format/distro list, so a newly-added format or distro is caught
// automatically.
// rejectUnknownCandyTopLevelKeys hard-errors on an unknown top-level candy key
// (a plural/singular typo). This is the load-time typo-detection the deleted
// CandyYAML.UnmarshalYAML used to do — CUE-decode is lenient and would silently
// drop the key. Comprehensive closed-schema validation is `charly box validate`.
func rejectUnknownCandyTopLevelKeys(path string, body *yaml.Node) error {
	if body == nil || body.Kind != yaml.MappingNode {
		return nil
	}
	var unknown []string
	for i := 0; i+1 < len(body.Content); i += 2 {
		key := body.Content[i].Value
		if candyYAMLKnownFields[key] {
			continue
		}
		unknown = append(unknown, key)
	}
	if len(unknown) > 0 {
		return fmt.Errorf("%s: candy has unknown top-level key(s) %v — almost always a plural/singular typo: use the SINGULAR form (task: not tasks:, var: not vars:, candy: not layers:, env_provide: not env_provides:); a package format (rpm:/deb:/pac:/aur:) nests under the `distro:` map, never at the candy root", path, unknown)
	}
	return nil
}

func rejectLegacyCandyKeys(path string, body *yaml.Node) error {
	for i := 0; i+1 < len(body.Content); i += 2 {
		key := body.Content[i].Value
		switch key {
		case "depends":
			return fmt.Errorf("%s: candy manifest uses the removed `depends:` field — rename it to `require:`", path)
		case "directory":
			return fmt.Errorf("%s: candy manifest uses the removed `directory:` field — the candy directory is implicit", path)
		case "info":
			return fmt.Errorf("%s: candy manifest uses the removed `info:` field — use `description:`", path)
		}
		// A package-format family key (pac:/deb:/rpm:/aur:) or a per-distro tag
		// section (`debian:`, `debian:13:`, `debian,ubuntu:`) at the candy ROOT
		// belongs UNDER the `distro:` map. Both vocabularies come from the embedded build vocabulary.
		if looksLikeDistroOrFormatKey(key) {
			return fmt.Errorf("%s: candy manifest places `%s:` at the top level — package-format and per-distro sections nest under the `distro:` map (e.g. `distro:\n  %s:\n    package: [...]`)", path, key, key)
		}
	}
	return nil
}

// looksLikeDistroOrFormatKey reports whether a candy-manifest top-level key is a
// package-format family name (pac/deb/rpm/aur) or a per-distro tag section
// (`debian`, `debian:13`, `debian,ubuntu`) — shapes that nest under the `distro:`
// map, never at the candy root. The vocabulary is the dynamic build vocabulary
// registered by RegisterBuildVocabulary from the embedded build vocabulary; this
// helper holds no
// hardcoded distro/format list. Returns false when the vocabulary is unregistered
// (no false positives), leaving the explicit removed-field cases to fire.
func looksLikeDistroOrFormatKey(key string) bool {
	if key == "" {
		return false
	}
	if candyYAMLFormatNames[key] {
		return true
	}
	for part := range strings.SplitSeq(key, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return false
		}
		bare := part
		if before, _, ok := strings.Cut(part, ":"); ok {
			bare = before
		}
		if !candyYAMLDistroNames[bare] {
			return false
		}
	}
	return true
}

// scanCandy scans a single candy directory
func scanCandy(path, name, manifest string) (*Candy, error) {
	layer := &Candy{
		Name: name,
		Path: path,
	}

	// Parse the candy manifest FIRST so `directory:` can redirect the anchor
	// used by install-file detection and service-file globbing below.
	var ly *spec.CandyYAML
	if manifest == "" {
		manifest = UnifiedFileName
	}
	yamlPath := filepath.Join(path, manifest)
	if fileExists(yamlPath) {
		parsed, err := parseCandyYAML(yamlPath)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", manifest, err)
		}
		ly = parsed
	}

	// SourceDir always equals layer Path (the `directory:` field was deleted
	// in the 2026-05 Calamares cutover; 0 candies used it).
	_ = ly
	layer.SourceDir = path

	// Check for install files (anchored at SourceDir — honors `directory:`)
	layer.HasPixiToml = fileExists(filepath.Join(layer.SourceDir, "pixi.toml"))
	layer.HasPyprojectToml = fileExists(filepath.Join(layer.SourceDir, "pyproject.toml"))
	layer.HasEnvironmentYml = fileExists(filepath.Join(layer.SourceDir, "environment.yml"))
	layer.HasPackageJson = fileExists(filepath.Join(layer.SourceDir, "package.json"))
	layer.HasCargoToml = fileExists(filepath.Join(layer.SourceDir, "Cargo.toml"))
	layer.HasSrcDir = dirExists(filepath.Join(layer.SourceDir, "src"))
	layer.HasPixiLock = fileExists(filepath.Join(layer.SourceDir, "pixi.lock"))

	// Scan for systemd service files (init system detection happens in PopulateCandyInitSystem)
	svcFiles, _ := filepath.Glob(filepath.Join(layer.SourceDir, "*.service"))
	if len(svcFiles) > 0 {
		layer.serviceFiles = svcFiles
	}

	if ly != nil {
		// Single shared post-parse populator (see unified.go). Both this
		// discovered-candy path and the charly.yml inline path call it, so
		// they can never drift.
		populateCandyFromYAML(layer, ly)
	}

	return layer, nil
}

// HasInstallFiles returns true if the candy has at least one install file
func (l *Candy) HasInstallFiles() bool {
	return l.HasFormatPackages() || l.HasTagPackages() || len(l.topPackages) > 0 ||
		l.HasPixiToml || l.HasPyprojectToml || l.HasEnvironmentYml ||
		l.HasPackageJson || l.HasCargoToml ||
		l.HasTasks() || l.HasApk()
}

// HasTagPackages reports whether any per-distro/version tag section declares
// packages (the per-distro package surface that bare/versioned `distro:` keys
// produce).
func (l *Candy) HasTagPackages() bool {
	for _, s := range l.tagSections {
		if len(s.Package) > 0 {
			return true
		}
	}
	return false
}

// HasAnyPackages reports whether the candy declares ANY OS packages across all
// surfaces: per-distro/version tag sections, the always-included top-level
// package: base, or a format section (aur). Used where the question is "does
// this candy install packages at all?" (e.g. the use_packaged service check) —
// must not be narrowed to format sections, which now hold only aur.
func (l *Candy) HasAnyPackages() bool {
	return l.HasTagPackages() || len(l.topPackages) > 0 || l.HasFormatPackages()
}

// HasContent returns true if the candy has install files or any configuration
// that contributes to the Containerfile (env, ports, volumes, etc.)
func (l *Candy) HasContent() bool {
	return l.HasInstallFiles() || l.HasEnv() || l.HasPorts() || l.HasRoute() ||
		l.HasVolumes() || l.HasAliases() || l.HasExtract() || l.HasData() || l.HasLibvirt() ||
		l.HasAnyInit() || len(l.PortRelayPorts) > 0 ||
		len(l.serviceFiles) > 0 || len(l.service) > 0
}

// Derived Has* predicates. Each is computed from the populated field — there is
// no stored boolean (the former Has* fields were a denormalized cache that had
// to be kept in lockstep with the field at every parse site). The filesystem
// install-file probes (HasPixiToml/HasSrcDir/…) stay as cached fields; see the
// Candy struct.
func (l *Candy) HasEnv() bool     { return l.envConfig != nil }
func (l *Candy) HasPorts() bool   { return len(l.portSpecs) > 0 }
func (l *Candy) HasRoute() bool   { return l.route != nil }
func (l *Candy) HasVolumes() bool { return len(l.volumes) > 0 }
func (l *Candy) HasAliases() bool { return len(l.aliases) > 0 }
func (l *Candy) HasExtract() bool { return len(l.extract) > 0 }
func (l *Candy) HasData() bool    { return len(l.data) > 0 }
func (l *Candy) HasLibvirt() bool { return len(l.libvirt) > 0 }
func (l *Candy) HasTasks() bool   { return len(l.runOps()) > 0 }
func (l *Candy) HasApk() bool     { return len(l.apk) > 0 }

// runOps returns the Ops from the candy's plan: `run:` steps that form the
// install timeline (build/deploy context). A runtime-only run: step is
// plan-runtime provisioning the check Runner executes, not the build, so it
// is excluded. check:/agent-*/include: steps are never install ops.
func (l *Candy) runOps() []spec.Op {
	var out []spec.Op
	for i := range l.plan {
		step := &l.plan[i]
		kw, err := step.StepKind()
		if err != nil || kw != kit.KwRun {
			continue
		}
		op := step.Op
		if opInContext(&op, CtxRuntime) && !opInContext(&op, CtxBuild) && !opInContext(&op, CtxDeploy) {
			continue
		}
		out = append(out, op)
	}
	return out
}

// Apk returns the candy's Android app-install entries (the `apk:` package
// format). Empty for non-Android candies.
func (l *Candy) Apk() []ApkPackageSpec { return l.apk }

// LocalPkg returns the candy's native-package SOURCE dir for the given package
// FORMAT (pac/rpm/deb), or "" when the candy declares none for that format. See
// LocalPkgInstallStep.
func (l *Candy) LocalPkg(format string) string { return l.localpkg[format] }

// LocalPkgFormats returns the package formats (pac/rpm/deb) for which the candy
// declares a native-package source, sorted for deterministic iteration.
func (l *Candy) LocalPkgFormats() []string {
	out := make([]string, 0, len(l.localpkg))
	for f := range l.localpkg {
		out = append(out, f)
	}
	sortStrings(out)
	return out
}

// LocalPkgMap maps a package format (pac/rpm/deb) to a native-package source
// dir. Its UnmarshalYAML accepts only the per-format mapping shape.
type LocalPkgMap map[string]string

// UnmarshalYAML accepts only the per-format mapping shape; a bare scalar
// (e.g. `localpkg: pkg/arch`) is a hard authoring error.
func (m *LocalPkgMap) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return fmt.Errorf("localpkg: bare scalar form %q rejected; localpkg is a per-format map (e.g. {pac: pkg/arch, rpm: pkg/fedora, deb: pkg/debian})", value.Value)
	}
	var mm map[string]string
	if err := value.Decode(&mm); err != nil {
		return err
	}
	*m = mm
	return nil
}
func (l *Candy) HasEnvProvides() bool    { return len(l.envProvides) > 0 }
func (l *Candy) HasEnvRequires() bool    { return len(l.envRequires) > 0 }
func (l *Candy) HasEnvAccepts() bool     { return len(l.envAccepts) > 0 }
func (l *Candy) HasSecretRequires() bool { return len(l.secretRequires) > 0 }
func (l *Candy) HasSecretAccepts() bool  { return len(l.secretAccepts) > 0 }
func (l *Candy) HasMCPProvides() bool    { return len(l.mcpProvides) > 0 }
func (l *Candy) HasMCPRequires() bool    { return len(l.mcpRequires) > 0 }
func (l *Candy) HasMCPAccepts() bool     { return len(l.mcpAccepts) > 0 }

// PixiManifest returns the filename of the pixi manifest if it exists
func (l *Candy) PixiManifest() string {
	if l.HasPixiToml {
		return "pixi.toml"
	}
	if l.HasPyprojectToml {
		return "pyproject.toml"
	}
	if l.HasEnvironmentYml {
		return "environment.yml"
	}
	return ""
}

// FormatSection returns the generic package section for a format, or nil.
func (l *Candy) FormatSection(name string) *PackageSection {
	if l.formatSections == nil {
		return nil
	}
	return l.formatSections[name]
}

// HasFormatPackages returns true if any format section has packages.
func (l *Candy) HasFormatPackages() bool {
	for _, s := range l.formatSections {
		if len(s.Packages) > 0 {
			return true
		}
	}
	return false
}

// TagSection returns the tag-based package config for the given tag, or nil.
func (l *Candy) TagSection(tag string) *TagPkgConfig {
	if l.tagSections == nil {
		return nil
	}
	return l.tagSections[tag]
}

// TopPackages returns the candy's top-level package: list — the always-included
// BASE that the cascade resolver folds in for EVERY resolved format, regardless
// of which distro/version tag sections match. Folding it here (at resolve time)
// rather than into every section at parse time is what keeps debian and ubuntu
// from cross-contaminating each other's package lists.
func (l *Candy) TopPackages() []string { return l.topPackages }

// EnvConfig returns the environment config (pre-populated from the candy manifest)
func (l *Candy) EnvConfig() (*EnvConfig, error) { //nolint:unparam // error return kept for interface/API stability
	if l.envConfig != nil {
		return l.envConfig, nil
	}
	return nil, nil
}

// Ports returns the ports (pre-populated from the candy manifest)
func (l *Candy) Port() ([]string, error) { //nolint:unparam // error return kept for interface/API stability
	if l.ports != nil {
		return l.ports, nil
	}
	return nil, nil
}

// PortSpecs returns the port specs with protocol info (pre-populated from the candy manifest)
func (l *Candy) PortSpecs() []spec.PortSpec {
	return l.portSpecs
}

// Service returns the candy's unified service: list (ServiceEntry slice).
// This is the only service schema — legacy raw-INI and system_services: are
// retired entirely. External candies that still have the legacy forms must run
// `charly migrate`.
func (l *Candy) Service() []spec.ServiceEntry {
	return l.service
}

// HasService returns true when the candy declares at least one service entry.
func (l *Candy) HasService() bool {
	return len(l.service) > 0
}

// ServiceFiles returns detected *.service file paths from the candy directory
// (consumed by the systemd init's file_copy model, e.g. *.service globs).
func (l *Candy) ServiceFiles() []string {
	return l.serviceFiles
}

// HasAnyInit returns true if this candy triggers any init system.
func (l *Candy) HasAnyInit() bool {
	return len(l.InitSystems) > 0
}

// HasInit returns true if this candy triggers the named init system.
func (l *Candy) HasInit(initName string) bool {
	return l.InitSystems[initName]
}

// PopulateCandyInitSystem sets InitSystems on all candies based on the init config.
// Must be called after scanning candies and loading init config.
func PopulateCandyInitSystem(layers map[string]*Candy, initCfg *InitConfig) {
	if initCfg == nil {
		return
	}
	for _, layer := range layers {
		layer.InitSystems = make(map[string]bool)
		for initName, def := range initCfg.Init {
			// Schema-driven detection: iterate the unified service: entries.
			// Each entry binds to init systems per per-entry routing:
			//   - IsPackaged()  → inits with ServiceSchema.SupportsPackaged
			//   - custom exec   → inits with ServiceSchema.ServiceTemplate != ""
			// The legacy `candy_field: [service]` config just gates whether
			// this init participates in schema detection at all.
			participatesInSchema := slices.Contains(def.CandyFields, "service")
			if participatesInSchema {
				for i := range layer.service {
					entry := &layer.service[i]
					if entry.IsPackaged() {
						if def.ServiceSchema != nil && def.ServiceSchema.SupportsPackaged {
							layer.InitSystems[initName] = true
							break
						}
					} else {
						if def.ServiceSchema != nil && def.ServiceSchema.ServiceTemplate != "" {
							layer.InitSystems[initName] = true
							break
						}
					}
				}
			}
			// Check candy_file (anchored at SourceDir — honors `directory:`)
			// for init systems like systemd that use the file_copy model.
			for _, pattern := range def.CandyFiles {
				matches, _ := filepath.Glob(filepath.Join(layer.SourceDir, pattern))
				if len(matches) > 0 {
					layer.InitSystems[initName] = true
				}
			}
		}
	}
}

// Route returns the route config (pre-populated from the candy manifest)
func (l *Candy) Route() (*RouteConfig, error) { //nolint:unparam // error return kept for interface/API stability
	if l.route != nil {
		return l.route, nil
	}
	return nil, nil
}

// CandyNames returns a sorted list of candy names
func CandyNames(layers map[string]*Candy) []string {
	names := make([]string, 0, len(layers))
	for name := range layers {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// Volume returns the volume declarations (pre-populated from the candy manifest)
func (l *Candy) Volume() []VolumeYAML {
	return l.volumes
}

// Extract returns the extract declarations (pre-populated from the candy manifest)
func (l *Candy) Extract() []ExtractYAML {
	return l.extract
}

// Data returns the data mappings (pre-populated from the candy manifest)
func (l *Candy) Data() []DataYAML {
	return l.data
}

// Security returns the security config (pre-populated from the candy manifest, nil if not set)
func (l *Candy) Security() *SecurityConfig {
	return l.security
}

// Libvirt returns the libvirt XML snippets (pre-populated from the candy manifest)
func (l *Candy) Libvirt() []string {
	return l.libvirt
}

// Hooks returns the lifecycle hooks config (pre-populated from the candy manifest, nil if not set)
func (l *Candy) Hooks() *HooksConfig {
	return l.hooks
}

// Shell returns the shell-init declarations (pre-populated from the candy manifest,
// nil if not set). The returned config carries an intrinsic body (init,
// path_append, path, priority) plus per-shell sub-blocks (bash/zsh/fish/
// sh) in ByShell. Selection rule applied at install time — see
// deploykit.CompileShellSnippetSteps in sdk/deploykit/install_build.go.
func (l *Candy) Shell() *spec.Shell {
	return l.shell
}

// Secrets returns the secret declarations (pre-populated from the candy manifest)
func (l *Candy) Secret() []SecretYAML {
	return l.secrets
}

// Artifact returns the files this candy publishes back to the operator
// after its setup completes (pre-populated from the candy manifest artifact:).
func (l *Candy) Artifact() []CandyArtifact {
	return l.artifacts
}

// EnvProvides returns env vars this candy provides to other containers (pre-populated from the candy manifest)
func (l *Candy) EnvProvides() map[string]string {
	return l.envProvides
}

// EnvRequires returns env vars this candy must have from the environment (pre-populated from the candy manifest)
func (l *Candy) EnvRequire() []spec.EnvDependency {
	return l.envRequires
}

// EnvAccepts returns env vars this candy can optionally use (pre-populated from the candy manifest)
func (l *Candy) EnvAccept() []spec.EnvDependency {
	return l.envAccepts
}

// SecretAccepts returns credential-store-backed env vars this candy can optionally use.
// These entries flow through the credential store → podman secret → Secret=type=env quadlet
// directive pipeline, never touching plaintext charly.yml or quadlet Environment= lines.
// Pre-populated from the candy manifest.
func (l *Candy) SecretAccept() []spec.EnvDependency {
	return l.secretAccepts
}

// SecretRequires returns credential-store-backed env vars this candy MUST have.
// Missing entries cause charly config to hard-fail with actionable remediation.
// Pre-populated from the candy manifest.
func (l *Candy) SecretRequire() []spec.EnvDependency {
	return l.secretRequires
}

// MCPProvides returns MCP servers this candy provides to other containers (pre-populated from the candy manifest)
func (l *Candy) MCPProvide() []spec.MCPServerYAML {
	return l.mcpProvides
}

// MCPRequires returns MCP servers this candy must have from the environment (pre-populated from the candy manifest)
func (l *Candy) MCPRequire() []spec.EnvDependency {
	return l.mcpRequires
}

// MCPAccepts returns MCP servers this candy can optionally use (pre-populated from the candy manifest)
func (l *Candy) MCPAccept() []spec.EnvDependency {
	return l.mcpAccepts
}

// Engine returns the required run engine (pre-populated from the candy manifest, "" if not set)
func (l *Candy) Engine() string {
	return l.engine
}

// Alias returns the alias declarations (pre-populated from the candy manifest)
func (l *Candy) Alias() []AliasYAML {
	return l.aliases
}

// NeedsGit returns true if the pixi manifest contains git-based dependencies
func (l *Candy) NeedsGit() bool {
	manifest := l.PixiManifest()
	if manifest == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(l.SourceDir, manifest))
	if err != nil {
		return false
	}
	content := string(data)
	// Check for PyPI git+ format and pixi { git = "..." } format
	return strings.Contains(content, "git+") || strings.Contains(content, "{ git =")
}

// HasPypiDeps returns true if the pixi manifest has PyPI dependencies
func (l *Candy) HasPypiDeps() bool {
	manifest := l.PixiManifest()
	if manifest == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(l.SourceDir, manifest))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "[pypi-dependencies]")
}

// ScanRemoteCandy scans specific candies from a downloaded remote repository.
// Only imports candies whose bare refs are in the wantRefs set.
// Bare refs use the full path format: "github.com/org/repo/candy/name".
// qualifyRemoteSiblingDeps records, for a freshly-scanned remote candy, the
// fully-qualified "<repo>/<subpathprefix><dep>" map key of each plain-name
// require:/candy: dep (the same form ScanRemoteCandy keys fetched siblings
// under). It sets each ref's resolved key (CandyRef.Resolved) and leaves
// CandyRef.Raw intact, so the graph resolves on .Bare() (qualified) while the
// transitive fetch loop still keys on the original .Raw plain name. @-ref deps
// are left untouched — their bare path already resolves directly.
func qualifyRemoteSiblingDeps(layer *Candy) {
	for i := range layer.Require {
		if !layer.Require[i].IsRemote() {
			layer.Require[i].Resolved = layer.RepoPath + "/" + layer.SubPathPrefix + layer.Require[i].Raw
		}
	}
	for i := range layer.IncludedCandy {
		if !layer.IncludedCandy[i].IsRemote() {
			layer.IncludedCandy[i].Resolved = layer.RepoPath + "/" + layer.SubPathPrefix + layer.IncludedCandy[i].Raw
		}
	}
	// bake_plugin: refs name a sibling PLUGIN candy whose binary is baked into the
	// image (generate.go emitBakedPlugins). Qualify them like require:/candy: so a
	// plain-name ref on a REMOTE candy (e.g. charly-mcp's `bake_plugin: [plugin-mcp]`)
	// resolves via .Bare() to the same fetched-sibling map key the scanned set holds —
	// the plugin candy itself reaches that set through the candy's require: (the
	// established pattern that also build-connects it), so qualification is what makes
	// the baked-binary lookup find it.
	for i := range layer.BakePlugin {
		if !layer.BakePlugin[i].IsRemote() {
			layer.BakePlugin[i].Resolved = layer.RepoPath + "/" + layer.SubPathPrefix + layer.BakePlugin[i].Raw
		}
	}
}

func ScanRemoteCandy(repoDir string, repoPath string, wantRefs map[string]bool) (map[string]*Candy, error) {
	layers := make(map[string]*Candy)

	for bareRef := range wantRefs {
		// Extract sub-path from bare ref: "github.com/org/repo/candy/name" -> "candy/name"
		subPath := strings.TrimPrefix(bareRef, repoPath+"/")
		candyDir := filepath.Join(repoDir, subPath)

		// Derive name from last segment
		name := subPath
		if idx := strings.LastIndex(subPath, "/"); idx != -1 {
			name = subPath[idx+1:]
		}

		if _, err := os.Stat(candyDir); os.IsNotExist(err) {
			return nil, fmt.Errorf("remote candy %s not found at %s", bareRef, candyDir)
		}

		layer, err := scanCandy(candyDir, name, UnifiedFileName)
		if err != nil {
			return nil, fmt.Errorf("scanning remote candy %s: %w", bareRef, err)
		}
		layer.Remote = true
		layer.RepoPath = repoPath
		// Compute sub-path prefix for sibling dep resolution (e.g. "candy/")
		if idx := strings.LastIndex(subPath, "/"); idx != -1 {
			layer.SubPathPrefix = subPath[:idx+1]
		}
		// Qualify plain-name sibling deps (require:/candy:) to fully-qualified
		// "<repo>/<subpathprefix><dep>" map keys, matching how fetched siblings
		// are keyed. This lets the dependency graph (graph.go) and validator
		// resolve a remote candy's transitive deps against siblings pulled from
		// the same repo, without per-call-site repo-path plumbing.
		qualifyRemoteSiblingDeps(layer)

		layers[bareRef] = layer
	}

	return layers, nil
}

// ScanAllCandy scans local candies and all remote candies, returning a merged map.
// Local candies are keyed by short name, remote candies by fully-qualified path.
// Remote refs are collected from @-prefixed refs in the candy manifest and charly.yml.
func ScanAllCandy(dir string) (map[string]*Candy, error) {
	return ScanAllCandyWithConfig(dir, nil)
}

// ScanAllCandyWithConfig is the default-opts wrapper (enabled images only)
// around ScanAllCandyWithConfigOpts. Most call sites (deploy-mode, runtime,
// inspect) want enabled-only scanning and keep this two-arg form.
func ScanAllCandyWithConfig(dir string, cfg *Config) (map[string]*Candy, error) {
	return ScanAllCandyWithConfigOpts(dir, cfg, ResolveOpts{})
}

// ScanAllCandyWithConfigOpts scans local and remote candies.
// Collects remote refs from @-prefixed candy references and auto-downloads
// repos. opts is forwarded to CollectRemoteRefsOpts so a build with
// `--include-disabled <name>` also fetches the named disabled image's remote
// candies — keeping the FETCH set aligned with the RESOLVE set.
func ScanAllCandyWithConfigOpts(dir string, cfg *Config, opts ResolveOpts) (map[string]*Candy, error) {
	// 1. Scan local candies
	layers, err := ScanCandy(dir)
	if err != nil {
		return nil, err
	}

	// 2. Collect remote refs from @-prefixed candy references
	downloads, err := CollectRemoteRefsOpts(cfg, layers, opts)
	if err != nil {
		return nil, err
	}

	if len(downloads) == 0 {
		return layers, nil
	}

	// 3. Per-entity-version resolution. The git tag is ONLY the fetch coordinate;
	// the authority is each candy's own `version:`, read AFTER fetch. So fetch
	// EVERY distinct (repo, git-tag) referenced (directly or transitively),
	// collect each materialization as a candidate, then arbitrate per bare ref by
	// per-entity version (pickCandyVersion). A remote candy's plain-name
	// require:/candy: dep is a same-repo sibling at the SAME git tag; an @-ref
	// dep carries its own repo/git-tag. Fix-point until no new (repo, git-tag,
	// ref) surfaces, so cross-repo transitive closures are fully materialized.
	type repoVer struct{ repo, ver string }
	candidates := make(map[string][]candyCandidate) // bare ref -> all fetched materializations
	scanned := make(map[repoVer]map[string]bool)    // (repo, git-tag) -> refs already scanned
	defaultBranches := make(map[string]string)      // repo → resolved default branch

	queue := downloads
	for len(queue) > 0 {
		nextByKey := make(map[repoVer]map[string]bool)
		enqueue := func(repo, ver, bare string) error {
			if ver == "" {
				if b, ok := defaultBranches[repo]; ok {
					ver = b
				} else {
					b, err := GitDefaultBranch(RepoGitURL(repo))
					if err != nil {
						return fmt.Errorf("resolving default branch for %s: %w", repo, err)
					}
					defaultBranches[repo] = b
					ver = b
				}
			}
			key := repoVer{repo, ver}
			if scanned[key][bare] {
				return nil // this exact (repo, git-tag, ref) already scanned
			}
			if nextByKey[key] == nil {
				nextByKey[key] = make(map[string]bool)
			}
			nextByKey[key][bare] = true
			return nil
		}

		for _, dl := range queue {
			key := repoVer{dl.RepoPath, dl.Version}
			done := scanned[key]
			if done == nil {
				done = make(map[string]bool)
				scanned[key] = done
			}
			wantRefs := make(map[string]bool)
			for _, ref := range dl.Refs {
				if !done[ref] {
					wantRefs[ref] = true
				}
			}
			if len(wantRefs) == 0 {
				continue
			}
			cachePath, err := EnsureRepoDownloaded(dl.RepoPath, dl.Version)
			if err != nil {
				return nil, fmt.Errorf("downloading %s:%s: %w", dl.RepoPath, dl.Version, err)
			}
			remoteCandies, err := ScanRemoteCandy(cachePath, dl.RepoPath, wantRefs)
			if err != nil {
				return nil, fmt.Errorf("scanning %s:%s: %w", dl.RepoPath, dl.Version, err)
			}
			for ref := range wantRefs {
				done[ref] = true
			}
			for ref, layer := range remoteCandies {
				if layer.Version == "" {
					return nil, fmt.Errorf("remote candy %q (from %s@%s) declares no version:; its producer repo must declare one", ref, dl.RepoPath, dl.Version)
				}
				candidates[ref] = append(candidates[ref], candyCandidate{
					layer:   layer,
					version: layer.Version,
					gitTag:  dl.Version,
					source:  dl.RepoPath + "@" + dl.Version,
				})

				// Enqueue this materialization's transitive deps. A plain-name dep
				// is a same-repo sibling at the SAME git tag; an @-ref dep carries
				// its own pinned repo/git-tag.
				enqueueDep := func(dep CandyRef) error {
					if dep.IsRemote() {
						p := ParseRemoteRef(dep.Raw)
						return enqueue(p.RepoPath, p.Version, dep.Bare())
					}
					return enqueue(dl.RepoPath, dl.Version, dl.RepoPath+"/"+layer.SubPathPrefix+dep.Raw)
				}
				for _, dep := range layer.Require {
					if err := enqueueDep(dep); err != nil {
						return nil, err
					}
				}
				for _, dep := range layer.IncludedCandy {
					if err := enqueueDep(dep); err != nil {
						return nil, err
					}
				}
			}
		}

		queue = nil
		for key, refs := range nextByKey {
			refList := make([]string, 0, len(refs))
			for r := range refs {
				refList = append(refList, r)
			}
			queue = append(queue, RemoteDownload{RepoPath: key.repo, Version: key.ver, Refs: refList})
		}
	}

	// 4. Arbitrate each bare ref by per-entity version; materialize the winner.
	for ref, cands := range candidates {
		winner := pickCandyVersion(ref, cands)
		if _, ok := layers[winner.layer.Name]; ok {
			fmt.Fprintf(os.Stderr, "Note: local candy %q shadows remote candy %q\n", winner.layer.Name, ref)
		}
		layers[ref] = winner.layer
	}

	return layers, nil
}

// candyCandidate is one fetched materialization of a bare candy ref. The git tag
// is the fetch coordinate; version is the candy's own per-entity `version:`.
type candyCandidate struct {
	layer   *Candy
	version string // per-entity version (layer.Version) — mandatory, never ""
	gitTag  string // fetch coordinate (the @github :vTAG)
	source  string // "<repo>@<git-tag>" for warning attribution
}

// pickCandyVersion arbitrates the candidates of ONE bare ref by per-entity
// version. Same per-entity version across different git tags => NO warning, the
// newest git tag wins (freshness). Different per-entity versions => warn once
// (naming the winner + a loser) and the newest per-entity version wins. This is
// the sole candy-version arbiter — direct and transitive refs both flow through
// it. cands is non-empty.
func pickCandyVersion(bareRef string, cands []candyCandidate) candyCandidate {
	best := cands[0]
	for _, c := range cands[1:] {
		if kit.CompareCalVer(c.version, best.version) > 0 {
			best = c // newer per-entity version
		} else if c.version == best.version && compareSemver(c.gitTag, best.gitTag) > 0 {
			best = c // same per-entity version: prefer the newest git tag
		}
	}
	for _, c := range cands {
		if c.version != best.version {
			fmt.Fprintf(os.Stderr,
				"Warning: candy %s resolved to multiple versions; using newest %s (from %s), ignoring %s (from %s)\n",
				bareRef, best.version, best.source, c.version, c.source)
			break
		}
	}
	return best
}
