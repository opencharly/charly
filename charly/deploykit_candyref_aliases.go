package main

// deploykit_candyref_aliases.go — bindings onto the runtime candy-ref MODEL type +
// its pure ref-string parsers, moved to sdk/deploykit in P4 (with the runtime Candy
// graph). The clone/fetch LOGIC stays in charly/refs.go (P7).

import "github.com/opencharly/sdk/deploykit"

type CandyRef = deploykit.CandyRef

var (
	StripVersion     = deploykit.StripVersion
	IsRemoteCandyRef = deploykit.IsRemoteCandyRef
	BareRef          = deploykit.BareRef
	toCandyRefs      = deploykit.ToCandyRefs
)
