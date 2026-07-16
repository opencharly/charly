package main

import (
	"github.com/opencharly/sdk/spec"
	"testing"
)

// The E3 perf go/no-go gate, locked as a test (CLAUDE.md RDD; the
// EVERY-KIND-IS-A-PLUGIN plan's "E3 perf-RDD spike — the go/no-go gate for the
// whole vision"). The architecture's load-bearing invariant: a BUILT-IN provider
// dispatches through its typed fast path (CheckVerbProvider.RunVerb /
// KindProvider.DecodeNode / DeployTargetProvider.ResolveTarget / StepProvider.Emit*),
// which NEVER marshals the Op into the serializable Invoke
// envelope — the JSON envelope (marshalJSON, provider.go) is paid ONLY out-of-process
// (provider_checkenv.go: a CheckVerbProvider takes RunVerb; only a non-CheckVerbProvider
// out-of-proc plugin falls through to invokeVerbProvider's marshalJSON). If a builtin
// ever stopped taking the typed branch, every in-proc op would pay the hop — the exact
// regression the spike gated against. The verb class is the canonical, most-exercised
// fork; every other class (kind/deploy/step/builder) follows the identical
// typed-builtin / serializable-external split (provider.go). These tests FAIL on
// regression; the benchmarks quantify the envelope tax the typed path avoids.

// benchOp is a representative goss-verb Op (the file verb's authored shape).
func benchOp() *spec.Op {
	return &spec.Op{
		Plugin:      "file",
		PluginInput: map[string]any{"file": "/etc/os-release", "mode": "0644"},
		Content:     "x\n",
	}
}

// TestPerfGate_BuiltinVerbsSkipEnvelope is the GO/NO-GO ASSERTION: every
// representative builtin verb resolves to a CheckVerbProvider, so the dispatch
// fork takes the typed RunVerb branch and NEVER reaches invokeVerbProvider's
// marshalJSON. A builtin that stopped implementing CheckVerbProvider would
// silently pay the JSON envelope on every in-proc op — this gate catches it.
func TestPerfGate_BuiltinVerbsSkipEnvelope(t *testing.T) {
	for _, word := range []string{"file", "command", "package", "service"} {
		prov, ok := providerRegistry.ResolveVerb(word)
		if !ok {
			t.Fatalf("builtin verb %q is not registered", word)
		}
		if _, ok := prov.(CheckVerbProvider); !ok {
			t.Fatalf("builtin verb %q is NOT a CheckVerbProvider — it would fall through to the "+
				"JSON Invoke envelope (invokeVerbProvider/marshalJSON) on every in-proc op "+
				"(E3 perf go/no-go regression)", word)
		}
	}
}

// TestPerfGate_TypedDispatchIsZeroAllocVsEnvelopeTax proves the in-proc dispatch
// DECISION (the `prov.(CheckVerbProvider)` type assertion that selects the typed
// path) allocates nothing, while the envelope's marshalJSON allocates — so the
// builtin's avoidance of the envelope is a genuine, measurable saving, not a
// rounding error. This is the alloc dimension of the go/no-go.
func TestPerfGate_TypedDispatchIsZeroAllocVsEnvelopeTax(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("file")
	if !ok {
		t.Fatal("builtin verb \"file\" is not registered")
	}
	forkAllocs := testing.AllocsPerRun(1000, func() {
		if _, ok := prov.(CheckVerbProvider); !ok {
			t.Fatal("file verb unexpectedly not a CheckVerbProvider")
		}
	})
	if forkAllocs != 0 {
		t.Fatalf("typed dispatch fork allocated %v (want 0) — the in-proc builtin path must be "+
			"envelope-alloc-free", forkAllocs)
	}
	op := benchOp()
	marshalAllocs := testing.AllocsPerRun(1000, func() {
		if _, err := marshalJSON(op); err != nil {
			t.Fatal(err)
		}
	})
	if marshalAllocs == 0 {
		t.Fatal("envelope marshalJSON allocated 0 — expected a non-zero JSON-envelope tax that the " +
			"typed builtin path avoids (the go/no-go has no saving to protect if this is 0)")
	}
	t.Logf("E3 perf gate: typed dispatch fork = %v allocs (0 ✓); envelope marshalJSON = %v allocs/op "+
		"(paid only out-of-process)", forkAllocs, marshalAllocs)
}

// BenchmarkVerbEnvelopeMarshal quantifies the per-op envelope tax — the cost a
// builtin skips and an out-of-process plugin pays once per Invoke.
func BenchmarkVerbEnvelopeMarshal(b *testing.B) {
	op := benchOp()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := marshalJSON(op); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkVerbTypedDispatchFork quantifies the in-proc dispatch decision (the
// type assertion that selects the typed RunVerb path) — the builtin hot path's
// per-op overhead above running the probe itself.
func BenchmarkVerbTypedDispatchFork(b *testing.B) {
	prov, ok := providerRegistry.ResolveVerb("file")
	if !ok {
		b.Fatal("builtin verb \"file\" is not registered")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := prov.(CheckVerbProvider); !ok {
			b.Fatal("file verb unexpectedly not a CheckVerbProvider")
		}
	}
}
