package main

import (
	"testing"
	"time"
)

// TestPluginInvokeTimeoutFromEnv pins the CHARLY_PLUGIN_INVOKE_TIMEOUT override:
// a long host->plugin call (a full R10 `[update]` reinstall) must be raisable
// without a rebuild; a bad value keeps the 10m default.
func TestPluginInvokeTimeoutFromEnv(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 10 * time.Minute},          // unset -> default
		{"30m", 30 * time.Minute},       // override
		{"1h", time.Hour},               // override
		{"-5m", 10 * time.Minute},       // non-positive -> default
		{"not-a-dur", 10 * time.Minute}, // unparseable -> default
	}
	for _, c := range cases {
		t.Setenv(PluginInvokeTimeoutEnv, c.in)
		if got := pluginInvokeTimeoutFromEnv(); got != c.want {
			t.Errorf("pluginInvokeTimeoutFromEnv() with %q = %s, want %s", c.in, got, c.want)
		}
	}
}
