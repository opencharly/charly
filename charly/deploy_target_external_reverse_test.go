package main

import (
	"testing"

	"github.com/opencharly/charly/charly/spec"
)

// TestExternalDeploy_FillsPackageRemoveUninstallCmdOnRecord proves the C19
// latent-drop fix: fillReverseUninstallCmds runs HOST-SIDE on an external deploy's
// recorded ReverseOps BEFORE they are ledger-persisted (recordDeploy). The aur
// builder (kit.BuilderReverse) echoes back a ReverseOpPackageRemove with an EMPTY
// UninstallCmd, deferring to this host render (the host has the DistroConfig; the
// out-of-process plugin does not). Before the fix the fill call — previously in the
// deleted in-proc local/vm deploy targets — was never relocated into the external
// deploy record path, so the persisted op stayed empty and `charly bundle del`
// teardown failed loudly (reversePackageRemove errors on an empty command).
//
// This test FAILS without the fillReverseUninstallCmds call in recordDeploy: the
// persisted UninstallCmd would be "" instead of the rendered pacman -Rs command.
// The live aur-teardown path is the SAME runReverseOps → reversePackageRemove
// already exercised by check-exampledeploy's localpkg (pacman -U) teardown.
func TestExternalDeploy_FillsPackageRemoveUninstallCmdOnRecord(t *testing.T) {
	// The real embedded build vocabulary — its pac format carries the
	// uninstall_template the filler renders (pacman -Rs …).
	dc, _, _, err := LoadBuildConfigForBox(repoRootDir(t))
	if err != nil {
		t.Fatalf("LoadBuildConfigForBox: %v", err)
	}

	paths := withTempLedger(t)
	tgt := &externalDeployTarget{
		name:  "check-aur-local",
		prov:  &grpcProvider{word: "local"},
		paths: paths,
		// The SAME DistroConfig the deploy compile used (Add sets this from the
		// DeployContext) — the datum the plugin cannot reach across the boundary.
		build: buildEngineContext{DistroCfg: dc},
	}

	// The reply an aur-builder deploy walk echoes back: a ReverseOpPackageRemove
	// with an EMPTY UninstallCmd (kit.BuilderReverse names only Kind/Format/Targets/
	// Scope, deferring the host render).
	reply := spec.DeployReply{
		Record: spec.DeployReplyRecord{Candy: "chrome", Version: "2026.1.1"},
		ReverseOps: []spec.ReverseOp{{
			Kind:    spec.ReverseOpPackageRemove,
			Format:  "pac",
			Targets: []string{"google-chrome"},
			Scope:   spec.ScopeSystem,
			// UninstallCmd intentionally empty — the exact latent-drop condition.
		}},
	}

	if err := tgt.recordDeploy(reply); err != nil {
		t.Fatalf("recordDeploy: %v", err)
	}

	rec, err := ReadCandyRecord(paths, "chrome")
	if err != nil || rec == nil {
		t.Fatalf("ReadCandyRecord: %v / %+v", err, rec)
	}
	if len(rec.ReverseOps) != 1 {
		t.Fatalf("recorded ReverseOps = %d, want 1: %+v", len(rec.ReverseOps), rec.ReverseOps)
	}
	got := rec.ReverseOps[0].UninstallCmd
	want := "pacman -Rs --noconfirm google-chrome"
	if got != want {
		t.Fatalf("persisted UninstallCmd = %q, want %q "+
			"(fillReverseUninstallCmds did not run on record — the C19 latent drop)", got, want)
	}
}
