package main

import (
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/opencharly/spec/proc"
	"github.com/opencharly/spec/spec"

	"github.com/alecthomas/kong"
)

// TestFoldMembers_* / TestValidateMembers_* relocated to
// candy/plugin-loader/bundle_members_test.go (#55 decoupling cone, Batch C) —
// they asserted loaderkit.FoldMembers / loaderkit.ValidateMembers directly,
// zero charly coupling.

// TestIsPodMember covers the pod-vs-other routing used by bringUp/tearDownMembers.
func TestIsPodMember(t *testing.T) {
	if !isPodMember(&spec.BundleNode{Target: ""}) || !isPodMember(&spec.BundleNode{Target: "pod"}) {
		t.Errorf("empty/pod target should be a pod member")
	}
	if isPodMember(&spec.BundleNode{Target: "vm"}) || isPodMember(&spec.BundleNode{Target: "local"}) {
		t.Errorf("vm/local target should NOT be a pod member")
	}
}

// TestSortedMemberKeys is deterministic ascending order.
func TestSortedMemberKeys(t *testing.T) {
	got := spec.SortedMemberKeys(map[string]*spec.BundleNode{"c": {}, "a": {}, "b": {}})
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sortedMemberKeys = %v, want %v", got, want)
	}
}

// TestTearDownMembers_RoutingAndOrder: tearDownMembers iterates members in sorted
// order and routes a pod member to `charly remove --purge`, a non-pod member to
// `charly bundle del --assume-yes` — the same iteration/routing logic bringUpMembers
// uses, verified here with the stubbable proc.RunCharlySubcommand package var (no side
// effects). The flag itself is proven valid against real Kong parsing by
// TestDeployDelArgv_KongAccepts (this stub-based test cannot — it never invokes
// flag parsing, which is exactly how a `--yes`/`--force` drift once slipped through).
func TestTearDownMembers_RoutingAndOrder(t *testing.T) {
	orig := proc.RunCharlySubcommand
	defer func() { proc.RunCharlySubcommand = orig }()
	var calls [][]string
	proc.RunCharlySubcommand = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	node := &spec.BundleNode{Members: map[string]*spec.BundleNode{
		"zeta-pod":   {Target: "pod"},
		"alpha-host": {Target: "local"},
	}}
	if err := tearDownMembers(node); err != nil {
		t.Fatalf("tearDownMembers: %v", err)
	}
	want := [][]string{
		spec.BundleDelArgv("alpha-host"),  // sorted first; non-pod → deploy del --assume-yes (unattended)
		{"remove", "zeta-pod", "--purge"}, // pod → remove --purge
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("tearDownMembers calls = %v, want %v", calls, want)
	}
}

// TestTearDownMembers_NoMembersNoop: nothing happens when there are no members.
func TestTearDownMembers_NoMembersNoop(t *testing.T) {
	orig := proc.RunCharlySubcommand
	defer func() { proc.RunCharlySubcommand = orig }()
	called := false
	proc.RunCharlySubcommand = func(args ...string) error { called = true; return nil }
	if err := tearDownMembers(&spec.BundleNode{}); err != nil {
		t.Fatalf("tearDownMembers(empty): %v", err)
	}
	if called {
		t.Errorf("tearDownMembers ran a subcommand for a node with no members")
	}
}

func TestTearDownMembers_AttemptsAllAndReturnsJoinedErrors(t *testing.T) {
	orig := proc.RunCharlySubcommand
	defer func() { proc.RunCharlySubcommand = orig }()
	firstErr := errors.New("first teardown failed")
	secondErr := errors.New("second teardown failed")
	var calls [][]string
	proc.RunCharlySubcommand = func(args ...string) error {
		calls = append(calls, args)
		if len(calls) == 1 {
			return firstErr
		}
		return secondErr
	}
	err := tearDownMembers(&spec.BundleNode{Members: map[string]*spec.BundleNode{
		"a-local": {Target: "local"},
		"b-pod":   {Target: "pod"},
	}})
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("tearDownMembers error = %v, want both member failures", err)
	}
	if len(calls) != 2 {
		t.Fatalf("tearDownMembers stopped early: calls = %v", calls)
	}
}

// TestBundleDelArgv_KongAccepts proves spec.BundleDelArgv emits a flag the REAL
// `charly bundle del` Kong grammar accepts, and that the two historically-wrong
// flags are rejected. The stub-based TestTearDownMembers_RoutingAndOrder asserts
// arg strings without ever invoking Kong, so it CANNOT catch a flag the binary
// rejects — which is exactly how `--yes` (and `--force` at the ephemeral/reap
// call sites) shipped while silently aborting teardown at arg-parse and leaking
// the resource. This test exercises real flag parsing so the drift can never
// silently re-land.
func TestBundleDelArgv_KongAccepts(t *testing.T) {
	// delGrammarStub mirrors the command:bundle plugin's `charly bundle del` leaf grammar
	// (candy/plugin-bundle) — the Kong-tagged field set the real CLI parses. The plugin
	// owns the grammar now (P13) and a core unit test cannot import a separate module, so
	// this stub reproduces the exact tag shape (AssumeYes → --assume-yes / -y; the
	// historically-wrong --yes/--force absent) to keep the spec.BundleDelArgv regression guard.
	type delGrammarStub struct {
		Name            string `arg:""`
		AssumeYes       bool   `name:"assume-yes" short:"y"`
		KeepRepoChanges bool   `name:"keep-repo-changes"`
		KeepServices    bool   `name:"keep-services"`
		KeepImage       bool   `name:"keep-image"`
		DryRun          bool   `name:"dry-run"`
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
	if err := parse(spec.BundleDelArgv("x")...); err != nil {
		t.Errorf("spec.BundleDelArgv produced args `charly bundle del` rejects: %v (args=%v)", err, spec.BundleDelArgv("x"))
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
