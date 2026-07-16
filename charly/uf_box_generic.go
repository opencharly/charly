package main

import (
	"encoding/json"
	"sort"

	"github.com/opencharly/sdk/spec"
)

// uf_box_generic.go — the generic kind-keyed IMAGE map + its typed accessors (P6 map-killing).
// UnifiedFile.Box (and the Config.Box projection that shares its map) hold opaque JSON bodies —
// the loader stores a marshaled spec.Box per image name; consumers decode the authored spec.BoxConfig
// on demand via these accessors, so the kernel holds NO per-kind TYPE (boundary law: no per-kind
// Go map). The already-generic substrate maps (uf.Pod/VM/K8s/Local/Android = json.RawMessage) are
// the same shape; this folds Box (and, in uf_candy_generic.go, Candy) into it.

// boxMap is the generic image map type — name → opaque marshaled spec.BoxConfig.
type boxMap = map[string]json.RawMessage

// decodeBox decodes one opaque image body into the authored spec.BoxConfig.
func decodeBox(raw json.RawMessage) (spec.BoxConfig, bool) {
	if len(raw) == 0 {
		return spec.BoxConfig{}, false
	}
	var b spec.BoxConfig
	if err := json.Unmarshal(raw, &b); err != nil {
		return spec.BoxConfig{}, false
	}
	return b, true
}

// encodeBox marshals an authored spec.BoxConfig into its opaque body.
func encodeBox(b spec.BoxConfig) json.RawMessage {
	raw, err := json.Marshal(b)
	if err != nil {
		// A spec.BoxConfig always marshals (plain struct); a failure is a programming error.
		panic("encodeBox: " + err.Error())
	}
	return raw
}

// boxConfigFrom decodes name's image config from a generic image map.
func boxConfigFrom(m boxMap, name string) (spec.BoxConfig, bool) {
	raw, ok := m[name]
	if !ok {
		return spec.BoxConfig{}, false
	}
	return decodeBox(raw)
}

// boxNamesOf returns the image names in a generic image map, sorted for determinism.
func boxNamesOf(m boxMap) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// BoxConfig decodes the authored image config for name; ok=false when absent.
func (uf *UnifiedFile) BoxConfig(name string) (spec.BoxConfig, bool) {
	return boxConfigFrom(uf.Box, name)
}

// HasBox reports whether an image named name is present.
func (uf *UnifiedFile) HasBox(name string) bool { _, ok := uf.Box[name]; return ok }

// BoxNames returns the image names, sorted.
func (uf *UnifiedFile) BoxNames() []string { return boxNamesOf(uf.Box) }

// SetBox stores an authored image config under name (marshaling it opaque).
func (uf *UnifiedFile) SetBox(name string, b spec.BoxConfig) {
	if uf.Box == nil {
		uf.Box = boxMap{}
	}
	uf.Box[name] = encodeBox(b)
}

// BoxConfig / HasBox on the projected Config share the same generic image map. (Config.BoxNames
// stays in config.go — it filters to ENABLED images, which requires a decode.)
func (c *Config) BoxConfig(name string) (spec.BoxConfig, bool) { return boxConfigFrom(c.Box, name) }
func (c *Config) HasBox(name string) bool                      { _, ok := c.Box[name]; return ok }

// SetBox stores an authored image config under name on the projected Config (marshaling it opaque).
func (c *Config) SetBox(name string, b spec.BoxConfig) {
	if c.Box == nil {
		c.Box = boxMap{}
	}
	c.Box[name] = encodeBox(b)
}

// allBoxNames returns every image name (enabled or not), sorted — the raw-map view Config.BoxNames
// filters. Used by the map-killing accessors where the enabled filter is applied separately.
func (c *Config) allBoxNames() []string { return boxNamesOf(c.Box) }

// eachBox iterates every image as (name, decoded spec.BoxConfig) in sorted name order — the
// decode-on-iterate view consumers walk INSTEAD of the kernel holding a typed map. Ranging
// `for name, img := range c.eachBox` reads exactly like the former `range c.Box`, but the
// STORED representation stays generic (boxMap) and each image decodes on demand.
func (c *Config) eachBox(yield func(string, spec.BoxConfig) bool) {
	for _, name := range boxNamesOf(c.Box) {
		b, _ := decodeBox(c.Box[name])
		if !yield(name, b) {
			return
		}
	}
}
