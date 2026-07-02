package vmshared

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// OvmfPaths is the pair of firmware image paths needed to boot a UEFI
// VM: OVMF_CODE (read-only, shared across all VMs) and an OVMF_VARS
// template (read-only, copied to a per-VM writable NVRAM file on first
// VM create).
type OvmfPaths struct {
	// CodePath is the OVMF_CODE firmware image. Read-only.
	CodePath string

	// VarsTemplate is the OVMF_VARS template with standard UEFI CA
	// keys pre-enrolled (when secure=true). The per-VM NVRAM file is
	// copied from this template on first VM create.
	VarsTemplate string

	// Secure indicates whether this is the secure-boot-enabled variant.
	Secure bool
}

// ResolveOvmfPaths picks the correct OVMF_CODE + OVMF_VARS paths for
// the host distro + secure-boot setting. Returns an error when no
// candidate path exists on disk so `charly vm create` fails with a clean
// remediation hint instead of a cryptic QEMU pflash error.
//
// D17 path table:
//
//	Fedora         /usr/share/OVMF/OVMF_CODE{.secboot,}.fd
//	               /usr/share/OVMF/OVMF_VARS{.secboot,}.fd
//	Arch           /usr/share/edk2/x64/OVMF_CODE{.secboot,}.4m.fd
//	               /usr/share/edk2/x64/OVMF_VARS.4m.fd
//	Debian/Ubuntu  /usr/share/OVMF/OVMF_CODE_4M{.ms,}.fd
//	               /usr/share/OVMF/OVMF_VARS_4M{.ms,}.fd
func ResolveOvmfPaths(distroID string, secure bool) (OvmfPaths, error) {
	candidates := ovmfCandidatesForDistro(distroID, secure)
	for _, c := range candidates {
		if _, err := os.Stat(c.CodePath); err != nil {
			continue
		}
		if _, err := os.Stat(c.VarsTemplate); err != nil {
			continue
		}
		c.Secure = secure
		return c, nil
	}
	return OvmfPaths{}, ovmfNotFoundError(distroID, secure)
}

// ovmfPathYAML is the decode shape for one ovmf_paths candidate in the embedded charly.yml.
type ovmfPathYAML struct {
	Code string `yaml:"code"`
	Vars string `yaml:"vars"`
}

// ovmfFamilyPaths holds the secure/nonsecure candidate lists for one distro family.
type ovmfFamilyPaths struct {
	Secure    []OvmfPaths
	Nonsecure []OvmfPaths
}

// ovmfPathTable is the per-family OVMF firmware candidate table, read from the ovmf_paths
// directive in the embedded charly.yml (Phase 4: the path DATA moved out of Go). The
// alias→family resolution, secure selection, and unknown-distro union remain logic in
// ovmfCandidatesForDistro below.
var ovmfPathTable = sync.OnceValue(parseEmbeddedOvmfPaths)

func parseEmbeddedOvmfPaths() map[string]ovmfFamilyPaths {
	var doc struct {
		OvmfPaths map[string]struct {
			Secure    []ovmfPathYAML `yaml:"secure"`
			Nonsecure []ovmfPathYAML `yaml:"nonsecure"`
		} `yaml:"ovmf_paths"`
	}
	UnmarshalEmbeddedDefaults(&doc)
	if len(doc.OvmfPaths) == 0 {
		panic("ovmf: embedded charly.yml has no ovmf_paths: directive")
	}
	conv := func(in []ovmfPathYAML) []OvmfPaths {
		out := make([]OvmfPaths, len(in))
		for i, p := range in {
			out[i] = OvmfPaths{CodePath: p.Code, VarsTemplate: p.Vars}
		}
		return out
	}
	table := make(map[string]ovmfFamilyPaths, len(doc.OvmfPaths))
	for fam, fp := range doc.OvmfPaths {
		table[fam] = ovmfFamilyPaths{Secure: conv(fp.Secure), Nonsecure: conv(fp.Nonsecure)}
	}
	return table
}

// ovmfDistroAliases maps a host distro ID to its OVMF firmware family (fedora|arch|debian),
// read from the ovmf_distro_aliases directive in the embedded charly.yml (Phase 4: data
// moved out of Go). An unlisted distro has no entry — ovmfCandidatesForDistro then unions
// all families and ovmfNotFoundError emits the generic install hint. Both readers derive
// the family from this ONE map (R3 — no duplicated alias grouping).
var ovmfDistroAliases = sync.OnceValue(parseEmbeddedOvmfDistroAliases)

