package main

import (
	"testing"
	"time"

	"github.com/opencharly/spec/poll"
)

// TestStepDispatchUsesTheHeavyIdleWindow pins the fix for the CachyOS VM bed's
// deploy: step:system-packages runs a multi-minute pacman install with sparse
// host-visible progress, and the 90s probe no_progress killed it after a single
// reverse leg. Step dispatches must use the heavy per-attempt window; every other
// class keeps the probe window.
func TestStepDispatchUsesTheHeavyIdleWindow(t *testing.T) {
	stepWindow := pluginInvokeNoProgressFor(ClassStep, "")
	if stepWindow != loadedReadiness().PerAttemptHeavy {
		t.Fatalf("pluginInvokeNoProgressFor(ClassStep) = %s, want the heavy window %s", stepWindow, loadedReadiness().PerAttemptHeavy)
	}
	if stepWindow <= poll.ReadinessNoProgressFallback {
		t.Fatalf("the step window %s does not exceed the probe fallback %s — the install would still be killed", stepWindow, poll.ReadinessNoProgressFallback)
	}
	for _, class := range []ProviderClass{ClassVerb, ClassDeployTarget, ClassBuild, ClassCommand} {
		if got := pluginInvokeNoProgressFor(class, ""); got != pluginInvokeNoProgress() {
			t.Fatalf("pluginInvokeNoProgressFor(%s) = %s, want the probe window %s", class, got, pluginInvokeNoProgress())
		}
	}
	if stepWindow < 10*time.Minute {
		t.Fatalf("the step window %s is too small for a multi-minute package install", stepWindow)
	}
}

// TestRebuildDispatchUsesTheHeavyIdleWindow is the discriminator for
// opencharly/charly#1322: `charly update <pod> --build` runs `charly box build`
// INSIDE the deploy plugin, and buildkitd does the work in its own detached daemon,
// so the plugin's tree shows no CPU for minutes. On the probe window that dispatch was
// cancelled mid-build and surfaced as a bare `signal: killed`; with the heavy window
// (CHARLY_PLUGIN_NO_PROGRESS=15m, measured) the same rebuild completed. The window is
// scoped to the OP, so the rest of the deploy class keeps the probe window.
func TestRebuildDispatchUsesTheHeavyIdleWindow(t *testing.T) {
	if got := pluginInvokeNoProgressFor(ClassDeployTarget, opRebuildDispatch); got != loadedReadiness().PerAttemptHeavy {
		t.Fatalf("pluginInvokeNoProgressFor(deploy, rebuild) = %s, want the heavy window %s", got, loadedReadiness().PerAttemptHeavy)
	}
	// The rest of the deploy class is fast and keeps the probe window: a start/stop that
	// stalls must still be caught at 90s, not 15m.
	for _, op := range []string{"config", "start", "stop", "status"} {
		if got := pluginInvokeNoProgressFor(ClassDeployTarget, op); got != pluginInvokeNoProgress() {
			t.Fatalf("pluginInvokeNoProgressFor(deploy, %s) = %s, want the probe window %s", op, got, pluginInvokeNoProgress())
		}
	}
}
