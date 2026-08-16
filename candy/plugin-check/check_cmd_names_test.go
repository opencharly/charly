package check

import (
	"testing"

	"github.com/opencharly/sdk"
)

// check_cmd_names_test.go — the DECLARED-NAME gate for the `charly box feature run` cutover.
//
// Kong never reads a `cmd:"<value>"` tag's VALUE as the command name (sdk/kong_reflect.go documents
// the RDD spike); it falls back to the dash-cased FIELD name. `FeatureBox` declared
// `cmd:"__feature-box"` therefore dispatched as `feature-box`, while candy/plugin-box's bridge
// forwarded `__feature-box` — so `charly box feature run` reached a leaf that did not exist and
// died with `unexpected argument __feature-box`. The names below are a CONTRACT between two
// plugins, which is why they are asserted rather than left to a tag that looks authoritative.
func TestCheckCmdDeclaredNames(t *testing.T) {
	got := map[string]bool{}
	for _, sc := range sdk.KongSubcommands(&CheckCmd{}) {
		got[sc.Name] = true
	}
	// __feature-box is the load-bearing one: candy/plugin-box's dispatchFeature forwards exactly
	// this token. Fails without the fix (the struct yields "feature-box").
	for _, want := range []string{
		"__feature-box",
		"box", "live", "feature", "run",
		"run-local", "sync-credential", "scope", "last-tag", "self-evaluate",
		"list", "report", "note", "list-agent",
	} {
		if !got[want] {
			t.Errorf("CheckCmd declares no subcommand named %q — the name Kong actually dispatches is not the one callers use", want)
		}
	}
	// The pre-fix name must be GONE: leaving both would mean the rename half-landed and the bridge
	// could silently keep working through the wrong leaf (R5).
	if got["feature-box"] {
		t.Error("CheckCmd still declares \"feature-box\" — the pre-fix Kong-derived name survived the rename")
	}
}

// TestCheckCmdFeatureBoxHidden pins the other half of the leaf's contract: it is HIDDEN (never a
// user-facing surface — `charly box feature run` is) but must stay REACHABLE, which is exactly the
// hidden-but-reachable property KongSubcommands carries through to the host grammar.
func TestCheckCmdFeatureBoxHidden(t *testing.T) {
	for _, sc := range sdk.KongSubcommands(&CheckCmd{}) {
		if sc.Name == "__feature-box" {
			if !sc.Hidden {
				t.Fatal("__feature-box is not hidden — an internal bridge leaf must not appear in --help or the MCP tool surface")
			}
			return
		}
	}
	t.Fatal("__feature-box not declared at all")
}
