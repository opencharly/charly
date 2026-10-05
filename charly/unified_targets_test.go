package main

import (
	"encoding/json"
	"testing"

	"github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// TestPluginDeployTarget_ApplyParentExecOverride is the regression test for the FIX ROUND bug
// (R10 bed-found, S3b follow-up): every PLAIN vm-class deploy's NESTED non-lifecycle child (a
// `local:`/`android:`/`kubernetes:` deploy under a vm/pod, tree position — e.g.
// check-group's check-group-member, check-builder-vm's
// check-builder-member) silently ran its plan/step walk on the OPERATOR'S HOST instead of the
// parent venue, because pluginDeployTarget.Add never restored the pre-S3b former core-resident
// deploy target's apply's `t.exec = opts.ParentExec` swap. This test asserts BOTH halves of
// the fix directly, without needing a live plugin round-trip: t.exec is mutated to the LIVE
// parent executor, AND the returned venue_json describes that SAME executor — so a future
// refactor that restores only one half (e.g. sets t.exec but forgets the wire-safe descriptor, or
// vice versa) fails this test immediately instead of surfacing as a silent host-execution bug
// only a live disposable bed catches.
func TestPluginDeployTarget_ApplyParentExecOverride(t *testing.T) {
	guestSSH := &exec.SSHExecutor{User: "arch", Host: "charly-eval-vm", Port: 2222, ConnectTimeout: 10}

	t.Run("non-lifecycle nested child swaps to the parent executor", func(t *testing.T) {
		tgt := &pluginDeployTarget{hasLifecycle: false, exec: exec.ShellExecutor{}}
		venueJSON := tgt.applyParentExecOverride(spec.EmitOpts{ParentExec: guestSSH})

		if tgt.exec != spec.DeployExecutor(guestSSH) {
			t.Fatalf("t.exec = %#v (%T), want the live ParentExec value %#v unchanged — a nested "+
				"child's plan/step walk (and every RunHostStep/RunSystem/RunUser reverse leg this "+
				"dispatch threads) must run against the PARENT venue, not the ResolveTarget-time "+
				"host/ssh default this field started as", tgt.exec, tgt.exec, guestSSH)
		}

		if len(venueJSON) == 0 {
			t.Fatalf("applyParentExecOverride returned empty venue_json for a non-nil ParentExec — " +
				"candy/plugin-fleet's resolveRootExecutor has nothing to re-materialize from and " +
				"silently falls back to specexec.RootExecutorForDeployNode(req.Node), which for a " +
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

		// Round-trip through the real inverse (exec.VenueFromDescriptor) to prove
		// candy/plugin-fleet's resolveRootExecutor would ACTUALLY re-materialize the guest, not
		// merely that the intermediate JSON looks right.
		reExec, err := exec.VenueFromDescriptor(got)
		if err != nil {
			t.Fatalf("exec.VenueFromDescriptor(%+v): %v", got, err)
		}
		reSSH, ok := reExec.(*exec.SSHExecutor)
		if !ok {
			t.Fatalf("re-materialized executor = %T, want *exec.SSHExecutor (the guest)", reExec)
		}
		if reSSH.Host != guestSSH.Host || reSSH.Port != guestSSH.Port || reSSH.User != guestSSH.User {
			t.Fatalf("re-materialized executor = %+v, want a guest connection matching %+v", reSSH, guestSSH)
		}
	})

	t.Run("lifecycle substrate (vm/pod) is untouched — it composes its own venue in PrepareVenue", func(t *testing.T) {
		hostExec := exec.ShellExecutor{}
		tgt := &pluginDeployTarget{hasLifecycle: true, exec: hostExec}
		venueJSON := tgt.applyParentExecOverride(spec.EmitOpts{ParentExec: guestSSH})

		if venueJSON != nil {
			t.Fatalf("venue_json = %s, want nil — a lifecycle substrate (vm/pod) must run its OWN "+
				"PrepareVenue, never have its venue pre-empted by an ancestor's ParentExec", venueJSON)
		}
		if tgt.exec != spec.DeployExecutor(hostExec) {
			t.Fatalf("t.exec = %#v, want unchanged %#v — the lifecycle branch must not mutate t.exec "+
				"at all (PrepareVenue is the sole venue authority for vm/pod)", tgt.exec, hostExec)
		}
	})

	t.Run("root (non-nested) deploy is untouched — ParentExec absent", func(t *testing.T) {
		hostExec := exec.ShellExecutor{}
		tgt := &pluginDeployTarget{hasLifecycle: false, exec: hostExec}
		venueJSON := tgt.applyParentExecOverride(spec.EmitOpts{})

		if venueJSON != nil {
			t.Fatalf("venue_json = %s, want nil for a root deploy with no ParentExec — it must derive "+
				"its OWN root executor from its own node's host: field, never a phantom parent venue", venueJSON)
		}
		if tgt.exec != spec.DeployExecutor(hostExec) {
			t.Fatalf("t.exec = %#v, want unchanged %#v", tgt.exec, hostExec)
		}
	})
}

// TestPluginDeployTarget_ApplyDelParentExec is applyParentExecOverride's DEL twin regression test
// (opencharly/charly#765). `charly deploy del <root>.<member>` for an IN-SUBSTRATE nested member is
// forked as a FRESH process by the check bed's cleanup step (candy/plugin-check/bed_run.go), so the
// in-process venue carry between add and del never applied: the del dispatch carried no venue_json,
// candy/plugin-fleet's resolveRootExecutor fell through to RootExecutorForDeployNode(node) — the
// OPERATOR'S HOST for a member with no `host:` field — and the member's reversible ops (a
// `package:` list's `pacman -R`) replayed against the workstation while its `add` had landed
// correctly in the guest. This asserts BOTH halves directly (t.exec mutated AND the wire-safe
// descriptor written), so a refactor restoring only one fails here instead of only in a live bed.
func TestPluginDeployTarget_ApplyDelParentExec(t *testing.T) {
	guestSSH := &exec.SSHExecutor{User: "arch", Host: "charly-check-kind-host-vm", Port: 2222, ConnectTimeout: 10}

	t.Run("non-lifecycle nested member's teardown swaps to the ancestor venue", func(t *testing.T) {
		tgt := &pluginDeployTarget{hasLifecycle: false, exec: exec.ShellExecutor{}}
		tgt.applyDelParentExec(guestSSH)

		if tgt.exec != spec.DeployExecutor(guestSSH) {
			t.Fatalf("t.exec = %#v (%T), want the re-derived ancestor executor %#v — a nested member's "+
				"teardown (and every reverse leg this dispatch drives, the `pacman -R` of a `package:` "+
				"list included) must run against the member's PARENT venue, never the host ShellExecutor "+
				"ResolveTarget fell back to", tgt.exec, tgt.exec, guestSSH)
		}
		if len(tgt.venueJSON) == 0 {
			t.Fatalf("applyDelParentExec wrote no venue_json — pluginDeployTarget.dispatch threads " +
				"t.venueJSON as the del request's VenueJSON, and candy/plugin-fleet's resolveRootExecutor " +
				"has nothing to re-materialize from without it: it silently falls back to " +
				"specexec.RootExecutorForDeployNode(req.Node) = THE OPERATOR'S HOST, exactly the " +
				"destructive regression this fix closes (charly#765)")
		}
		var got spec.VenueDescriptor
		if err := json.Unmarshal(tgt.venueJSON, &got); err != nil {
			t.Fatalf("venue_json does not decode as spec.VenueDescriptor: %v (raw=%s)", err, tgt.venueJSON)
		}
		want := spec.VenueDescriptor{Kind: "ssh", User: "arch", Host: "charly-check-kind-host-vm", Port: 2222, ConnectTimeout: 10}
		if got.Kind != want.Kind || got.User != want.User || got.Host != want.Host ||
			got.Port != want.Port || got.ConnectTimeout != want.ConnectTimeout || len(got.Args) != 0 {
			t.Fatalf("venue_json descriptor = %+v, want %+v (must describe the GUEST venue, not a "+
				"shell/empty descriptor that re-materializes to the host)", got, want)
		}
		// Round-trip through the real inverse the plugin uses, so this proves the guest is ACTUALLY
		// re-materialized plugin-side rather than merely that the intermediate JSON looks right.
		reExec, err := exec.VenueFromDescriptor(got)
		if err != nil {
			t.Fatalf("exec.VenueFromDescriptor(%+v): %v", got, err)
		}
		reSSH, ok := reExec.(*exec.SSHExecutor)
		if !ok {
			t.Fatalf("re-materialized executor = %T, want *exec.SSHExecutor (the guest)", reExec)
		}
		if reSSH.Host != guestSSH.Host || reSSH.Port != guestSSH.Port || reSSH.User != guestSSH.User {
			t.Fatalf("re-materialized executor = %+v, want a guest connection matching %+v", reSSH, guestSSH)
		}
	})

	t.Run("lifecycle substrate (vm/pod) is untouched — its teardown-executor op owns the venue", func(t *testing.T) {
		hostExec := exec.ShellExecutor{}
		tgt := &pluginDeployTarget{hasLifecycle: true, exec: hostExec}
		tgt.applyDelParentExec(guestSSH)

		if len(tgt.venueJSON) != 0 {
			t.Fatalf("venue_json = %s, want empty — a lifecycle substrate resolves its teardown venue "+
				"through its own teardown-executor op (candy/plugin-fleet/deploy_target.go), which must "+
				"not be pre-empted by an ancestor's venue", tgt.venueJSON)
		}
		if tgt.exec != spec.DeployExecutor(hostExec) {
			t.Fatalf("t.exec = %#v, want unchanged %#v", tgt.exec, hostExec)
		}
	})

	t.Run("top-level deploy is untouched — no ancestor chain shipped", func(t *testing.T) {
		hostExec := exec.ShellExecutor{}
		tgt := &pluginDeployTarget{hasLifecycle: false, exec: hostExec}
		tgt.applyDelParentExec(nil) // spec.ReconstructParentExec returns a nil parentExec for empty lists

		if len(tgt.venueJSON) != 0 {
			t.Fatalf("venue_json = %s, want empty for a top-level target with no ancestor chain — it "+
				"must derive its OWN root executor from its own node, never a phantom ancestor venue",
				tgt.venueJSON)
		}
		if tgt.exec != spec.DeployExecutor(hostExec) {
			t.Fatalf("t.exec = %#v, want unchanged %#v", tgt.exec, hostExec)
		}
	})
}

// TestPluginDeployTarget_BracketedLifecycle is the regression test for the deploy-cone cutover 1
// item-1 fix: Start/Stop's "does this substrate need the Q1 resource-arbiter bracket" signal comes
// from the DECLARED #DeployTraits.bracketed_lifecycle resolved BY the substrate WORD from the
// provider registry (deployTraitsFor) — never from a hardcoded word comparison, and never from the
// unstamped dispatch node (t.node is dctx.Node, which StampDescent does not stamp). Keying on
// t.word — the provider's substrate word set in ResolveDeploy — keeps the bracket POD-SCOPED and
// safe: pod is bracketed, vm/local are not, and an unknown/empty word resolves nil → not bracketed
// (never the effectiveTarget "pod" default a node-keyed read would fall into for an untargeted node).
func TestPluginDeployTarget_BracketedLifecycle(t *testing.T) {
	cases := []struct {
		name string
		word string
		want bool
	}{
		{"empty word (unresolved) → not bracketed", "", false},
		{"vm (declares bracketed_lifecycle=false) → not bracketed", "vm", false},
		{"local (declares bracketed_lifecycle=false) → not bracketed", "local", false},
		{"pod (declares bracketed_lifecycle=true) → bracketed", "pod", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tgt := &pluginDeployTarget{word: c.word}
			if got := tgt.bracketedLifecycle(); got != c.want {
				t.Fatalf("bracketedLifecycle(word=%q) = %v, want %v", c.word, got, c.want)
			}
		})
	}
}
