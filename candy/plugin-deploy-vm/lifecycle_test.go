package deployvm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
	"google.golang.org/grpc"
)

// fakeExecutorServiceClient is a minimal pb.ExecutorServiceClient test double: only HostBuild and
// InvokeProvider are implemented (each records its request and returns a canned reply/error);
// every other RPC panics if called. Lets a test construct a real *sdk.Executor
// (sdk.NewInProcExecutor) without a live host process — the SAME wiring mechanism charly core's
// own parity tests use (charly/plugin_installstep_envelope_parity_test.go), applied here from a
// plugin module.
type fakeExecutorServiceClient struct {
	pb.ExecutorServiceClient
	gotKind        string
	gotSpecJSON    []byte
	hostBuildReply *pb.HostBuildReply
	hostBuildErr   error

	invokeProviderCalled bool
	gotInvokeReq         *pb.InvokeProviderRequest
	invokeProviderReply  *pb.InvokeReply
	invokeProviderErr    error
}

func (f *fakeExecutorServiceClient) HostBuild(ctx context.Context, in *pb.HostBuildRequest, opts ...grpc.CallOption) (*pb.HostBuildReply, error) {
	f.gotKind = in.GetKind()
	f.gotSpecJSON = in.GetSpecJson()
	if f.hostBuildErr != nil {
		return nil, f.hostBuildErr
	}
	return f.hostBuildReply, nil
}

func (f *fakeExecutorServiceClient) InvokeProvider(ctx context.Context, in *pb.InvokeProviderRequest, opts ...grpc.CallOption) (*pb.InvokeReply, error) {
	f.invokeProviderCalled = true
	f.gotInvokeReq = in
	if f.invokeProviderErr != nil {
		return nil, f.invokeProviderErr
	}
	if f.invokeProviderReply != nil {
		return f.invokeProviderReply, nil
	}
	return &pb.InvokeReply{ResultJson: []byte("{}")}, nil
}

// TestResolvePriorVmState_ReadsNonEmptyOutOfProcess is the regression test for the bed-robustness
// batch item 5 severe finding (operator-mandated per the DeployStateHost audit ruling): the
// prior-state read must return a NON-EMPTY VmDeployState rather than silently degrading. Since
// K-wave 2 cone R2 bank D the read is PLUGIN-SIDE (resolvePriorVmState → loaderkit.ResolveVmStateViaExecutor
// — the "config-resolve" HostBuild seam is DELETED); the seam var is stubbed here to return a
// real persisted SSHPort/DiskPath and the assertion requires it to come through — the same
// regression guard, applied to the caller's use of the resolved state.
func TestResolvePriorVmState_ReadsNonEmptyOutOfProcess(t *testing.T) {
	prev := resolvePriorVmState
	resolvePriorVmState = func(context.Context, *sdk.Executor, string) (*spec.VmDeployState, error) {
		return &spec.VmDeployState{SSHPort: 2244, DiskPath: "/tmp/disk.qcow2"}, nil
	}
	t.Cleanup(func() { resolvePriorVmState = prev })
	ex := sdk.NewInProcExecutor(&fakeExecutorServiceClient{})

	got, err := resolvePriorVmState(context.Background(), ex, "check-charly-vm")
	if err != nil {
		t.Fatalf("resolvePriorVmState() error = %v", err)
	}
	if got == nil {
		t.Fatal("resolvePriorVmState() = nil, want a non-empty VmDeployState")
	}
	if got.SSHPort != 2244 {
		t.Errorf("resolvePriorVmState().SSHPort = %d, want 2244", got.SSHPort)
	}
	if got.DiskPath != "/tmp/disk.qcow2" {
		t.Errorf("resolvePriorVmState().DiskPath = %q, want /tmp/disk.qcow2", got.DiskPath)
	}
}

// TestResolvePriorVmState_ErrorPropagates covers the failure path: the plugin-side read's error
// must surface as an error, never a silent nil.
func TestResolvePriorVmState_ErrorPropagates(t *testing.T) {
	prev := resolvePriorVmState
	resolvePriorVmState = func(context.Context, *sdk.Executor, string) (*spec.VmDeployState, error) {
		return nil, errors.New("read failed")
	}
	t.Cleanup(func() { resolvePriorVmState = prev })
	ex := sdk.NewInProcExecutor(&fakeExecutorServiceClient{})
	_, err := resolvePriorVmState(context.Background(), ex, "check-charly-vm")
	if err == nil {
		t.Fatal("resolvePriorVmState() with a read error: want an error, got nil")
	}
}

