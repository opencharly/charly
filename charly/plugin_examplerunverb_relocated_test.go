package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// TestRelocatedExamplerunverbVerb_DispatchesViaKit proves the reference host-coupled
// `examplerunverb` verb — relocated to candy/plugin-examplerunverb (a compiled-in kit
// candy) — dispatches through the providerRegistry as a CheckVerbProvider (the
// kitVerbAdapter passing the live *Runner as a kit.CheckContext) and echoes the marker
// + the live run mode read off the context.
func TestRelocatedExamplerunverbVerb_DispatchesViaKit(t *testing.T) {
	prov, ok := providerRegistry.ResolveVerb("examplerunverb")
	if !ok {
		t.Fatal("examplerunverb not registered — compiled-in kit candy (candy/plugin-examplerunverb) failed")
	}
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("examplerunverb provider is not a CheckVerbProvider: %T", prov)
	}
	res := cv.RunVerb(context.Background(), hostVerbResolverFor(nil, RunModeLive),
		&spec.Op{PluginInput: map[string]any{"marker": "runverb-xyz"}})
	if res.Status != TestPass {
		t.Fatalf("want pass, got %v: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "runverb-xyz") || !strings.Contains(res.Message, "mode=live") {
		t.Fatalf("message %q missing marker or live mode (proves it read the live CheckContext)", res.Message)
	}
}
