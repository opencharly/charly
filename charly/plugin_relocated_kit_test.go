package main

import (
	"context"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// relocatedVerbCase is one sub-case of a relocated-kit-verb dispatch test: a fakeExecutor
// response (matched command prefix + exit code), the run mode, the authored PluginInput,
// and the CheckStatus the verb must return.
type relocatedVerbCase struct {
	desc        string
	matchPrefix string
	exit        int
	mode        RunMode
	input       map[string]any
	want        CheckStatus
}

// assertRelocatedVerbDispatch proves a check verb relocated to a compiled-in kit candy
// (candy/plugin-<verb>) dispatches through the providerRegistry as a CheckVerbProvider (the
// kitVerbAdapter passing the live *Runner as a kit.CheckContext) and runs its relocated logic
// against a fakeExecutor for each case. Shared by the addr/dns/process relocated-verb dispatch
// tests (R3).
func assertRelocatedVerbDispatch(t *testing.T, verb string, cases []relocatedVerbCase) {
	t.Helper()
	prov, ok := providerRegistry.ResolveVerb(verb)
	if !ok {
		t.Fatalf("%s verb not registered — compiled-in kit candy (candy/plugin-%s) failed", verb, verb)
	}
	cv, ok := prov.(CheckVerbProvider)
	if !ok {
		t.Fatalf("%s provider is not a CheckVerbProvider: %T", verb, prov)
	}
	for _, tc := range cases {
		fe := &fakeExecutor{responses: []fakeResponse{{matchPrefix: tc.matchPrefix, exit: tc.exit}}}
		res := cv.RunVerb(context.Background(), hostVerbResolverFor(fe, tc.mode), &spec.Op{PluginInput: tc.input})
		if res.Status != tc.want {
			t.Fatalf("%s/%s: want %v, got %v: %s", verb, tc.desc, tc.want, res.Status, res.Message)
		}
	}
}
