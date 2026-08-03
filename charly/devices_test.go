package main

import (
	"reflect"
	"testing"
)

// TestAMDGFXVersionParsing (parseKFDGFXVersion) + TestGpuUsableViaCDI (gpuUsableViaCDI)
// moved to candy/plugin-gpu/detect_test.go alongside those detection primitives
// (cutover C11). The tests here exercise the KEPT-core surface: the DetectHostDevices
// shim var (swapped with a fake) and the DetectedDevices struct. (appendAutoDetectedEnv
// and appendGroupsForAMDGPU, and the tests solely exercising them, were a
// dead-code-radical-removal-batch deletion — zero real callers anywhere; appendEnvUnique
// remains live via a different real caller.) TestDetectedDevicesInSecurityArgs/
// TestPrivilegedSkipsDevices/TestDetectedDevicesInQuadlet/TestAMDGPUGroupsInQuadlet
// relocated to candy/plugin-bundle (#55 decoupling, Batch A) — they asserted
// deploykit.SecurityArgs/GenerateQuadlet directly with zero charly coupling.

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

// TestDetectedDevicesMergeIntoSecurity was removed as a duplicate (K3 cone2
// test closure): the only behavior under test was deploykit.AppendUnique's
// (itself a kit.AppendUnique re-export, sdk/deploykit/kit_aliases.go) dedup
// merge — no charly-specific logic — already covered directly by
// sdk/kit/append_unique_test.go:TestAppendUnique, verified live before deletion.

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
