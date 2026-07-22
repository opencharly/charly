package main

import (
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// TestStepToView_CapturesReverseOps proves the Fork-A capture: stepToView computes each
// step's host-side Reverse() into InstallStepView.ReverseOps, so an out-of-process plugin
// that EXECUTES a plugin-renderable step itself can ECHO them for record-and-replay teardown
// (the Reverse() rule stays ONCE in package main). The deploy-time-stateful kinds carry
// faithful ops once the host has captured their state (EnvFile/PriorEnabled) before
// projecting — exercised here by setting those fields, the same way
// candy/plugin-bundle's prepareReverseState does on the live venue (S3b).
func TestStepToView_CapturesReverseOps(t *testing.T) {
	t.Run("file step → rm-file-system", func(t *testing.T) {
		v := deploykit.StepToView(&deploykit.FileStep{Source: "/tmp/src", Dest: "/etc/marker", CandyName: "x"})
		if len(v.ReverseOps) != 1 {
			t.Fatalf("FileStep view ReverseOps = %d, want 1", len(v.ReverseOps))
		}
		op := v.ReverseOps[0]
		if op.Kind != spec.ReverseOpRmFileSystem || len(op.Targets) != 1 || op.Targets[0] != "/etc/marker" {
			t.Fatalf("FileStep reverse op = %+v, want rm-file-system /etc/marker", op)
		}
	})

	t.Run("shell-hook with EnvFile → remove-envd-file", func(t *testing.T) {
		// EnvFile is set by the host (prepareReverseState) BEFORE projecting; without it
		// ShellHook.Reverse() is nil (the deploy-time-state dependency Fork A captures).
		v := deploykit.StepToView(&deploykit.ShellHookStep{CandyName: "mycandy", EnvFile: "/home/u/.config/opencharly/env.d/mycandy.env"})
		if len(v.ReverseOps) != 1 || v.ReverseOps[0].Kind != spec.ReverseOpRemoveEnvdFile {
			t.Fatalf("ShellHook view ReverseOps = %+v, want one remove-envd-file", v.ReverseOps)
		}
	})

	t.Run("service-packaged with PriorEnabled → restore-enabled recorded", func(t *testing.T) {
		// PriorEnabled is probed on the venue by prepareReverseState; with it set, teardown
		// records BOTH the disable AND the restore-enabled op.
		v := deploykit.StepToView(&deploykit.ServicePackagedStep{Unit: "foo.service", Enable: true, PriorEnabled: true, TargetScope: spec.ScopeSystem})
		var sawRestore bool
		for _, op := range v.ReverseOps {
			if op.Kind == spec.ReverseOpRestoreEnabled {
				sawRestore = true
			}
		}
		if !sawRestore {
			t.Fatalf("ServicePackaged(PriorEnabled) view ReverseOps = %+v, want a restore-enabled op", v.ReverseOps)
		}
	})
}
