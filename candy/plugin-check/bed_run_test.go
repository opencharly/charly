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

func TestRunTaggedImageRefPinsArtifactCheckToBedBuild(t *testing.T) {
	const tag = "check-agent-pod-2026.199.1654"
	if got := runTaggedImageRef("check-agent-box", tag); got != "check-agent-box:"+tag {
		t.Fatalf("runTaggedImageRef() = %q, want the exact per-run build reference", got)
	}
	if got := runTaggedImageRef("check-agent-box", ""); got != "check-agent-box" {
		t.Fatalf("runTaggedImageRef() without tag = %q, want logical image unchanged", got)
	}
}
