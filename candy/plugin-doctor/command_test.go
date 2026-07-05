package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencharly/sdk/spec"
)

func TestCheckBinaryFound(t *testing.T) {
	orig := execLookPath
	defer func() { execLookPath = orig }()

	execLookPath = func(name string) (string, error) {
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
	orig := execLookPath
	defer func() { execLookPath = orig }()

	execLookPath = func(name string) (string, error) {
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

// TestRunHardwareChecks proves the hardware report is rendered from the hostprobe seam's raw facts:
// GPU/AMDGPU reflect the reply, and each device carries its seam-supplied description; a present device
// contributes its --device container flag.
func TestRunHardwareChecks(t *testing.T) {
	reply := spec.HostProbeReply{
		GPU:    false,
		AMDGPU: false,
		Devices: []spec.HostProbeDevice{
			{Pattern: "/dev/kvm", Path: "/dev/kvm", Present: true, Description: "KVM virtualization"},
			{Pattern: "/dev/dri/renderD*", Path: "/dev/dri/renderD*", Present: false, Description: "GPU render node"},
		},
	}
	hw := runHardwareChecks(reply)

	if hw.GPU {
		t.Error("expected GPU=false from the reply")
	}
	if hw.AMDGPU {
		t.Error("expected AMDGPU=false from the reply")
	}
	if len(hw.Devices) != 2 {
		t.Fatalf("expected 2 device entries, got %d", len(hw.Devices))
	}
	for _, d := range hw.Devices {
		if d.Description == "" {
			t.Errorf("device %q has no description", d.Path)
		}
	}
	// The present /dev/kvm contributes --device /dev/kvm; the absent renderD* does not.
	if len(hw.ContainerFlags) != 2 || hw.ContainerFlags[0] != "--device" || hw.ContainerFlags[1] != "/dev/kvm" {
		t.Errorf("ContainerFlags = %v, want [--device /dev/kvm]", hw.ContainerFlags)
	}
}

// TestCheckFuseAllowOther proves the encrypted-storage user_allow_other check (a pure host op the
// plugin runs itself) reports OK when set and a WARNING + fix hint when missing.
func TestCheckFuseAllowOther(t *testing.T) {
	withFuseConf := func(body string) {
		orig := fuseConfPath
		t.Cleanup(func() { fuseConfPath = orig })
		p := filepath.Join(t.TempDir(), "fuse.conf")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		fuseConfPath = p
	}

	withFuseConf("user_allow_other\n")
	if r := checkFuseAllowOther(); r.Status != CheckOK {
		t.Fatalf("enabled: status = %v, want CheckOK", r.Status)
	}
	withFuseConf("#user_allow_other\n")
	if r := checkFuseAllowOther(); r.Status != CheckWarning || r.InstallHint == "" {
		t.Fatalf("missing: status = %v hint = %q, want CheckWarning + a fix hint", r.Status, r.InstallHint)
	}
}