func parseEmbeddedOvmfDistroAliases() map[string]string {
	var doc struct {
		OvmfDistroAliases map[string]string `yaml:"ovmf_distro_aliases"`
	}
	UnmarshalEmbeddedDefaults(&doc)
	if len(doc.OvmfDistroAliases) == 0 {
		panic("ovmf: embedded charly.yml has no ovmf_distro_aliases: directive")
	}
	return doc.OvmfDistroAliases
}

// ovmfCandidatesForDistro returns the ordered candidate path pairs for a given distro +
// secure-boot combination. More than one candidate per distro covers historical path
// variation (e.g. Fedora pre-40 used /usr/share/edk2-ovmf/ instead of /usr/share/OVMF/).
// Both the path data (ovmfPathTable) and the distro→family aliases (ovmfDistroAliases) live
// in the embedded charly.yml; this selects secure/nonsecure and unions the three families
// for an unknown distro.
func ovmfCandidatesForDistro(distroID string, secure bool) []OvmfPaths {
	family, ok := ovmfDistroAliases()[distroID]
	if !ok {
		// Unknown distro — try the union of common paths.
		fedora := ovmfCandidatesForDistro("fedora", secure)
		arch := ovmfCandidatesForDistro("arch", secure)
		debian := ovmfCandidatesForDistro("debian", secure)
		merged := make([]OvmfPaths, 0, len(fedora)+len(arch)+len(debian))
		merged = append(merged, fedora...)
		merged = append(merged, arch...)
		merged = append(merged, debian...)
		return merged
	}
	fp := ovmfPathTable()[family]
	if secure {
		return fp.Secure
	}
	return fp.Nonsecure
}

// ovmfNotFoundError returns a clear error with distro-appropriate
// install instructions.
func ovmfNotFoundError(distroID string, secure bool) error {
	var installHint string
	switch ovmfDistroAliases()[distroID] {
	case "fedora":
		installHint = "sudo dnf install edk2-ovmf"
	case "arch":
		installHint = "sudo pacman -S edk2-ovmf"
	case "debian":
		installHint = "sudo apt-get install ovmf"
	default:
		installHint = "install the 'edk2-ovmf' (Fedora/Arch) or 'ovmf' (Debian/Ubuntu) package"
	}
	mode := "insecure"
	if secure {
		mode = "secure"
	}
	return fmt.Errorf("OVMF firmware (%s) not found for host distro %q; %s", mode, distroID, installHint)
}

// EnsurePerVmNvram copies the OVMF_VARS template to a per-VM NVRAM
// file on first use. Returns the absolute path of the per-VM NVRAM
// (which is what rt.NVRAMPath should be set to). Idempotent: if the
// per-VM file already exists, it's preserved (contains the guest's
// accumulated UEFI variables).
func EnsurePerVmNvram(templatePath, perVmDir string) (string, error) {
	if err := os.MkdirAll(perVmDir, 0o755); err != nil {
		return "", fmt.Errorf("creating per-VM NVRAM dir: %w", err)
	}
	dst := filepath.Join(perVmDir, "nvram.fd")
	if _, err := os.Stat(dst); err == nil {
		// Already present — preserve accumulated UEFI variables.
		return dst, nil
	}
	src, err := os.Open(templatePath)
	if err != nil {
		return "", fmt.Errorf("opening OVMF_VARS template %q: %w", templatePath, err)
	}
	defer src.Close() //nolint:errcheck
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("creating per-VM NVRAM file %q: %w", dst, err)
	}
	defer out.Close() //nolint:errcheck
	if _, err := io.Copy(out, src); err != nil {
		return "", fmt.Errorf("copying OVMF_VARS template: %w", err)
	}
	return dst, nil
}

// ResolveOvmfForSpec is a convenience wrapper: detects the host distro,
// picks the correct OVMF_CODE/OVMF_VARS pair for the VmSpec's firmware
// setting, and provisions the per-VM NVRAM file. Returns (CodePath,
// NVRAMPath) — the two values needed to populate VmRuntimeParams.
//
// Returns ("", "", nil) when firmware == "bios" (BIOS boot needs no
// firmware images).
func ResolveOvmfForSpec(spec *VmSpec, vmStateDir string) (codePath, nvramPath string, err error) {
	if spec.Firmware == "" || spec.Firmware == "bios" {
		return "", "", nil
	}
	secure := spec.Firmware == "uefi-secure"

	hd, err := DetectHostDistro()
	if err != nil {
		return "", "", fmt.Errorf("ResolveOvmfForSpec: %w", err)
	}
	paths, err := ResolveOvmfPaths(hd.ID, secure)
	if err != nil {
		return "", "", err
	}
	nvram, err := EnsurePerVmNvram(paths.VarsTemplate, vmStateDir)
	if err != nil {
		return "", "", err
	}
	return paths.CodePath, nvram, nil
}
