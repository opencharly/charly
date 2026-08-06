package fleet

import (
	"io"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/opencharly/spec/spec"
)

// TestFleetDelArgv_KongAccepts is the real-Kong guard for the helper every programmatic
// teardown builds its command through. sdk/deploykit's stub-based
// TestTearDownMembers_RoutingAndOrder asserts arg STRINGS without ever invoking Kong, so it
// cannot catch a flag the binary rejects — which is exactly how `--yes` (and `--force` at the
// ephemeral / reap call sites) shipped while silently aborting teardown at arg-parse and
// leaking the resource.
//
// It lived in charly/fleet_members_test.go against a hand-copied stub of this grammar,
// because a core unit test cannot import a separate module. The grammar's owner is this
// plugin, so the guard now binds the REAL FleetDelCmd — no stub to drift out of step.
func TestFleetDelArgv_KongAccepts(t *testing.T) {
	type fleetGrammar struct {
		Fleet struct {
			Del FleetDelCmd `cmd:""`
		} `cmd:""`
	}
	parse := func(args ...string) error {
		var g fleetGrammar
		k, err := kong.New(&g, kong.Name("charly"), kong.Exit(func(int) {}), kong.Writers(io.Discard, io.Discard))
		if err != nil {
			t.Fatalf("kong.New: %v", err)
		}
		_, err = k.Parse(args)
		return err
	}
	if err := parse(spec.FleetDelArgv("x")...); err != nil {
		t.Errorf("spec.FleetDelArgv produced args `charly fleet del` rejects: %v (args=%v)", err, spec.FleetDelArgv("x"))
	}
	if err := parse("fleet", "del", "x", "-y"); err != nil {
		t.Errorf("`charly fleet del -y` should be accepted, got: %v", err)
	}
	// The two flags wrongly used at call sites MUST be rejected (regression guard).
	for _, bad := range []string{"--yes", "--force"} {
		if err := parse("fleet", "del", "x", bad); err == nil {
			t.Errorf("`charly fleet del %s` must be REJECTED by Kong (it silently aborted teardown)", bad)
		}
	}
}
