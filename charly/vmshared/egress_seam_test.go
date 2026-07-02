package vmshared

// The host (package main, or candy/plugin-vm) wires the ValidateEgress seam at
// init; a bare `go test ./vmshared/` binary does not, so RenderCloudInit's egress
// gate would nil-deref. Wire a pass-through for the test binary — the egress
// gate's REAL behavior on rendered cloud-init is covered by package main's
// TestRenderCloudInit_OutputValidatesAgainstSchema (egress_test.go), where the
// real validator is wired. Here we only exercise the render structure.
func init() {
	if ValidateEgress == nil {
		ValidateEgress = func(kind, label string, data []byte) error { return nil }
	}
}
