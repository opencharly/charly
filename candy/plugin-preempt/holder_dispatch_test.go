package preempt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"google.golang.org/grpc"
)

// holder_dispatch_test.go — unit coverage for the FLOOR-SLIM-proper Unit-8 MOVE: the 6 arbiter
// host-seam impls (running/stop[+wait]/start/switchMode/ensureCDI/gpuCDI) that relocated from
// charly/preempt.go + charly/arbiter_host.go (deleted, K1-unblock wave 1) + charly/gpu_shim.go
// into this plugin (holder_dispatch.go). Real coverage for the moved code — NOT a rerun of a
// throwaway spike test — per the pod-venue (target="") paths, which need no live
// exec.InvokeProvider round-trip to exercise (a departed pod holder never reaches the
// VM/InvokeProvider branch).

// captureStderr runs fn with os.Stderr redirected to a pipe and returns whatever was written —
// the plugin-side twin of charly's own captureStderr test helper (R3: one copy per module, since
// no shared test-helper import crosses the module boundary).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	_ = w.Close()
	<-done
	return buf.String()
}

// TestPluginHolderStart_DepartedHolderIsNoOp guards the stranded-lease fix (relocated from
// charly's TestHolderStart_DepartedHolderIsNoOp, which exercised the pre-move holderStart/
// holderExists): a holder whose runtime object (container/quadlet) no longer EXISTS — a departed
// holder, e.g. a torn-down check-bed member — must make pluginHolderStart a NO-OP SUCCESS, not a
// hard `podman start: no such container` error. The error path would make restoreHolders fail and
// strand the lease FOREVER (no `charly preempt restore` could clear it, since it can never restart
// a holder that is gone). A guaranteed-nonexistent deploy name exercises the departed path
// deterministically without any live container or a *sdk.Executor round-trip — the pod-venue
// (addr.Vm=="") existence check is pure sdk/kit + exec.Command, so `exec` stays nil here.
func TestPluginHolderStart_DepartedHolderIsNoOp(t *testing.T) {
	addr := spec.HolderAddr{
		Target:   "pod",
		Name:     "charly-preempt-departed-holder-probe",
		Base:     "preempt-departed-holder-probe-does-not-exist",
		Instance: "",
	}
	if pluginHolderExists(addr) {
		t.Fatalf("test precondition: holder %q must not exist", addr.Name)
	}
	var startErr error
	stderr := captureStderr(t, func() {
		startErr = pluginHolderStart(context.Background(), nil, addr)
	})
	if startErr != nil {
		t.Fatalf("pluginHolderStart on a DEPARTED holder must be a no-op success (else its lease strands forever); got: %v", startErr)
	}
	if !strings.Contains(stderr, "has departed") {
		t.Fatalf("stderr = %q, want departed-holder diagnostic", stderr)
	}
}

// TestPluginHolderRunning_PodVenue exercises the pod-venue (addr.Vm=="") branch of
// pluginHolderRunning against a guaranteed-nonexistent container — the venue discriminator
// (addr.Vm != "") must route to podRunning, not vmRunning, for a plain pod holder.
func TestPluginHolderRunning_PodVenue(t *testing.T) {
	addr := spec.HolderAddr{
		Target: "pod",
		Name:   "charly-preempt-running-probe",
		Base:   "preempt-running-probe-does-not-exist",
	}
	if pluginHolderRunning(context.Background(), nil, addr) {
		t.Fatalf("a nonexistent pod holder must report not-running")
	}
}

// TestDevicesHoldNvidiaGPU covers the pure CDI/device-node matcher the gpuCDI reclaim scan uses.
func TestDevicesHoldNvidiaGPU(t *testing.T) {
	cases := []struct {
		name    string
		devices []string
		want    bool
	}{
		{"empty", nil, false},
		{"cdi-name", []string{"nvidia.com/gpu=all"}, true},
		{"dev-node", []string{"/dev/nvidia0"}, true},
		{"unrelated", []string{"/dev/dri/renderD128"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := devicesHoldNvidiaGPU(c.devices); got != c.want {
				t.Errorf("devicesHoldNvidiaGPU(%v) = %v, want %v", c.devices, got, c.want)
			}
		})
	}
}

