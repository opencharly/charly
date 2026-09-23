package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/ops"
	"github.com/opencharly/spec/spec"
)

// engine_provider.go — the compiled-in servant of the `engine` provider class
// (spec/schema/engine.cue). It answers every engine op by delegating to the pure
// body in spec/container.InvokeEngineOp, so the op behavior is written ONCE and
// served from compiled-in (this) or out-of-process (a future plugin-nerdctl)
// placement identically.
//
// WHY A DEDICATED FILE (the externalizable dedicated-provider pattern, see
// registry_bootstrap.go): the engine provider carries NO authored input schema of
// its own (its envelopes live in the base schema/spec), so it registers from a
// package-var initializer via RegisterBuiltinProvider and is intentionally absent
// from both builtinProviderInstances and the `providers:` manifest. It is
// compiled in for the same bootstrap reasoning as loader/refs (the engine is
// needed early), and it is consumed today by engine_provider_test.go's wiring
// proof; the host's engine SELECTION still reads the capability table directly,
// and routing those call sites through this class is the remaining Phase-1 unit
// (opencharly/charly#633).
//
// podman and docker are compiled in here; nerdctl is the out-of-process
// engine:nerdctl plugin (a project that references it connects it by word). All
// three serve the SAME op bodies, because the bodies are engine-word-keyed data +
// one dispatch in spec/container.

// engineProvider serves the engine class for ONE engine word. The word is both
// the registry key ("engine:podman") and the fallback engine when a request omits
// its own `engine` field.
type engineProvider struct {
	word string
}

func (p engineProvider) Reserved() string     { return p.word }
func (p engineProvider) Class() ProviderClass { return ClassEngine }

// Invoke dispatches one engine op through the pure fabric servant. The engine
// word the request carries (or this provider's own) selects the capability row;
// the servant validates the word and errors on an unknown one.
func (p engineProvider) Invoke(_ context.Context, op *Operation) (*Result, error) {
	if op == nil {
		return nil, fmt.Errorf("engine:%s: nil operation", p.word)
	}
	out, err := container.InvokeEngineOp(p.word, op.Op, json.RawMessage(op.Params))
	if err != nil {
		return nil, err
	}
	return &Result{JSON: out}, nil
}

// The compiled-in engine providers, registered at package init (the dedicated-
// provider pattern). Each answers the full engine op set (container.EngineOps).
func init() {
	for _, word := range []string{"podman", "docker"} {
		RegisterBuiltinProvider(engineProvider{word: word})
	}
}

// engineProviderOpsAreServed is the self-test hook used by the test suite to
// assert the declared class ops are answered — mirrored from
// container.EngineOps so a new op cannot ship without a servant.
var engineProviderOpsAreServed = container.EngineOps

// engineDescribeFor resolves an engine capability through the CLASS (registry)
// rather than the data table directly. It is the typed accessor the host uses to
// ask the engine PROVIDER (not the data table) for a capability, and it is
// exercised by engine_provider_test.go's wiring proof.
func engineDescribeFor(engine string) (spec.EngineCapability, bool) {
	prov, ok := providerRegistry.ResolveEngine(engine)
	if !ok {
		return spec.EngineCapability{}, false
	}
	res, err := prov.Invoke(context.Background(), &Operation{Reserved: engine, Op: ops.OpEngineDescribe})
	if err != nil || res == nil {
		return spec.EngineCapability{}, false
	}
	var reply spec.EngineDescribeReply
	if err := json.Unmarshal(res.JSON, &reply); err != nil || reply.Capability == nil {
		return spec.EngineCapability{}, false
	}
	return *reply.Capability, true
}

// hostEngineBinary returns the binary the host drives its OWN local container
// storage with, resolved through the engine PROVIDER CLASS (Registry.ResolveEngine
// -> provider.Invoke -> container.InvokeEngineOp), never the data table. The
// host's local-storage probes (the overlay base-image resolution) call it, so the
// class drives a production path. Falls back to "podman" if the class is somehow
// unwired, preserving the former behavior.
func hostEngineBinary() string {
	if cap, ok := engineDescribeFor("podman"); ok && cap.Binary != "" {
		return cap.Binary
	}
	return "podman"
}
