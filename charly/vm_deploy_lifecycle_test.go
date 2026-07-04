package main

import (
	"context"
	"encoding/json"
	"testing"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// TestVmSubstrateLifecycleRegistered proves the vm AND pod substrates register a lifecycle
// hook (vm: the host-side VM boot/destroy + guest SSH executor; pod: the host-side overlay
// image build + container config/start/remove), and that the in-place externalized
// substrates (local/android/k8s) do NOT (their venue has no charly-owned lifecycle —
// externalDeployTarget errors on Start/Stop/Logs/Shell).
func TestVmSubstrateLifecycleRegistered(t *testing.T) {
	for _, w := range []string{"vm", "pod"} {
		if _, ok := substrateLifecycleFor(w); !ok {
			t.Errorf("%s must register a substrateLifecycle", w)
		}
	}
	for _, w := range []string{"local", "android", "k8s"} {
		if _, ok := substrateLifecycleFor(w); ok {
			t.Errorf("%s must NOT register a substrateLifecycle", w)
		}
	}
}

// TestVmEntityForLifecycle covers the (name, node) → kind:vm entity resolution: the node's
// `vm:` cross-ref (node.From) wins, then a legacy "vm:<entity>" prefix, then the deploy name.
func TestVmEntityForLifecycle(t *testing.T) {
	cases := []struct {
		name string
		node *BundleNode
		want string
	}{
		{"check-k3s-vm", &BundleNode{From: "k3s-vm"}, "k3s-vm"}, // cross-ref wins
		{"vm:arch", nil, "arch"},                                // legacy prefix
		{"vm:arch/inst", nil, "arch"},                           // legacy prefix + instance
		{"arch", &BundleNode{}, "arch"},                         // bare name fallback
		{"check-arch-vm", &BundleNode{From: "arch"}, "arch"},    // bed key != entity
	}
	for _, tc := range cases {
		if got := vmEntityForLifecycle(tc.name, tc.node); got != tc.want {
			t.Errorf("vmEntityForLifecycle(%q, %+v) = %q, want %q", tc.name, tc.node, got, tc.want)
		}
	}
}

// TestVmLifecycleArtifactKey proves candy artifacts (+ the k3s ClusterProfile) key under
// "vm:<entity>", NOT the deploy name — so a k3s cluster reached by several beds resolves to
// the shared "vm-<entity>" profile name the `cluster:` refs use.
func TestVmLifecycleArtifactKey(t *testing.T) {
	life := vmSubstrateLifecycle{}
	if got := life.ArtifactKey("check-k8s-deploy-cluster", &BundleNode{From: "k3s-vm"}); got != "vm:k3s-vm" {
		t.Errorf("ArtifactKey = %q, want vm:k3s-vm (keyed by entity, not deploy name)", got)
	}
}

// TestVmLifecycleTeardownExecutor proves Del replays over the GUEST SSH alias (no boot).
func TestVmLifecycleTeardownExecutor(t *testing.T) {
	life := vmSubstrateLifecycle{}
	exec, err := life.TeardownExecutor("check-arch-vm", &BundleNode{From: "arch"})
	if err != nil {
		t.Fatalf("TeardownExecutor: %v", err)
	}
	ssh, ok := exec.(*SSHExecutor)
	if !ok {
		t.Fatalf("TeardownExecutor = %T, want *SSHExecutor (the guest)", exec)
	}
	if ssh.Host != VmSshAlias("arch") {
		t.Errorf("teardown SSH host = %q, want the managed alias %q", ssh.Host, VmSshAlias("arch"))
	}
}

// TestReverseRunnerForExecutor proves the Δ2 derivation: an *SSHExecutor venue replays
// teardown over SSH (in the guest / on the remote), a ShellExecutor venue replays locally
// (nil runner → reverse_ops local fallback), and an explicit injected runner always wins.
func TestReverseRunnerForExecutor(t *testing.T) {
	if r := reverseRunnerForExecutor(&SSHExecutor{Host: "charly-arch"}, nil); r == nil {
		t.Error("an *SSHExecutor venue must derive an sshReverseRunner (guest teardown)")
	} else if _, ok := r.(*sshReverseRunner); !ok {
		t.Errorf("derived runner = %T, want *sshReverseRunner", r)
	}
	if r := reverseRunnerForExecutor(ShellExecutor{}, nil); r != nil {
		t.Errorf("a ShellExecutor venue must yield a nil runner (local exec.Command fallback), got %T", r)
	}
	injected := &sshReverseRunner{exec: &SSHExecutor{Host: "x"}}
	if r := reverseRunnerForExecutor(ShellExecutor{}, injected); r != injected {
		t.Error("an explicit injected runner must win over the derivation")
	}
}

// rebootFakeExec is a DeployExecutor whose RunCapture returns a frozen boot_id on the first
// call (the pre-reboot read) then a CHANGED boot_id on every subsequent poll, so
// rebootVenueAndWait completes on the first poll without a real reboot.
type rebootFakeExec struct {
	captures    int
	rebootFired bool
}

func (e *rebootFakeExec) Venue() string { return "ssh://reboot-fake" }
func (e *rebootFakeExec) RunSystem(_ context.Context, script string, _ EmitOpts) error {
	if len(script) > 0 {
		e.rebootFired = true
	}
	return nil
}
func (e *rebootFakeExec) RunUser(context.Context, string, EmitOpts) error { return nil }
func (e *rebootFakeExec) RunBuilder(context.Context, BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *rebootFakeExec) PutFile(context.Context, string, string, uint32, bool, EmitOpts) error {
	return nil
}
func (e *rebootFakeExec) GetFile(context.Context, string, bool, EmitOpts) ([]byte, error) {
	return nil, nil
}
func (e *rebootFakeExec) RunCapture(context.Context, string) (string, string, int, error) {
	e.captures++
	if e.captures == 1 {
		return "boot-OLD\n", "", 0, nil
	}
	return "boot-NEW\n", "", 0, nil
}
func (e *rebootFakeExec) Kind() string                                        { return "ssh" }
func (e *rebootFakeExec) ResolveHome(context.Context, string) (string, error) { return "/home/x", nil }

func rebootStepRequest(t *testing.T) *pb.HostStepRequest {
	t.Helper()
	view := stepToView(&RebootStep{CandyName: "nvidia-driver"})
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal reboot view: %v", err)
	}
	return &pb.HostStepRequest{StepJson: b}
}

