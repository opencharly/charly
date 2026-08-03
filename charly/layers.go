package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/opencharly/spec/spec"

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
func RegisterBuildVocabulary(dc *spec.DistroConfig) {
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
// spec.DefaultCandyDir is the single source of truth for the on-disk directory that
// holds candy definitions. The discover: block overrides it per project
// for discovery; write/resolve paths fall back to this default. Renaming the
// candy directory project-wide is a one-line change in spec.
// The value lives in spec (the types-only fabric module shared with out-of-tree
// plugin candies). (W0 deleted the former in-core DefaultCandyDir/DefaultBoxDir
// aliases — every consumer reads spec.DefaultCandyDir directly; DefaultBoxDir itself
// had zero live callers, pure residue — spec.DefaultBoxDir is still the symmetric
// on-disk box-definitions directory, discovered per-box as
// <spec.DefaultBoxDir>/<name>/<spec.UnifiedFileName>.)

// The per-directory discovery manifest filename is the ONE filename the code
// knows — UnifiedFileName ("charly.yml", defined in unified.go). There is no
// separate manifest constant: a project's root file, every discovered box, and
// every discovered candy all use the single charly.yml name. Each `discover[]`
// spec may still override it via `manifest:` in charly.yml.

// ScanCandy scans candies for the project at dir into their FINAL spec.CandyReader
// form (W9: the type-Candy move — core never holds a concrete Candy struct; every
// candy is a spec.CandyModel + spec.CandyView pair scanned by the registered loader
// plugin's typed CandyScanner seam, then wrapped via deploykit.NewSpecCandyModel).
// Delegates to scanLocalCandies + the ONE choke point (FinalizeScannedCandies, reached via the
// ProjectLoader seam, no InitCfg in scope for a standalone call) — see ScanAllCandyWithConfigOpts's
// doc comment for why a local candy is NEVER wrapped anywhere else.
func ScanCandy(dir string) (map[string]spec.CandyReader, error) {
	scanned, err := scanLocalCandies(dir)
	if err != nil {
		return nil, err
	}
	return requireProjectLoader().FinalizeScannedCandies(scanned, nil), nil
}

// scanLocalCandies is the UNWRAPPED local-scan dispatcher — the ONE place every construction
// path (ScanCandy directly, or ScanAllCandyWithConfigOpts combining locals with remote winners)
// gets a project's local candies as pre-completion, pre-finalize spec.ScannedCandy values, so
// completion (RunOps + InitSystems, the latter opts.InitCfg-gated) always runs at the SAME final
// choke point a remote candy goes through — never earlier, never with a different InitCfg.
func scanLocalCandies(dir string) (map[string]spec.ScannedCandy, error) {
	uf, present, err := LoadUnified(dir)
	if err != nil {
		return nil, fmt.Errorf("loading charly.yml: %w", err)
	}
	if present {
		if err := ApplyDiscover(uf, dir); err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}
		return projectCandiesScanned(uf, dir)
	}
	return legacyScanCandiesDirScanned(dir)
}

