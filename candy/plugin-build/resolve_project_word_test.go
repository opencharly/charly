package build

import (
	"context"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// resolve_project_word_test.go — coverage for the `build:project` word (#55 step3 unit 3b),
// relocated from the deleted charly/box_list_test.go's TestResolvedProject_NoProjectIsEmptyNotError
// (the empty-project contract) and charly/resolved_project_host_test.go's
// TestResolvedProject_SeamRoundTrip (the seam-consumer round trip lived host-side; the seam itself
// is now this word). The full-project scan/vocab/box-resolve path is exercised live by the R10
// exploratory run (box inspect / status / check-project / fleet resolve against a real project) —
// unit-testing it here would require faking the entire `loader-*`/`buildengine-*`/`kind:*` HostBuild
// leg family this word's LoadUnified/scan/vocab calls dial, which the R10 bed proves end-to-end
// instead (cutover-policy: pick the gate that exercises the change).

// TestResolveProjectEnvelope_NoProjectIsEmptyNotError proves a project-less directory resolves to
// an EMPTY envelope (nil boxes/candies, no error) rather than a hard "no charly.yml found" failure
// — the charly-mcp box.list.boxes contract (the MCP server runs the tool in CHARLY_PROJECT_DIR
// before any charly.yml exists, so `box list boxes` must exit 0 with no output). The executor is
// backed by a nil ExecutorServiceClient: loaderkit.LoadUnified must resolve "no project" WITHOUT
// ever dialing a host leg for a directory with no charly.yml at all, so any unexpected HostBuild/
// InvokeProvider call panics immediately on the nil client — a stronger assertion than a working
// stub would give.
func TestResolveProjectEnvelope_NoProjectIsEmptyNotError(t *testing.T) {
	empty := t.TempDir()
	ex := sdk.NewInProcExecutor(nil)

	rp, err := resolveProjectEnvelope(context.Background(), ex, spec.ResolvedProjectRequest{Dir: empty})
	if err != nil {
		t.Fatalf("resolveProjectEnvelope on a project-less dir must not error (empty envelope), got: %v", err)
	}
	if len(rp.Boxes) != 0 || len(rp.Candies) != 0 {
		t.Fatalf("project-less envelope must be empty; got %d boxes, %d candies", len(rp.Boxes), len(rp.Candies))
	}
}