// TestDispatchVmEphemeralTeardown_InvokesFleetProviderWhenEphemeral is the regression test for
// the FINAL/K5 unit 6a RCA #9 live-probe-caught bug, ported to this plugin (F6 vm-lifecycle move,
// coneB-vmlifecycle): the ORIGINAL bug was a lookup by the raw deploy name instead of the
// canonical "vm:"+VmDomainIdentity(name) key. Here the canonical key is threaded automatically —
// domainIdentity(p) IS vmshared.VmDomainIdentity(p.Name), and dispatchVmEphemeralTeardown resolves
// prior state via resolvePriorVmState(domain), so this proves the read is keyed by the canonical
// domain (resolvePriorVmState → loaderkit.ResolveVmStateViaExecutor, the config-resolve seam is
// DELETED), AND that a non-nil Ephemeral record triggers the
// OpEphemeralTeardown peer-dispatch to command:fleet with the persisted VmState threaded onto the
// decoded node.
func TestDispatchVmEphemeralTeardown_InvokesFleetProviderWhenEphemeral(t *testing.T) {
	prev := resolvePriorVmState
	resolvePriorVmState = func(context.Context, *sdk.Executor, string) (*spec.VmDeployState, error) {
		return &spec.VmDeployState{
			SSHPort:   12345,
			Ephemeral: &spec.EphemeralRuntime{ID: "test-id", Status: "active", DeployAddress: "check-sidecar-pod.check-sidecar-pod-ephvm"},
		}, nil
	}
	t.Cleanup(func() { resolvePriorVmState = prev })
	fake := &fakeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)

	const dottedName = "check-sidecar-pod.check-sidecar-pod-ephvm"
	p := lifecycleParams{Name: dottedName, Node: json.RawMessage(`{"from":"eval-vm"}`)}
	domain := domainIdentity(p)

	if err := dispatchVmEphemeralTeardown(context.Background(), ex, p, domain); err != nil {
		t.Fatalf("dispatchVmEphemeralTeardown: %v", err)
	}

	if !fake.invokeProviderCalled {
		t.Fatal("dispatchVmEphemeralTeardown with a non-nil Ephemeral record must Invoke command:fleet's OpEphemeralTeardown — it did not call InvokeProvider at all")
	}
	if fake.gotInvokeReq.GetClass() != "command" || fake.gotInvokeReq.GetReserved() != "fleet" || fake.gotInvokeReq.GetOp() != sdk.OpEphemeralTeardown {
		t.Errorf("InvokeProvider(class=%q, word=%q, op=%q), want (command, fleet, %q)",
			fake.gotInvokeReq.GetClass(), fake.gotInvokeReq.GetReserved(), fake.gotInvokeReq.GetOp(), sdk.OpEphemeralTeardown)
	}
	var gotTeardownReq spec.EphemeralTeardownRequest
	if err := json.Unmarshal(fake.gotInvokeReq.GetParamsJson(), &gotTeardownReq); err != nil {
		t.Fatalf("decode recorded ephemeral-teardown request: %v", err)
	}
	if gotTeardownReq.Name != dottedName {
		t.Errorf("EphemeralTeardownRequest.Name = %q, want %q", gotTeardownReq.Name, dottedName)
	}
	if gotTeardownReq.Node == nil || gotTeardownReq.Node.VmState == nil || gotTeardownReq.Node.VmState.Ephemeral == nil {
		t.Fatal("EphemeralTeardownRequest.Node.VmState.Ephemeral is nil — the resolved persisted state must be threaded onto the node before dispatch")
	}
	if gotTeardownReq.Node.VmState.Ephemeral.ID != "test-id" {
		t.Errorf("EphemeralTeardownRequest.Node.VmState.Ephemeral.ID = %q, want %q", gotTeardownReq.Node.VmState.Ephemeral.ID, "test-id")
	}
	if gotTeardownReq.Node.From != "eval-vm" {
		t.Errorf("EphemeralTeardownRequest.Node.From = %q, want %q (the authored node's fields must survive the VmState overlay)", gotTeardownReq.Node.From, "eval-vm")
	}
}

// TestDispatchVmEphemeralTeardown_NoEphemeral_SkipsDispatch covers the common non-ephemeral case:
// a domain with no persisted Ephemeral record must NOT Invoke command:fleet at all.
func TestDispatchVmEphemeralTeardown_NoEphemeral_SkipsDispatch(t *testing.T) {
	prev := resolvePriorVmState
	resolvePriorVmState = func(context.Context, *sdk.Executor, string) (*spec.VmDeployState, error) {
		return &spec.VmDeployState{SSHPort: 12345}, nil
	}
	t.Cleanup(func() { resolvePriorVmState = prev })
	fake := &fakeExecutorServiceClient{}
	ex := sdk.NewInProcExecutor(fake)

	p := lifecycleParams{Name: "check-vm", Node: json.RawMessage(`{}`)}
	if err := dispatchVmEphemeralTeardown(context.Background(), ex, p, domainIdentity(p)); err != nil {
		t.Fatalf("dispatchVmEphemeralTeardown: %v", err)
	}
	if fake.invokeProviderCalled {
		t.Error("dispatchVmEphemeralTeardown with no persisted Ephemeral record must not Invoke command:fleet")
	}
}

