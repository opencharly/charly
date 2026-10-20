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
	stepWindow := pluginInvokeNoProgressFor(ClassStep)
	if stepWindow != loadedReadiness().PerAttemptHeavy {
		t.Fatalf("pluginInvokeNoProgressFor(ClassStep) = %s, want the heavy window %s", stepWindow, loadedReadiness().PerAttemptHeavy)
	}
	if stepWindow <= poll.ReadinessNoProgressFallback {
		t.Fatalf("the step window %s does not exceed the probe fallback %s — the install would still be killed", stepWindow, poll.ReadinessNoProgressFallback)
	}
	for _, class := range []ProviderClass{ClassVerb, ClassDeployTarget, ClassBuild, ClassCommand} {
		if got := pluginInvokeNoProgressFor(class); got != pluginInvokeNoProgress() {
			t.Fatalf("pluginInvokeNoProgressFor(%s) = %s, want the probe window %s", class, got, pluginInvokeNoProgress())
		}
	}
	if stepWindow < 10*time.Minute {
		t.Fatalf("the step window %s is too small for a multi-minute package install", stepWindow)
	}
}
