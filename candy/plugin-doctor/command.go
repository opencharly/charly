package doctor

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// command.go — the externalized `charly doctor` command. The plugin OWNS the ENTIRE host-dependency
// report: the check list, the group orchestration, the pass/warn/fail verdicts, the human + JSON
// formatting, the exit code, AND the pure host ops it runs itself (binary probes via exec.LookPath /
// exec.Command, file reads via os.Stat / os.ReadFile). Core keeps ONLY the genuine host-hardware
// subsystem the plugin cannot hold — the GPU/VFIO/device detection primitives, credentialHealth, and
// the core install-hint / device-description tables — reached ONCE via the generic "hostprobe" HostBuild
// seam, which returns RAW FACTS ONLY (no formatting or verdict logic in core). No hidden core-command
// forward.
//
// doctor is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process and
// gets the in-proc reverse channel (provider_command_external.go dispatchInProcCommand threads it), so
// HostBuild("hostprobe") reaches the host engine. The out-of-process CliMain path has NO reverse
// channel, so it errors — doctor cannot run out-of-process (it needs the hostprobe host seam).

// execLookPath wraps os/exec.LookPath; a package var so tests swap it (mirrors core's exec_LookPath).
var execLookPath = osexec.LookPath

// fuseConfPath is the fuse.conf location; a package var so tests point it elsewhere.
var fuseConfPath = "/etc/fuse.conf"

// DoctorCheckStatus represents the result of a single dependency check.
type DoctorCheckStatus int

const (
	CheckOK      DoctorCheckStatus = iota // installed and working
	CheckMissing                          // not found
	CheckWarning                          // found but with caveats
	CheckInfo                             // informational (hardware, not a dep)
	CheckAbsent                           // hardware not present (neutral)
)

// DoctorCheckResult holds the outcome of a single check.
type DoctorCheckResult struct {
	Name        string            `json:"name"`
	Status      DoctorCheckStatus `json:"status"`
	Version     string            `json:"version,omitempty"`
	Detail      string            `json:"detail,omitempty"`
	InstallHint string            `json:"install_hint,omitempty"`
}

// CheckGroup organizes checks by feature area.
type CheckGroup struct {
	Name     string              `json:"name"`
	Required bool                `json:"required"`
	OrLogic  bool                `json:"or_logic,omitempty"` // at least one check must pass
	Checks   []DoctorCheckResult `json:"checks"`
}

// HardwareInfo holds device detection results for JSON output.
type HardwareInfo struct {
	GPU            bool         `json:"gpu"`
	AMDGPU         bool         `json:"amd_gpu"`
	AMDGFXVersion  string       `json:"amd_gfx_version,omitempty"`
	GPUFlags       []string     `json:"gpu_flags"`
	Devices        []DeviceInfo `json:"devices"`
	ContainerFlags []string     `json:"container_flags"`
}

// DeviceInfo describes a single detected/absent device.
type DeviceInfo struct {
	Pattern     string `json:"pattern"`
	Path        string `json:"path,omitempty"`
	Description string `json:"description"`
	Present     bool   `json:"present"`
}

// Distro is the HOST distribution reported by the hostprobe seam plus the core install-hint tables the
// plugin renders hints from. The three scalar fields mirror core's Distro (no json tags → JSON keys
// ID/Name/Manager); the two maps are unexported so they never leak into --json output.
type Distro struct {
	ID      string
	Name    string
	Manager string
	hints   map[string]map[string]string
	family  map[string]string
}

// distroFamily maps a distro ID to its base family for install-hint lookup. An unlisted distro maps to
// itself.
func (d Distro) distroFamily(id string) string {
	if fam, ok := d.family[id]; ok {
		return fam
	}
	return id
}

// installHint returns a distro-appropriate install command for the given binary (empty manager → the
// bare binary name).
func (d Distro) installHint(binary string) string {
	if d.Manager == "" {
		return binary
	}
	if pkgMap, ok := d.hints[binary]; ok {
		// Try exact distro ID first
		if pkg, ok := pkgMap[d.ID]; ok {
			// AUR packages include their own install command
			if strings.Contains(pkg, "AUR:") {
				return strings.TrimSpace(pkg[strings.Index(pkg, "AUR:")+4:])
			}
			return fmt.Sprintf("%s %s", d.Manager, pkg)
		}
		// Try distro family
		family := d.distroFamily(d.ID)
		if pkg, ok := pkgMap[family]; ok {
			if strings.Contains(pkg, "AUR:") {
				return strings.TrimSpace(pkg[strings.Index(pkg, "AUR:")+4:])
			}
			return fmt.Sprintf("%s %s", d.Manager, pkg)
		}
	}
	return fmt.Sprintf("%s %s", d.Manager, binary)
}

