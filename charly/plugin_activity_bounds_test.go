package main

import (
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

// TestPluginLeafCapDefault_IsTheShippedTenMinutes — the default is deliberately NOT raised by this
// change: operators opt in, the binary keeps its previous behaviour.
func TestPluginLeafCapDefault_IsTheShippedTenMinutes(t *testing.T) {
	if pluginLeafCapDefault != 10*time.Minute {
		t.Errorf("pluginLeafCapDefault = %v, want the shipped 10m", pluginLeafCapDefault)
	}
}
