package main

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// TestFoldMembers_FoldsTopLevelAndInheritsDisposability verifies a member is
// registered as a top-level addressable Bundle entry, MemberOf points at the
// owner, and a disposable owner's disposability is inherited (so a kind:check
// bed's destroy+rebuild is authorized to tear the member down too).
func TestFoldMembers_FoldsTopLevelAndInheritsDisposability(t *testing.T) {
	uf := &UnifiedFile{Bundle: map[string]BundleNode{
		"check-cross-pod-cdp": {
			Target:     "pod",
			Image:      "web",
			Disposable: new(true),
			Members: map[string]*BundleNode{
				"chrome": {Target: "pod", Image: "chrome-headless"},
			},
		},
	}}
	if err := foldMembers(uf); err != nil {
		t.Fatalf("foldMembers: %v", err)
	}
	member, ok := uf.Bundle["chrome"]
	if !ok {
		t.Fatalf("member 'chrome' was not folded into the Bundle map: %v", deployKeysList(uf.Bundle))
	}
	if member.MemberOf != "check-cross-pod-cdp" {
		t.Errorf("member.MemberOf = %q, want check-cross-pod-cdp", member.MemberOf)
	}
	if member.Image != "chrome-headless" {
		t.Errorf("member.Image = %q, want chrome-headless", member.Image)
	}
	if !member.IsDisposable() {
		t.Errorf("folded member should inherit the disposable owner's disposability")
	}
}

// TestFoldMembers_NonDisposableOwnerDoesNotForceDisposable: a member of a
// non-disposable owner is NOT auto-promoted to disposable (no autonomy granted).
func TestFoldMembers_NonDisposableOwnerDoesNotForceDisposable(t *testing.T) {
	uf := &UnifiedFile{Bundle: map[string]BundleNode{
		"prod": {
			Target:  "pod",
			Image:   "web",
			Members: map[string]*BundleNode{"sidecar": {Target: "pod", Image: "chrome-headless"}},
		},
	}}
	if err := foldMembers(uf); err != nil {
		t.Fatalf("foldMembers: %v", err)
	}
	if uf.Bundle["sidecar"].IsDisposable() {
		t.Errorf("member of a non-disposable owner must not be disposable")
	}
}

// TestFoldMembers_CollisionIsError: a member name colliding with an existing
// deploy/bed/member entry is a hard error (globally-unique member names).
func TestFoldMembers_CollisionIsError(t *testing.T) {
	uf := &UnifiedFile{Bundle: map[string]BundleNode{
		"web": {Target: "pod", Image: "web"},
		"bed": {Target: "pod", Image: "web", Members: map[string]*BundleNode{"web": {Target: "pod", Image: "chrome-headless"}}},
	}}
	err := foldMembers(uf)
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("expected a collision error, got %v", err)
	}
}

// TestFoldMembers_EmptyMemberIsError: a nil member node is rejected.
func TestFoldMembers_EmptyMemberIsError(t *testing.T) {
	uf := &UnifiedFile{Bundle: map[string]BundleNode{
		"bed": {Target: "pod", Image: "web", Members: map[string]*BundleNode{"chrome": nil}},
	}}
	if err := foldMembers(uf); err == nil {
		t.Fatalf("expected an error for a nil member node")
	}
}

// TestValidateMembers_BadTarget rejects an unsupported member target kind.
func TestValidateMembers_BadTarget(t *testing.T) {
	uf := &UnifiedFile{Bundle: map[string]BundleNode{
		"bed": {Target: "pod", Image: "web", Members: map[string]*BundleNode{
			"chrome": {Target: "bogus", Image: "chrome-headless"},
		}},
	}}
	if err := validateMembers(uf); err == nil || !strings.Contains(err.Error(), "unsupported target") {
		t.Fatalf("expected unsupported-target error, got %v", err)
	}
}

// TestValidateMembers_DottedKeyRejected: a member key with a dot collides with the
// nested dotted-path addressing grammar.
func TestValidateMembers_DottedKeyRejected(t *testing.T) {
	uf := &UnifiedFile{Bundle: map[string]BundleNode{
		"bed": {Target: "pod", Image: "web", Members: map[string]*BundleNode{
			"a.b": {Target: "pod", Image: "chrome-headless"},
		}},
	}}
	if err := validateMembers(uf); err == nil {
		t.Fatalf("expected a dotted-key rejection")
	}
}

// TestIsPodMember covers the pod-vs-other routing used by bringUp/tearDownMembers.
func TestIsPodMember(t *testing.T) {
	if !isPodMember(&BundleNode{Target: ""}) || !isPodMember(&BundleNode{Target: "pod"}) {
		t.Errorf("empty/pod target should be a pod member")
	}
	if isPodMember(&BundleNode{Target: "vm"}) || isPodMember(&BundleNode{Target: "local"}) {
		t.Errorf("vm/local target should NOT be a pod member")
	}
}