// DoctorOutput is the JSON output structure.
type DoctorOutput struct {
	System   Distro       `json:"system"`
	Groups   []CheckGroup `json:"groups"`
	Hardware HardwareInfo `json:"hardware"`
	Summary  struct {
		Installed int `json:"installed"`
		Missing   int `json:"missing"`
		Warnings  int `json:"warnings"`
		Devices   int `json:"devices"`
	} `json:"summary"`
}

// runDoctorCLI parses the doctor flags, fetches the raw host facts ONCE over the hostprobe seam, then
// runs the ENTIRE report (checks, verdicts, formatting, exit code) against those facts.
func runDoctorCLI(ctx context.Context, exec *sdk.Executor, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "Output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	reply, err := hostProbe(ctx, exec)
	if err != nil {
		return err
	}
	distro := Distro{
		ID:      reply.Distro.ID,
		Name:    reply.Distro.Name,
		Manager: reply.Distro.Manager,
		hints:   reply.InstallHints,
		family:  reply.DistroFamilyMap,
	}
	groups := runDoctorChecks(distro, reply)
	hardware := runHardwareChecks(reply)

	if *jsonOut {
		return printJSON(distro, groups, hardware)
	}
	return printHuman(distro, groups, hardware)
}

// hostProbe asks the host to run the GPU/VFIO/device/credential detection primitives via the generic
// "hostprobe" HostBuild kind, returning RAW facts. exec is nil on the out-of-process CliMain path (no
// reverse channel) → a clear error.
func hostProbe(ctx context.Context, exec *sdk.Executor) (spec.HostProbeReply, error) {
	if exec == nil {
		return spec.HostProbeReply{}, fmt.Errorf("charly doctor requires compiled-in placement (the hostprobe host seam is unavailable out-of-process)")
	}
	reqJSON, err := json.Marshal(spec.HostProbeRequest{})
	if err != nil {
		return spec.HostProbeReply{}, err
	}
	resJSON, err := exec.HostBuild(ctx, "hostprobe", reqJSON)
	if err != nil {
		return spec.HostProbeReply{}, err
	}
	var reply spec.HostProbeReply
	if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
		return spec.HostProbeReply{}, uerr
	}
	if reply.Error != "" {
		return spec.HostProbeReply{}, fmt.Errorf("%s", reply.Error)
	}
	return reply, nil
}

// runDoctorChecks runs all dependency checks and returns grouped results.
func runDoctorChecks(distro Distro, reply spec.HostProbeReply) []CheckGroup {
	groups := []CheckGroup{
		{
			Name:     "Container Engine (required -- at least one)",
			Required: true,
			OrLogic:  true,
			Checks: []DoctorCheckResult{
				checkBinary("docker", distro),
				checkBinary("podman", distro),
			},
		},
		{
			Name:     "Build Infrastructure (recommended)",
			Required: false,
			Checks:   buildInfraChecks(distro),
		},
		{
			Name:     "Service Management (quadlet mode)",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("systemctl", distro),
				checkQuadletPodman(distro),
			},
		},
		{
			Name:     "Virtual Machines",
			Required: false,
			Checks:   vmChecks(distro),
		},
		{
			Name:     "VFIO / GPU passthrough",
			Required: false,
			Checks:   vfioChecks(reply),
		},
		{
			Name:     "Encrypted Storage",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("gocryptfs", distro),
				checkBinary("fusermount3", distro),
				checkBinary("systemd-ask-password", distro),
				checkFuseAllowOther(),
			},
		},
		{
			Name:     "Secret Storage",
			Required: false,
			Checks:   secretStorageChecks(reply),
		},
		{
			Name:     "Tunnels",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("tailscale", distro),
				checkBinary("cloudflared", distro),
			},
		},
		{
			Name:     "Merge & Registry",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("skopeo", distro),
			},
		},
		{
			Name:     "Shell & TTY",
			Required: false,
			Checks: []DoctorCheckResult{
				checkBinary("script", distro),
			},
		},
	}

	// Only show podman machine group if podman is installed
	if _, err := execLookPath("podman"); err == nil {
		groups = append(groups, CheckGroup{
			Name:     "Podman Machine",
			Required: false,
			Checks: []DoctorCheckResult{
				checkGvproxyDoctor(distro),
			},
		})
	}

	return groups
}

