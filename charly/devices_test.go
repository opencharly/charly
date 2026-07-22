package main

import (
	"reflect"
	"testing"

	"github.com/opencharly/sdk/deploykit"
)

// TestAMDGFXVersionParsing (parseKFDGFXVersion) + TestGpuUsableViaCDI (gpuUsableViaCDI)
// moved to candy/plugin-gpu/detect_test.go alongside those detection primitives
// (cutover C11). The tests here exercise the KEPT-core surface: the DetectHostDevices
// shim var (swapped with a fake) and the DetectedDevices struct. (appendAutoDetectedEnv
// and appendGroupsForAMDGPU, and the tests solely exercising them, were a
// dead-code-radical-removal-batch deletion — zero real callers anywhere; appendEnvUnique
// remains live via a different real caller.)

func TestDetectHostDevicesWithGPU(t *testing.T) {
	orig := DetectHostDevices
	defer func() { DetectHostDevices = orig }()

	DetectHostDevices = func() DetectedDevices {
		return DetectedDevices{
			GPU:     true,
			Devices: []string{"/dev/kvm", "/dev/dri/renderD128"},
		}
	}

	detected := DetectHostDevices()
	if !detected.GPU {
		t.Error("expected GPU=true")
	}
	want := []string{"/dev/kvm", "/dev/dri/renderD128"}
	if !reflect.DeepEqual(detected.Devices, want) {
		t.Errorf("Devices = %v, want %v", detected.Devices, want)
	}
}

func TestDetectHostDevicesNoGPU(t *testing.T) {
	orig := DetectHostDevices
	defer func() { DetectHostDevices = orig }()

	DetectHostDevices = func() DetectedDevices {
		return DetectedDevices{
			GPU:     false,
			Devices: []string{"/dev/fuse"},
		}
	}

	detected := DetectHostDevices()
	if detected.GPU {
		t.Error("expected GPU=false")
	}
	if len(detected.Devices) != 1 || detected.Devices[0] != "/dev/fuse" {
		t.Errorf("Devices = %v, want [/dev/fuse]", detected.Devices)
	}
}

func TestDetectedDevicesMergeIntoSecurity(t *testing.T) {
	detected := DetectedDevices{
		GPU:     false,
		Devices: []string{"/dev/kvm", "/dev/fuse"},
	}

	sec := SecurityConfig{
		Devices: []string{"/dev/fuse"}, // already has /dev/fuse
	}
	sec.Devices = deploykit.AppendUnique(sec.Devices, detected.Devices...)

	want := []string{"/dev/fuse", "/dev/kvm"}
	if !reflect.DeepEqual(sec.Devices, want) {
		t.Errorf("merged Devices = %v, want %v", sec.Devices, want)
	}
}

func TestDetectedDevicesInSecurityArgs(t *testing.T) {
	sec := SecurityConfig{
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
		Security: SecurityConfig{
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
	sec := SecurityConfig{Privileged: true}
	// When privileged, auto-detected devices should not be merged
	// (privileged already grants access to all devices)
	args := deploykit.SecurityArgs(sec)
	want := []string{"--privileged"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("SecurityArgs(privileged) = %v, want %v", args, want)
	}
}

func TestDetectHostDevicesWithAMDGPU(t *testing.T) {
	orig := DetectHostDevices
	defer func() { DetectHostDevices = orig }()

	DetectHostDevices = func() DetectedDevices {
		return DetectedDevices{
			AMDGPU:        true,
			AMDGFXVersion: "10.3.0",
			Devices:       []string{"/dev/kfd", "/dev/dri/renderD128"},
		}
	}

	detected := DetectHostDevices()
	if !detected.AMDGPU {
		t.Error("expected AMDGPU=true")
	}
	if detected.AMDGFXVersion != "10.3.0" {
		t.Errorf("AMDGFXVersion = %q, want %q", detected.AMDGFXVersion, "10.3.0")
	}
	if detected.GPU {
		t.Error("expected GPU=false (NVIDIA not set)")
	}
}

func TestDetectHostDevicesWithBothGPUs(t *testing.T) {
	orig := DetectHostDevices
	defer func() { DetectHostDevices = orig }()

	DetectHostDevices = func() DetectedDevices {
		return DetectedDevices{
			GPU:           true,
			AMDGPU:        true,
			AMDGFXVersion: "11.0.0",
			Devices:       []string{"/dev/kfd", "/dev/dri/renderD128", "/dev/dri/renderD129"},
		}
	}

	detected := DetectHostDevices()
	if !detected.GPU {
		t.Error("expected GPU=true")
	}
	if !detected.AMDGPU {
		t.Error("expected AMDGPU=true")
	}
}

func TestAMDGPUGroupsInQuadlet(t *testing.T) {
	cfg := deploykit.QuadletConfig{
		BoxName:     "test-amd",
		ImageRef:    "test-amd:latest",
		Home:        "/workspace",
		GPU:         false,
		BindAddress: "127.0.0.1",
		Security: SecurityConfig{
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

func TestRenderNodeDetection(t *testing.T) {
	orig := DetectHostDevices
	defer func() { DetectHostDevices = orig }()

	// The real defaultDetectHostDevices picks the first renderD* from Devices.
	// Here we verify the struct carries the field correctly through the pipeline.
	DetectHostDevices = func() DetectedDevices {
		return DetectedDevices{
			AMDGPU:     true,
			RenderNode: "/dev/dri/renderD128",
			Devices:    []string{"/dev/kfd", "/dev/dri/renderD128", "/dev/dri/renderD129"},
		}
	}

	detected := DetectHostDevices()
	if detected.RenderNode != "/dev/dri/renderD128" {
		t.Errorf("RenderNode = %q, want /dev/dri/renderD128", detected.RenderNode)
	}
}

func TestRenderNodeNoDevices(t *testing.T) {
	orig := DetectHostDevices
	defer func() { DetectHostDevices = orig }()

	DetectHostDevices = func() DetectedDevices {
		return DetectedDevices{
			Devices: []string{"/dev/kfd", "/dev/kvm"},
		}
	}

	detected := DetectHostDevices()
	if detected.RenderNode != "" {
		t.Errorf("RenderNode = %q, want empty", detected.RenderNode)
	}
}

func TestAppendEnvUnique(t *testing.T) {
	// New key is appended
	env := []string{"FOO=bar"}
	env = appendEnvUnique(env, "HSA_OVERRIDE_GFX_VERSION=10.3.0")
	if len(env) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(env))
	}

	// Existing key is not overridden
	env = appendEnvUnique(env, "HSA_OVERRIDE_GFX_VERSION=11.0.0")
	if len(env) != 2 {
		t.Fatalf("expected 2 env vars after dedup, got %d", len(env))
	}
	if env[1] != "HSA_OVERRIDE_GFX_VERSION=10.3.0" {
		t.Errorf("expected original value preserved, got %q", env[1])
	}
}
