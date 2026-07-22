package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// exec_LookPath wraps os/exec.LookPath to avoid importing os/exec in syscall code. Relocated from
// the deleted shell.go (Cutover B unit 2) — this file is its only remaining caller.
var exec_LookPath = defaultLookPath

func defaultLookPath(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("executable not found: %s", name)
}

// host_build_hostprobe.go — the generic "hostprobe" F10 host-builder. The externalized `charly doctor`
// command plugin (candy/plugin-doctor) OWNS the ENTIRE host-dependency report — the check list, the group
// orchestration, the pass/warn/fail verdicts, the human + JSON formatting, the exit code, AND the pure host
// ops it runs itself (binary probes via exec.LookPath, file reads via os.Stat/os.ReadFile). Core keeps ONLY
// the genuine host-hardware subsystem the plugin cannot hold: the GPU/VFIO/device detection primitives (the
// C11 shims, multi-caller with the vm/deploy paths), the credential-store health (verb:credential, which
// lazy-connects host-side), and the core data tables (install-hints / device-descriptions). Those flow over
// THIS one generic action noun, NOT a provider word (F11), and it returns RAW FACTS ONLY — zero formatting
// or verdict logic crosses into core. Every probe is best-effort: a failure leaves its field zero/empty
// (mirroring the detection shims), never a hard fail; only a truly-fatal decode error returns Error.
const hostProbeBuilderKind = "hostprobe"

func hostBuildHostProbe(_ context.Context, req spec.HostProbeRequest, _ buildEngineContext) (spec.HostProbeReply, error) {
	reply := spec.HostProbeReply{}

	// GPU / AMD GPU detection + the container GPU run-flags. GPUFlags depend on the target engine:
	// the request may hint it (req.Engine), else resolve it the way `charly doctor` did (podman-first).
	reply.GPU = DetectGPU()
	if reply.GPU {
		engine := req.Engine
		if engine == "" {
			if _, err := exec_LookPath("podman"); err == nil {
				engine = "podman"
			} else if _, err := exec_LookPath("docker"); err == nil {
				engine = "docker"
			}
		}
		if engine != "" {
			reply.GPUFlags = kit.GPURunArgs(engine)
		}
	}
	reply.AMDGPU = DetectAMDGPU()
	if reply.AMDGPU {
		reply.AMDGFXVersion = detectAMDGFXVersion()
	}

	// VFIO / GPU-passthrough readiness (the same DetectVFIO probe `charly vm gpu` consumes) + the
	// memlock limit, the vfio-pci driver presence, and per-GPU-group /dev/vfio accessibility.
	vfio := DetectVFIO()
	reply.Vfio = &vfio
	reply.MemlockSoft, reply.MemlockHard = MemlockLimitBytes()
	reply.VfioPciAvailable = vfioPciAvailable()
	// GroupAccessible is string-keyed (the SDD conversion reshaped the wire map
	// from map[int]bool to map[string]bool — CUE has no int-keyed-map
	// construct; encoding/json already converts a map[int]bool's keys to their
	// decimal string form on the wire, so this is a pure representation fix,
	// zero wire-format change).
	reply.GroupAccessible = map[string]bool{}
	for _, g := range vfio.GPUs {
		if g.IOMMUGroup >= 0 {
			reply.GroupAccessible[strconv.Itoa(g.IOMMUGroup)] = VfioGroupAccessible(g.IOMMUGroup)
		}
	}

	// Host device probing: glob each core devicePattern and attach its human description (the core
	// data tables). A present pattern yields one entry per match; an absent one a single Present:false
	// entry keyed on the pattern itself — the exact shape the doctor device report renders.
	for _, pattern := range devicePatterns {
		desc := deviceDescriptions[pattern]
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			for _, m := range matches {
				reply.Devices = append(reply.Devices, spec.HostProbeDevice{Pattern: pattern, Path: m, Present: true, Description: desc})
			}
		} else {
			reply.Devices = append(reply.Devices, spec.HostProbeDevice{Pattern: pattern, Path: pattern, Present: false, Description: desc})
		}
	}

	// Host distro identity + the core install-hint / distro-family tables the plugin renders hints from.
	d := detectDistro()
	reply.Distro = spec.HostProbeDistro{ID: d.ID, Name: d.Name, Manager: d.Manager}
	reply.InstallHints = installHints
	reply.DistroFamilyMap = distroFamilyMap

	// Core-owned config path (the plugin stats it for the permissions check).
	if p, err := kit.RuntimeConfigPath(); err == nil {
		reply.ConfigPath = p
	}

	// Credential-store health via verb:credential (the plugin owns the Secret Service now). Best-effort:
	// an error is recorded, never fatal — the plugin renders it as a "backend unavailable" warning.
	if h, err := credentialHealth(); err != nil {
		reply.CredentialErr = err.Error()
	} else {
		reply.Credential = h
	}

	return reply, nil
}

var _ = func() bool {
	registerHostBuilder(hostProbeBuilderKind, typedHostBuilder(hostProbeBuilderKind, hostBuildHostProbe))
	return true
}()

// deviceDescriptions maps a host device path to a human description for the `charly doctor` hardware
// section, read from the device_descriptions directive in the embedded charly.yml (data-out-of-Go) via
// the shared minimal decoder. Kept in core (the hostprobe seam above reads it to build the device report
// that candy/plugin-doctor renders, R3). Panics if the directive is empty/malformed (a build-time
// invariant, never a runtime input).
var deviceDescriptions = parseEmbeddedDeviceDescriptions()

func parseEmbeddedDeviceDescriptions() map[string]string {
	var doc struct {
		DeviceDescriptions map[string]string `yaml:"device_descriptions"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.DeviceDescriptions) == 0 {
		panic("doctor: embedded charly.yml has no device_descriptions: directive")
	}
	return doc.DeviceDescriptions
}
