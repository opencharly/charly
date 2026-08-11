package check

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// testSubstrateTraits mirrors candy/plugin-substrate/plugin.go's substrateTraits table (the
// per-word #DeployTraits every substrate word declares over Describe) — this package cannot
// reach the core registry-coupled deployTraitsFor charly/deploy_chain_test.go's
// stampTestDescents used, so tests here stamp Descent via the SAME sdk/kit.StampDescent
// mechanism fed this literal copy of the declared traits instead.
func testSubstrateTraits(word string) *spec.DeployTraits {
	traits := map[string]*spec.DeployTraits{
		"pod":        {Venue: "container", ImageBacked: true, ImageContext: true},
		"vm":         {Venue: "ssh", MachineVenue: true, ExclusiveVenue: true},
		"local":      {Venue: "shell", MachineVenue: true},
		"kubernetes": {Venue: "shell", ImageContext: true, LeafOnly: true},
		"android":    {Venue: "parent"},
	}
	return traits[word]
}

// stampTestDescents stamps Descent on every root + its nested Children/Members via
// kit.StampDescent, the SAME generic mechanism the production loader uses.
func stampTestDescents(roots map[string]spec.FleetNode) map[string]spec.FleetNode {
	out := make(map[string]spec.FleetNode, len(roots))
	for k, v := range roots {
		n := v
		kit.StampDescent(&n, testSubstrateTraits)
		out[k] = n
	}
	return out
}

// TestPluginRunCheckLive_PureCycleEmitsFailVerdictsNoPropagation ports
// charly/check_runner_live_test.go's TestRunCheckLive_PureCycleEmitsFailVerdictsNoPropagation:
// exercises the depends_on cycle handling — with every scored step in a cycle,
// topoSortScored returns an empty ordered set + the cyclic remainder, and pluginRunCheckLive
// emits one fail verdict per cyclic step (SkippedReason prefix "cycle:") instead of erroring
// out. A pure cycle produces NO buckets (groupScoredByPod(nil) == nil), so this exercises
// zero live dispatch — a nil ex (no reverse channel) is safe here.
func TestPluginRunCheckLive_PureCycleEmitsFailVerdictsNoPropagation(t *testing.T) {
	// a depends_on b, b depends_on a — pure cycle (id-keyed).
	// venue is loader-derived (yaml:"-") from tree position; this in-package test
	// sets it directly to stand in for the flatten pass.
	plan := []spec.Step{
		{Check: "a", Op: spec.Op{ID: "a", Venue: "test-pod", DependsOn: []string{"b"}, Plugin: "file", PluginInput: map[string]any{"file": "/a"}}},
		{Check: "b", Op: spec.Op{ID: "b", Venue: "test-pod", DependsOn: []string{"a"}, Plugin: "file", PluginInput: map[string]any{"file": "/b"}}},
	}
	res, err := pluginRunCheckLive(nil, context.Background(), "test-score", plan)
	if err != nil {
		t.Fatalf("a depends_on cycle must NOT propagate as an error — got: %v", err)
	}
	if res == nil {
		t.Fatalf("expected non-nil CheckRunResults even on a pure cycle")
	}
	if len(res.Step) != 2 {
		t.Fatalf("2 cyclic steps → 2 fail verdicts, got %d", len(res.Step))
	}
	for _, sc := range res.Step {
		if sc.Status != "fail" {
			t.Errorf("step %q: status = %q, want fail", sc.ID, sc.Status)
		}
		if !strings.HasPrefix(sc.SkippedReason, "cycle:") {
			t.Errorf("step %q: SkippedReason = %q, want prefix 'cycle:'", sc.ID, sc.SkippedReason)
		}
	}
	if res.Summary.Fail != 2 || res.Summary.Total != 2 {
		t.Errorf("summary mismatch: total=%d fail=%d, want total=2 fail=2", res.Summary.Total, res.Summary.Fail)
	}
}

