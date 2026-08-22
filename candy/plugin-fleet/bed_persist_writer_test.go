package fleet

// bed_persist_writer_test.go — relocated WRITER-BEHAVIOR half of the #55 final-tail bed-persist
// cluster (charly/deploy_f3_test.go, charly/check_bed_run_test.go,
// charly/node_fleet_venue_test.go — team-lead directive, 2026-08-03 split-by-assertion round).
// Each of these tests asserts deploykit.PersistBedDeployOverrides / MarshalFleetNode /
// SaveFleetConfig / ClassifyTarget's OWN field-preservation/skip-logic contract — genuine
// deploykit-mechanism behavior, not charly-loader-specific parsing (the prior "stays in charly
// per Ambiguous-item-1" ruling predates the gate that forced this split question). The
// externalInPlace argument (spec.ExternalInPlaceVenue, #55 W3 B2-full — the former core-private
// bedExternalInPlace(target)'s registry query was refuted as an incomplete-seam trap; every node
// reaching PersistBedDeployOverrides is already Descent-stamped) collapses to a LITERAL bool
// here — false for "pod" (a container-venue external substrate — NOT in-place, so it persists),
// true for "local" (a shell-venue external substrate — in-place, so PersistBedDeployOverrides
// self-skips it) — the exact values production resolves for these targets; this test suite is
// not re-testing spec.ExternalInPlaceVenue's OWN Descent-read logic (that's covered by its own
// spec-package tests).
//
// The full write→read cycle these tests exercise in isolation runs LIVE in production on every
// check-pod / check-cross-pod-cdp bed run (candy/plugin-check's persistBedDeployOverridePluginSide,
// bed_persist.go) — this suite pins the WRITER's contract at the unit level so a regression is
// caught in milliseconds rather than only during an R10 bed run.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"

	"github.com/opencharly/sdk/deploykit"
)

// bedTestMarshalNode is the real deploykit.MarshalFleetNode with nil primaries — these fixtures
// carry no plugin-verb sugar, matching the original charly-side tests' own testBedMarshalNode.
func bedTestMarshalNode(_ string, node *deploykit.FleetNode) (*yaml.Node, error) {
	return deploykit.MarshalFleetNode(node, nil)
}

// bedTestLoadFleetConfig reads back the per-host overlay by decoding the COMPACT NODE-FORM
// MarshalFleetNode writes (the kind-discriminator carries the body inline; nested/peer entries
// are flat siblings of the discriminator — see deploykit's deploy_nodeform.go). R1 finding:
// deploykit.LoadDeployFile is a PLAIN yaml.Unmarshal into FleetConfig and does NOT understand
// this node-form shape (it silently decodes to an empty Fleet map — confirmed live: a persist
// followed by LoadDeployFile always read back Fleet:map[], causing every multi-call test below
// to silently clobber its own prior writes rather than genuinely testing PersistBedDeployOverrides'
// merge/preserve contract). There is no lighter-weight node-form-aware reader at the deploykit
// layer (sdk/loaderkit's real decoder needs a live executor/registry a standalone plugin test
// has none of) — this is a THIN, TEST-SCOPED decoder for exactly the shapes this suite's
// fixtures use (pod/local/group discriminators; image/port/disposable/requires_exclusive/
// preemptible scalar fields; Children vs Members disambiguated by whether the parent's own
// discriminator is "group"), not a general node-form parser.
func bedTestLoadFleetConfig() (*deploykit.FleetConfig, error) {
	path, err := spec.DefaultDeployConfigPath()
	if err != nil {
		return nil, nil
	}
	data, statErr := os.ReadFile(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, nil
		}
		return nil, statErr
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{}}
	for name, v := range raw {
		if name == "version" || name == "provides" {
			continue
		}
		entryMap, ok := v.(map[string]any)
		if !ok {
			continue
		}
		node := bedTestDecodeNode(entryMap)
		dc.Fleet[name] = *node
	}
	return dc, nil
}

// bedTestDiscKeys are the discriminator words this test-scoped decoder recognizes — the only
// ones this suite's fixtures author.
var bedTestDiscKeys = map[string]string{"pod": "pod", "vm": "vm", "local": "local", "group": ""}

