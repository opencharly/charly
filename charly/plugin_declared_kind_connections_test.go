package main

// plugin_declared_kind_connections_test.go — the DECLARED kind-connection error-retention contract.
//
// Moved here verbatim from plugin_build_cache_test.go when that file's plugin-build-cache tests were
// deleted with the loader's cache cluster (the phase-1 store cutover, charly#896): this test is
// about the declared-kind CONNECT registry, not about a cache path, so it must not be deleted along
// with its former file-mates.

import (
	"fmt"
	"testing"
)

func TestFinalizeDeclaredKindConnectionsRetainsUnconnectedCause(t *testing.T) {
	const word = "test-unconnected-kind"
	original := declaredKindConnectErr
	declaredKindConnectErr = map[string]error{word: fmt.Errorf("original connection failure")}
	t.Cleanup(func() { declaredKindConnectErr = original })

	finalizeDeclaredKindConnections(map[string]struct{}{word: {}})
	if got := declaredKindConnectError(word); got == nil || got.Error() != "original connection failure" {
		t.Fatalf("unconnected declared kind lost its causal error: %v", got)
	}
}
