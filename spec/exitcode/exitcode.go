// Package exitcode (github.com/opencharly/spec/exitcode, #55 import-purity) holds the
// process exit-code contract a command plugin's Invoke(OpRun) returns to the host
// and the check-command 0/1/2/3 convention single-sourced here so both the host
// (main()'s exit mapping + `charly box feature run`) and the check-engine candy
// reference ONE contract. It is a fabric slice of the spec contract module — pure
// Go constants + a tiny error wrapper, no heavy deps (#55 Rule 2) — relocated from
// the github.com/opencharly/sdk root package. charly core imports this slice
// INSTEAD of the sdk root; the sdk root keeps a thin re-export during cutover then
// is deleted.
package exitcode

import "fmt"

// ExitCodeError carries a specific PROCESS exit code from a command plugin's Invoke(OpRun) back to
// the host, which maps it to os.Exit(Code). A compiled-in command candy returns its error verbatim
// through the in-proc dispatch, but the host cannot classify the plugin's OWN error TYPES across the
// module boundary — so a command that must set a NON-1 exit code (the check 0/1/2/3 convention)
// wraps its failure in *ExitCodeError, which the host detects with errors.As and honors as the exit
// status. Code 0 falls back to the host's default error handling (no special code).
type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("exit code %d", e.Code)
}

func (e *ExitCodeError) Unwrap() error { return e.Err }

// The check-command exit-code convention (goss/pytest 0/1/2/3), single-sourced here so both the HOST
// (main()'s exit mapping + `charly box feature run`) and candy/plugin-check reference ONE contract:
//
//	0  all checks passed
//	1  infra / usage error — no pass/fail verdict was produced (the host default)
//	2  the check RAN and one or more checks FAILED
//	3  the bed was SKIPPED (a required host prerequisite — e.g. a GPU — is absent)
const (
	CheckFailExitCode    = 2
	CheckSkippedExitCode = 3
)
