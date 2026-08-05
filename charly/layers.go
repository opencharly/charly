package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/spec/spec"
)

// PortSpec shorthand (scalar `8080` / `"tcp:5900"`) is canonicalized to the
// {port, protocol} struct form by the CUE loader's normalizer (cue_normalize.go,
// expandPortSpecNode); the custom UnmarshalYAML was deleted in the CUE loader
// switch (Cutover 1).

// ShellAllowlist enumerates valid per-shell sub-block keys inside `shell:`.
// Adding a new shell here is a renderer change (new managed-block / drop-in
// destination); keep in sync with deploy_host_helpers.go shell-detection
// probe and the shell-snippet destination table (deploykit.CompileShellSnippetSteps).

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

// withLocalRawRefs moved to spec.WithLocalRawRefs (spec/resolve_opts.go, K-wave 2 cone R1 A2):
// candy/plugin-build's own CollectRemoteRefsOpts call needs the identical augmentation, so the
// rule lives in the shared fabric module both sides import instead of being duplicated across the
// boundary. See its doc comment there for the pinned-remote-dep rationale.

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
// namespaced-box resolve used to call it too (via the deleted host namespaced-box fill), but that
// whole walk now runs plugin-side — candy/plugin-build's fillNamespacedBoxes calls
// loaderkit.ScanCandyFromLocal over inputs it scans and collects itself.
// Behavior-identical to the pre-move function (same steps 2-5).
func scanCandyFromLocal(localScanned map[string]spec.ScannedCandy, cfg *spec.Config, opts spec.ResolveOpts) (map[string]spec.CandyReader, error) {
	return requireProjectLoader().ScanCandyFromLocal(localScanned, opts.InitCfg, scanSeamsFor(cfg, opts))
}

// scanSeamsFor builds the host closures the loaderkit scan mechanism reaches through the seam: cfg+opts
// are captured here so they never cross into loaderkit (the opts-agnostic seam pattern, mirroring the U2
// ResolveProjectSeams closures). CollectRemoteRefs threads the throwaway nil-initCfg finalize +
// spec.WithLocalRawRefs the reachability walk needs (see its doc comment for why the
// wrapped-view walk can't discover a local candy's pinned remote dep alone); EnsureRepo /
// ScanRemote wrap the host git-cache (+ auto-migrate) and the registry-coupled per-candy manifest
// scan (parseCandyYAML). candy/plugin-build supplies InvokeProvider-backed closures instead in U6.
func scanSeamsFor(cfg *spec.Config, opts spec.ResolveOpts) spec.ScanSeams {
	return spec.ScanSeams{
		CollectRemoteRefs: func(localScanned map[string]spec.ScannedCandy) ([]spec.RemoteDownload, error) {
			return requireProjectLoader().CollectRemoteRefsOpts(hostInProcCtx(), cfg, requireProjectLoader().FinalizeScannedCandies(localScanned, nil), spec.WithLocalRawRefs(opts, localScanned))
		},
		EnsureRepo: func(repoPath, version string) (string, error) {
			return requireProjectLoader().EnsureRepoDownloaded(hostInProcCtx(), repoPath, version)
		},
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