// bedTestDecodeNode decodes one compact node-form entry map into a spec.FleetNode. entryMap's
// keys are the discriminator (exactly one of bedTestDiscKeys) plus any sibling child/member
// entry names (recursively decoded the same way).
func bedTestDecodeNode(entryMap map[string]any) *spec.FleetNode {
	node := &spec.FleetNode{}
	var discBody map[string]any
	isGroup := false
	siblings := map[string]any{}
	for k, v := range entryMap {
		if target, isDisc := bedTestDiscKeys[k]; isDisc {
			node.Target = target
			isGroup = k == "group"
			if m, ok := v.(map[string]any); ok {
				discBody = m
			}
			continue
		}
		siblings[k] = v
	}
	if discBody != nil {
		if s, ok := discBody["image"].(string); ok {
			node.Image = s
		}
		if s, ok := discBody["from"].(string); ok {
			node.From = s
		}
		if lifecycle, ok := discBody["lifecycle"].(string); ok {
			node.Lifecycle = lifecycle
		}
		if b, ok := discBody["disposable"].(bool); ok {
			node.Disposable = &b
		}
		if ports, ok := discBody["port"].([]any); ok {
			for _, p := range ports {
				if s, ok := p.(string); ok {
					node.Port = append(node.Port, s)
				}
			}
		}
		if reqs, ok := discBody["requires_exclusive"].([]any); ok {
			for _, r := range reqs {
				if s, ok := r.(string); ok {
					node.RequiresExclusive = append(node.RequiresExclusive, s)
				}
			}
		}
		if pm, ok := discBody["preemptible"].(map[string]any); ok {
			pc := &spec.PreemptibleConfig{}
			if holds, ok := pm["holds"].([]any); ok {
				for _, h := range holds {
					if s, ok := h.(string); ok {
						pc.Holds = append(pc.Holds, s)
					}
				}
			}
			if restore, ok := pm["restore"].(string); ok {
				pc.Restore = restore
			}
			node.Preemptible = pc
		}
		if envMap, ok := discBody["env"].(map[string]any); ok {
			node.Env = map[string]string{}
			for k, v := range envMap {
				if s, ok := v.(string); ok {
					node.Env[k] = s
				}
			}
		}
		if _, ok := discBody["tunnel"]; ok {
			// Presence only — this suite's fixtures check Tunnel != nil, never its field values.
			node.Tunnel = &spec.TunnelYAML{}
		}
		if vs, ok := discBody["vm_state"].(map[string]any); ok {
			state := &spec.VmDeployState{}
			if s, ok := vs["instance_id"].(string); ok {
				state.InstanceID = s
			}
			if n, ok := vs["ssh_port"].(int); ok {
				state.SSHPort = n
			}
			node.VmState = state
		}
	}
	for name, v := range siblings {
		childMap, ok := v.(map[string]any)
		if !ok {
			continue
		}
		child := bedTestDecodeNode(childMap)
		if isGroup {
			if node.Members == nil {
				node.Members = map[string]*spec.FleetNode{}
			}
			node.Members[name] = child
		} else {
			if node.Children == nil {
				node.Children = map[string]*spec.FleetNode{}
			}
			node.Children[name] = child
		}
	}
	return node
}

// TestPersistBedDeployOverrides_SkipsLocalBed pins the bed-infra fix for the cross-project
// host-overlay pollution: a LOCAL bed's only persistable cross-ref (a `local:` template) would
// make the GLOBAL per-host overlay un-loadable from every OTHER project, so PersistBedDeployOverrides
// skips it (local deploys persist via the install ledger instead); a POD bed is still persisted.
func TestPersistBedDeployOverrides_SkipsLocalBed(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte("version: 2026.225.1508\n"), 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// A LOCAL bed — persisting it would write an un-loadable `local:` cross-ref. externalInPlace
	// is literally true for "local" (a shell-venue external substrate).
	disp := true
	localBed := spec.FleetNode{
		Target:     "local",
		From:       "check-local-app",
		Disposable: &disp,
		Lifecycle:  "dev",
	}
	deploykit.PersistBedDeployOverrides("check-local", localBed, true, bedTestMarshalNode, bedTestLoadFleetConfig)

	dc, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("overlay unloadable after local-bed persist (it should have been SKIPPED): %v", err)
	}
	if dc != nil {
		if _, ok := dc.Fleet["check-local"]; ok {
			t.Error("local bed was persisted to the global overlay — must be skipped (cross-project pollution)")
		}
	}

	// A POD bed is STILL persisted (the skip is not too broad). externalInPlace is literally
	// false for "pod" (a container-venue external substrate).
	podBed := spec.FleetNode{
		Target: "pod",
		Image:  "pod-deploy-x",
	}
	deploykit.PersistBedDeployOverrides("pod-deploy-x", podBed, false, bedTestMarshalNode, bedTestLoadFleetConfig)
	dc2, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload after pod-bed persist: %v", err)
	}
	if _, ok := dc2.Fleet["pod-deploy-x"]; !ok {
		t.Error("pod bed was NOT persisted — the local skip is too broad")
	}
}

