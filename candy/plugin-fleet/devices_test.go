package fleet

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/vmshared"
)

// devices_test.go — relocated (in part) from charly/devices_test.go (#55 decoupling, Batch A):
// these 4 tests assert deploykit.SecurityArgs/GenerateQuadlet directly, zero charly coupling.
// The DetectHostDevices-swap tests + TestAppendEnvUnique + TestDetectedDevicesMergeIntoSecurity
// were DELETED with charly/devices_test.go + charly/devices.go (K-wave 2 cone R3 — the
// detection moved into candy/plugin-deploy-pod's detectDevices, peer verb:gpu).
// containsLine/splitLines live once in this package's security_test.go (R3).

func TestDetectedDevicesInSecurityArgs(t *testing.T) {
	sec := vmshared.SecurityConfig{
		Devices: []string{"/dev/kvm", "/dev/fuse"},
	}
	args := deploykit.SecurityArgs(sec)
	want := []string{
		"--device", "/dev/kvm",
		"--device", "/dev/fuse",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs = %v, want %v", args, want)
	}
}

func TestDetectedDevicesInQuadlet(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:     "test",
		ImageRef:    "test:latest",
		Home:        "/workspace",
		GPU:         true,
		BindAddress: "127.0.0.1",
		Security: vmshared.SecurityConfig{
			Devices: []string{"/dev/kvm", "/dev/fuse"},
		},
	}
	content := deploykit.GenerateQuadlet(cfg)
	if !containsLine(content, "AddDevice=nvidia.com/gpu=all") {
		t.Error("expected AddDevice=nvidia.com/gpu=all for GPU")
	}
	if !containsLine(content, "AddDevice=/dev/kvm") {
		t.Error("expected AddDevice=/dev/kvm")
	}
	if !containsLine(content, "AddDevice=/dev/fuse") {
		t.Error("expected AddDevice=/dev/fuse")
	}
}

func TestPrivilegedSkipsDevices(t *testing.T) {
	sec := vmshared.SecurityConfig{Privileged: true}
	// When privileged, auto-detected devices should not be merged
	// (privileged already grants access to all devices)
	args := deploykit.SecurityArgs(sec)
	want := []string{"--privileged"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged) = %v, want %v", args, want)
	}
}

func TestAMDGPUGroupsInQuadlet(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:     "test-amd",
		ImageRef:    "test-amd:latest",
		Home:        "/workspace",
		GPU:         false,
		BindAddress: "127.0.0.1",
		Security: vmshared.SecurityConfig{
			Devices:  []string{"/dev/kfd", "/dev/dri/renderD128"},
			GroupAdd: []string{"keep-groups"},
		},
	}
	content := deploykit.GenerateQuadlet(cfg)
	if !containsLine(content, "GroupAdd=keep-groups") {
		t.Error("expected GroupAdd=keep-groups in quadlet")
	}
	if !containsLine(content, "AddDevice=/dev/kfd") {
		t.Error("expected AddDevice=/dev/kfd in quadlet")
	}
}
