package main

import "testing"

// TestPluginPrimaries_ParseTimeParity — the frozen 11-entry shorthand table expectation, now a
// PARITY ASSERTION (parser consolidation F2.6): the parse-time desugar set for the 11
// live-container verbs must stay deterministically {word: method}. The table is seeded at init
// from the embedded charly.yml verb_primaries: declaration (the compiled-in DATA source) and
// re-registered by each connected provider's served primary; a drift between the declared /
// served universe and THIS expectation fails here — the deleted literal is never silently
// re-introduced, and any change to the platform-verb primary convention must update both the
// embedded declaration and this assertion in lockstep.
func TestPluginPrimaries_ParseTimeParity(t *testing.T) {
	expected := map[string]string{
		"cdp": "method", "wl": "method", "dbus": "method", "vnc": "method",
		"mcp": "method", "record": "method", "spice": "method", "libvirt": "method",
		"kube": "method", "adb": "method", "appium": "method",
	}
	for word, want := range expected {
		got, ok := pluginPrimaryFor(word)
		if !ok || got != want {
			t.Errorf("parse-time primary for %q = %q (present=%v), want %q — the live-container shorthand determinism broke; update the embedded verb_primaries declaration and this assertion together", word, got, ok, want)
		}
	}
}