// TestVmDomainName covers the deploy-name-keyed libvirt domain identity the VM dispatch
// helpers use — the regression guard for the bug check-preempt-vm-live caught: the domain
// MUST be keyed by the addr's DEPLOY name (addr.Name), never by addr.Vm (the inherited vm
// TEMPLATE entity multiple deploys may share).
func TestVmDomainName(t *testing.T) {
	cases := []struct {
		name string
		addr spec.HolderAddr
		want string
	}{
		{
			name: "deploy name differs from the shared vm template entity",
			addr: spec.HolderAddr{Name: "preempt-vm-holder", Vm: "web-vm"},
			want: "charly-preempt-vm-holder",
		},
		{
			name: "instance-qualified deploy name",
			addr: spec.HolderAddr{Name: "arch/test", Vm: "arch"},
			want: "charly-arch-test",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := vmDomainName(c.addr); got != c.want {
				t.Errorf("vmDomainName(%+v) = %q, want %q", c.addr, got, c.want)
			}
		})
	}
}

// TestIsDomainNotFoundErr covers the pure classification isDomainNotFoundErr — the departed-VM
// detection folded into pluginHolderStart's start-RPC error handling (see its doc comment for
// the TOCTOU this replaces: a separate existence pre-check racing a concurrent teardown).
func TestIsDomainNotFoundErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			name: "candy/plugin-vm's actual reply text for a missing domain",
			err:  errors.New(`starting VM charly-preempt-vm-holder: domain not found: Domain not found: no domain with matching name 'charly-preempt-vm-holder'`),
			want: true,
		},
		{"unrelated libvirt failure", errors.New("starting VM charly-x: some other libvirt error"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDomainNotFoundErr(c.err); got != c.want {
				t.Errorf("isDomainNotFoundErr(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// fakeVmInvokeProviderClient is a minimal pb.ExecutorServiceClient test double: only
// InvokeProvider is implemented, and it is OP-AWARE — it decodes the request's env_json into
// spec.VmPluginEnv and replies differently per VmOp (domainStateResult for "domain-state",
// startResult for "start") — every other RPC panics if called, since pluginHolderStart's VM
// branch touches ONLY InvokeProvider. Being op-aware is what makes this fake capable of
// SIMULATING THE ACTUAL RACE WINDOW (see TestPluginHolderStart_VmRace_DomainGoneBetweenCheckAndAct
// below): an op-BLIND fake that returns one canned reply regardless of which op fired cannot
// discriminate the pre-fix check-then-act shape from the fixed shape at all — the pre-fix code's
// domain-state pre-check would ALSO see the canned "not found" reply and take the SAME no-op path
// coincidentally, proving nothing about the actual defect. Lets a test construct a real
// *sdk.Executor (sdk.NewInProcExecutor) without a live candy/plugin-vm process, mirroring
// candy/plugin-deploy-vm/lifecycle_test.go's fakeExecutorServiceClient pattern (R3 — same test
// double shape, one per module since no test helper crosses the module boundary).
type fakeVmInvokeProviderClient struct {
	pb.ExecutorServiceClient
	domainStateResult []byte // reply for VmOp "domain-state" (the pre-fix code's existence pre-check)
	startResult       []byte // reply for VmOp "start" (the act)
}

func (f *fakeVmInvokeProviderClient) InvokeProvider(ctx context.Context, in *pb.InvokeProviderRequest, opts ...grpc.CallOption) (*pb.InvokeReply, error) {
	var env spec.VmPluginEnv
	_ = json.Unmarshal(in.GetEnvJson(), &env)
	switch env.VmOp {
	case "domain-state":
		return &pb.InvokeReply{ResultJson: f.domainStateResult}, nil
	case "start":
		return &pb.InvokeReply{ResultJson: f.startResult}, nil
	default:
		return &pb.InvokeReply{ResultJson: []byte(`{}`)}, nil
	}
}

// TestPluginHolderStart_VmRace_DomainGoneBetweenCheckAndAct is the DISCRIMINATING break-it-proven
// regression test for the TOCTOU restore-race the validator RCA'd on check-preempt-vm-live. It
// simulates the EXACT race window: the domain-state op (the pre-fix code's existence pre-check)
// replies exists:true — the holder looks ALIVE at check time — but the start op (the act) replies
// domain-not-found — the domain was destroyed by a concurrent teardown in the window between the
// check and the act. Against the FIXED shape (this file's current pluginHolderStart, which never
// calls domain-state for the VM branch at all — there is no check to race) this sequence still
// produces a clean no-op, because the start RPC's OWN error is the sole signal. Against the
// PRE-FIX shape (check-then-act: pluginHolderExists's domain-state call decided existence BEFORE
// calling startVMPlugin) this exact sequence reproduces the bug: the pre-check said "exists", so
// the old code proceeded to call start unconditionally, hit the real domain-not-found error, and
// returned it as a HARD ERROR — stranding the lease. Verified: running this exact test body
// against a restored copy of the pre-fix holder_dispatch.go (git show <pre-fix-commit>, the
// version before this cutover's TOCTOU fix) FAILS with exactly that hard error; see the PR body
// for the pasted verbatim old-shape FAIL alongside this file's new-shape PASS.
func TestPluginHolderStart_VmRace_DomainGoneBetweenCheckAndAct(t *testing.T) {
	fake := &fakeVmInvokeProviderClient{
		domainStateResult: []byte(`{"exists":true,"running":true}`),
		startResult:       []byte(`{"error":"starting VM charly-preempt-vm-holder: domain not found: Domain not found: no domain with matching name 'charly-preempt-vm-holder'"}`),
	}
	ex := sdk.NewInProcExecutor(fake)
	addr := spec.HolderAddr{Target: "vm", Name: "preempt-vm-holder", Vm: "web-vm"}

	var startErr error
	stderr := captureStderr(t, func() {
		startErr = pluginHolderStart(context.Background(), ex, addr)
	})
	if startErr != nil {
		t.Fatalf("pluginHolderStart racing a domain destroyed between check and act must be a no-op success (else its lease strands forever); got: %v", startErr)
	}
	if !strings.Contains(stderr, "has departed") {
		t.Fatalf("stderr = %q, want departed-holder diagnostic", stderr)
	}
}

// TestPluginHolderStart_VmDomainNotFoundIsNoOp covers the simpler, non-racing case: the start RPC
// itself replies domain-not-found (the domain was already long gone, no race involved) — still a
// clean no-op. Kept alongside the discriminating race test above (which is the one that actually
// distinguishes the fix from the bug); this one guards the base case.
func TestPluginHolderStart_VmDomainNotFoundIsNoOp(t *testing.T) {
	fake := &fakeVmInvokeProviderClient{
		startResult: []byte(`{"error":"starting VM charly-preempt-vm-holder: domain not found: Domain not found: no domain with matching name 'charly-preempt-vm-holder'"}`),
	}
	ex := sdk.NewInProcExecutor(fake)
	addr := spec.HolderAddr{Target: "vm", Name: "preempt-vm-holder", Vm: "web-vm"}

	var startErr error
	stderr := captureStderr(t, func() {
		startErr = pluginHolderStart(context.Background(), ex, addr)
	})
	if startErr != nil {
		t.Fatalf("pluginHolderStart on a domain-not-found VM must be a no-op success (else its lease strands forever); got: %v", startErr)
	}
	if !strings.Contains(stderr, "has departed") {
		t.Fatalf("stderr = %q, want departed-holder diagnostic", stderr)
	}
}

// TestPluginHolderStart_VmOtherErrorPropagates guards the OTHER half of the fix: a genuine
// libvirt failure (NOT a missing domain) must still surface as a real error, never be
// mis-classified as departed and silently swallowed.
func TestPluginHolderStart_VmOtherErrorPropagates(t *testing.T) {
	fake := &fakeVmInvokeProviderClient{
		startResult: []byte(`{"error":"starting VM charly-preempt-vm-holder: Requested operation is not valid: network 'default' is not active"}`),
	}
	ex := sdk.NewInProcExecutor(fake)
	addr := spec.HolderAddr{Target: "vm", Name: "preempt-vm-holder", Vm: "web-vm"}

	err := pluginHolderStart(context.Background(), ex, addr)
	if err == nil {
		t.Fatal("pluginHolderStart on a genuine (non-domain-not-found) libvirt failure must propagate the error, not silently no-op")
	}
	if !strings.Contains(err.Error(), "network 'default' is not active") {
		t.Errorf("error = %q, want the underlying libvirt failure text preserved", err.Error())
	}
}
