// Package hostenv holds kind-agnostic HOST-ENVIRONMENT probes — pure host
// inspection (os-release distro identity, glibc version) + best-effort host
// setup actions (libvirt user-session spawn) with no charly-config / kind /
// registry coupling. Relocated from sdk/vmshared (#55 vmshared Bucket C) so
// kernel-floor charly files reach these host facts without importing the
// sdk/vmshared mechanism kit; vmshared keeps re-export forwarders for its
// plugin/test callers. stdlib + os/exec only — no heavy external dependency.
package hostenv

// hostdistro.go — host distro + glibc detection for the local deploy target.
//
// The host target needs to know (a) which distro family it's running on
// so the compiler can pick the right format section (rpm/deb/pac), and
// (b) the host's glibc version so we can refuse deploys whose container
// builder was built against a newer glibc than what the host ships.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/opencharly/spec/spec"
)

// HostDistro identifies the host's distro for BuildDeployPlan.
type HostDistro struct {
	// ID is the primary identifier, e.g. "fedora", "arch", "ubuntu",
	// "debian". Matches /etc/os-release's ID= field.
	ID string

	// VersionID is the release identifier, e.g. "43" for Fedora 43,
	// "24.04" for Ubuntu 24.04. Empty for rolling-release distros
	// (arch).
	VersionID string

	// IDLike is the list of distros this system claims compatibility
	// with, in order. Populated from ID_LIKE=; enables fallback when a
	// candy only has a parent-distro section (e.g. an ubuntu host
	// picking up a debian: section).
	IDLike []string

	// Tags is the ordered list of distro tags to use for format-section
	// matching: [exact ID+Version, ID, ID_LIKE entries]. Matches the
	// img.Distro list structure used by candy tag-section resolution.
	Tags []string
}

