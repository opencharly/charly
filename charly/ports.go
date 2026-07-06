package main

import (
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// CollectBoxPorts returns the published container ports a box exposes, inherited
// from EVERY candy in its full base chain (boxCandyChain) — the single source of
// truth now that boxes no longer declare ports themselves. Each candy `port:`
// (a bare container port, optionally "/udp") contributes one published port; the
// HOST mapping is decided at deploy time (auto-allocated on 127.0.0.1, or pinned
// by a deploy `port:` entry — see ResolveDeployPorts). Result is deduplicated by
// container port and sorted ascending for deterministic OCI-label / Containerfile
// output (stable label ⇒ no spurious cache-miss cascades).
func CollectBoxPorts(cfg *Config, layers map[string]*Candy, boxName string) ([]string, error) {
	names, err := cfg.boxCandyChain(layers, boxName)
	if err != nil {
		return nil, err
	}
	type portEntry struct {
		cp    int
		proto string
	}
	seen := map[int]bool{}
	var entries []portEntry
	for _, candyName := range names {
		layer, ok := layers[candyName]
		if !ok || !layer.HasPorts() {
			continue
		}
		cports, perr := layer.Port()
		if perr != nil {
			continue
		}
		for _, cpStr := range cports {
			clean, proto := stripPortSuffix(cpStr)
			cp, aerr := strconv.Atoi(clean)
			if aerr != nil || cp <= 0 || cp > 65535 || seen[cp] {
				continue
			}
			seen[cp] = true
			entries = append(entries, portEntry{cp: cp, proto: proto})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].cp < entries[j].cp })
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		s := strconv.Itoa(e.cp)
		if e.proto != "" {
			s += "/" + e.proto
		}
		out = append(out, s)
	}
	return out, nil
}

// PortConflict describes a host port that is already in use.
type PortConflict struct {
	HostPort  int
	ContPort  int
	Owner     string // container name, or "unknown"
	OwnerType string // "charly-container", "container", "host-process"
}

// stripPortSuffix removes /tcp or /udp protocol suffix from a port string.
// "47998/udp" -> "47998", "udp"; "8000" -> "8000", ""
func stripPortSuffix(s string) (string, string) {
	if idx := strings.LastIndex(s, "/"); idx != -1 {
		return s[:idx], s[idx+1:]
	}
	return s, ""
}

// ParsedPortMapping describes the four possible shapes podman accepts:
//
//	"P"               -> {Host: P, Container: P}
//	"H:C"             -> {Host: H, Container: C}
//	"IP:H:C"          -> {Host: H, Container: C, BindAddr: "IP"}
//	"[v6]:H:C"        -> {Host: H, Container: C, BindAddr: "[v6]"}
//
// Any of those forms may carry a /tcp or /udp suffix on the trailing port.
type ParsedPortMapping struct {
	BindAddr  string // explicit bind prefix if present (e.g. "127.0.0.1" or "[::1]"); empty otherwise
	Host      int
	Container int
	Protocol  string // "udp" / "tcp" / "" — extracted from /udp or /tcp suffix
}

// ParsePortMapping is the canonical port-mapping parser.
//
// Returns ok=false on unparseable input. Callers that want a loud failure
// (warning logged, port skipped) should branch on ok.
//
// All in-tree port handling routes through this — ParseHostPort,
// ParseContainerPort, parseHostPorts (tunnel.go), buildPortMapping (tunnel.go),
// and localizePort (shell.go) — so a single fix here covers every site that
// would otherwise mis-handle the IP:H:C form.
func ParsePortMapping(mapping string) (ParsedPortMapping, bool) {
	clean, proto := stripPortSuffix(mapping)
	parts := splitMappingParts(clean)
	var bindAddr, hostStr, contStr string
	switch len(parts) {
	case 1: // "P"
		hostStr = parts[0]
		contStr = parts[0]
	case 2: // "H:C"
		hostStr = parts[0]
		contStr = parts[1]
	case 3: // "IP:H:C"
		bindAddr = parts[0]
		hostStr = parts[1]
		contStr = parts[2]
	default:
		return ParsedPortMapping{}, false
	}
	host, err1 := strconv.Atoi(hostStr)
	cont, err2 := strconv.Atoi(contStr)
	if err1 != nil || err2 != nil {
		return ParsedPortMapping{}, false
	}
	if host <= 0 || host > 65535 || cont <= 0 || cont > 65535 {
		return ParsedPortMapping{}, false
	}
	return ParsedPortMapping{
		BindAddr:  bindAddr,
		Host:      host,
		Container: cont,
		Protocol:  proto,
	}, true
}