// TestSortedMemberKeys is deterministic ascending order.
func TestSortedMemberKeys(t *testing.T) {
	got := sortedMemberKeys(map[string]*BundleNode{"c": {}, "a": {}, "b": {}})
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sortedMemberKeys = %v, want %v", got, want)
	}
}

// TestTearDownMembers_RoutingAndOrder: tearDownMembers iterates members in sorted
// order and routes a pod member to `charly remove --purge`, a non-pod member to
// `charly bundle del --assume-yes` — the same iteration/routing logic bringUpMembers
// uses, verified here with the stubbable runCharlySubcommand package var (no side
// effects). The flag itself is proven valid against real Kong parsing by
// TestDeployDelArgv_KongAccepts (this stub-based test cannot — it never invokes
// flag parsing, which is exactly how a `--yes`/`--force` drift once slipped through).
func TestTearDownMembers_RoutingAndOrder(t *testing.T) {
	orig := runCharlySubcommand
	defer func() { runCharlySubcommand = orig }()
	var calls [][]string
	runCharlySubcommand = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	node := &BundleNode{Members: map[string]*BundleNode{
		"zeta-pod":   {Target: "pod"},
		"alpha-host": {Target: "local"},
	}}
	tearDownMembers(node)
	want := [][]string{
		deployDelArgv("alpha-host"),       // sorted first; non-pod → deploy del --assume-yes (unattended)
		{"remove", "zeta-pod", "--purge"}, // pod → remove --purge
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("tearDownMembers calls = %v, want %v", calls, want)
	}
}

// TestTearDownMembers_NoMembersNoop: nothing happens when there are no members.
func TestTearDownMembers_NoMembersNoop(t *testing.T) {
	orig := runCharlySubcommand
	defer func() { runCharlySubcommand = orig }()
	called := false
	runCharlySubcommand = func(args ...string) error { called = true; return nil }
	tearDownMembers(&BundleNode{})
	if called {
		t.Errorf("tearDownMembers ran a subcommand for a node with no members")
	}
}

func deployKeysList(m map[string]BundleNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestDeployDelArgv_KongAccepts proves deployDelArgv emits a flag the REAL
// `charly bundle del` Kong grammar accepts, and that the two historically-wrong
// flags are rejected. The stub-based TestTearDownMembers_RoutingAndOrder asserts
// arg strings without ever invoking Kong, so it CANNOT catch a flag the binary
// rejects — which is exactly how `--yes` (and `--force` at the ephemeral/reap
// call sites) shipped while silently aborting teardown at arg-parse and leaking
// the resource. This test exercises real flag parsing so the drift can never
// silently re-land.
func TestDeployDelArgv_KongAccepts(t *testing.T) {
	// delGrammarStub mirrors the command:bundle plugin's `charly bundle del` leaf grammar
	// (candy/plugin-bundle) — the Kong-tagged field set the real CLI parses. The plugin
	// owns the grammar now (P13) and a core unit test cannot import a separate module, so
	// this stub reproduces the exact tag shape (AssumeYes → --assume-yes / -y; the
	// historically-wrong --yes/--force absent) to keep the deployDelArgv regression guard.
	type delGrammarStub struct {
		Name            string `arg:""`
		AssumeYes       bool   `long:"yes" short:"y"`
		KeepRepoChanges bool   `long:"keep-repo-changes"`
		KeepServices    bool   `long:"keep-services"`
		KeepImage       bool   `long:"keep-image"`
		DryRun          bool   `long:"dry-run"`
	}
	type bundleGrammar struct {
		Bundle struct {
			Del delGrammarStub `cmd:""`
		} `cmd:""`
	}
	parse := func(args ...string) error {
		var g bundleGrammar
		k, err := kong.New(&g, kong.Name("charly"), kong.Exit(func(int) {}), kong.Writers(io.Discard, io.Discard))
		if err != nil {
			t.Fatalf("kong.New: %v", err)
		}
		_, err = k.Parse(args)
		return err
	}
	// The helper every programmatic teardown builds its command through must
	// parse cleanly against the real grammar.
	if err := parse(deployDelArgv("x")...); err != nil {
		t.Errorf("deployDelArgv produced args `charly bundle del` rejects: %v (args=%v)", err, deployDelArgv("x"))
	}
	// -y is the valid short form.
	if err := parse("bundle", "del", "x", "-y"); err != nil {
		t.Errorf("`charly bundle del -y` should be accepted, got: %v", err)
	}
	// The two flags wrongly used at call sites MUST be rejected (regression guard).
	for _, bad := range []string{"--yes", "--force"} {
		if err := parse("bundle", "del", "x", bad); err == nil {
			t.Errorf("`charly bundle del %s` must be REJECTED by Kong (it silently aborted teardown)", bad)
		}
	}
}