// TestPersistBedDeployOverrides_RoundtripsArbiterFields pins the group-member resource-arbitration
// persistence fix: PersistBedDeployOverrides must seed a member's arbiter role — the holder-side
// preemptible block and the claimant-side requires_exclusive token list — into the per-host
// overlay, so a reloaded member's RequiredExclusive()/IsPreemptible() reflect it and the arbiter
// actually fires. This test FAILS without the fix (saveDeployState used to drop all three).
func TestPersistBedDeployOverrides_RoundtripsArbiterFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte("version: 2026.225.1508\n"), 0o600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	takerBed := spec.FleetNode{
		Target:            "pod",
		Image:             "check-pod",
		RequiresExclusive: []string{"test-lock"},
	}
	holderBed := spec.FleetNode{
		Target:      "pod",
		Image:       "check-pod",
		Preemptible: &spec.PreemptibleConfig{Holds: []string{"test-lock"}, Restore: "always"},
	}
	deploykit.PersistBedDeployOverrides("preempt-taker", takerBed, false, bedTestMarshalNode, bedTestLoadFleetConfig)
	deploykit.PersistBedDeployOverrides("preempt-holder", holderBed, false, bedTestMarshalNode, bedTestLoadFleetConfig)

	dc, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload per-host overlay: %v", err)
	}

	taker, ok := dc.Fleet["preempt-taker"]
	if !ok {
		t.Fatal("claimant member was not persisted")
	}
	if got := taker.RequiredExclusive(); len(got) != 1 || got[0] != "test-lock" {
		t.Errorf("claimant lost requires_exclusive on round-trip: got %v, want [test-lock] — the arbiter would no-op for this member", got)
	}

	holder, ok := dc.Fleet["preempt-holder"]
	if !ok {
		t.Fatal("holder member was not persisted")
	}
	if !holder.IsPreemptible() {
		t.Errorf("holder lost preemptible on round-trip: IsPreemptible()=false (holds=%v), want true — the arbiter would not gather this holder from the overlay", holder.PreemptionHolds())
	}
	if got := holder.PreemptionHolds(); len(got) != 1 || got[0] != "test-lock" {
		t.Errorf("holder lost preemptible.holds on round-trip: got %v, want [test-lock]", got)
	}
}

// TestPersistBedDeployOverrides_SeedsPortBeforeConfig pins the fix for the bug class where a
// kind:check pod bed's project-declared deploy-shaped fields (port:/volume:/env:/tunnel:) never
// reached the per-host deploy.yml. PersistBedDeployOverrides seeds the bed node's overrides up
// front so the existing charly config -> MergeDeployOntoMetadata -> quadlet path honors them.
func TestPersistBedDeployOverrides_SeedsPortBeforeConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, "charly"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-existing unrelated deploy must survive the seed (merge, not clobber).
	initialYAML := `version: 2026.225.1508
ollama:
    pod:
        image: ollama
        port:
            - 11434:11434
`
	path := filepath.Join(dir, "charly", "charly.yml")
	if err := os.WriteFile(path, []byte(initialYAML), 0600); err != nil {
		t.Fatalf("write initial: %v", err)
	}

	// A bed whose key differs from its image and whose port remaps off the image default —
	// exactly the check-cachyos-ollama-pod shape.
	disp := true
	bed := spec.FleetNode{
		Target:     "pod",
		Image:      "ollama",
		Port:       []string{"45434:11434"},
		Disposable: &disp,
		Lifecycle:  "dev",
	}
	deploykit.PersistBedDeployOverrides("check-cachyos-ollama-pod", bed, false, bedTestMarshalNode, bedTestLoadFleetConfig)

	dc, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("reload after seed: %v", err)
	}
	entry, ok := dc.Fleet["check-cachyos-ollama-pod"]
	if !ok {
		t.Fatal("bed entry not seeded into deploy.yml")
	}
	if len(entry.Port) != 1 || entry.Port[0] != "45434:11434" {
		t.Errorf("bed port not seeded: got %v, want [45434:11434]", entry.Port)
	}
	if entry.Image != "ollama" || entry.Target != "pod" {
		t.Errorf("bed image/target not seeded: got image=%q target=%q", entry.Image, entry.Target)
	}
	if entry.Disposable == nil || !*entry.Disposable {
		t.Error("bed disposable not seeded (the check-runner requires it to authorize the unattended fresh-rebuild)")
	}
	// The sibling production deploy must be untouched (distinct key).
	sib, ok := dc.Fleet["ollama"]
	if !ok || len(sib.Port) != 1 || sib.Port[0] != "11434:11434" {
		t.Errorf("sibling 'ollama' deploy clobbered: got %+v", sib)
	}
}

