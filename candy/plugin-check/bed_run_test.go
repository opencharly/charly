package check

import (
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

func TestCliStepLogPreservesSpawnError(t *testing.T) {
	got := cliStepLog(spec.CliReply{ExitCode: -1, Error: "fork/exec /missing/charly: no such file or directory"})
	if !strings.Contains(got, "/missing/charly") || !strings.Contains(got, "no such file") {
		t.Fatalf("cliStepLog() = %q, want executable path and OS error", got)
	}
}
