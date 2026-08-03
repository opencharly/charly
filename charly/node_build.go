package main

// node_build.go — the GENERIC entity-body access for the compact node-form model, kept ONLY for
// node_candy.go's buildCandy (bootstrap-critical, clause B — the discovered-candy pre-check in
// unified.go calls it directly, so it stays *genericNode-typed permanently). The entity-body
// assembler itself (the former assembleEntityBody/entityBodyMapping/cloneYAMLNode) is now
// sdk/loaderkit.AssembleEntityBody + the relocated decode mechanism (K1 unit 3b) — decodeNodeValue
// converts gn to the wire-safe spec.ParsedNode (genericToParsedNode, node_parsed.go) and routes
// through it, the ONE remaining genericNode→pn conversion in the whole materialize path outside
// provider_kind_invoke.go's own two local reconstructions (foldCandyKind/validateKindValueCUE).

// decodeNodeValue decodes gn's body via the shared CUE entity decoder into out (a *struct) — kept
// as a same-named/same-signature core wrapper (R3) since node_candy.go's buildCandy calls it by
// this name.
func decodeNodeValue(gn *genericNode, out any) error {
	pn, err := genericToParsedNode(gn)
	if err != nil {
		return err
	}
	return requireProjectLoader().DecodeNodeValue(pn, out)
}
