package spec

import "strings"

// candy_vocab.go — the build VOCABULARY the candy-manifest shape guard consults (K-wave 2 cone R1,
// A2 unit 2). Relocated from charly/layers.go, where it was the pair of process-global caches
// candyYAMLFormatNames / candyYAMLDistroNames plus looksLikeDistroOrFormatKey.
//
// It became a VALUE rather than staying a global because the parse mechanism it feeds moved to
// sdk/loaderkit, where both charly core (via the CandyScanner seam) and candy/plugin-build (directly)
// call it. A compiled-in plugin shares the host's process, so package globals would have APPEARED to
// work while silently coupling two modules through mutable state — and would fail open for an
// out-of-process placement, where the plugin's own copy is never registered. Passing the value keeps
// the mechanism honest in both placements.
//
// The vocabulary is NOT hardcoded in Go: it is DERIVED at load time from the embedded build
// vocabulary (plus any project override) — the `distro:` section (the DistroConfig). Adding a new
// distro or package format is therefore purely a vocabulary edit, with no code change.
//
// These sets are consumed ONLY by the candy-manifest shape guard, to recognize a package-format or
// per-distro section mistakenly placed at the candy root. The FORWARD package parser
// (loaderkit.derivePackageSections) needs no vocabulary at all — it routes every `distro:` sub-key
// structurally and lets the cascade resolver match on the image's real Distro/Pkg.
type CandyVocab struct {
	// FormatNames is the union of every distro's declared package formats (rpm/deb/pac/aur/…),
	// inherited chains resolved.
	FormatNames map[string]bool
	// DistroNames is every distro name declared in the build vocabulary.
	DistroNames map[string]bool
}

// NewCandyVocab derives the distro/format vocabulary from a DistroConfig. A nil config yields the
// zero vocabulary, under which the shape guard fails OPEN (no false positives) — byte-identical to
// the pre-move RegisterBuildVocabulary(nil) contract, which cleared the caches for the same reason.
func NewCandyVocab(dc *DistroConfig) CandyVocab {
	v := CandyVocab{FormatNames: map[string]bool{}, DistroNames: map[string]bool{}}
	if dc == nil {
		return v
	}
	for _, name := range dc.AllFormatNames() {
		v.FormatNames[name] = true
	}
	for name := range dc.Distro {
		v.DistroNames[name] = true
	}
	return v
}

// LooksLikeDistroOrFormatKey reports whether a candy-manifest top-level key is a package-format
// family name (pac/deb/rpm/aur) or a per-distro tag section (`debian`, `debian:13`, `debian,ubuntu`)
// — shapes that nest under the `distro:` map, never at the candy root. Returns false for an
// unpopulated vocabulary (no false positives), leaving the explicit removed-field cases to fire.
func (v CandyVocab) LooksLikeDistroOrFormatKey(key string) bool {
	if key == "" {
		return false
	}
	if v.FormatNames[key] {
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
		if !v.DistroNames[bare] {
			return false
		}
	}
	return true
}
