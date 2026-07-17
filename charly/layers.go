package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"

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
	"bake_plugin": true,
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
// FORWARD package parser (sdk/loaderkit.derivePackageSections) needs no
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
func RegisterBuildVocabulary(dc *buildkit.DistroConfig) {
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

// Format-specific structs (RpmConfig, DebConfig, PacConfig, AurConfig) removed.
// All format sections are now parsed dynamically as PackageSection via the embedded distro format names.
// See PackageSection type and CandyYAML.UnmarshalYAML for the generic parsing.

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

// ScanCandy scans candies for the project at dir into their FINAL spec.CandyReader
// form (W9: the type-Candy move — core never holds a concrete Candy struct; every
// candy is a spec.CandyModel + spec.CandyView pair scanned by the registered loader
// plugin's typed CandyScanner seam, then wrapped via deploykit.NewSpecCandyModel).
func ScanCandy(dir string) (map[string]spec.CandyReader, error) {
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
// fixtures (and the migration tool) that don't yet have an charly.yml. Every
// candy here is LOCAL (no remote-sibling qualification needed — mirrors the
// W9 spike's local-candy case), so completion + FinalizeCandyRefs run immediately
// per candy (RunOps/HasContent/HasInstallFiles — completeCandyRunOps — since this
// path never reaches ScanAllCandyWithConfigOpts's winners loop).
func legacyScanCandiesDir(dir string) (map[string]spec.CandyReader, error) {
	candiesDir := filepath.Join(dir, DefaultCandyDir)
	entries, err := os.ReadDir(candiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]spec.CandyReader), nil
		}
		return nil, fmt.Errorf("reading candy directory: %w", err)
	}
	layers := make(map[string]spec.CandyReader)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		m, v, refs, err := requireCandyScanner().ScanCandyManifest(filepath.Join(candiesDir, name), name, UnifiedFileName, parseCandyYAML)
		if err != nil {
			return nil, fmt.Errorf("scanning candy %s: %w", name, err)
		}
		completeCandyRunOps(&m, &v)
		spec.FinalizeCandyRefs(&m, &v, refs)
		layers[name] = deploykit.NewSpecCandyModel(m, v)
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

// PopulateCandyInitSystem sets the per-candy CandyView.InitSystems map based on the
// init config — the cross-candy host-completion pass (#67 pattern): scanning a
// SINGLE candy can't know the project's init vocabulary, so this runs once, after
// EVERY candy in the project has been scanned, over the mutable pre-wrap
// map[string]spec.ScannedCandy (a spec.CandyReader is read-only from here, so this
// MUST run before the final FinalizeCandyRefs+NewSpecCandyModel wrap — see
// ResolveOpts.InitCfg's doc comment). Byte-identical logic to the pre-move
// *Candy.InitSystems population, retargeted at scanned[name].Model.Service /
// .Model.SourceDir / .View.InitSystems.
func PopulateCandyInitSystem(scanned map[string]spec.ScannedCandy, initCfg *InitConfig) {
	if initCfg == nil {
		return
	}
	for name, sc := range scanned {
		sc.View.InitSystems = make(map[string]bool)
		for initName, def := range initCfg.Init {
			// Schema-driven detection: iterate the unified service: entries.
			// Each entry binds to init systems per per-entry routing:
			//   - IsPackaged()  → inits with ServiceSchema.SupportsPackaged
			//   - custom exec   → inits with ServiceSchema.ServiceTemplate != ""
			// The legacy `candy_field: [service]` config just gates whether
			// this init participates in schema detection at all.
			participatesInSchema := slices.Contains(def.CandyFields, "service")
			if participatesInSchema {
				for i := range sc.Model.Service {
					entry := &sc.Model.Service[i]
					if entry.IsPackaged() {
						if def.ServiceSchema != nil && def.ServiceSchema.SupportsPackaged {
							sc.View.InitSystems[initName] = true
							break
						}
					} else {
						if def.ServiceSchema != nil && def.ServiceSchema.ServiceTemplate != "" {
							sc.View.InitSystems[initName] = true
							break
						}
					}
				}
			}
			// Check candy_file (anchored at SourceDir — honors `directory:`)
			// for init systems like systemd that use the file_copy model.
			for _, pattern := range def.CandyFiles {
				matches, _ := filepath.Glob(filepath.Join(sc.Model.SourceDir, pattern))
				if len(matches) > 0 {
					sc.View.InitSystems[initName] = true
				}
			}
		}
		scanned[name] = sc
	}
}

