package spec

import "strings"

// candy_ref_entry.go — CandyRefEntry, the runtime candy-reference MODEL type + its pure
// ref-string parsers (promoted from sdk/deploykit's CandyRef alongside CandyReader — both are
// spec-hosted contract types an import-clean charly file must be able to reach without an sdk
// mechanism-kit import). Named CandyRefEntry, not CandyRef, because spec.CandyRef already names
// the DISTINCT CUE-authored wire form (a bare string — the `require:`/`candy:` list element as
// decoded straight off YAML); CandyRefEntry is the RUNTIME model built from one, with derived
// .Bare()/.Version()/.IsRemote() so a ref's identity and its pinned version can never drift apart.

// CandyRefEntry is a single candy reference as authored in the candy manifest `require:` /
// `candy:` (or charly.yml `candy:`). It carries the ORIGINAL ref string — with any `@repo` prefix
// and `:version` suffix — as the single source of truth; the bare map-key form (.Bare()) and the
// pinned version (.Version()) are DERIVED on demand.
type CandyRefEntry struct {
	Raw string // original ref, e.g. "@github.com/org/repo/candy/x:v1" or bare "x"
	// Resolved is the qualified candy-map key assigned when a freshly-fetched
	// remote candy's plain-name sibling deps are qualified to
	// "<repo>/<subpathprefix><name>" (qualifyRemoteSiblingDeps). Empty for every
	// other ref, where the map key derives from Raw.
	Resolved string
}

// Bare returns the candy-map key (no @ prefix, no :version) — the form used for
// dependency resolution and graph keying.
func (r CandyRefEntry) Bare() string {
	if r.Resolved != "" {
		return r.Resolved
	}
	return BareCandyRef(r.Raw)
}

// Version returns the pinned version (the ":vX" suffix), or "" for an unpinned
// remote ref or a local (bare-name) ref.
func (r CandyRefEntry) Version() string { _, v := StripCandyRefVersion(r.Raw); return v }

// IsRemote reports whether this is an @-prefixed remote ref.
func (r CandyRefEntry) IsRemote() bool { return IsRemoteCandyRefString(r.Raw) }

// ToCandyRefEntries wraps raw ref strings into CandyRefEntry values. Returns nil for a
// nil/empty input so an absent list stays absent.
func ToCandyRefEntries(raw []string) []CandyRefEntry {
	if len(raw) == 0 {
		return nil
	}
	out := make([]CandyRefEntry, len(raw))
	for i, s := range raw {
		out[i] = CandyRefEntry{Raw: s}
	}
	return out
}

// StripCandyRefVersion splits an @-prefixed ref into its bare form and pinned version.
// A non-@ ref returns (ref, "").
func StripCandyRefVersion(ref string) (string, string) {
	if !strings.HasPrefix(ref, "@") {
		return ref, ""
	}
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		return ref[:idx], ref[idx+1:]
	}
	return ref, ""
}

// IsRemoteCandyRefString reports whether a ref is an @-prefixed remote ref.
func IsRemoteCandyRefString(ref string) bool {
	return strings.HasPrefix(ref, "@")
}

// BareCandyRef returns the map-key form of a ref (no @ prefix, no :version).
func BareCandyRef(ref string) string {
	bare, _ := StripCandyRefVersion(ref)
	return strings.TrimPrefix(bare, "@")
}

// FinalizeCandyRefs projects the RICH pre-qualification refs (CandyRefs) down to their
// bare-string FINAL wire form — v.Require / v.IncludedCandy (CandyView) and m.BakePlugin
// (CandyModel) — run ONCE, after any remote-sibling qualification (loaderkit.QualifyRemoteSiblingDeps)
// has had the chance to set .Resolved. Mirrors the pre-move projectCandyView/projectCandyModel
// bare-ref projection (charly/resolved_project_host.go's bareRefs() calls, pre-W9), which ran
// AFTER qualifyRemoteSiblingDeps on the live *Candy. Lives in spec (not loaderkit, alongside its
// sibling CandyRefEntry/ToCandyRefEntries) — a pure, non-registry-coupled data-shape transform
// charly core must reach directly at the fix-point arbitration's final step (import-purity: core
// imports spec but never an sdk mechanism kit).
func FinalizeCandyRefs(m *CandyModel, v *CandyView, refs CandyRefs) {
	v.Require = bareCandyRefEntries(refs.Require)
	v.IncludedCandy = bareCandyRefEntries(refs.IncludedCandy)
	m.BakePlugin = bareCandyRefEntries(refs.BakePlugin)
}

// bareCandyRefEntries projects a []CandyRefEntry down to its bare-string []CandyRef form (the
// CandyView wire shape — identity/graph refs are bare strings, resolved-key details like a remote
// candy's Resolved field are a build-model concern only).
func bareCandyRefEntries(refs []CandyRefEntry) []CandyRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]CandyRef, len(refs))
	for i, r := range refs {
		out[i] = r.Bare()
	}
	return out
}
