package main

import (
	"fmt"
	"os"
	"strings"
)

// osReleasePath is the path to the os-release file, overridable for testing.
var osReleasePath = "/etc/os-release"

// Distro is the HOST distribution detected at runtime (from /etc/os-release).
// Distinct from the build-vocabulary distro definition (spec.DistroDef / the
// CUE #Distro) — this one describes the machine charly runs ON, not an image's
// package format.
type Distro struct {
	ID      string // "arch", "fedora", "debian", "ubuntu", etc.
	Name    string // "Arch Linux", "Fedora Linux", etc.
	Manager string // "pacman -S", "sudo dnf install", "sudo apt-get install"
}

// detectDistro reads /etc/os-release and returns the detected distribution.
func detectDistro() Distro {
	data, err := os.ReadFile(osReleasePath)
	if err != nil {
		return Distro{ID: "unknown", Name: "Unknown", Manager: ""}
	}
	return parseOsRelease(string(data))
}

// parseOsRelease parses os-release content and returns a Distro.
func parseOsRelease(content string) Distro {
	d := Distro{ID: "unknown", Name: "Unknown"}
	for line := range strings.SplitSeq(content, "\n") {
		if after, ok := strings.CutPrefix(line, "ID="); ok {
			d.ID = strings.Trim(after, "\"")
		}
		if after, ok := strings.CutPrefix(line, "NAME="); ok {
			d.Name = strings.Trim(after, "\"")
		}
	}
	d.Manager = distroPackageManagers[d.ID]
	return d
}

// distroPackageManagers maps a host distro ID to its install command prefix, read from the
// distro_package_managers directive in the embedded charly.yml (Phase 4: data moved out of
// Go). An unlisted distro yields the zero value "" (no manager, no install hint).
var distroPackageManagers = parseEmbeddedDistroPackageManagers()

func parseEmbeddedDistroPackageManagers() map[string]string {
	var doc struct {
		DistroPackageManagers map[string]string `yaml:"distro_package_managers"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.DistroPackageManagers) == 0 {
		panic("distro: embedded charly.yml has no distro_package_managers: directive")
	}
	return doc.DistroPackageManagers
}

// installHints maps binary names to per-distro package names, read from the install_hints
// directive in the embedded charly.yml (Phase 4: data moved out of Go) via the shared
// minimal decoder. Panics if the directive is empty/malformed (a build-time invariant).
var installHints = parseEmbeddedInstallHints()

// parseEmbeddedInstallHints reads the install_hints map from the embedded charly.yml.
func parseEmbeddedInstallHints() map[string]map[string]string {
	var doc struct {
		InstallHints map[string]map[string]string `yaml:"install_hints"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.InstallHints) == 0 {
		panic("distro: embedded charly.yml has no install_hints: directive")
	}
	return doc.InstallHints
}

func (d Distro) installHint(binary string) string {
	if d.Manager == "" {
		return binary
	}
	if pkgMap, ok := installHints[binary]; ok {
		// Try exact distro ID first
		if pkg, ok := pkgMap[d.ID]; ok {
			// AUR packages include their own install command
			if strings.Contains(pkg, "AUR:") {
				return strings.TrimSpace(pkg[strings.Index(pkg, "AUR:")+4:])
			}
			return fmt.Sprintf("%s %s", d.Manager, pkg)
		}
		// Try distro family
		family := distroFamily(d.ID)
		if pkg, ok := pkgMap[family]; ok {
			if strings.Contains(pkg, "AUR:") {
				return strings.TrimSpace(pkg[strings.Index(pkg, "AUR:")+4:])
			}
			return fmt.Sprintf("%s %s", d.Manager, pkg)
		}
	}
	return fmt.Sprintf("%s %s", d.Manager, binary)
}

// distroFamilyMap maps a host distro ID to its base family for install-hint package-name
// lookup, read from the distro_family_map directive in the embedded charly.yml (Phase 4:
// data moved out of Go). An unlisted distro maps to itself (see distroFamily).
var distroFamilyMap = parseEmbeddedDistroFamilyMap()

func parseEmbeddedDistroFamilyMap() map[string]string {
	var doc struct {
		DistroFamilyMap map[string]string `yaml:"distro_family_map"`
	}
	unmarshalEmbeddedDefaults(&doc)
	if len(doc.DistroFamilyMap) == 0 {
		panic("distro: embedded charly.yml has no distro_family_map: directive")
	}
	return doc.DistroFamilyMap
}

// distroFamily maps distro IDs to their base family for install hint lookup. An unlisted
// distro maps to itself.
func distroFamily(id string) string {
	if fam, ok := distroFamilyMap[id]; ok {
		return fam
	}
	return id
}
