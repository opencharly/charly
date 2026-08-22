package spec

import "encoding/json"

// MergePluginKindsMap merges src's plugin-kind entities INTO *dst under project-wins semantics: an
// entity already present in dst (by kind, then name) is never overwritten. A pure map/json.RawMessage
// merge over the plugin-kind carrier shape, relocated to the dedicated spec module (#55 2b) so the
// binary-embedded-defaults merge (charly/embed_defaults.go) reaches it without importing loaderkit;
// loaderkit re-exports it as a forwarder for its own merge path.
func MergePluginKindsMap(dst *map[string]map[string]json.RawMessage, src map[string]map[string]json.RawMessage) {
	if len(src) == 0 {
		return
	}
	if *dst == nil {
		*dst = make(map[string]map[string]json.RawMessage)
	}
	for kind, entities := range src {
		d := (*dst)[kind]
		if d == nil {
			d = make(map[string]json.RawMessage)
			(*dst)[kind] = d
		}
		for name, body := range entities {
			if _, exists := d[name]; !exists {
				d[name] = body
			}
		}
	}
}
