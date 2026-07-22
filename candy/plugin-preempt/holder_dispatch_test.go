package preempt

import (
	"bytes"
	"context"
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
// charly/preempt.go + charly/arbiter_host.go + charly/gpu_shim.go into this plugin
// (holder_dispatch.go). Real coverage for the moved code — NOT a rerun of a throwaway spike
// test — per the pod-venue (target="") paths, which need no live exec.HostArbiter / InvokeProvider
// round-trip to exercise (a departed pod holder never reaches the VM/InvokeProvider branch).

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
// InvokeProvider is implemented (replies with a canned result_json), every other RPC panics if
// called — since pluginHolderStart's VM branch touches ONLY InvokeProvider. Lets a test construct
// a real *sdk.Executor (sdk.NewInProcExecutor) without a live candy/plugin-vm process, mirroring
// candy/plugin-deploy-vm/lifecycle_test.go's fakeExecutorServiceClient pattern (R3 — same test
// double shape, one per module since no test helper crosses the module boundary).
type fakeVmInvokeProviderClient struct {
	pb.ExecutorServiceClient
	resultJSON []byte
}

func (f *fakeVmInvokeProviderClient) InvokeProvider(ctx context.Context, in *pb.InvokeProviderRequest, opts ...grpc.CallOption) (*pb.InvokeReply, error) {
	return &pb.InvokeReply{ResultJson: f.resultJSON}, nil
}

// TestPluginHolderStart_VmDomainNotFoundIsNoOp is the break-it-proven regression test for the
// TOCTOU restore-race the validator RCA'd on check-preempt-vm-live: the fix eliminates the
// separate existence pre-check entirely, so this test simply proves that a "domain not found"
// reply FROM THE START RPC ITSELF (exactly what candy/plugin-vm's provider.go replies for a
// domain destroyed by a concurrent teardown, or one that never existed — the two cases are now
// indistinguishable BY DESIGN, which is the point: there is no longer a stale-observation window
// for a race to land in) is treated as a clean no-op, not a hard error stranding the lease. FAILS
// against the pre-fix code, which never even reached this point for a departed VM UNLESS the
// stale pre-check's own race window happened to close the other way.
func TestPluginHolderStart_VmDomainNotFoundIsNoOp(t *testing.T) {
	fake := &fakeVmInvokeProviderClient{
		resultJSON: []byte(`{"error":"starting VM charly-preempt-vm-holder: domain not found: Domain not found: no domain with matching name 'charly-preempt-vm-holder'"}`),
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
		resultJSON: []byte(`{"error":"starting VM charly-preempt-vm-holder: Requested operation is not valid: network 'default' is not active"}`),
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