func buildInfraChecks(distro Distro) []DoctorCheckResult {
	checks := []DoctorCheckResult{
		checkGo(),
		checkBinary("git", distro),
	}
	// Only check buildx if docker is available
	if _, err := execLookPath("docker"); err == nil {
		checks = append(checks, checkBuildxBuilder())
	}
	// transient_store lifts the podman store-lock ceiling for concurrent bed/build runs
	if _, err := execLookPath("podman"); err == nil {
		checks = append(checks, checkTransientStore())
	}
	return checks
}

func vmChecks(distro Distro) []DoctorCheckResult {
	qemuBin := vmshared.QemuSystemBinary()
	checks := []DoctorCheckResult{
		checkBinary(qemuBin, distro),
		checkBinary("qemu-img", distro),
		checkVirtiofsd(distro),
		checkBinary("virsh", distro),
		checkBinary("ssh", distro),
		checkLibvirtSocket(distro),
	}
	return checks
}

// vfioChecks reports host readiness for VFIO GPU passthrough, rendered from the hostprobe seam's raw
// VFIO facts (the same DetectVFIO probe `charly vm gpu` consumes).
func vfioChecks(reply spec.HostProbeReply) []DoctorCheckResult {
	var rep spec.VFIOReport
	if reply.Vfio != nil {
		rep = *reply.Vfio
	}
	var checks []DoctorCheckResult

	if rep.IOMMUEnabled {
		detail := "enabled"
		if rep.IOMMUKind != "" {
			detail = rep.IOMMUKind + " — enabled"
		}
		checks = append(checks, DoctorCheckResult{Name: "IOMMU", Status: CheckOK, Detail: detail})
	} else {
		checks = append(checks, DoctorCheckResult{
			Name:        "IOMMU",
			Status:      CheckWarning,
			Detail:      "not enabled (/sys/kernel/iommu_groups empty)",
			InstallHint: "add intel_iommu=on or amd_iommu=on plus iommu=pt to the kernel cmdline, then reboot",
		})
	}

	if reply.VfioPciAvailable {
		checks = append(checks, DoctorCheckResult{Name: "vfio-pci driver", Status: CheckOK})
	} else {
		checks = append(checks, DoctorCheckResult{
			Name:        "vfio-pci driver",
			Status:      CheckWarning,
			Detail:      "not loaded",
			InstallHint: "sudo modprobe vfio-pci (libvirt managed='yes' loads it on VM start)",
		})
	}

	// memlock — VFIO pins all guest RAM; a rootless session needs a high limit.
	hard := reply.MemlockHard
	if memlockUnlimited(hard) {
		checks = append(checks, DoctorCheckResult{Name: "memlock limit", Status: CheckOK, Detail: "unlimited"})
	} else if hard >= 16<<30 {
		checks = append(checks, DoctorCheckResult{Name: "memlock limit", Status: CheckOK, Detail: fmt.Sprintf("%d MiB", hard>>20)})
	} else {
		checks = append(checks, DoctorCheckResult{
			Name:        "memlock limit",
			Status:      CheckWarning,
			Detail:      fmt.Sprintf("%d MiB — too low for GPU passthrough (needs >= guest RAM)", hard>>20),
			InstallHint: "raise RLIMIT_MEMLOCK for the libvirt session (limits.d 'hard memlock unlimited' + re-login)",
		})
	}

	if len(rep.GPUs) == 0 {
		checks = append(checks, DoctorCheckResult{Name: "passthrough GPU", Status: CheckAbsent, Detail: "none detected"})
		return checks
	}
	for _, g := range rep.GPUs {
		grp := "no IOMMU group"
		access := ""
		if g.IOMMUGroup >= 0 {
			grp = fmt.Sprintf("group %d", g.IOMMUGroup)
			if reply.GroupAccessible[g.IOMMUGroup] {
				access = ", /dev/vfio rw"
			} else {
				access = fmt.Sprintf(", /dev/vfio/%d NOT accessible (charly udev install)", g.IOMMUGroup)
			}
		}
		drv := g.Driver
		if drv == "" {
			drv = "unbound"
		}
		checks = append(checks, DoctorCheckResult{
			Name:   g.Addr,
			Status: CheckInfo,
			Detail: fmt.Sprintf("%s:%s %s — driver=%s, %s%s", trim0x(g.VendorID), trim0x(g.DeviceID), g.ClassLabel, drv, grp, access),
		})
	}
	return checks
}

