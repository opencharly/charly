package main

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// TestPluginDeployTarget_ApplyParentExecOverride is the regression test for the FIX ROUND bug
// (R10 bed-found, S3b follow-up): every PLAIN vm-class deploy's NESTED non-lifecycle child (a
// `local:`/`android:`/`k8s:` deploy under a vm/pod, tree position — e.g. check-substrate's
// check-substrate-member, check-group's check-group-member, check-builder-vm's
// check-builder-member) silently ran its plan/step walk on the OPERATOR'S HOST instead of the
// parent venue, because pluginDeployTarget.Add never restored the pre-S3b
// externalDeployTarget.apply's `t.exec = opts.ParentExec` swap. This test asserts BOTH halves of
// the fix directly, without needing a live plugin round-trip: t.exec is mutated to the LIVE
// parent executor, AND the returned venue_json describes that SAME executor — so a future
// refactor that restores only one half (e.g. sets t.exec but forgets the wire-safe descriptor, or
// vice versa) fails this test immediately instead of surfacing as a silent host-execution bug
// only a live disposable bed catches.
func TestPluginDeployTarget_ApplyParentExecOverride(t *testing.T) {
	guestSSH := &kit.SSHExecutor{User: "arch", Host: "charly-eval-vm", Port: 2222, ConnectTimeout: 10}

	t.Run("non-lifecycle nested child swaps to the parent executor", func(t *testing.T) {
		tgt := &pluginDeployTarget{hasLifecycle: false, exec: kit.ShellExecutor{}}
		venueJSON := tgt.applyParentExecOverride(deploykit.EmitOpts{ParentExec: guestSSH})

		if tgt.exec != deploykit.DeployExecutor(guestSSH) {
			t.Fatalf("t.exec = %#v (%T), want the live ParentExec value %#v unchanged — a nested "+
				"child's plan/step walk (and every RunHostStep/RunSystem/RunUser reverse leg this "+
				"dispatch threads) must run against the PARENT venue, not the ResolveTarget-time "+
				"host/ssh default this field started as", tgt.exec, tgt.exec, guestSSH)
		}

		if len(venueJSON) == 0 {
			t.Fatalf("applyParentExecOverride returned empty venue_json for a non-nil ParentExec — " +
				"candy/plugin-bundle's resolveRootExecutor has nothing to re-materialize from and " +
				"silently falls back to deploykit.RootExecutorForDeployNode(req.Node), which for a " +
				"nested child (no host: field of its own) resolves the OPERATOR'S HOST — exactly " +
				"the regression this fix closes")
		}
		var got spec.VenueDescriptor
		if err := json.Unmarshal(venueJSON, &got); err != nil {
			t.Fatalf("venue_json does not decode as spec.VenueDescriptor: %v (raw=%s)", err, venueJSON)
		}
		want := spec.VenueDescriptor{Kind: "ssh", User: "arch", Host: "charly-eval-vm", Port: 2222, ConnectTimeout: 10}
		if got.Kind != want.Kind || got.User != want.User || got.Host != want.Host ||
			got.Port != want.Port || got.ConnectTimeout != want.ConnectTimeout || len(got.Args) != 0 {
			t.Fatalf("venue_json descriptor = %+v, want %+v (must describe the GUEST venue, not a "+
				"shell/empty descriptor that re-materializes to the host)", got, want)
		}

		// Round-trip through the real inverse (kit.VenueFromDescriptor) to prove
		// candy/plugin-bundle's resolveRootExecutor would ACTUALLY re-materialize the guest, not
		// merely that the intermediate JSON looks right.
		reExec, err := kit.VenueFromDescriptor(got)
		if err != nil {
			t.Fatalf("kit.VenueFromDescriptor(%+v): %v", got, err)
		}
		reSSH, ok := reExec.(*kit.SSHExecutor)
		if !ok {
			t.Fatalf("re-materialized executor = %T, want *kit.SSHExecutor (the guest)", reExec)
		}
		if reSSH.Host != guestSSH.Host || reSSH.Port != guestSSH.Port || reSSH.User != guestSSH.User {
			t.Fatalf("re-materialized executor = %+v, want a guest connection matching %+v", reSSH, guestSSH)
		}
	})

	t.Run("lifecycle substrate (vm/pod) is untouched — it composes its own venue in PrepareVenue", func(t *testing.T) {
		hostExec := kit.ShellExecutor{}
		tgt := &pluginDeployTarget{hasLifecycle: true, exec: hostExec}
		venueJSON := tgt.applyParentExecOverride(deploykit.EmitOpts{ParentExec: guestSSH})

		if venueJSON != nil {
			t.Fatalf("venue_json = %s, want nil — a lifecycle substrate (vm/pod) must run its OWN "+
				"PrepareVenue, never have its venue pre-empted by an ancestor's ParentExec", venueJSON)
		}
		if tgt.exec != deploykit.DeployExecutor(hostExec) {
			t.Fatalf("t.exec = %#v, want unchanged %#v — the lifecycle branch must not mutate t.exec "+
				"at all (PrepareVenue is the sole venue authority for vm/pod)", tgt.exec, hostExec)
		}
	})

	t.Run("root (non-nested) deploy is untouched — ParentExec absent", func(t *testing.T) {
		hostExec := kit.ShellExecutor{}
		tgt := &pluginDeployTarget{hasLifecycle: false, exec: hostExec}
		venueJSON := tgt.applyParentExecOverride(deploykit.EmitOpts{})

		if venueJSON != nil {
			t.Fatalf("venue_json = %s, want nil for a root deploy with no ParentExec — it must derive "+
				"its OWN root executor from its own node's host: field, never a phantom parent venue", venueJSON)
		}
		if tgt.exec != deploykit.DeployExecutor(hostExec) {
			t.Fatalf("t.exec = %#v, want unchanged %#v", tgt.exec, hostExec)
		}
	})
}
