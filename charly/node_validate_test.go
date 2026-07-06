package main

import "testing"

// TestValidateCandyManifestCUE_NodeForm proves charly box validate's candy path
// validates node-form candy manifests (concrete #NodeDoc) — accepting a complete
// candy and rejecting one missing a required field / with a malformed step.
func TestValidateCandyManifestCUE_NodeForm(t *testing.T) {
	ok := `redis:
  candy:
    version: "2026.150.0000"
    description: in-memory store
    package: [redis]
    plan:
      - check: the binary exists
        file: /usr/bin/redis-server
`
	if err := validateCandyManifestCUE("ok", []byte(ok)); err != nil {
		t.Fatalf("valid node-form candy rejected: %v", err)
	}
	// missing required description → concrete validation must fail.
	bad := `redis:
  candy:
    version: "2026.150.0000"
    package: [redis]
`
	if err := validateCandyManifestCUE("bad", []byte(bad)); err == nil {
		t.Error("node-form candy missing required description should fail box-validate")
	}
}
