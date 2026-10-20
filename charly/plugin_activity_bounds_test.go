package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestApplyCallBoundEnv_SetsBothOverrides — the two env knobs must land on the package vars the
// dispatch path actually reads. Without this, the bounds stay fixed at 10m and a long image build
// is killed (measured: buildkitd logs `context canceled`, buildctl reports `signal: killed`).
func TestApplyCallBoundEnv_SetsBothOverrides(t *testing.T) {
	leafBackup, idleBackup := pluginLeafCapOverride, pluginInvokeNoProgressOverride
	defer func() { pluginLeafCapOverride, pluginInvokeNoProgressOverride = leafBackup, idleBackup }()

	pluginLeafCapOverride, pluginInvokeNoProgressOverride = 0, 0
	env := map[string]string{
		"CHARLY_PLUGIN_LEAF_CAP":    "120m",
		"CHARLY_PLUGIN_NO_PROGRESS": "90m",
	}
	applyCallBoundEnv(func(k string) string { return env[k] })

	// The CONSUMERS are the contract: these are what the dispatch path calls.
	if got := pluginLeafCap(); got != 120*time.Minute {
		t.Errorf("pluginLeafCap() = %v, want the 120m override", got)
	}
	if got := pluginInvokeNoProgress(); got != 90*time.Minute {
		t.Errorf("pluginInvokeNoProgress() = %v, want the 90m override", got)
	}
}

// TestApplyCallBoundEnv_IgnoresJunkAndAbsence — a malformed or absent value must leave the shipped
// default standing, never set a zero bound (which would cancel every call immediately).
func TestApplyCallBoundEnv_IgnoresJunkAndAbsence(t *testing.T) {
	leafBackup, idleBackup := pluginLeafCapOverride, pluginInvokeNoProgressOverride
	defer func() { pluginLeafCapOverride, pluginInvokeNoProgressOverride = leafBackup, idleBackup }()

	for _, env := range []map[string]string{
		{},
		{"CHARLY_PLUGIN_LEAF_CAP": "not-a-duration", "CHARLY_PLUGIN_NO_PROGRESS": "nonsense"},
		{"CHARLY_PLUGIN_LEAF_CAP": "", "CHARLY_PLUGIN_NO_PROGRESS": ""},
	} {
		pluginLeafCapOverride, pluginInvokeNoProgressOverride = 0, 0
		applyCallBoundEnv(func(k string) string { return env[k] })
		if pluginLeafCapOverride != 0 || pluginInvokeNoProgressOverride != 0 {
			t.Errorf("env %v must not set an override, got leaf=%v idle=%v",
				env, pluginLeafCapOverride, pluginInvokeNoProgressOverride)
		}
		if got := pluginLeafCap(); got != pluginLeafCapDefault {
			t.Errorf("env %v changed the leaf default: %v != %v", env, got, pluginLeafCapDefault)
		}
	}
}

// TestPluginLeafCapDefault_IsTheShippedTwoHours — the default is RAISED to 2h, and this
// pins the new shipped value so a silent change has to come with a rationale. The former
// 10m was the #474 hung-plugin guard applied as a TOTAL bound, which killed the work that
// most needs a leaf: a cold cold-cache image build (measured: `box build openwebui` on a
// fresh base digest died at exactly 10m, twice, with `signal: killed` on buildctl).
func TestPluginLeafCapDefault_IsTheShippedTwoHours(t *testing.T) {
	if pluginLeafCapDefault != 2*time.Hour {
		t.Errorf("pluginLeafCapDefault = %v, want the shipped 2h", pluginLeafCapDefault)
	}
}

// TestLeafCapExpiryNamesTheBound — an expired leaf cap must identify ITSELF. The failure it
// used to produce is `context canceled` / `signal: killed` from inside the plugin's
// children, which reads as a crash and names neither the bound nor the knob that raises it.
func TestLeafCapExpiryNamesTheBound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done() // wait on the signal, never a sleep — the deadline has fired by definition here

	err := leafCapErr(ctx, true, "host->plugin build", errors.New("rpc error: code = Canceled"))
	if !errors.Is(err, errPluginLeafCap) {
		t.Fatalf("leaf-cap expiry is not identifiable: %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"plugin leaf cap exceeded", "CHARLY_PLUGIN_LEAF_CAP", "host->plugin build", "2h0m0s"} {
		if !strings.Contains(msg, want) {
			t.Errorf("leaf-cap error %q does not name %q", msg, want)
		}
	}

	// A call that did NOT get the cap must pass through untouched — the wrapper must not
	// claim a bound nobody applied.
	if got := leafCapErr(ctx, false, "host->plugin build", errors.New("boom")); got.Error() != "boom" {
		t.Errorf("uncapped call was rewritten: %v", got)
	}
}