// checkVirtiofsd checks for virtiofsd which may be installed outside PATH.
// On Arch Linux it installs to /usr/lib/virtiofsd, on RHEL to /usr/libexec/virtiofsd.
func checkVirtiofsd(distro Distro) DoctorCheckResult {
	if path, err := execLookPath("virtiofsd"); err == nil {
		return DoctorCheckResult{
			Name:   "virtiofsd",
			Status: CheckOK,
			Detail: path,
		}
	}
	// Check non-PATH locations where distros install virtiofsd
	for _, path := range []string{"/usr/lib/virtiofsd", "/usr/libexec/virtiofsd"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return DoctorCheckResult{
				Name:   "virtiofsd",
				Status: CheckOK,
				Detail: path,
			}
		}
	}
	return DoctorCheckResult{
		Name:        "virtiofsd",
		Status:      CheckMissing,
		Detail:      "not found",
		InstallHint: distro.installHint("virtiofsd"),
	}
}

// checkBinary checks if a binary exists in PATH and tries to get its version.
func checkBinary(name string, distro Distro) DoctorCheckResult {
	path, err := execLookPath(name)
	if err != nil {
		return DoctorCheckResult{
			Name:        name,
			Status:      CheckMissing,
			Detail:      "not found",
			InstallHint: distro.installHint(name),
		}
	}
	version := getBinaryVersion(name)
	return DoctorCheckResult{
		Name:    name,
		Status:  CheckOK,
		Version: version,
		Detail:  path,
	}
}

// checkGo checks for Go and validates the version.
func checkGo() DoctorCheckResult {
	path, err := execLookPath("go")
	if err != nil {
		return DoctorCheckResult{
			Name:        "go",
			Status:      CheckMissing,
			Detail:      "not found (required to build charly from source)",
			InstallHint: "install Go 1.25.3+",
		}
	}
	version := getBinaryVersion("go")
	return DoctorCheckResult{
		Name:    "go",
		Status:  CheckOK,
		Version: version,
		Detail:  path,
	}
}

// checkQuadletPodman checks if podman is available for quadlet mode.
func checkQuadletPodman(distro Distro) DoctorCheckResult {
	if _, err := execLookPath("podman"); err != nil {
		return DoctorCheckResult{
			Name:        "podman (for quadlet)",
			Status:      CheckWarning,
			Detail:      "quadlet mode requires podman",
			InstallHint: distro.installHint("podman"),
		}
	}
	return DoctorCheckResult{
		Name:   "podman (for quadlet)",
		Status: CheckOK,
		Detail: "available",
	}
}

// checkBuildxBuilder checks if Docker buildx is available.
func checkBuildxBuilder() DoctorCheckResult {
	cmd := osexec.Command("docker", "buildx", "version")
	out, err := cmd.Output()
	if err != nil {
		return DoctorCheckResult{
			Name:        "docker buildx",
			Status:      CheckMissing,
			Detail:      "not available",
			InstallHint: "install docker-buildx",
		}
	}
	version := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return DoctorCheckResult{
		Name:    "docker buildx",
		Status:  CheckOK,
		Version: version,
	}
}

// checkTransientStore reports whether podman's container store runs in TRANSIENT mode
// (container run-state in a per-boot tmpfs runroot DB). charly's disposable check beds
// churn many CONCURRENT container ops (build stages, `podman run --rm` probes, pod
// create, teardown `rm`); with the default persistent store that concurrency contends
// on the single graphroot sqlite write lock past its busy-timeout and ops fail with
// `Error: beginning transaction: database is locked` (measured ceiling ~14-20 concurrent
// beds; transient_store lifts it — the same maxjobs-22 roster went from every-bed-locked
// to 0 locks, 32/37 pass). A single serial `charly check run` never contends, so OFF is
// a WARNING (not an error) with the exact fix. Images stay persistent in graphroot
// either way; only container run-state is tmpfs (recreated from quadlets on reboot,
// volume data persists).
func checkTransientStore() DoctorCheckResult {
	out, err := osexec.Command("podman", "info", "--format", "{{.Store.TransientStore}}").Output()
	if err != nil {
		return DoctorCheckResult{Name: "transient_store", Status: CheckWarning, Detail: "could not query podman store"}
	}
	if strings.TrimSpace(string(out)) == "true" {
		return DoctorCheckResult{Name: "transient_store", Status: CheckOK, Detail: "on -- concurrent check-run/build store-lock contention removed"}
	}
	return DoctorCheckResult{
		Name:        "transient_store",
		Status:      CheckWarning,
		Detail:      "off -- concurrent `charly check run`/builds can hit `database is locked` (podman store-lock); harmless for serial runs",
		InstallHint: `printf '[storage]\ntransient_store = true\n' >> ~/.config/containers/storage.conf  # container state -> tmpfs; recreates from quadlets on reboot, volume data persists`,
	}
}

