package check

import (
	"context"
	"fmt"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// TestNewPluginCheckRunner_VerbResolverTracksLiveExec is the regression test for the
// check-k3s-vm SIGSEGV (K1-unblock wave, blocking bug found via a live bed run): pluginVerbResolver
// used to thread a VenueDescriptor computed ONCE at newPluginCheckRunner-construction time (or
// nil, for every venue kind this package didn't bother computing one for — VM/local/group). A
// `command:` (or any detached CheckVerbProvider) step on a venue whose default executor never
// round-tripped therefore crashed on a nil cc.Exec() the instant the reverse-channel
// InvokeProvider call fell back to the calling command's own ambient (nil) executor.
//
// The fix wires the VerbResolver with a BACK-REFERENCE to the constructed *kit.Runner (mirroring
// charly/checkrun.go's hvr.kr = kr) so RunVerb derives the VenueDescriptor fresh from the
// runner's CURRENT Exec() on every call (including one SwapVenue retargeted mid-plan, per
// runner.go's own SwapVenue doc: "mutates the Runner in place so EffectiveEnv + the verb
// dispatch ... see the swapped venue"). This test proves the back-reference exists and observes
// live runner state rather than a frozen snapshot — the actual reverse-channel InvokeProvider
// round trip needs a live host process a unit test cannot construct (the identical constraint
// agent.go's own test-coverage note documents for resolveAgentSpec).
func TestNewPluginCheckRunner_VerbResolverTracksLiveExec(t *testing.T) {
	wantExec := &kit.SSHExecutor{Host: "charly-k1-regression-test", ConnectTimeout: 10}

	runner := newPluginCheckRunner(nil, context.Background(), spec.CheckEnv{Mode: "live"}, kit.RunnerConfig{
		Exec: wantExec,
		Mode: kit.ModeLive,
	})

	pvr, ok := runner.Verbs().(*pluginVerbResolver)
	if !ok {
		t.Fatalf("runner.Verbs() = %T, want *pluginVerbResolver", runner.Verbs())
	}
	if pvr.kr == nil {
		t.Fatal("pluginVerbResolver.kr is nil — RunVerb can never derive a live VenueDescriptor (the exact regression: falls back to the caller's nil ambient executor)")
	}
	if pvr.kr != runner {
		t.Error("pluginVerbResolver.kr does not point at the SAME *kit.Runner newPluginCheckRunner returned — a copy would desync from any later SwapVenue mutation")
	}
	if pvr.kr.Exec() != kit.Executor(wantExec) {
		t.Errorf("pvr.kr.Exec() = %#v, want the SAME executor passed via RunnerConfig.Exec (%#v)", pvr.kr.Exec(), wantExec)
	}

	// DescriptorFromExecutor must round-trip this exact executor kind (a plain *kit.SSHExecutor,
	// the shape every non-dotted VM target uses) — this is the OTHER half of the regression: a
	// nil kr, or a kr.Exec() a descriptor can't derive from, both leave InvokeProvider's
	// VenueDescriptor unset.
	if d := kit.DescriptorFromExecutor(pvr.kr.Exec().(spec.DeployExecutor)); d.Kind != "ssh" {
		t.Errorf("DescriptorFromExecutor(pvr.kr.Exec()) = %+v, want Kind=%q", d, "ssh")
	}
}

// TestPluginSnapshotCheckEnv_ReflectsSwapVenue is the regression test for the #55 W3
// check-cross-pod-cdp bed RCA: a GROUP bed's runner starts with Box=<group-root-name> (a pure
// group has no container of its own) and SwapVenue retargets Box to the OWNING MEMBER for every
// step (a group root can carry no direct plan steps). The former pluginVerbResolver.env was a
// STATIC field frozen at construction time — the wire envelope RunVerb sends to
// charly/plugin_dispatch_reverse.go's InvokeProvider handler (which decodes it to construct the
// detached CheckContext serving cc.ResolveEndpoint et al.) kept reporting the group's own bare
// name even after SwapVenue moved the runner to a real member — "container
// charly-check-cross-pod-cdp is not running" for a bed whose actual cdp subject is the chrome
// member. This proves pluginSnapshotCheckEnv(pvr.kr) — the value RunVerb now marshals on every
// call — tracks a mid-plan SwapVenue rather than the frozen construction-time snapshot, sibling to
// TestNewPluginCheckRunner_VerbResolverTracksLiveExec's identical proof for the Exec() half of the
// same staleness class.
func TestPluginSnapshotCheckEnv_ReflectsSwapVenue(t *testing.T) {
	const groupRoot = "check-cross-pod-cdp"
	chromeExec := &kit.SSHExecutor{Host: "charly-" + groupRoot + "-chrome-regression", ConnectTimeout: 10}
	resolver := kit.VenueResolver(func(venue string) (kit.Executor, map[string]string, bool, error) {
		if venue == "chrome" {
			return chromeExec, nil, true, nil
		}
		return nil, nil, false, fmt.Errorf("unexpected venue %q", venue)
	})

	runner := newPluginCheckRunner(nil, context.Background(), spec.CheckEnv{
		Mode: "live",
		Box:  groupRoot,
	}, kit.RunnerConfig{
		Mode:           kit.ModeLive,
		Box:            groupRoot,
		TargetResolver: resolver,
	})
	pvr, ok := runner.Verbs().(*pluginVerbResolver)
	if !ok {
		t.Fatalf("runner.Verbs() = %T, want *pluginVerbResolver", runner.Verbs())
	}

	if before := pluginSnapshotCheckEnv(pvr.kr); before.Box != groupRoot {
		t.Fatalf("pre-swap pluginSnapshotCheckEnv(pvr.kr).Box = %q, want the group root %q", before.Box, groupRoot)
	}

	restore, failReason := runner.SwapVenue(&spec.Op{Venue: "chrome"})
	if failReason != "" {
		t.Fatalf("SwapVenue(chrome) failed: %s", failReason)
	}
	defer restore()

	after := pluginSnapshotCheckEnv(pvr.kr)
	if after.Box != "chrome" {
		t.Errorf("post-swap pluginSnapshotCheckEnv(pvr.kr).Box = %q, want the swapped member %q — RunVerb's wire envelope must track SwapVenue, not the group root frozen at construction", after.Box, "chrome")
	}
}
