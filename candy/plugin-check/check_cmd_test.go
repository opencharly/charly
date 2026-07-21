package check

import (
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// TestFailErrorFor proves the R44 exit-classification: a run whose ONLY failures are podman
// container-SETUP infra failures maps to the INFRA exit class (1, a plain error), NEVER
// checks-failed (2, *CheckFailedError); a genuine check failure maps to checks-failed; and a
// run with BOTH surfaces as checks-failed (a real failure dominates). Keyed on the kit infra
// marker the executor stamps. This is the superproject half of the fix that turns a
// store-contention transient from a spurious exit-2 verdict into an honest exit-1 infra error.
func TestFailErrorFor(t *testing.T) {
	pass := kit.StepResult{Result: kit.CheckResult{CheckResult: spec.CheckResult{Status: kit.StatusPass, Message: "exit=0"}}}
	checkFail := kit.StepResult{Result: kit.CheckResult{CheckResult: spec.CheckResult{Status: kit.StatusFail, Message: "exit=1, want 0"}}}
	infraFail := kit.StepResult{Result: kit.CheckResult{CheckResult: spec.CheckResult{Status: kit.StatusFail,
		Message: "execution error: " + kit.ContainerInfraErrMarker + " [creating temporary passwd file]: podman exit=127"}}}
	skip := kit.StepResult{Result: kit.CheckResult{CheckResult: spec.CheckResult{Status: kit.StatusSkip, Message: "excluded"}}}

	isCheckFailed := func(err error) bool {
		var cf *CheckFailedError
		return errors.As(err, &cf)
	}

	t.Run("all pass -> nil", func(t *testing.T) {
		if err := failErrorFor([]kit.StepResult{pass, skip}); err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})

	t.Run("genuine check failure -> CheckFailedError (exit 2)", func(t *testing.T) {
		err := failErrorFor([]kit.StepResult{pass, checkFail})
		if !isCheckFailed(err) {
			t.Fatalf("a real check failure must be checks-failed (exit 2); got %T: %v", err, err)
		}
	})

	t.Run("infra-only -> plain infra error (exit 1), NOT checks-failed", func(t *testing.T) {
		err := failErrorFor([]kit.StepResult{pass, infraFail})
		if err == nil {
			t.Fatal("an infra-only run must still surface an error (never a pass-by-exhaustion)")
		}
		if isCheckFailed(err) {
			t.Fatalf("a container-setup infra failure must NOT be checks-failed (exit 2) — it is the infra exit class (1); got %T", err)
		}
		if !strings.Contains(err.Error(), "infra") {
			t.Errorf("infra error message should name the infra class; got %q", err.Error())
		}
	})

	t.Run("mixed check-fail + infra-fail -> checks-failed (a real failure dominates)", func(t *testing.T) {
		err := failErrorFor([]kit.StepResult{checkFail, infraFail})
		if !isCheckFailed(err) {
			t.Fatalf("a run with a genuine check failure must be checks-failed (exit 2) even alongside infra; got %T", err)
		}
	})
}