// checkLibvirtSocket checks if the libvirt session socket exists.
func checkLibvirtSocket(distro Distro) DoctorCheckResult {
	sockPath := vmshared.LibvirtSessionSocket()
	if _, err := os.Stat(sockPath); err == nil {
		return DoctorCheckResult{
			Name:   "libvirt session socket",
			Status: CheckOK,
			Detail: sockPath,
		}
	}

	// Determine the best hint based on what's available
	hint := "systemctl --user enable --now libvirtd.socket"

	// On Arch/modern distros, user-level libvirtd.socket may not exist.
	// Check if system-level virtqemud is available instead.
	if distro.ID == "arch" {
		hint = "sudo systemctl enable --now virtqemud.socket && sudo usermod -aG libvirt $USER"
	}

	return DoctorCheckResult{
		Name:        "libvirt session socket",
		Status:      CheckMissing,
		Detail:      sockPath,
		InstallHint: hint,
	}
}

// checkGvproxyDoctor checks gvproxy availability (same logic as checkGvproxy in machine.go).
func checkGvproxyDoctor(distro Distro) DoctorCheckResult {
	if _, err := execLookPath("gvproxy"); err == nil {
		path, _ := execLookPath("gvproxy")
		return DoctorCheckResult{
			Name:   "gvproxy",
			Status: CheckOK,
			Detail: path,
		}
	}
	for _, path := range []string{"/usr/libexec/podman/gvproxy", "/usr/local/libexec/podman/gvproxy", "/usr/lib/podman/gvproxy"} {
		if _, err := os.Stat(path); err == nil {
			return DoctorCheckResult{
				Name:   "gvproxy",
				Status: CheckOK,
				Detail: path,
			}
		}
	}
	return DoctorCheckResult{
		Name:        "gvproxy",
		Status:      CheckMissing,
		Detail:      "not found",
		InstallHint: distro.installHint("gvproxy"),
	}
}

// getBinaryVersion tries to get the version string from a binary.
func getBinaryVersion(name string) string {
	cmd := osexec.Command(name, "--version")
	out, err := cmd.Output()
	if err != nil {
		// Some tools use "version" instead of "--version"
		cmd = osexec.Command(name, "version")
		out, err = cmd.Output()
		if err != nil {
			return ""
		}
	}
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if len(line) > 80 {
		line = line[:80]
	}
	return line
}

// runHardwareChecks renders the HardwareInfo report from the hostprobe seam's raw GPU/device facts,
// computing the container-flag set (the derived verdict) plugin-side.
func runHardwareChecks(reply spec.HostProbeReply) HardwareInfo {
	hw := HardwareInfo{}

	hw.GPU = reply.GPU
	if hw.GPU {
		hw.GPUFlags = reply.GPUFlags
		hw.ContainerFlags = append(hw.ContainerFlags, hw.GPUFlags...)
	}

	hw.AMDGPU = reply.AMDGPU
	if hw.AMDGPU {
		hw.AMDGFXVersion = reply.AMDGFXVersion
		hw.ContainerFlags = append(hw.ContainerFlags, "--group-add", "keep-groups")
	}

	for _, d := range reply.Devices {
		hw.Devices = append(hw.Devices, DeviceInfo{
			Pattern:     d.Pattern,
			Path:        d.Path,
			Description: d.Description,
			Present:     d.Present,
		})
		if d.Present {
			hw.ContainerFlags = append(hw.ContainerFlags, "--device", d.Path)
		}
	}

	return hw
}

