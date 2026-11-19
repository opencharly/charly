package main

// provider_kind.go — there is no KindProvider, the typed in-proc form of a kind Provider
// (DecodeNode(*genericNode, *spec.UnifiedFile) + CueDefPath): it has no place beside the ONE
// genericNode representation (parser consolidation F2.1). Every authoring kind is a
// ClassKind plugin provider routed through runPluginKind/foldSubstrateKind/foldCandyKind over
// spec.ParsedNode — no in-proc KindProvider remained (registry_bootstrap carries the
// bijection history), so the interface and its no-op gate (spec.KindWords is EMPTY) died with
// the type it named, never re-implemented on the parsed node.
