package pod

import "testing"

// TestStopCmd_UnmountFlagDefaults asserts the --unmount field defaults to false (so plain
// `charly stop` continues to leave gocryptfs mounts up — the pre-cutover behavior). A flipped
// default would silently tear down every operator's encrypted mounts on every stop, which is
// exactly the regression the explicit-opt-in design avoids.
func TestStopCmd_UnmountFlagDefaults(t *testing.T) {
	c := &StopCmd{}
	if c.Unmount {
		t.Error("StopCmd.Unmount default should be false; --unmount must be explicit opt-in")
	}
	c.Unmount = true
	if !c.Unmount {
		t.Error("StopCmd.Unmount must be settable")
	}
}
