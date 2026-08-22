package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCodeError_ErrorWithErr(t *testing.T) {
	e := &ExitCodeError{Code: 2, Err: errors.New("check failed")}
	if got, want := e.Error(), "check failed"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if err := errors.Unwrap(e); err == nil || err.Error() != "check failed" {
		t.Fatalf("Unwrap() = %v, want the wrapped error", err)
	}
}

func TestExitCodeError_ErrorWithoutErr(t *testing.T) {
	e := &ExitCodeError{Code: 3}
	if got, want := e.Error(), "exit code 3"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if err := errors.Unwrap(e); err != nil {
		t.Fatalf("Unwrap() = %v, want nil", err)
	}
}

func TestExitCodeError_AsDetection(t *testing.T) {
	wrapped := fmt.Errorf("boom: %w", &ExitCodeError{Code: 2, Err: errors.New("nope")})
	var target *ExitCodeError
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As did not detect *ExitCodeError through a wrap")
	}
	if target.Code != 2 {
		t.Fatalf("detected Code = %d, want 2", target.Code)
	}
}

func TestCheckExitCodeConstants(t *testing.T) {
	if CheckFailExitCode != 2 {
		t.Fatalf("CheckFailExitCode = %d, want 2", CheckFailExitCode)
	}
	if CheckSkippedExitCode != 3 {
		t.Fatalf("CheckSkippedExitCode = %d, want 3", CheckSkippedExitCode)
	}
}
