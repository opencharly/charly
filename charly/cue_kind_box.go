package main

// EDGE-INHERIT cutover D: `box` is NO LONGER a YAML kind keyword (it merged into
// `candy:`), but #Box remains the IMAGE def (the image arm of #CandyValue) and uf.Box
// still holds images. This registers the INTERNAL "box" validation key so validate.go
// and the corpus validators type a uf.Box image against #Box — a candy: image decodes
// into uf.Box by its base/from marker, and the per-entity validation keys it as "box".
// (cueKindDefs is not bijective with the kind keywords, so an internal key is fine.)
func init() { registerCueKind("box", "#Box") }