func printHuman(distro Distro, groups []CheckGroup, hw HardwareInfo) error {
	fmt.Println("charly doctor")
	fmt.Println("=========")
	fmt.Printf("System: %s (%s)\n", distro.Name, managerShort(distro.Manager))
	fmt.Println()

	installed, missing, warnings := 0, 0, 0
	requiredFailed := false

	for _, g := range groups {
		groupStatus := groupStatusSymbol(g)
		fmt.Printf("[%s] %s\n", groupStatus, g.Name)

		for _, ch := range g.Checks {
			symbol, line := formatCheck(ch)
			fmt.Printf("  [%s] %s\n", symbol, line)
			switch ch.Status {
			case CheckOK:
				installed++
			case CheckMissing:
				missing++
				if g.Required && !g.OrLogic {
					requiredFailed = true
				}
			case CheckWarning:
				warnings++
			}
		}

		// For OR-logic groups, check if at least one passed
		if g.Required && g.OrLogic {
			anyOK := false
			for _, ch := range g.Checks {
				if ch.Status == CheckOK {
					anyOK = true
					break
				}
			}
			if !anyOK {
				requiredFailed = true
			}
		}

		fmt.Println()
	}

	// Hardware section
	deviceCount := 0
	fmt.Println("[OK] Hardware & Auto-Detected Devices")
	if hw.GPU {
		fmt.Printf("  [+] NVIDIA GPU -- detected (%s)\n", strings.Join(hw.GPUFlags, " "))
	} else {
		fmt.Println("  [ ] NVIDIA GPU -- not detected")
	}
	if hw.AMDGPU {
		label := "detected (--group-add keep-groups)"
		if hw.AMDGFXVersion != "" {
			label = fmt.Sprintf("detected gfx %s (--group-add keep-groups)", hw.AMDGFXVersion)
		}
		fmt.Printf("  [+] AMD GPU -- %s\n", label)
	} else {
		fmt.Println("  [ ] AMD GPU -- not detected")
	}

	for _, d := range hw.Devices {
		if d.Present {
			fmt.Printf("  [+] %s -- %s\n", d.Path, d.Description)
			deviceCount++
		} else {
			fmt.Printf("  [ ] %s -- not present\n", d.Path)
		}
	}

	if hw.GPU {
		deviceCount++
	}
	if hw.AMDGPU {
		deviceCount++
	}

	fmt.Println()
	if len(hw.ContainerFlags) > 0 {
		fmt.Printf("  Containers will receive: %s\n", strings.Join(hw.ContainerFlags, " "))
	} else {
		fmt.Println("  No devices will be passed to containers")
	}
	fmt.Println("  Disable with: --no-autodetect")
	fmt.Println()

	fmt.Printf("Summary: %d found, %d missing, %d warnings, %d devices detected\n",
		installed, missing, warnings, deviceCount)

	if requiredFailed {
		return fmt.Errorf("required dependencies missing")
	}
	return nil
}

