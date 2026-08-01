package check

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
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

// TestConfigStartArgs_AddCandyOmitsTag proves the K5-A item 2 overlay-plans-fix companion bug fix
// (bug 3 of 3, check-pod-overlay's R10): an add_candy: overlay bed's config/start steps must NOT
// pass --tag <base-build-tag> — doing so forces config/start to deploy the un-overlaid base image
// (the pod plugin resolver's explicit-ref-wins contract bypasses the persisted resolved_image lookup),
// silently dropping every add_candy candy from the running container. A non-overlay bed's
// freshness proof is unchanged (still --tag'd) — no regression for the common case.
func TestConfigStartArgs_AddCandyOmitsTag(t *testing.T) {
	const name, tag = "check-pod-overlay", "check-pod-overlay-2026.205.1032"

	configArgs, startArgs := configStartArgs(name, tag, true)
	wantNoTag := []string{"config", name}
	if !equalArgs(configArgs, wantNoTag) {
		t.Errorf("configStartArgs(add_candy=true) config args = %v, want %v (no --tag)", configArgs, wantNoTag)
	}
	wantNoTagStart := []string{"start", name}
	if !equalArgs(startArgs, wantNoTagStart) {
		t.Errorf("configStartArgs(add_candy=true) start args = %v, want %v (no --tag)", startArgs, wantNoTagStart)
	}

	configArgs, startArgs = configStartArgs(name, tag, false)
	wantTagged := []string{"config", name, "--tag", tag}
	if !equalArgs(configArgs, wantTagged) {
		t.Errorf("configStartArgs(add_candy=false) config args = %v, want %v (--tag kept, no regression)", configArgs, wantTagged)
	}
	wantTaggedStart := []string{"start", name, "--tag", tag}
	if !equalArgs(startArgs, wantTaggedStart) {
		t.Errorf("configStartArgs(add_candy=false) start args = %v, want %v (--tag kept, no regression)", startArgs, wantTaggedStart)
	}
}

func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