// completeCandyRunOps finishes the ONE host-completed predicate scanFromParsed's own doc comment
// flags as not scan-computable standalone: RunOps needs opInContext (registry-adjacent D-data only
// charly core holds), so a single candy's scan can't derive it — this runs the SAME live-compute the
// pre-move *Candy.runOps() did (a `run:` step passes unless it is PURELY runtime-context), then
// OR-completes HasInstallFiles/HasContent with it (+ the already-known InitSystems term, when
// PopulateCandyInitSystem has run for this candy) — the associative-OR completion the scan-time
// partial computation deliberately deferred. MUST run on the mutable pre-wrap (Model, View) pair,
// before FinalizeCandyRefs+NewSpecCandyModel (a spec.CandyReader is read-only after that).
func completeCandyRunOps(m *spec.CandyModel, v *spec.CandyView) {
	for i := range m.Plan {
		step := &m.Plan[i]
		kw, err := step.StepKind()
		if err != nil || kw != kit.KwRun {
			continue
		}
		op := step.Op
		if opInContext(&op, spec.CtxRuntime) && !opInContext(&op, spec.CtxBuild) && !opInContext(&op, spec.CtxDeploy) {
			continue
		}
		m.RunOps = append(m.RunOps, op)
	}
	hasAnyInit := false
	for _, triggers := range v.InitSystems {
		if triggers {
			hasAnyInit = true
			break
		}
	}
	m.HasInstallFiles = m.HasInstallFiles || len(m.RunOps) > 0
	m.HasContent = m.HasContent || m.HasInstallFiles || hasAnyInit
}

// CandyNames returns a sorted list of candy names
func CandyNames(layers map[string]spec.CandyReader) []string {
	names := make([]string, 0, len(layers))
	for name := range layers {
		names = append(names, name)
	}
	kit.SortStrings(names)
	return names
}

// ScanAllCandy scans local candies and all remote candies, returning a merged map.
// Local candies are keyed by short name, remote candies by fully-qualified path.
// Remote refs are collected from @-prefixed refs in the candy manifest and charly.yml.
func ScanAllCandy(dir string) (map[string]spec.CandyReader, error) {
	return ScanAllCandyWithConfig(dir, nil)
}

// ScanAllCandyWithConfig is the default-opts wrapper (enabled images only)
// around ScanAllCandyWithConfigOpts. Most call sites (deploy-mode, runtime,
// inspect) want enabled-only scanning and keep this two-arg form.
func ScanAllCandyWithConfig(dir string, cfg *Config) (map[string]spec.CandyReader, error) {
	return ScanAllCandyWithConfigOpts(dir, cfg, ResolveOpts{})
}