func printJSON(distro Distro, groups []CheckGroup, hw HardwareInfo) error {
	output := DoctorOutput{
		System:   distro,
		Groups:   groups,
		Hardware: hw,
	}

	deviceCount := 0
	if hw.GPU {
		deviceCount++
	}
	if hw.AMDGPU {
		deviceCount++
	}
	for _, d := range hw.Devices {
		if d.Present {
			deviceCount++
		}
	}

	for _, g := range groups {
		for _, ch := range g.Checks {
			switch ch.Status {
			case CheckOK:
				output.Summary.Installed++
			case CheckMissing:
				output.Summary.Missing++
			case CheckWarning:
				output.Summary.Warnings++
			}
		}
	}
	output.Summary.Devices = deviceCount

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func groupStatusSymbol(g CheckGroup) string {
	if g.OrLogic {
		// At least one must pass
		for _, ch := range g.Checks {
			if ch.Status == CheckOK {
				return "OK"
			}
		}
		if g.Required {
			return "!!"
		}
		return "--"
	}

	allOK := true
	anyOK := false
	for _, ch := range g.Checks {
		if ch.Status != CheckOK {
			allOK = false
		} else {
			anyOK = true
		}
	}
	if allOK {
		return "OK"
	}
	if g.Required {
		return "!!"
	}
	if anyOK {
		return "!!"
	}
	return "--"
}

func formatCheck(ch DoctorCheckResult) (string, string) {
	switch ch.Status {
	case CheckOK:
		parts := []string{ch.Name}
		if ch.Version != "" {
			parts = append(parts, "--", ch.Version)
		}
		if ch.Detail != "" && ch.Version == "" {
			parts = append(parts, "--", ch.Detail)
		}
		return "+", strings.Join(parts, " ")
	case CheckMissing:
		line := ch.Name + " -- " + ch.Detail
		if ch.InstallHint != "" {
			line += " (" + ch.InstallHint + ")"
		}
		return "-", line
	case CheckWarning:
		line := ch.Name + " -- " + ch.Detail
		if ch.InstallHint != "" {
			line += " (" + ch.InstallHint + ")"
		}
		return "!", line
	default:
		return " ", ch.Name
	}
}

func managerShort(manager string) string {
	if manager == "" {
		return "unknown package manager"
	}
	return manager
}

// checkFuseAllowOther reports whether fuse.conf enables user_allow_other — required for the
// `gocryptfs -allow_other` every charly encrypted-volume mount uses (rootless-podman keep-id
// access). Missing → a WARNING with the exact fix, so `charly doctor` flags the prereq
// proactively instead of leaving the operator to hit the raw fusermount3 error at mount time.
// A pure host op the plugin runs itself (os.ReadFile).
func checkFuseAllowOther() DoctorCheckResult {
	if deploykit.FuseAllowOtherEnabled() {
		return DoctorCheckResult{Name: "user_allow_other", Status: CheckOK, Detail: fuseConfPath}
	}
	return DoctorCheckResult{
		Name:        "user_allow_other",
		Status:      CheckWarning,
		Detail:      "not enabled in " + fuseConfPath + " (encrypted volumes will fail to mount)",
		InstallHint: "echo user_allow_other | sudo tee -a " + fuseConfPath,
	}
}

// fuseAllowOtherEnabled DELETED (Cutover B unit 2) — a duplicate of the now-shared
// deploykit.FuseAllowOtherEnabled (sdk/deploykit/enc_probe.go); checkFuseAllowOther calls that
// directly. fuseConfPath stays HERE as a plain path constant for the Detail/InstallHint message
// text (not logic duplication — deploykit's own equivalent var is unexported and serves the
// actual check, this one only formats a display string).

// secretStorageChecks returns checks for the credential/secret storage subsystem, rendered from the
// hostprobe seam's credential-health reply (the keyring + Secret Service probing lives in
// candy/plugin-secrets, reached host-side via verb:credential). The config-file PERMISSIONS check is a
// pure host op the plugin runs itself (os.Stat) against the seam-reported config path.
func secretStorageChecks(reply spec.HostProbeReply) []DoctorCheckResult {
	var checks []DoctorCheckResult

	h := reply.Credential
	if h == nil {
		return append(checks, DoctorCheckResult{
			Name:        "Secret backend",
			Status:      CheckWarning,
			Detail:      fmt.Sprintf("credential plugin unavailable: %s", reply.CredentialErr),
			InstallHint: "Install candy/plugin-secrets alongside charly (/usr/lib/charly/plugins), or run from a project composing it",
		})
	}

	// Check 1: Secret backend availability
	switch {
	case h.KeyringAvailable && !h.KeyringLocked:
		checks = append(checks, DoctorCheckResult{Name: "Secret backend", Status: CheckOK, Version: "system keyring"})
	case h.KeyringLocked:
		checks = append(checks, DoctorCheckResult{
			Name:        "Secret backend",
			Status:      CheckWarning,
			Version:     "system keyring (LOCKED)",
			Detail:      "keyring is locked — credentials unavailable until unlocked",
			InstallHint: "Unlock your keyring, or run: charly settings set secret_backend config",
		})
	case h.ConfiguredBackend == "config":
		checks = append(checks, DoctorCheckResult{Name: "Secret backend", Status: CheckOK, Version: "config file (explicit)"})
	default:
		checks = append(checks, DoctorCheckResult{
			Name:        "Secret backend",
			Status:      CheckWarning,
			Detail:      "config file (no keyring available)",
			InstallHint: "Install gnome-keyring or keepassxc for secure credential storage, or run: charly settings set secret_backend config",
		})
	}

	// Check 2: Config file permissions (config path from the seam).
	if reply.ConfigPath != "" {
		if info, statErr := os.Stat(reply.ConfigPath); statErr == nil {
			perm := info.Mode().Perm()
			if perm&0077 == 0 {
				checks = append(checks, DoctorCheckResult{Name: "Config permissions", Status: CheckOK, Version: fmt.Sprintf("%04o", perm)})
			} else {
				checks = append(checks, DoctorCheckResult{
					Name:        "Config permissions",
					Status:      CheckWarning,
					Detail:      fmt.Sprintf("%04o (world-readable)", perm),
					InstallHint: fmt.Sprintf("Run: chmod 600 %s", reply.ConfigPath),
				})
			}
		}
	}

	// Check 3: Plaintext credentials count
	if h.PlaintextCount == 0 {
		checks = append(checks, DoctorCheckResult{Name: "Plaintext credentials", Status: CheckOK, Version: "0"})
	} else {
		checks = append(checks, DoctorCheckResult{
			Name:        "Plaintext credentials",
			Status:      CheckWarning,
			Detail:      fmt.Sprintf("%d in config.yml", h.PlaintextCount),
			InstallHint: "Run: charly secrets migrate-secrets --dry-run",
		})
	}

	// Check 4: Secret Service collection health + Check 5: shadow index consistency.
	checks = append(checks, keyringCollectionChecks(h)...)
	checks = append(checks, keyringIndexChecks(h)...)
	return checks
}

// keyringCollectionChecks renders the Secret Service collection-health DoctorCheckResult
// from the seam's health reply. Skips silently when there's no session bus / no
// collections (already covered by "Secret backend" above).
func keyringCollectionChecks(h *spec.CredentialHealth) []DoctorCheckResult {
	if h.CollErr != "" {
		return []DoctorCheckResult{{
			Name:        "Secret Service collections",
			Status:      CheckWarning,
			Detail:      fmt.Sprintf("cannot list collections: %s", h.CollErr),
			InstallHint: "Check that your Secret Service provider (gnome-keyring, keepassxc) is running correctly",
		}}
	}
	if h.NoSession {
		return nil
	}
	if len(h.BrokenColls) == 0 {
		return []DoctorCheckResult{{
			Name:    "Secret Service collections",
			Status:  CheckOK,
			Version: fmt.Sprintf("%d healthy", len(h.HealthyColls)),
			Detail:  strings.Join(h.HealthyColls, ", "),
		}}
	}
	return []DoctorCheckResult{{
		Name:    "Secret Service collections",
		Status:  CheckWarning,
		Version: fmt.Sprintf("%d healthy + %d broken", len(h.HealthyColls), len(h.BrokenColls)),
		Detail: fmt.Sprintf(
			"charly will iterate and skip broken. Broken: %s. Healthy: %s",
			strings.Join(h.BrokenColls, ", "), strings.Join(h.HealthyColls, ", ")),
		InstallHint: "Consider cleaning stale entries in your Secret Service provider (e.g. KeePassXC → Tools → Settings → Secret Service Integration → Exposed Databases)",
	}}
}

// keyringIndexChecks renders the config.yml KeyringKeys shadow-index consistency
// DoctorCheckResult from the seam's health reply (indexed entries not retrievable from
// any collection indicate drift).
func keyringIndexChecks(h *spec.CredentialHealth) []DoctorCheckResult {
	if h.IndexTotal == 0 {
		return nil
	}
	if len(h.IndexMissing) == 0 {
		return []DoctorCheckResult{{
			Name:    "Keyring index consistency",
			Status:  CheckOK,
			Version: fmt.Sprintf("%d/%d", h.IndexTotal, h.IndexTotal),
		}}
	}
	return []DoctorCheckResult{{
		Name:    "Keyring index consistency",
		Status:  CheckWarning,
		Version: fmt.Sprintf("%d indexed, %d missing", h.IndexTotal, len(h.IndexMissing)),
		Detail: fmt.Sprintf(
			"indexed but not found in any collection: %s", strings.Join(h.IndexMissing, ", ")),
		InstallHint: "Re-store with `charly secrets set <service> <key>` or prune stale index entries",
	}}
}

// memlockUnlimited reports whether the hard limit is effectively unlimited (a cross-module twin of the
// core helper; core keeps its own copy for vm_gpu_cmd, so this is not in-module duplication).
func memlockUnlimited(hard uint64) bool { return hard >= 1<<62 }

// trim0x drops a leading 0x for compact "vendor:device" display (cross-module twin of the core helper).
func trim0x(s string) string { return strings.TrimPrefix(s, "0x") }