// legacyScanCandiesDirScanned is the pre-unified filesystem walk's UNWRAPPED body. Kept for test
// fixtures (and the migration tool) that don't yet have an charly.yml. Every candy here is LOCAL
// (no remote-sibling qualification needed — mirrors the W9 spike's local-candy case); completion
// + finalize + wrap happen ONLY at the choke point (loaderkit.FinalizeScannedCandies), never here.
func legacyScanCandiesDirScanned(dir string) (map[string]spec.ScannedCandy, error) {
	candiesDir := filepath.Join(dir, spec.DefaultCandyDir)
	entries, err := os.ReadDir(candiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]spec.ScannedCandy), nil
		}
		return nil, fmt.Errorf("reading candy directory: %w", err)
	}
	scanned := make(map[string]spec.ScannedCandy)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		m, v, refs, err := requireCandyScanner().ScanCandyManifest(filepath.Join(candiesDir, name), name, spec.UnifiedFileName, parseCandyYAML)
		if err != nil {
			return nil, fmt.Errorf("scanning candy %s: %w", name, err)
		}
		scanned[name] = spec.ScannedCandy{Model: m, View: v, Refs: refs}
	}
	return scanned, nil
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
		if err := requireProjectLoader().DecodeEntityViaCUE(body, reflect.TypeOf(spec.CandyYAML{}), &ly, path); err != nil {
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

// withLocalRawRefs returns opts with every local candy's RAW (pre-finalize) require:/candy:
// refs appended to ExtraCandyRefs. CollectRemoteRefsOpts's own "candy manifest require:/candy:"
// walk reads CandyView.Require/.IncludedCandy — the FINALIZED bare-string wire form
// (FinalizeCandyRefs strips a "@repo:vTAG" pin down to the bare graph-topology name; correct
// for its OWN consumers, ExpandCandy/ResolveCandyOrder, which are version-agnostic). Feeding
// that walk a wrapped view therefore leaves it structurally UNABLE to discover a local candy's
// pinned remote dep at all (a bare name never looks remote to IsRemoteCandyRef) — the confirmed
// root cause of a "depends: unknown candy" crash a live box/cachyos generate surfaced (a local
// candy's require: pins a remote plugin candy). So the raw pre-finalize refs (still carrying the
// full pin, from spec.ScannedCandy.Refs) are harvested here and fed in as ExtraCandyRefs — the
// SAME mechanism a deploy's add_candy: already uses to reach a ref no base/builder/require edge
// would otherwise surface. A local (non-remote) ref is a harmless no-op (IsRemoteCandyRef gates it).
func withLocalRawRefs(opts spec.ResolveOpts, localScanned map[string]spec.ScannedCandy) spec.ResolveOpts {
	extraRefs := append([]string(nil), opts.ExtraCandyRefs...)
	for _, sc := range localScanned {
		for _, dep := range sc.Refs.Require {
			extraRefs = append(extraRefs, dep.Raw)
		}
		for _, dep := range sc.Refs.IncludedCandy {
			extraRefs = append(extraRefs, dep.Raw)
		}
	}
	opts.ExtraCandyRefs = extraRefs
	return opts
}

// ScanAllCandyWithConfig is the default-opts wrapper (enabled images only)
// around ScanAllCandyWithConfigOpts. Most call sites (deploy-mode, runtime,
// inspect) want enabled-only scanning and keep this two-arg form.
func ScanAllCandyWithConfig(dir string, cfg *spec.Config) (map[string]spec.CandyReader, error) {
	return ScanAllCandyWithConfigOpts(dir, cfg, spec.ResolveOpts{})
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
// survives through remote-sibling qualification. LOCAL candies stay UNWRAPPED here
// too (scanLocalCandies, not ScanCandy) so opts.InitCfg reaches them at the SAME
// loaderkit.FinalizeScannedCandies choke point the remote winners go through — a local-only
// project (or the early-return below) must complete InitSystems/RunOps identically
// to one with remote candies, never via a separate, InitCfg-less wrap. The one
// exception is the throwaway wrap CollectRemoteRefsOpts needs (it walks Require/
// IncludedCandy edges, unaffected by initCfg) — loaderkit.FinalizeScannedCandies never mutates
// its input map (each candidate is completed off a range-loop COPY), so calling it
// twice (once throwaway, once final) is safe and cheap.
func ScanAllCandyWithConfigOpts(dir string, cfg *spec.Config, opts spec.ResolveOpts) (map[string]spec.CandyReader, error) {
	// 1. Scan local candies (unwrapped — see doc comment above).
	localScanned, err := scanLocalCandies(dir)
	if err != nil {
		return nil, err
	}
	return scanCandyFromLocal(localScanned, cfg, opts)
}

// scanCandyFromLocal is ScanAllCandyWithConfigOpts's step-2-onward body (remote-ref collect,
// fix-point fetch, per-entity-version arbitration, host-completion + finalize) — now a THIN host
// wrapper (K3 U4-b) that builds the ScanSeams host-coupled legs and delegates the pure fix-point to
// the loaderkit scan mechanism through the ProjectLoader seam (requireProjectLoader). The ROOT
// project's ScanAllCandyWithConfigOpts calls this with its own (localScanned, cfg); the
// namespaced-box resolve used to call it too (via the deleted host namespaced-box fill), but that fold
// now runs plugin-side — candy/plugin-build's foldNamespaceScanEntries calls loaderkit.
// ScanCandyFromLocal directly over the host's per-namespace NamespaceScanReply inputs.
// Behavior-identical to the pre-move function (same steps 2-5).
func scanCandyFromLocal(localScanned map[string]spec.ScannedCandy, cfg *spec.Config, opts spec.ResolveOpts) (map[string]spec.CandyReader, error) {
	return requireProjectLoader().ScanCandyFromLocal(localScanned, opts.InitCfg, scanSeamsFor(cfg, opts))
}

// scanSeamsFor builds the host closures the loaderkit scan mechanism reaches through the seam: cfg+opts
// are captured here so they never cross into loaderkit (the opts-agnostic seam pattern, mirroring the U2
// ResolveProjectSeams closures). CollectRemoteRefs threads the throwaway nil-initCfg finalize +
// withLocalRawRefs the reachability walk needs (see withLocalRawRefs' doc comment for why the
// wrapped-view walk can't discover a local candy's pinned remote dep alone); EnsureRepo /
// ScanRemote wrap the host git-cache (+ auto-migrate) and the registry-coupled per-candy manifest
// scan (parseCandyYAML). candy/plugin-build supplies InvokeProvider-backed closures instead in U6.
func scanSeamsFor(cfg *spec.Config, opts spec.ResolveOpts) spec.ScanSeams {
	return spec.ScanSeams{
		CollectRemoteRefs: func(localScanned map[string]spec.ScannedCandy) ([]spec.RemoteDownload, error) {
			return CollectRemoteRefsOpts(cfg, requireProjectLoader().FinalizeScannedCandies(localScanned, nil), withLocalRawRefs(opts, localScanned))
		},
		EnsureRepo: EnsureRepoDownloaded,
		ScanRemote: func(cacheDir, repoPath string, wantRefs map[string]bool) (map[string]spec.ScannedCandy, error) {
			return requireCandyScanner().ScanRemoteCandy(cacheDir, repoPath, wantRefs, parseCandyYAML)
		},
	}
}

// The per-entity candy-version arbiter (candyCandidate + pickCandyVersion) moved
// to sdk/loaderkit (candy_version.go) as spec.CandyCandidate /
// loaderkit.PickCandyVersion — a kind-blind MECHANISM (boundary-law clause M)
// with zero core coupling. scanCandyFromLocal above calls it directly.

// Inject the VerbCatalog-coupled op-context classifier (checkspec.go's opInContext) into
// spec's swappable seam (spec holds no VerbCatalog — that vocabulary is core,
// reserved_registry.go; the seam var moved to spec so the fabric libraries read it without
// a deploykit import, #55 import-purity cone-render). Hosted here (not checkspec.go) so
// checkspec.go needs no kit/deploykit import at all (K3, #39).
func init() { spec.OpInContext = opInContext }