// ScanAllCandyWithConfigOpts scans local and remote candies, returning each in its
// FINAL wrapped spec.CandyReader form. Collects remote refs from @-prefixed candy
// references and auto-downloads repos. opts is forwarded to CollectRemoteRefsOpts so
// a build with `--include-disabled <name>` also fetches the named disabled image's
// remote candies — keeping the FETCH set aligned with the RESOLVE set.
//
// Internally the whole scan→fetch→qualify→arbitrate pipeline (W9) carries each
// candidate as a mutable (spec.CandyModel, spec.CandyView, spec.CandyRefs) triple
// (spec.ScannedCandy) in place of the pre-move *Candy — the RICH CandyRefEntry form
// survives through remote-sibling qualification, and ONLY the arbitration WINNERS
// are host-completed (PopulateCandyInitSystem, opts.InitCfg) + finalized
// (spec.FinalizeCandyRefs, bare-stringing the refs) + wrapped
// (deploykit.NewSpecCandyModel) into the returned map[string]spec.CandyReader — a
// candidate that loses arbitration never pays that cost.
func ScanAllCandyWithConfigOpts(dir string, cfg *Config, opts ResolveOpts) (map[string]spec.CandyReader, error) {
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
					b, err := kit.GitDefaultBranch(kit.RepoGitURL(repo))
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
			remoteCandies, err := requireCandyScanner().ScanRemoteCandy(cachePath, dl.RepoPath, wantRefs, parseCandyYAML)
			if err != nil {
				return nil, fmt.Errorf("scanning %s:%s: %w", dl.RepoPath, dl.Version, err)
			}
			for ref := range wantRefs {
				done[ref] = true
			}
			for ref, sc := range remoteCandies {
				if sc.Model.Version == "" {
					return nil, fmt.Errorf("remote candy %q (from %s@%s) declares no version:; its producer repo must declare one", ref, dl.RepoPath, dl.Version)
				}
				candidates[ref] = append(candidates[ref], candyCandidate{
					scanned: sc,
					version: sc.Model.Version,
					gitTag:  dl.Version,
					source:  dl.RepoPath + "@" + dl.Version,
				})

				// Enqueue this materialization's transitive deps. A plain-name dep
				// is a same-repo sibling at the SAME git tag; an @-ref dep carries
				// its own pinned repo/git-tag.
				enqueueDep := func(dep spec.CandyRefEntry) error {
					if dep.IsRemote() {
						p := spec.ParseRemoteRef(dep.Raw)
						return enqueue(p.RepoPath, p.Version, dep.Bare())
					}
					return enqueue(dl.RepoPath, dl.Version, dl.RepoPath+"/"+sc.View.SubPathPrefix+dep.Raw)
				}
				for _, dep := range sc.Refs.Require {
					if err := enqueueDep(dep); err != nil {
						return nil, err
					}
				}
				for _, dep := range sc.Refs.IncludedCandy {
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
	winners := make(map[string]spec.ScannedCandy, len(candidates))
	for ref, cands := range candidates {
		winner := pickCandyVersion(ref, cands)
		if _, ok := layers[winner.scanned.Model.Name]; ok {
			fmt.Fprintf(os.Stderr, "Note: local candy %q shadows remote candy %q\n", winner.scanned.Model.Name, ref)
		}
		winners[ref] = winner.scanned
	}

	// 5. Host-completion (InitSystems, opts.InitCfg-gated — nil by default, matching
	// every caller but generate.go; then RunOps + the HasInstallFiles/HasContent
	// fold, unconditional) THEN finalize (bare-string the refs) THEN wrap into the
	// FINAL spec.CandyReader — in that order, since a CandyReader is read-only and
	// can't be mutated after this point.
	PopulateCandyInitSystem(winners, opts.InitCfg)
	for ref, sc := range winners {
		completeCandyRunOps(&sc.Model, &sc.View)
		spec.FinalizeCandyRefs(&sc.Model, &sc.View, sc.Refs)
		layers[ref] = deploykit.NewSpecCandyModel(sc.Model, sc.View)
	}

	return layers, nil
}

// candyCandidate is one fetched materialization of a bare candy ref. The git tag
// is the fetch coordinate; version is the candy's own per-entity `version:`.
type candyCandidate struct {
	scanned spec.ScannedCandy
	version string // per-entity version (scanned.Model.Version) — mandatory, never ""
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
		} else if c.version == best.version && kit.CompareSemver(c.gitTag, best.gitTag) > 0 {
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

// Inject the VerbCatalog-coupled op-context classifier (checkspec.go's opInContext) into
// deploykit's swappable seam (deploykit itself holds no VerbCatalog — that vocabulary is
// core, reserved_registry.go). Hosted here (not checkspec.go) so checkspec.go needs no
// kit/deploykit import at all (K3, #39) — this file already imports deploykit.
func init() { deploykit.OpInContext = opInContext }
