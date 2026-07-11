package main

import "encoding/json"

// uf_candy_generic.go — the generic kind-keyed LAYER map + its typed accessors (P6 map-killing).
// UnifiedFile.Candy holds opaque JSON bodies — the loader stores a marshaled InlineCandy per layer
// name; ProjectCandies decodes it on demand, so the kernel holds NO per-kind TYPE (boundary law: no
// per-kind Go map). The sibling image map is uf_box_generic.go; together they fold the last two typed
// uf.* maps into the same generic RawMessage shape the substrate maps (uf.Pod/VM/K8s/Local/Android)
// already use.

// candyMap is the generic layer map type — name → opaque marshaled InlineCandy.
type candyMap = map[string]json.RawMessage

// decodeInlineCandy decodes one opaque layer body into the InlineCandy loader shape.
func decodeInlineCandy(raw json.RawMessage) (*InlineCandy, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var il InlineCandy
	if err := json.Unmarshal(raw, &il); err != nil {
		return nil, false
	}
	return &il, true
}

// encodeInlineCandy marshals a loader InlineCandy into its opaque body.
func encodeInlineCandy(il *InlineCandy) json.RawMessage {
	raw, err := json.Marshal(il)
	if err != nil {
		// An InlineCandy always marshals (plain struct + generated spec fields); a failure is a
		// programming error.
		panic("encodeInlineCandy: " + err.Error())
	}
	return raw
}

// SetCandy stores a layer under name (marshaling it opaque).
func (uf *UnifiedFile) SetCandy(name string, il *InlineCandy) {
	if uf.Candy == nil {
		uf.Candy = candyMap{}
	}
	uf.Candy[name] = encodeInlineCandy(il)
}
