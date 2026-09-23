package main

import (
	"context"
	"testing"

	"github.com/opencharly/spec/ops"
)

// engine_provider_test.go — the in-tree consumer proof for the compiled-in
// `engine` provider class. It exercises the class the same way the host does:
// resolve the word through Registry.ResolveEngine, invoke the provider, and read
// the capability the servant returns. It fails if the class is not wired (no
// registered servant for podman/docker), if the servant does not answer the
// declared op set, or if the describe round-trip stops carrying the capability.
func TestEngineProviderClassServesEveryDeclaredOp(t *testing.T) {
	opsDeclared := engineProviderOpsAreServed()
	if len(opsDeclared) == 0 {
		t.Fatal("engineProviderOpsAreServed: the engine class declares no ops")
	}
	for _, op := range opsDeclared {
		if op == "" {
			t.Fatal("engine op set carries an empty op word")
		}
	}

	for _, word := range []string{"podman", "docker"} {
		prov, ok := providerRegistry.ResolveEngine(word)
		if !ok {
			t.Fatalf("ResolveEngine(%q): the class is not wired", word)
		}
		if prov.Class() != ClassEngine {
			t.Fatalf("engine:%s provider class = %v, want ClassEngine", word, prov.Class())
		}

		// The describe op runs Invoke -> container.InvokeEngineOp end to end.
		res, err := prov.Invoke(context.Background(), &Operation{Reserved: word, Op: ops.OpEngineDescribe})
		if err != nil || res == nil {
			t.Fatalf("engine:%s describe via the provider: err=%v res=%v", word, err, res)
		}
		cap, ok := engineDescribeFor(word)
		if !ok {
			t.Fatalf("engineDescribeFor(%q): the class did not serve a capability", word)
		}
		if string(cap.Name) != word || cap.Binary == "" {
			t.Fatalf("engineDescribeFor(%q) = %+v, want Name=%q and a non-empty Binary", word, cap, word)
		}
	}

	// An unknown word must NOT resolve through the class.
	if _, ok := providerRegistry.ResolveEngine("not-an-engine"); ok {
		t.Fatal("ResolveEngine(not-an-engine) resolved — the class is not word-scoped")
	}

	// hostEngineBinary is the production consumer: the host's local-storage probes
	// resolve the engine binary through the class.
	if bin := hostEngineBinary(); bin == "" {
		t.Fatal("hostEngineBinary() returned empty — the class does not drive the host's local probes")
	}
}