// splitMappingParts splits a port mapping while honoring an IPv6 bracket
// prefix as a single token (so "[::1]:8080:80" -> ["[::1]", "8080", "80"]).
func splitMappingParts(s string) []string {
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			head := s[:i+1]
			tail := strings.TrimPrefix(s[i+1:], ":")
			if tail == "" {
				return []string{head}
			}
			return append([]string{head}, strings.Split(tail, ":")...)
		}
	}
	return strings.Split(s, ":")
}

// FormatPortMapping is the inverse of ParsePortMapping. Empty bindAddr / proto
// are omitted; trailing-zero / equal ports collapse to canonical short forms
// that podman accepts.
func FormatPortMapping(p ParsedPortMapping) string {
	suffix := ""
	if p.Protocol != "" {
		suffix = "/" + p.Protocol
	}
	core := fmt.Sprintf("%d:%d", p.Host, p.Container)
	if p.BindAddr != "" {
		return p.BindAddr + ":" + core + suffix
	}
	return core + suffix
}

// ParseHostPort extracts the host port from a mapping. Accepts every form
// ParsePortMapping does, including the IP:H:C bind-address form.
func ParseHostPort(mapping string) (int, error) {
	p, ok := ParsePortMapping(mapping)
	if !ok {
		return 0, fmt.Errorf("invalid port mapping %q", mapping)
	}
	return p.Host, nil
}

// ParseContainerPort extracts the container port from a mapping. Accepts every
// form ParsePortMapping does, including the IP:H:C bind-address form.
func ParseContainerPort(mapping string) (int, error) {
	p, ok := ParsePortMapping(mapping)
	if !ok {
		return 0, fmt.Errorf("invalid port mapping %q", mapping)
	}
	return p.Container, nil
}

// CheckPortAvailability tests whether each host port can be bound.
// Returns a list of conflicts for ports that are already in use.
// Detects /udp suffix and uses UDP bind check accordingly.
func CheckPortAvailability(ports []string, bindAddr string, engine string) []PortConflict {
	var conflicts []PortConflict
	for _, mapping := range ports {
		hostPort, err := ParseHostPort(mapping)
		if err != nil {
			continue
		}
		contPort, _ := ParseContainerPort(mapping)

		addr := fmt.Sprintf("%s:%d", bindAddr, hostPort)

		if strings.HasSuffix(mapping, "/udp") {
			conn, err := net.ListenPacket("udp", addr)
			if err != nil {
				owner, ownerType := FindPortOwner(hostPort, engine)
				conflicts = append(conflicts, PortConflict{
					HostPort:  hostPort,
					ContPort:  contPort,
					Owner:     owner,
					OwnerType: ownerType,
				})
			} else {
				_ = conn.Close()
			}
		} else {
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				owner, ownerType := FindPortOwner(hostPort, engine)
				conflicts = append(conflicts, PortConflict{
					HostPort:  hostPort,
					ContPort:  contPort,
					Owner:     owner,
					OwnerType: ownerType,
				})
			} else {
				_ = ln.Close()
			}
		}
	}
	return conflicts
}

// FindPortOwner checks running containers to identify what is using a port.
func FindPortOwner(port int, engine string) (owner string, ownerType string) {
	binary := EngineBinary(engine)
	portStr := strconv.Itoa(port)

	cmd := exec.Command(binary, "ps", "--format", "{{.Names}} {{.Ports}}")
	out, err := cmd.Output()
	if err != nil {
		return "unknown", "host-process"
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		if strings.Contains(line, ":"+portStr+"->") || strings.Contains(line, ":"+portStr+"/") {
			name := strings.Fields(line)[0]
			if strings.HasPrefix(name, "charly-") {
				return name, "charly-container"
			}
			return name, "container"
		}
	}
	return "unknown", "host-process"
}

