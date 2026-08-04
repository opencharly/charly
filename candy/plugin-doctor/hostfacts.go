package doctor

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/container"
	"github.com/opencharly/spec/hostenv"
	"github.com/opencharly/spec/spec"
	"gopkg.in/yaml.v3"
)

// hostfacts.go — K5 seam-death: the FORMER "hostprobe" HostBuild kind (charly/
// host_build_hostprobe.go) dissolved. Its "must stay core" header rested on three claims that
// do not survive: (1) the GPU/VFIO/device detection primitives — the plugin reaches
// candy/plugin-gpu's verb:gpu PEER-TO-PEER over InvokeProvider, the exact pattern the arbiter
// and candy/plugin-vm already use for the same primitives (gpu_shim.go's own header documents
// them dispatching "verb:gpu directly"); (2) the credential-store health probe — same pattern,
// verb:credential; (3) the install-hint / device-description / distro data tables — this
// plugin's OWN embed now (data.go/data.yml), the sidecar.go precedent (W3 adjudication item 3:
// "the embed MOVES INTO plugin-deploy-pod, its own go:embed") applied here. Nothing here
// crosses the process boundary except the two peer verb calls a compiled-in plugin already
// makes elsewhere in this codebase.

//go:embed data.yml
var embeddedData []byte

type doctorDataDoc struct {
	InstallHints          map[string]map[string]string `yaml:"install_hints"`
	DeviceDescriptions    map[string]string            `yaml:"device_descriptions"`
	DevicePatterns        []string                     `yaml:"device_patterns"`
	DistroPackageManagers map[string]string            `yaml:"distro_package_managers"`
	DistroFamilyMap       map[string]string            `yaml:"distro_family_map"`
}

var doctorData = parseEmbeddedData()

func parseEmbeddedData() doctorDataDoc {
	var doc doctorDataDoc
	if err := yaml.Unmarshal(embeddedData, &doc); err != nil {
		panic("plugin-doctor: embedded data.yml unparseable: " + err.Error())
	}
	if len(doc.InstallHints) == 0 || len(doc.DeviceDescriptions) == 0 || len(doc.DevicePatterns) == 0 ||
		len(doc.DistroPackageManagers) == 0 || len(doc.DistroFamilyMap) == 0 {
		panic("plugin-doctor: embedded data.yml missing a directive")
	}
	return doc
}

// osReleasePath is the path to the os-release file, overridable for testing.
var osReleasePath = "/etc/os-release"

// detectDistro reads /etc/os-release and returns the detected distribution (ported from
// charly/distro.go — the ONLY consumer was the "hostprobe" seam this plugin now replaces).
func detectDistro() Distro {
	data, err := os.ReadFile(osReleasePath)
	if err != nil {
		return Distro{ID: "unknown", Name: "Unknown", hints: doctorData.InstallHints, family: doctorData.DistroFamilyMap}
	}
	return parseOsRelease(string(data))
}

func parseOsRelease(content string) Distro {
	d := Distro{ID: "unknown", Name: "Unknown", hints: doctorData.InstallHints, family: doctorData.DistroFamilyMap}
	for _, line := range strings.Split(content, "\n") {
		if after, ok := strings.CutPrefix(line, "ID="); ok {
			d.ID = strings.Trim(after, "\"")
		}
		if after, ok := strings.CutPrefix(line, "NAME="); ok {
			d.Name = strings.Trim(after, "\"")
		}
	}
	d.Manager = doctorData.DistroPackageManagers[d.ID]
	return d
}

// probeDevices globs every known device pattern and attaches its human description — this
// plugin's own copy of the former host_build_hostprobe.go loop.
func probeDevices() []DeviceInfo {
	var out []DeviceInfo
	for _, pattern := range doctorData.DevicePatterns {
		desc := doctorData.DeviceDescriptions[pattern]
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			for _, m := range matches {
				out = append(out, DeviceInfo{Pattern: pattern, Path: m, Present: true, Description: desc})
			}
		} else {
			out = append(out, DeviceInfo{Pattern: pattern, Path: pattern, Present: false, Description: desc})
		}
	}
	return out
}