// TestPluginRunCheckLive_EmptyInputReturnsEarly ports
// charly/check_runner_live_test.go's TestRunCheckLive_EmptyInputReturnsEarly — a regression on
// the empty-plan fast path.
func TestPluginRunCheckLive_EmptyInputReturnsEarly(t *testing.T) {
	res, err := pluginRunCheckLive(nil, context.Background(), "test-score", nil)
	if err != nil {
		t.Fatalf("nil plan should not error: %v", err)
	}
	if res == nil || len(res.Step) != 0 {
		t.Errorf("nil plan should yield empty result; got %v", res)
	}
}

// TestPluginResolveScoringChain_Local ports charly/check_local_test.go's
// TestResolveScoringChain_Local: a flat score/bed target that resolves to a `target: local` node
// must run on the host venue, NOT a fabricated charly-<pod> container.
func TestPluginResolveScoringChain_Local(t *testing.T) {
	roots := stampTestDescents(map[string]spec.FleetNode{
		"localbed": {Target: "local"},
		"podbed":   {Target: "pod"},
	})
	exec, err := pluginResolveScoringChain(roots, "localbed")
	if err != nil {
		t.Fatalf("local bed: %v", err)
	}
	if _, ok := exec.(kit.ShellExecutor); !ok {
		t.Errorf("local bed → %T, want ShellExecutor (host venue, not a container)", exec)
	}
	// A pod target still routes to a container chain (no regression).
	exec, err = pluginResolveScoringChain(roots, "podbed")
	if err != nil {
		t.Fatalf("pod bed: %v", err)
	}
	if _, ok := exec.(*kit.NestedExecutor); !ok {
		t.Errorf("pod bed → %T, want *NestedExecutor (container chain)", exec)
	}
}

// TestPluginResolveDottedAgentProvisionedVenue (Risk 5b) ports
// charly/node_fleet_venue_test.go's TestResolveDottedAgentProvisionedVenue's
// resolveScoringChain half: pluginResolveScoringChain must reach a 3-level agent-provisioned
// venue (vm → pod → pod) written into a scratch deploy-tree map — without a live connection
// (the chain is built, not dialed). The ResolveDeployChain half of the original test stays in
// charly/node_fleet_venue_test.go (that sdk-portable function never moved).
func TestPluginResolveDottedAgentProvisionedVenue(t *testing.T) {
	roots := stampTestDescents(map[string]spec.FleetNode{
		"nested-check-vm": {
			Target:           "vm",
			From:             "nested-check-vm",
			AgentProvisioned: true,
			Children: map[string]*spec.FleetNode{
				"inner-app-pod": {
					Target:           "pod",
					AgentProvisioned: true,
					Children: map[string]*spec.FleetNode{
						"nested-redis-pod": {
							Target:           "pod",
							AgentProvisioned: true,
						},
					},
				},
			},
		},
	})
	const dotted = "nested-check-vm.inner-app-pod.nested-redis-pod"

	sc, err := pluginResolveScoringChain(roots, dotted)
	if err != nil {
		t.Fatalf("pluginResolveScoringChain(%q): %v", dotted, err)
	}
	if sc == nil {
		t.Fatalf("pluginResolveScoringChain(%q): nil chain", dotted)
	}
}

// TestPluginResolveBareAgentProvisionedVenue ports
// charly/node_fleet_venue_test.go's TestResolveBareAgentProvisionedVenue: a bare
// agent-provisioned venue (the common iterate-bench case, e.g. `os`) resolves via
// pluginResolveScoringChain's bare-name fallback to the `charly-<name>` container the agent
// deploys — without any top-level fleet entry (agent-provisioned members are not folded).
func TestPluginResolveBareAgentProvisionedVenue(t *testing.T) {
	roots := stampTestDescents(map[string]spec.FleetNode{}) // os is NOT a top-level entry (not folded)
	sc, err := pluginResolveScoringChain(roots, "os")
	if err != nil {
		t.Fatalf("pluginResolveScoringChain(os): %v", err)
	}
	if sc == nil {
		t.Fatalf("pluginResolveScoringChain(os): nil chain (bare-name fallback failed)")
	}
}