// TestRebootStepViaRunHostStep_SkipsNonRebootableVenue proves a RebootStep on a NON-rebootable
// venue (a host venue — local; rebootable=false) is skip-and-noted: no reboot fired, empty
// teardown ops, no error. This preserves the prior in-proc LocalDeployTarget behaviour (never
// reboot the operator/remote host from a plugin walk).
func TestRebootStepViaRunHostStep_SkipsNonRebootableVenue(t *testing.T) {
	fe := &rebootFakeExec{}
	s := &executorReverseServer{exec: fe, rebootable: false}
	reply, err := s.RunHostStep(context.Background(), rebootStepRequest(t))
	if err != nil {
		t.Fatalf("RunHostStep: %v", err)
	}
	if reply.GetError() != "" {
		t.Fatalf("reply error: %s", reply.GetError())
	}
	if fe.rebootFired {
		t.Error("a non-rebootable venue must NOT fire a reboot")
	}
	var ops []ReverseOp
	if len(reply.GetReverseOpsJson()) > 0 {
		_ = json.Unmarshal(reply.GetReverseOpsJson(), &ops)
	}
	if len(ops) != 0 {
		t.Errorf("RebootStep has no teardown op, got %d", len(ops))
	}
}

// TestRebootStepViaRunHostStep_RebootsRebootableVenue proves a RebootStep on a REBOOTABLE
// venue (a VM guest; rebootable=true) fires the reboot and waits for the boot_id change
// (the fake returns a changed boot_id on the first poll), completing without error.
func TestRebootStepViaRunHostStep_RebootsRebootableVenue(t *testing.T) {
	fe := &rebootFakeExec{}
	s := &executorReverseServer{exec: fe, rebootable: true}
	reply, err := s.RunHostStep(context.Background(), rebootStepRequest(t))
	if err != nil {
		t.Fatalf("RunHostStep: %v", err)
	}
	if reply.GetError() != "" {
		t.Fatalf("reply error: %s", reply.GetError())
	}
	if !fe.rebootFired {
		t.Error("a rebootable venue must fire the reboot")
	}
	if fe.captures < 2 {
		t.Errorf("expected a pre-reboot boot_id read + at least one post-reboot poll, got %d captures", fe.captures)
	}
}

// TestVmDeployVenueWireRoundTrip proves the deploy:vm substrate ships NO substrate payload
// (the executor IS the venue; {{.Home}} resolves guest-side host-side) — the venue
// descriptor carries only the deploy name + env, exactly like deploy:local.
func TestVmDeployVenueWireRoundTrip(t *testing.T) {
	v := spec.DeployVenue{DeployName: "check-arch-vm"}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got spec.DeployVenue
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.DeployName != "check-arch-vm" || len(got.Substrate) != 0 {
		t.Errorf("vm venue should carry only the deploy name + no substrate payload, got %+v", got)
	}
}
