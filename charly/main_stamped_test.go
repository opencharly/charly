package main

import (
	"testing"
)

// TestShouldRefuseUnstamped and TestCheckSubcommandIsRun moved to
// candy/plugin-doctor/freshness_stamped_test.go (K5 seam-death: the checks themselves moved to
// verb:freshness-guard, freshness.go). TestVersionCmd_UnstampedReturnsError stays here — it tests
// VersionCmd, the deliberate value/risk EXCEPTION kept core (see main.go's CLI struct comment),
// unrelated to the preflight-phase move.

// TestVersionCmd_UnstampedReturnsError proves `charly version` exits non-zero (returns an error) on an
// UNSTAMPED binary so scripts can gate on it, and stays clean (nil) when stamped (#74).
func TestVersionCmd_UnstampedReturnsError(t *testing.T) {
	saved := BuildCalVer
	defer func() { BuildCalVer = saved }()

	BuildCalVer = "2026.154.0943"
	if err := (&VersionCmd{}).Run(); err != nil {
		t.Errorf("stamped VersionCmd.Run() = %v, want nil", err)
	}
	BuildCalVer = ""
	if err := (&VersionCmd{}).Run(); err == nil {
		t.Error("unstamped VersionCmd.Run() = nil, want a non-nil error (scripts gate on the non-zero exit)")
	}
}
