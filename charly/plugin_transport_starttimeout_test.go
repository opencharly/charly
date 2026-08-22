package main

import (
	"os/exec"
	"testing"
	"time"

	"github.com/opencharly/spec/poll"
)

// TestLocalPluginClientConfig_StartTimeoutIsReadinessBound guards the exec→handshake bound. With
// StartTimeout left at zero, go-plugin substitutes its own unconfigurable 60s (client.go:398), and
// a healthy child that starves past it under parallel load is SIGKILLed mid-handshake — the "plugin
// did not connect" failures the 32-bed roster produced during its OOM storm. The bound must be the
// project's resolved readiness per_attempt, so raising defaults.readiness.per_attempt for a heavy
// roster moves it too.
func TestLocalPluginClientConfig_StartTimeoutIsReadinessBound(t *testing.T) {
	cfg := localPluginClientConfig(exec.Command("/bin/true"))
	if cfg.StartTimeout == 0 {
		t.Fatal("StartTimeout is zero — go-plugin would silently apply its own 60s default, the bound this fix exists to replace")
	}
	want := loadedReadiness().PerAttempt
	if cfg.StartTimeout != want {
		t.Errorf("StartTimeout = %s, want the resolved readiness per_attempt (%s)", cfg.StartTimeout, want)
	}
	if cfg.StartTimeout <= time.Minute {
		t.Errorf("StartTimeout = %s, want strictly more than go-plugin's 60s default — a bound at or below it fixes nothing", cfg.StartTimeout)
	}
}

// TestLocalPluginClientConfig_StartTimeoutHonorsOperatorOverride proves the bound is genuinely
// configuration-driven rather than a constant that merely happens to match the fallback: an
// operator raising CHARLY_READINESS_PER_ATTEMPT for a heavy roster must move this bound with it.
func TestLocalPluginClientConfig_StartTimeoutHonorsOperatorOverride(t *testing.T) {
	// loadedReadiness caches through a sync.Once, so resolve the override the same way it does
	// rather than fighting the cache — this asserts the field the config feeds, not the cache.
	t.Setenv("CHARLY_READINESS_PER_ATTEMPT", "7m")
	rr, err := poll.ResolveReadiness(nil)
	if err != nil {
		t.Fatalf("ResolveReadiness() error = %v", err)
	}
	if rr.PerAttempt != 7*time.Minute {
		t.Fatalf("resolved per_attempt = %s, want 7m — the env override the plugin-spawn bound rides on is not being honored", rr.PerAttempt)
	}
}