// DetectHostDistro reads /etc/os-release and derives the structured
// distro identity. Errors only when /etc/os-release is unreadable.
func DetectHostDistro() (*HostDistro, error) {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return nil, fmt.Errorf("DetectHostDistro: %w", err)
	}
	defer f.Close() //nolint:errcheck

	hd := &HostDistro{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		key, val, ok := SplitOsReleaseLine(line)
		if !ok {
			continue
		}
		switch key {
		case "ID":
			hd.ID = val
		case "VERSION_ID":
			hd.VersionID = val
		case "ID_LIKE":
			for s := range strings.FieldsSeq(val) {
				if s != "" {
					hd.IDLike = append(hd.IDLike, s)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("DetectHostDistro: scanning /etc/os-release: %w", err)
	}
	hd.PopulateTags()
	return hd, nil
}

// SplitOsReleaseLine parses a single line of /etc/os-release into
// (key, value). Values may be unquoted, single-quoted, or double-quoted.
// Comments (# ...) and blank lines return ok=false.
func SplitOsReleaseLine(line string) (key, val string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	before, after, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(before)
	val = strings.TrimSpace(after)
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') ||
			(val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
	}
	return key, val, true
}

// distroIDAliases maps /etc/os-release ID= values to the canonical
// name used inside the embedded vocabulary's (charly/charly.yml) distro:
// map. Arch Linux reports ID=arch but the embedded distro: vocabulary keys
// the distro as "arch"; several Fedora spin-offs report their own name but
// charly treats them as "fedora".
//
// Populated tags include both the os-release name and the embedded
// distro: vocabulary's canonical name so candy tag-section matching and
// distro format-lookup both succeed.
var distroIDAliases = map[string]string{
	"arch":        "arch",
	"archarm":     "arch",
	"manjaro":     "arch",
	"endeavouros": "arch",
	"almalinux":   "fedora",
	"rocky":       "fedora",
	"centos":      "fedora",
	"rhel":        "fedora",
}

// PopulateTags derives HostDistro.Tags from the other fields. The
// resulting list includes both the os-release ID (exact match for
// candy tag sections like `arch:`) and the embedded vocabulary's
// (charly/charly.yml) canonical name
// (for DistroConfig.ResolveDistro to find the format definitions).
func (hd *HostDistro) PopulateTags() {
	hd.Tags = hd.Tags[:0]
	if hd.ID != "" {
		if hd.VersionID != "" {
			hd.Tags = append(hd.Tags, hd.ID+":"+hd.VersionID)
		}
		hd.Tags = append(hd.Tags, hd.ID)
		if canonical, ok := distroIDAliases[hd.ID]; ok {
			hd.Tags = append(hd.Tags, canonical)
		}
	}
	for _, like := range hd.IDLike {
		hd.Tags = append(hd.Tags, like)
		if canonical, ok := distroIDAliases[like]; ok {
			hd.Tags = append(hd.Tags, canonical)
		}
	}
}

// PrimaryTag returns the first tag (most specific). Convenience for
// callers that want a single "best match" string.
func (hd *HostDistro) PrimaryTag() string {
	if len(hd.Tags) == 0 {
		return ""
	}
	return hd.Tags[0]
}

// FormatForDistroID maps an /etc/os-release-style distro ID (or an embedded
// distro: vocabulary key — the same token space) to its package format.
//
// The table is spec.DistroFormats, GENERATED from schema/distro_vocab.cue's #Distros by
// `task cue:gen`. It was a hand-written Go map here, which duplicated the id space the
// CUE schema separately declared for a VM source's `distro:` — two lists to keep in
// step, with no gate, and the SDD rule is that schema-shaped Go is generated rather than
// transcribed. Returns "" for an unknown ID.
func FormatForDistroID(id string) string { return spec.DistroFormats[id] }

// FormatHint returns the best-guess format name (rpm/deb/pac) based on the
// host distro's ID / ID_LIKE, via the generated spec.DistroFormats table. Used when
// the caller has no DistroDef in hand (e.g. the synthetic host-adhoc
// image). For a resolved DistroDef, prefer DistroDef.PrimaryFormat.
func (hd *HostDistro) FormatHint() string {
	for _, id := range append([]string{hd.ID}, hd.IDLike...) {
		if f := FormatForDistroID(id); f != "" {
			return f
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Glibc detection
// ---------------------------------------------------------------------------

// glibcRegexp matches the version number in `ldd --version` output. The
// shape varies across distros ("ldd (GNU libc) 2.39", "ldd (Ubuntu
// GLIBC 2.39-1ubuntu2) 2.39", etc.) — we only care about the trailing
// "MAJOR.MINOR".
var glibcRegexp = regexp.MustCompile(`(\d+)\.(\d+)(?:\.\d+)?\s*$`)

// DetectHostGlibc runs `ldd --version` and extracts the version. Returns
// "" with no error when glibc can't be detected (e.g. musl hosts) —
// callers should treat an empty string as "unknown, skip the preflight
// check".
func DetectHostGlibc() (string, error) { //nolint:unparam // error return kept for interface/API stability
	out, err := exec.Command("ldd", "--version").Output()
	if err != nil {
		// Non-glibc systems (alpine/musl) return an error; signal
		// "unknown" via empty return rather than failing.
		return "", nil
	}
	return ParseGlibcVersion(string(out)), nil
}

// ParseGlibcVersion extracts "MAJOR.MINOR" from ldd output. Broken out
// for unit-testing against stable output strings.
func ParseGlibcVersion(out string) string {
	scanner := bufio.NewScanner(strings.NewReader(out))
	if !scanner.Scan() {
		return ""
	}
	// First line typically: "ldd (GNU libc) 2.39"
	line := scanner.Text()
	m := glibcRegexp.FindStringSubmatch(line)
	if len(m) < 3 {
		return ""
	}
	return m[1] + "." + m[2]
}

// CompareGlibc returns -1 / 0 / 1 for a vs b, where each is "MAJOR.MINOR".
// Empty strings compare as equal (unknown vs unknown). A single-empty
// comparison returns 0 (treat unknown as compatible).
func CompareGlibc(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	aMaj, aMin := parseMajMin(a)
	bMaj, bMin := parseMajMin(b)
	if aMaj != bMaj {
		if aMaj < bMaj {
			return -1
		}
		return 1
	}
	if aMin < bMin {
		return -1
	}
	if aMin > bMin {
		return 1
	}
	return 0
}

// parseMajMin returns the major + minor numbers from "MAJOR.MINOR".
// Non-numeric fields are treated as 0.
func parseMajMin(v string) (maj, min int) {
	parts := strings.SplitN(v, ".", 2)
	if len(parts) > 0 {
		_, _ = fmt.Sscanf(parts[0], "%d", &maj) // best-effort: non-numeric field stays 0 (documented)
	}
	if len(parts) > 1 {
		_, _ = fmt.Sscanf(parts[1], "%d", &min) // best-effort: non-numeric field stays 0 (documented)
	}
	return maj, min
}
