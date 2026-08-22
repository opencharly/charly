package spec

import (
	"fmt"
	"strconv"
	"strings"
)

// port_mapping.go — the pure podman port-mapping PARSE/FORMAT value-vocabulary, promoted from
// sdk/kit (#55 import-purity, value-type consolidation cone). These carry NO mechanism
// dependency (stdlib fmt/strconv/strings only) — the parse-result struct plus the pure
// string parse/format helpers — so they are spec-hosted contract helpers. sdk/kit keeps thin
// re-export forwarders (`type ParsedPortMapping = spec.ParsedPortMapping`, `var
// ParsePortMapping = spec.ParsePortMapping`, …) so its own host-coupled port helpers
// (ParseHostPort / CheckPortAvailability / AllocateAutoPorts / …) + the deploy candies compile
// unchanged. Distinct from PortSpec (the candy `port:` AUTHORING type in hand_state_types.go):
// this is the podman host↔container MAPPING string parse result.

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

// StripPortSuffix removes /tcp or /udp protocol suffix from a port string.
// "47998/udp" -> "47998", "udp"; "8000" -> "8000", ""
func StripPortSuffix(s string) (string, string) {
	if idx := strings.LastIndex(s, "/"); idx != -1 {
		return s[:idx], s[idx+1:]
	}
	return s, ""
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
	clean, proto := StripPortSuffix(mapping)
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
