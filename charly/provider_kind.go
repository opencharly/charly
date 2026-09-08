package main

// provider_kind.go — KindProvider was the typed in-proc form of a kind Provider
// (DecodeNode(*genericNode, *spec.UnifiedFile) + CueDefPath). It is DELETED with the
// genericNode dual representation (parser consolidation F2.1): every authoring kind is now a
// ClassKind plugin provider routed through runPluginKind/foldSubstrateKind/foldCandyKind over
// spec.ParsedNode — no in-proc KindProvider remained (registry_bootstrap carries the
// bijection history), so the interface and its no-op gate (spec.KindWords is EMPTY) died with
// the type it named, never re-implemented on the parsed node.
