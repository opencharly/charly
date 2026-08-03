package main

// Unified node-form document validation — the load-time "validate-before-execute"
// gate (CLAUDE.md / the bundle cutover's mandated guarantee #2). A whole charly.yml
// document in unified node-form is validated against #NodeDoc (schema/node.cue)
// BEFORE any reshape / normalize / build / deploy runs, so a typo'd discriminator,
// an unknown field in a kind-value, a wrong-kind child, or a leaf-with-children is
// a hard load error — never silently executed. This is the Go counterpart to the
// offline `cue vet` proof (RDD-1). The mechanism itself is sdk/loaderkit.ValidateNodeDocCUE
// (K1 unit 2); this file keeps a same-named/same-signature core wrapper (R3) since
// GateDoc/materialize.go call it by that exact function VALUE.

// validateNodeDocCUE validates a unified node-form document (raw YAML bytes) via the relocated
// CUE-validate seam. label identifies the document in errors.
func validateNodeDocCUE(label string, data []byte) error {
	return requireProjectLoader().ValidateNodeDocCUE(coreCueSchema(), label, data)
}