// vfioPciAvailable reports whether the vfio-pci driver is present on the host (a byte-for-byte
// copy of charly/gpu_allocate.go's helper of the same name — that copy is now DEAD there, its
// one caller having been this seam; gpu_allocate.go is another wave's active file, so its
// cleanup rides that wave, not this one).
func vfioPciAvailable() bool {
	for _, p := range []string{"/sys/bus/pci/drivers/vfio-pci", "/sys/module/vfio_pci"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// --- GPU/VFIO detection: peer InvokeProvider to candy/plugin-gpu's verb:gpu ---------------

func gpuProbe(ctx context.Context, exec *sdk.Executor, action string, group int) spec.GpuProbeReply {
	if exec == nil {
		return spec.GpuProbeReply{}
	}
	inJSON, err := json.Marshal(spec.GpuProbeInput{Action: action, Group: group})
	if err != nil {
		return spec.GpuProbeReply{}
	}
	resJSON, err := exec.InvokeProvider(ctx, "verb", "gpu", sdk.OpRun, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return spec.GpuProbeReply{}
	}
	var reply spec.GpuProbeReply
	if len(resJSON) > 0 {
		_ = json.Unmarshal(resJSON, &reply)
	}
	return reply
}

// gpuFacts is the subset of GPU/VFIO facts the doctor report renders, gathered via a handful of
// peer verb:gpu calls (mirrors the former hostBuildHostProbe body one-for-one).
type gpuFacts struct {
	GPU              bool
	AMDGPU           bool
	AMDGFXVersion    string
	GPUFlags         []string
	Vfio             *spec.VFIOReport
	MemlockSoft      uint64
	MemlockHard      uint64
	VfioPciAvailable bool
	GroupAccessible  map[string]bool
}

func gatherGPUFacts(ctx context.Context, exec *sdk.Executor, engine string) gpuFacts {
	var f gpuFacts

	f.GPU = gpuProbe(ctx, exec, "detect-gpu", 0).Bool
	if f.GPU {
		if engine == "" {
			if _, err := execLookPath("podman"); err == nil {
				engine = "podman"
			} else if _, err := execLookPath("docker"); err == nil {
				engine = "docker"
			}
		}
		if engine != "" {
			f.GPUFlags = container.GPURunArgs(engine)
		}
	}
	f.AMDGPU = gpuProbe(ctx, exec, "detect-amd-gpu", 0).Bool
	if f.AMDGPU {
		f.AMDGFXVersion = gpuProbe(ctx, exec, "amd-gfx-version", 0).Str
	}

	vfio := gpuProbe(ctx, exec, "detect-vfio", 0)
	f.Vfio = vfio.Vfio
	if f.Vfio == nil {
		f.Vfio = &spec.VFIOReport{}
	}
	memlock := gpuProbe(ctx, exec, "memlock", 0)
	f.MemlockSoft, f.MemlockHard = memlock.MemlockSoft, memlock.MemlockHard
	f.VfioPciAvailable = vfioPciAvailable()
	f.GroupAccessible = map[string]bool{}
	for _, g := range f.Vfio.GPUs {
		if g.IOMMUGroup >= 0 {
			f.GroupAccessible[strconv.Itoa(g.IOMMUGroup)] = gpuProbe(ctx, exec, "vfio-group-accessible", g.IOMMUGroup).Bool
		}
	}
	return f
}

// --- Credential-store health: peer InvokeProvider to candy/plugin-secrets' verb:credential ---

// credentialHealthInput / credentialHealthReply are the verb:credential `health` wire forms —
// byte-compatible with charly/credential_plugin.go's copy and candy/plugin-secrets/
// verb_credential.go's own. A fourth small hand copy, not a new pattern (see
// sdk/deploykit/credential_executor.go's header: "process-boundary wire shapes are not worth a
// cross-module import for a 3-field struct" — this one carries the Health payload instead).
type credentialHealthInput struct {
	Method string `json:"method"`
}

type credentialHealthReply struct {
	Health *spec.CredentialHealth `json:"health,omitempty"`
	Error  string                 `json:"error,omitempty"`
}

func gatherCredentialHealth(ctx context.Context, exec *sdk.Executor) (*spec.CredentialHealth, string) {
	if exec == nil {
		return nil, "no host reverse channel"
	}
	inJSON, err := json.Marshal(credentialHealthInput{Method: "health"})
	if err != nil {
		return nil, err.Error()
	}
	resJSON, err := exec.InvokeProvider(ctx, "verb", "credential", sdk.OpRun, inJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, fmt.Sprintf(
			"credential plugin (verb:credential) did not connect — install candy/plugin-secrets "+
				"alongside charly (/usr/lib/charly/plugins) or run from a project composing it: %v", err)
	}
	var reply credentialHealthReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return nil, uerr.Error()
		}
	}
	if reply.Error != "" {
		return nil, reply.Error
	}
	return reply.Health, ""
}

// hostConfigPath returns the runtime config path for the "Config permissions" check — pure
// host-env file I/O, exactly the pattern candy/plugin-settings already calls directly (no
// core seam needed).
func hostConfigPath() string {
	p, err := hostenv.RuntimeConfigPath()
	if err != nil {
		return ""
	}
	return p
}

// hostReport bundles every raw host fact the doctor report renders — the direct replacement
// for the former "hostprobe" HostBuild seam's spec.HostProbeReply wire reply, now gathered
// entirely plugin-side.
type hostReport struct {
	Distro        Distro
	Devices       []DeviceInfo
	GPU           gpuFacts
	Credential    *spec.CredentialHealth
	CredentialErr string
	ConfigPath    string
}

// gatherHostReport is the doctor report's single entry point for every raw host fact: distro
// identity (local os-release read), the device-glob report (local, this plugin's own embed),
// GPU/VFIO detection (peer InvokeProvider to candy/plugin-gpu), credential-store health (peer
// InvokeProvider to candy/plugin-secrets), and the runtime config path (direct hostenv call).
// exec is nil on the out-of-process CliMain path — every leg here degrades gracefully (see
// gpuProbe/gatherCredentialHealth), mirroring the former shims' best-effort semantics.
func gatherHostReport(ctx context.Context, exec *sdk.Executor, engine string) hostReport {
	cred, credErr := gatherCredentialHealth(ctx, exec)
	return hostReport{
		Distro:        detectDistro(),
		Devices:       probeDevices(),
		GPU:           gatherGPUFacts(ctx, exec, engine),
		Credential:    cred,
		CredentialErr: credErr,
		ConfigPath:    hostConfigPath(),
	}
}
