package main

// Per-kind registration for `candy`. EDGE-INHERIT cutover D: `box:` merged INTO
// `candy:`. A candy: node decodes by its base/from marker into uf.Box (an IMAGE, typed
// "box"→#Box) or uf.Candy (a LAYER, typed "candy"→#Candy) — so the per-entity
// validation keys each map against its concrete def (the raw `#Candy | #Box` disjunction
// is not concretely validatable; the load gate uses the mutually-exclusive #CandyValue).

func init() {
	registerCueKind("candy", "#Candy")
}
