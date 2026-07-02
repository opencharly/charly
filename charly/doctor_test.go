package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestCheckBinaryFound(t *testing.T) {
	orig := exec_LookPath
	defer func() { exec_LookPath = orig }()

	exec_LookPath = func(name string) (string, error) {
		if name == "git" {
			return "/usr/bin/git", nil
		}
		return "", fmt.Errorf("not found: %s", name)
	}

	distro := Distro{ID: "arch", Name: "Arch Linux", Manager: "pacman -S"}
	result := checkBinary("git", distro)
	if result.Status != CheckOK {
		t.Errorf("Status = %d, want CheckOK (%d)", result.Status, CheckOK)
	}
	if result.Detail != "/usr/bin/git" {
		t.Errorf("Detail = %q, want %q", result.Detail, "/usr/bin/git")
	}
}

func TestCheckBinaryMissing(t *testing.T) {
	orig := exec_LookPath
	defer func() { exec_LookPath = orig }()

	exec_LookPath = func(name string) (string, error) {
		return "", fmt.Errorf("not found: %s", name)
	}

	distro := Distro{ID: "arch", Name: "Arch Linux", Manager: "pacman -S"}
	result := checkBinary("podman", distro)
	if result.Status != CheckMissing {
		t.Errorf("Status = %d, want CheckMissing (%d)", result.Status, CheckMissing)
	}
	if result.InstallHint != "pacman -S podman" {
		t.Errorf("InstallHint = %q, want %q", result.InstallHint, "pacman -S podman")
	}
}

func TestGroupStatusOrLogic(t *testing.T) {
	// At least one OK -> group OK
	g := CheckGroup{
		Required: true,
		OrLogic:  true,
		Checks: []DoctorCheckResult{
			{Name: "docker", Status: CheckOK},
			{Name: "podman", Status: CheckMissing},
		},
	}
	if got := groupStatusSymbol(g); got != "OK" {
		t.Errorf("groupStatusSymbol = %q, want %q", got, "OK")
	}

	// None OK -> group fails
	g.Checks = []DoctorCheckResult{
		{Name: "docker", Status: CheckMissing},
		{Name: "podman", Status: CheckMissing},
	}
	if got := groupStatusSymbol(g); got != "!!" {
		t.Errorf("groupStatusSymbol = %q, want %q", got, "!!")
	}
}

func TestGroupStatusAllOK(t *testing.T) {
	g := CheckGroup{
		Required: false,
		Checks: []DoctorCheckResult{
			{Name: "git", Status: CheckOK},
			{Name: "go", Status: CheckOK},
		},
	}
	if got := groupStatusSymbol(g); got != "OK" {
		t.Errorf("groupStatusSymbol = %q, want %q", got, "OK")
	}
}

func TestGroupStatusPartialOptional(t *testing.T) {
	g := CheckGroup{
		Required: false,
		Checks: []DoctorCheckResult{
			{Name: "tailscale", Status: CheckOK},
			{Name: "cloudflared", Status: CheckMissing},
		},
	}
	if got := groupStatusSymbol(g); got != "!!" {
		t.Errorf("groupStatusSymbol = %q, want %q (partial optional)", got, "!!")
	}
}

func TestGroupStatusAllMissingOptional(t *testing.T) {
	g := CheckGroup{
		Required: false,
		Checks: []DoctorCheckResult{
			{Name: "tailscale", Status: CheckMissing},
			{Name: "cloudflared", Status: CheckMissing},
		},
	}
	if got := groupStatusSymbol(g); got != "--" {
		t.Errorf("groupStatusSymbol = %q, want %q", got, "--")
	}
}

func TestFormatCheckOK(t *testing.T) {
	ch := DoctorCheckResult{Name: "docker", Status: CheckOK, Version: "Docker version 29.3.0"}
	sym, line := formatCheck(ch)
	if sym != "+" {
		t.Errorf("symbol = %q, want %q", sym, "+")
	}
	if line != "docker -- Docker version 29.3.0" {
		t.Errorf("line = %q", line)
	}
}

func TestFormatCheckMissing(t *testing.T) {
	ch := DoctorCheckResult{Name: "podman", Status: CheckMissing, Detail: "not found", InstallHint: "pacman -S podman"}
	sym, line := formatCheck(ch)
	if sym != "-" {
		t.Errorf("symbol = %q, want %q", sym, "-")
	}
	if line != "podman -- not found (pacman -S podman)" {
		t.Errorf("line = %q", line)
	}
}

func TestDoctorOutputJSON(t *testing.T) {
	output := DoctorOutput{
		System: Distro{ID: "arch", Name: "Arch Linux", Manager: "pacman -S"},
		Groups: []CheckGroup{
			{
				Name:     "Container Engine",
				Required: true,
				OrLogic:  true,
				Checks: []DoctorCheckResult{
					{Name: "docker", Status: CheckOK, Version: "29.3.0"},
				},
			},
		},
		Hardware: HardwareInfo{
			GPU:      false,
			GPUFlags: nil,
			Devices: []DeviceInfo{
				{Pattern: "/dev/kvm", Path: "/dev/kvm", Description: "KVM virtualization", Present: true},
			},
			ContainerFlags: []string{"--device", "/dev/kvm"},
		},
	}
	output.Summary.Installed = 1
	output.Summary.Devices = 1

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var parsed DoctorOutput
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if parsed.System.ID != "arch" {
		t.Errorf("System.ID = %q, want %q", parsed.System.ID, "arch")
	}
	if len(parsed.Hardware.ContainerFlags) != 2 {
		t.Errorf("ContainerFlags len = %d, want 2", len(parsed.Hardware.ContainerFlags))
	}
	if parsed.Summary.Devices != 1 {
		t.Errorf("Summary.Devices = %d, want 1", parsed.Summary.Devices)
	}
}

func TestRunHardwareChecks(t *testing.T) {
	origGPU := DetectGPU
	origAMDGPU := DetectAMDGPU
	defer func() {
		DetectGPU = origGPU
		DetectAMDGPU = origAMDGPU
	}()

	DetectGPU = func() bool { return false }
	DetectAMDGPU = func() bool { return false }

	distro := Distro{ID: "arch", Name: "Arch Linux", Manager: "pacman -S"}
	hw := runHardwareChecks(distro)

	if hw.GPU {
		t.Error("expected GPU=false with mocked DetectGPU")
	}
	if hw.AMDGPU {
		t.Error("expected AMDGPU=false with mocked DetectAMDGPU")
	}

	// Should have entries for all device patterns
	if len(hw.Devices) == 0 {
		t.Error("expected at least some device entries")
	}

	// Each device should have a description
	for _, d := range hw.Devices {
		if d.Description == "" {
			t.Errorf("device %q has no description", d.Path)
		}
	}
}
