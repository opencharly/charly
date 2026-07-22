package deployvm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"google.golang.org/grpc"
)

// fakeExecutorServiceClient is a minimal pb.ExecutorServiceClient test double: only HostBuild is
// implemented (it records the request and returns hostBuildReply/hostBuildErr); every other RPC
// panics if called, since resolvePriorVmState touches ONLY HostBuild. Lets a test construct a real
// *sdk.Executor (sdk.NewInProcExecutor) without a live host process — the SAME wiring mechanism
// charly core's own parity tests use (charly/plugin_installstep_envelope_parity_test.go), applied
// here from a plugin module.
type fakeExecutorServiceClient struct {
	pb.ExecutorServiceClient
	gotKind        string
	gotSpecJSON    []byte
	hostBuildReply *pb.HostBuildReply
	hostBuildErr   error
}

func (f *fakeExecutorServiceClient) HostBuild(ctx context.Context, in *pb.HostBuildRequest, opts ...grpc.CallOption) (*pb.HostBuildReply, error) {
	f.gotKind = in.GetKind()
	f.gotSpecJSON = in.GetSpecJson()
	if f.hostBuildErr != nil {
		return nil, f.hostBuildErr
	}
	return f.hostBuildReply, nil
}

// TestResolvePriorVmState_ReadsNonEmptyOutOfProcess is the regression test for the bed-robustness
// batch item 5 severe finding (operator-mandated per the DeployStateHost audit ruling): the
// prior-state read must go through the "config-resolve" HostBuild seam and correctly decode a
// NON-EMPTY VmDeployState — proving the fix actually crosses the plugin↔host boundary rather than
// silently degrading. This test FAILS on the pre-fix shape: the old code called
// deploykit.LoadDeployConfigForRead directly and NEVER touched the executor at all, so with this
// fake (which only answers HostBuild) the pre-fix code path would leave `prior` nil/empty
// regardless of what the fake returns — exactly the silent-empty-out-of-process defect. Here the
// fake DOES answer with a real persisted SshPort, and the assertion requires it to come through.
func TestResolvePriorVmState_ReadsNonEmptyOutOfProcess(t *testing.T) {
	wantReply := spec.ConfigResolveReply{VmState: &spec.VmDeployState{SshPort: 2244, DiskPath: "/tmp/disk.qcow2"}}
	replyJSON, err := json.Marshal(wantReply)
	if err != nil {
		t.Fatalf("marshal fixture reply: %v", err)
	}
	fake := &fakeExecutorServiceClient{hostBuildReply: &pb.HostBuildReply{ResultJson: replyJSON}}
	ex := sdk.NewInProcExecutor(fake)

	got, err := resolvePriorVmState(context.Background(), ex, "check-charly-vm")
	if err != nil {
		t.Fatalf("resolvePriorVmState() error = %v", err)
	}
	if got == nil {
		t.Fatal("resolvePriorVmState() = nil, want the fake's non-empty VmDeployState — the pre-fix direct-file-read path never reaches the executor at all and would leave this nil")
	}
	if got.SshPort != 2244 {
		t.Errorf("resolvePriorVmState().SshPort = %d, want 2244 (the fake's persisted value, proving the seam round-trip actually happened)", got.SshPort)
	}
	if got.DiskPath != "/tmp/disk.qcow2" {
		t.Errorf("resolvePriorVmState().DiskPath = %q, want /tmp/disk.qcow2", got.DiskPath)
	}

	// The request itself must target the "config-resolve" seam with the domain as Entity — the
	// EXACT shape candy/plugin-vm's own hostConfigResolve uses for the SAME persisted-port lookup
	// (R3 — one seam contract, not a divergent one this plugin invented).
	if fake.gotKind != "config-resolve" {
		t.Errorf("HostBuild kind = %q, want %q", fake.gotKind, "config-resolve")
	}
	var gotReq spec.ConfigResolveRequest
	if err := json.Unmarshal(fake.gotSpecJSON, &gotReq); err != nil {
		t.Fatalf("decode recorded HostBuild request: %v", err)
	}
	if gotReq.Entity != "check-charly-vm" {
		t.Errorf("ConfigResolveRequest.Entity = %q, want the domainID %q", gotReq.Entity, "check-charly-vm")
	}
}

// TestResolvePriorVmState_HostBuildErrorPropagates covers the failure path: a HostBuild transport
// error (e.g. no reverse channel) must surface as an error, never a silent nil.
func TestResolvePriorVmState_HostBuildErrorPropagates(t *testing.T) {
	fake := &fakeExecutorServiceClient{hostBuildErr: errors.New("no host reverse channel")}
	ex := sdk.NewInProcExecutor(fake)
	_, err := resolvePriorVmState(context.Background(), ex, "check-charly-vm")
	if err == nil {
		t.Fatal("resolvePriorVmState() with a HostBuild transport error: want an error, got nil")
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
		node    *spec.BundleNode
		deploy  string
		want    string
		wantErr bool
	}{
		{
			name:   "node.From wins over everything else",
			node:   &spec.BundleNode{From: "cachyos-gpu"},
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
			node:   &spec.BundleNode{Target: "vm"},
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
// zero-value BundleNode — masking a real request-corruption bug behind a confusing downstream
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