// FormatPortConflicts produces a user-friendly error message with remediation suggestions.
func FormatPortConflicts(conflicts []PortConflict, image string) string {
	var b strings.Builder
	for _, c := range conflicts {
		fmt.Fprintf(&b, "\n  Port %d is in use", c.HostPort)
		if c.Owner != "unknown" {
			fmt.Fprintf(&b, " by container %q", c.Owner)
		}
		b.WriteString("\n")

		switch c.OwnerType {
		case "charly-container":
			// Extract image name from "charly-<image>" or "charly-<image>-<instance>"
			charlyImage := strings.TrimPrefix(c.Owner, "charly-")
			fmt.Fprintf(&b, "    Fix: charly stop %s\n", charlyImage)
		case "container":
			fmt.Fprintf(&b, "    Fix: podman stop %s\n", c.Owner)
		default:
			fmt.Fprintf(&b, "    Fix: find and stop the process using port %d\n", c.HostPort)
		}

		fmt.Fprintf(&b, "    Or remap: charly start %s --port %d:%d\n", image, c.HostPort+1, c.ContPort)
	}
	return b.String()
}

// ApplyPortOverrides modifies port mappings based on --port flags.
// Each override is "newHost:containerPort". It replaces the host port
// for the matching container port in the ports list.
// Preserves protocol suffixes like /udp.
func ApplyPortOverrides(ports []string, overrides []string) ([]string, error) {
	// Parse overrides into container→host map
	overrideMap := make(map[int]int)
	for _, o := range overrides {
		parts := strings.SplitN(o, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid port override %q: expected host:container (e.g., 5901:5900)", o)
		}
		newHost, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("invalid host port in override %q: %w", o, err)
		}
		clean, _ := stripPortSuffix(parts[1])
		contPort, err := strconv.Atoi(clean)
		if err != nil {
			return nil, fmt.Errorf("invalid container port in override %q: %w", o, err)
		}
		overrideMap[contPort] = newHost
	}

	// Apply overrides
	result := make([]string, len(ports))
	for i, mapping := range ports {
		contPort, err := ParseContainerPort(mapping)
		if err != nil {
			result[i] = mapping
			continue
		}
		if newHost, ok := overrideMap[contPort]; ok {
			// Preserve protocol suffix (/udp, /tcp)
			suffix := ""
			if strings.HasSuffix(mapping, "/udp") {
				suffix = "/udp"
			} else if strings.HasSuffix(mapping, "/tcp") {
				suffix = "/tcp"
			}
			result[i] = fmt.Sprintf("%d:%d%s", newHost, contPort, suffix)
		} else {
			result[i] = mapping
		}
	}
	return result, nil
}

// SavePortOverride writes port overrides to charly.yml for persistence.
func SavePortOverride(box, instance string, ports []string) error {
	dc, err := loadDeployConfigForWrite("SavePortOverride")
	if err != nil {
		return err
	}

	key := deployKey(box, instance)
	overlay := dc.Bundle[key]
	overlay.Port = ports
	dc.Bundle[key] = overlay

	return SaveBundleConfig(dc)
}

// containerPortsFromMappings extracts the container-side port number from
// each mapping. "auto" sentinels are skipped (they have no container port
// to extract — they ARE the request to allocate one). Unparseable entries
// are silently dropped (the loud-skip warning lives in CheckPortAvailability).
func containerPortsFromMappings(mappings []string) []int {
	if len(mappings) == 0 {
		return nil
	}
	result := make([]int, 0, len(mappings))
	for _, m := range mappings {
		if IsAutoPort(m) {
			continue
		}
		cp, err := ParseContainerPort(m)
		if err != nil {
			continue
		}
		result = append(result, cp)
	}
	return result
}

// sameStringSlice reports whether two string slices are element-wise equal
// (order-sensitive) — used to skip a redundant resolved-port re-save.
func sameStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// IsAutoPort reports whether a port-list entry is the literal "auto" sentinel.
// Authors write `port: [auto]` (or `port: [auto, "8443:443"]` to mix
// auto-allocation with explicit pins) in charly.yml.
func IsAutoPort(mapping string) bool {
	return strings.TrimSpace(mapping) == "auto"
}

