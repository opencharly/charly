package main

import (
	"github.com/opencharly/sdk/spec"
)

// uf_box_generic.go — the generic kind-keyed IMAGE map + its typed accessors (P6 map-killing).
// UnifiedFile.Box (and the Config.Box projection that shares its map) hold opaque JSON bodies —
// the loader stores a marshaled spec.Box per image name; consumers decode the authored spec.BoxConfig
// on demand via these accessors, so the kernel holds NO per-kind TYPE (boundary law: no per-kind
// Go map). The already-generic substrate maps (uf.Pod/VM/K8s/Local/Android = json.RawMessage) are
// the same shape; this folds Box (and, in uf_candy_generic.go, Candy) into it.

// boxMap is the generic image map type — name → opaque marshaled spec.BoxConfig. An alias of
// spec.BoxMap (both resolve to the identical map[string]json.RawMessage type), kept locally so
// UnifiedFile's own field declarations below read unchanged.
type boxMap = spec.BoxMap

// decodeBox / encodeBox / boxConfigFrom / boxNamesOf moved to sdk/spec (FLOOR-SLIM Unit 5,
// spec.DecodeBox / spec.EncodeBox / spec.BoxConfigFrom / spec.BoxNamesOf) — Config's own 5
// accessor methods (BoxConfig/HasBox/SetBox/AllBoxNames/EachBox) moved WITH them, since Config is
// now `type Config = spec.Config`. UnifiedFile's OWN methods below (a DIFFERENT, still-charly-side
// type) now call the spec-exported forms directly rather than a private local copy (R3 — one
// decode/encode implementation, not two).

// BoxConfig decodes the authored image config for name; ok=false when absent.
func (uf *UnifiedFile) BoxConfig(name string) (spec.BoxConfig, bool) {
	return spec.BoxConfigFrom(uf.Box, name)
}

// HasBox reports whether an image named name is present.
func (uf *UnifiedFile) HasBox(name string) bool { _, ok := uf.Box[name]; return ok }

// BoxNames returns the image names, sorted.
func (uf *UnifiedFile) BoxNames() []string { return spec.BoxNamesOf(uf.Box) }

// SetBox stores an authored image config under name (marshaling it opaque).
func (uf *UnifiedFile) SetBox(name string, b spec.BoxConfig) {
	if uf.Box == nil {
		uf.Box = boxMap{}
	}
	uf.Box[name] = spec.EncodeBox(b)
}
