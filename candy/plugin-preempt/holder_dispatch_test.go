package preempt

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
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
	if pluginHolderExists(context.Background(), nil, addr) {
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