// AllocateAutoPorts probes free TCP host ports — one per container port,
// in declaration order. The `occupied` set names host ports already in
// use by other deployments (so two `port: [auto]` deploys on the same
// host don't collide). Returned mappings have BindAddr="" (default host
// bind) and Protocol="tcp". Each successful allocation is recorded back
// into `occupied` so subsequent calls in the same BundleConfig pass see
// the reservation.
//
// Free-port discovery uses the same net.Listen("tcp","127.0.0.1:0") +
// immediate-close pattern already used by ssh_tunnel.go:78 and
// vnc_helpers.go's unixToTcpBridge — the OS picks an ephemeral port, we
// close, and the caller binds in the (small) window before the OS reassigns.
func AllocateAutoPorts(containerPorts []int, occupied map[int]bool) ([]ParsedPortMapping, error) {
	if len(containerPorts) == 0 {
		return nil, nil
	}
	if occupied == nil {
		occupied = map[int]bool{}
	}
	result := make([]ParsedPortMapping, 0, len(containerPorts))
	for _, cp := range containerPorts {
		if cp <= 0 || cp > 65535 {
			return nil, fmt.Errorf("AllocateAutoPorts: invalid container port %d", cp)
		}
		var host int
		for range 32 {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return nil, fmt.Errorf("AllocateAutoPorts: probe failed for container port %d: %w", cp, err)
			}
			candidate := ln.Addr().(*net.TCPAddr).Port
			_ = ln.Close()
			if !occupied[candidate] {
				host = candidate
				break
			}
		}
		if host == 0 {
			return nil, fmt.Errorf("AllocateAutoPorts: exhausted 32 attempts for container port %d", cp)
		}
		occupied[host] = true
		result = append(result, ParsedPortMapping{Host: host, Container: cp})
	}
	return result, nil
}

// ResolveDeployPorts maps each image-declared container port to a host:container
// publish mapping — the AUTO-PORT-MAPPING default. For every container port:
//
//   - an explicit deploy pin (host:container, matched by container port) wins;
//   - else a still-valid prior allocation (from a previous `charly config` —
//     keeps a deploy's host ports STABLE across `charly update`) is reused;
//   - else a fresh free 127.0.0.1 host port is allocated.
//
// `occupied` seeds host ports already taken by SIBLING deployments so concurrent
// beds never collide; every chosen host port is recorded back into it. Pins for
// container ports the image does not expose are honored too (an operator
// publishing an extra port). A stray `auto` token in `pins` is ignored (treated
// as "no pin" → allocate), so a not-yet-migrated `port: [auto]` still works.
// Returned mappings carry no bind address; localizePort prepends BindAddress
// (127.0.0.1 by default) at quadlet/run time so every published port is loopback.
func ResolveDeployPorts(containerPorts []int, pins, prior []string, occupied map[int]bool) ([]string, error) {
	if len(containerPorts) == 0 && len(pins) == 0 {
		return nil, nil
	}
	if occupied == nil {
		occupied = map[int]bool{}
	}
	pinByCont := map[int]ParsedPortMapping{}
	var pinOrder []int
	for _, p := range pins {
		if IsAutoPort(p) {
			continue
		}
		pm, ok := ParsePortMapping(p)
		if !ok {
			return nil, fmt.Errorf("invalid deploy port pin %q (expected host:container)", p)
		}
		if _, dup := pinByCont[pm.Container]; !dup {
			pinOrder = append(pinOrder, pm.Container)
		}
		pinByCont[pm.Container] = pm
		occupied[pm.Host] = true
	}
	priorByCont := map[int]int{}
	for _, p := range prior {
		if pm, ok := ParsePortMapping(p); ok {
			priorByCont[pm.Container] = pm.Host
		}
	}
	out := make([]string, 0, len(containerPorts)+len(pinOrder))
	emitted := map[int]bool{}
	for _, cp := range containerPorts {
		if emitted[cp] {
			continue
		}
		emitted[cp] = true
		if pm, ok := pinByCont[cp]; ok {
			out = append(out, FormatPortMapping(pm))
			continue
		}
		if h, ok := priorByCont[cp]; ok && !occupied[h] {
			occupied[h] = true
			out = append(out, FormatPortMapping(ParsedPortMapping{Host: h, Container: cp}))
			continue
		}
		allocs, err := AllocateAutoPorts([]int{cp}, occupied)
		if err != nil {
			return nil, err
		}
		out = append(out, FormatPortMapping(allocs[0]))
	}
	// Honor pins for container ports the image does not expose.
	for _, cp := range pinOrder {
		if emitted[cp] {
			continue
		}
		emitted[cp] = true
		out = append(out, FormatPortMapping(pinByCont[cp]))
	}
	return out, nil
}