// TestOverlayRoundTrip_NestedChildSurvives (Risk 5a) proves the per-host overlay writer
// round-trips a deployment's NESTED CHILD + derived TARGET even though FleetNode.Children/Target
// are now yaml:"-" (the writer re-emits them via MarshalFleetNode -> node-form children). A lossy
// writer would silently drop the nested child on the next save.
func TestOverlayRoundTrip_NestedChildSurvives(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	disposable := true
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"myapp": {
			Target:     "pod",
			Image:      "web",
			Disposable: &disposable,
			Children: map[string]*spec.FleetNode{
				"inner": {
					Target: "pod",
					Image:  "db",
				},
			},
		},
	}}
	if err := deploykit.SaveFleetConfig(dc, bedTestMarshalNode, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("SaveFleetConfig: %v", err)
	}

	dc2, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("LoadFleetConfig (round-trip): %v", err)
	}
	got, ok := dc2.Fleet["myapp"]
	if !ok {
		t.Fatalf("round-trip lost the deploy entry myapp; got entries %v", fleetTestKeysOf(dc2.Fleet))
	}
	if deploykit.ClassifyTarget(&got) != "pod" {
		t.Errorf("round-trip target = %q, want pod (re-derived)", deploykit.ClassifyTarget(&got))
	}
	if got.Image != "web" {
		t.Errorf("round-trip box = %q, want web", got.Image)
	}
	inner, ok := got.Children["inner"]
	if !ok {
		t.Fatalf("round-trip LOST nested child %q (lossy overlay writer) — got children %v", "inner", childTestKeysOf(got.Children))
	}
	if deploykit.ClassifyTarget(inner) != "pod" {
		t.Errorf("nested child target = %q, want pod", deploykit.ClassifyTarget(inner))
	}
	if inner.Image != "db" {
		t.Errorf("nested child box = %q, want db", inner.Image)
	}
}

// TestOverlayRoundTrip_GroupMembersSurvive proves the per-host overlay writer round-trips a GROUP
// bed (Target=="" + sibling Members) without dropping its members. A lossy round-trip would
// re-emit a MEMBERLESS group bed.
func TestOverlayRoundTrip_GroupMembersSurvive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	disposable := true
	dc := &deploykit.FleetConfig{Fleet: map[string]spec.FleetNode{
		"shop": {
			Target:     "", // GROUP — no workload cross-ref
			Disposable: &disposable,
			Members: map[string]*spec.FleetNode{
				"web":    {Target: "pod", Image: "web"},
				"chrome": {Target: "pod", Image: "chrome-headless"},
			},
		},
	}}
	if err := deploykit.SaveFleetConfig(dc, bedTestMarshalNode, bedTestLoadFleetConfig); err != nil {
		t.Fatalf("SaveFleetConfig: %v", err)
	}
	dc2, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("LoadFleetConfig (round-trip): %v", err)
	}
	got, ok := dc2.Fleet["shop"]
	if !ok {
		t.Fatalf("round-trip lost the group fleet 'shop'; got %v", fleetTestKeysOf(dc2.Fleet))
	}
	if len(got.Members) != 2 || got.Members["web"] == nil || got.Members["chrome"] == nil {
		t.Fatalf("round-trip LOST group members: got %v", childTestKeysOf(got.Members))
	}
}

// TestPersistBedDeployOverrides_GroupBedNotPersisted proves the root-cause fix: persisting a
// GROUP bed root is a no-op, so it never writes a memberless bed to the per-host overlay.
func TestPersistBedDeployOverrides_GroupBedNotPersisted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	disposable := true
	groupBed := spec.FleetNode{
		Target:     "", // GROUP — no workload cross-ref
		Disposable: &disposable,
		Members:    map[string]*spec.FleetNode{"web": {Target: "pod", Image: "web"}},
	}
	deploykit.PersistBedDeployOverrides("check-cross-pod-cdp", groupBed, false, bedTestMarshalNode, bedTestLoadFleetConfig)

	dc, err := bedTestLoadFleetConfig()
	if err != nil {
		t.Fatalf("overlay poisoned by persisting a group bed root: %v", err)
	}
	if dc != nil {
		if _, present := dc.Fleet["check-cross-pod-cdp"]; present {
			t.Errorf("group bed root was persisted to the overlay — it must be skipped (no root deploy to seed)")
		}
	}
}

func fleetTestKeysOf(m map[string]spec.FleetNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func childTestKeysOf(m map[string]*spec.FleetNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