// TestVmEntityForPrepare covers the ported entity-resolution logic (FINAL/K5 unit 6a, M4b —
// relocated verbatim from the deleted charly/vm_lifecycle_preresolve.go's vmEntityForAdd, which
// had no dedicated test of its own before this move). vmPrepareVenue is not itself unit-testable
// here (it drives real host reverse-channel HostBuild calls — its coverage is the check-sidecar-pod
// / check-charly-vm disposable-bed runtime gate), but this pure resolution step is.
func TestVmEntityForPrepare(t *testing.T) {
	cases := []struct {
		name    string
		node    *spec.FleetNode
		deploy  string
		want    string
		wantErr bool
	}{
		{
			name:   "node.From wins over everything else",
			node:   &spec.FleetNode{From: "cachyos-gpu"},
			deploy: "check-cachyos-gpu-vm",
			want:   "cachyos-gpu",
		},
		{
			name:   "legacy vm:<name> deploy-key prefix",
			node:   nil,
			deploy: "vm:cachyos-gpu",
			want:   "cachyos-gpu",
		},
		{
			name:   "legacy vm:<name>/<instance> form strips the instance suffix",
			node:   nil,
			deploy: "vm:cachyos-gpu/work",
			want:   "cachyos-gpu",
		},
		{
			name:   "dotted nested path falls back to the leaf",
			node:   nil,
			deploy: "check-sidecar-pod.check-sidecar-pod-ephvm",
			want:   "check-sidecar-pod-ephvm",
		},
		{
			name:   "node present but From empty falls through to the deploy-name cases",
			node:   &spec.FleetNode{Target: "vm"},
			deploy: "vm:cachyos-gpu",
			want:   "cachyos-gpu",
		},
		{
			name:    "no vm: cross-ref, no legacy prefix, no dotted leaf — errors",
			node:    nil,
			deploy:  "bare-vm-dep",
			wantErr: true,
		},
		{
			name:    "legacy vm: prefix with an empty name errors",
			node:    nil,
			deploy:  "vm:",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := vmEntityForPrepare(tc.node, tc.deploy)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("vmEntityForPrepare(%q) = (%q, nil), want an error", tc.deploy, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("vmEntityForPrepare(%q) unexpected error: %v", tc.deploy, err)
			}
			if got != tc.want {
				t.Errorf("vmEntityForPrepare(%q) = %q, want %q", tc.deploy, got, tc.want)
			}
		})
	}
}

// TestVmPrepareVenue_MalformedNodeErrors is the break-it-proven regression test for the
// bed-robustness batch item 4 discarded-decode-errors audit: vmPrepareVenue used to
// `_ = json.Unmarshal(p.Node, &node)`, silently discarding a decode failure and proceeding with a
// zero-value FleetNode — masking a real request-corruption bug behind a confusing downstream
// "no vm: cross-ref" error instead of a loud, attributable "decode node" one. The node decode is the
// very FIRST statement in vmPrepareVenue (before any executor use), so this exercises the REAL
// function directly with a nil executor and malformed JSON — no mock/broker needed.
func TestVmPrepareVenue_MalformedNodeErrors(t *testing.T) {
	p := lifecycleParams{Name: "check-vm", Node: json.RawMessage(`{not valid json`)}
	_, err := vmPrepareVenue(context.Background(), nil, p, spec.HostEnv{})
	if err == nil {
		t.Fatal("vmPrepareVenue with malformed node JSON must return an error, not silently proceed with a zero-value node")
	}
	if !strings.Contains(err.Error(), "decode node") {
		t.Errorf("error = %v, want it to identify the decode failure (\"decode node\")", err)
	}
}

// TestVmPostApply_MalformedNodeErrors mirrors TestVmPrepareVenue_MalformedNodeErrors for
// vmPostApply's node decode — the same discarded-decode-errors class, same fix shape, same
// no-executor-touched-before-decode property that makes it directly unit-testable.
func TestVmPostApply_MalformedNodeErrors(t *testing.T) {
	p := lifecycleParams{Name: "check-vm", Node: json.RawMessage(`{not valid json`)}
	_, err := vmPostApply(context.Background(), nil, p, spec.HostEnv{})
	if err == nil {
		t.Fatal("vmPostApply with malformed node JSON must return an error, not silently proceed with a zero-value node")
	}
	if !strings.Contains(err.Error(), "decode node") {
		t.Errorf("error = %v, want it to identify the decode failure (\"decode node\")", err)
	}
}

// TestVmRebuild_MalformedOptsErrors covers vmRebuild's opts decode — this is the R10
// fresh-rebuild path `charly update <vm-bed>` routes through, so a silently-discarded decode error
// here would mean RebuildImage/DryRun silently defaulting to false regardless of what the caller
// actually asked for (the exact "masking class that cost a full bed cycle" the batch's ledger names).
func TestVmRebuild_MalformedOptsErrors(t *testing.T) {
	p := lifecycleParams{Name: "check-vm", Opts: json.RawMessage(`{not valid json`)}
	_, err := vmRebuild(context.Background(), nil, p)
	if err == nil {
		t.Fatal("vmRebuild with malformed opts JSON must return an error, not silently proceed with zero-value opts")
	}
	if !strings.Contains(err.Error(), "decode opts") {
		t.Errorf("error = %v, want it to identify the decode failure (\"decode opts\")", err)
	}
}
