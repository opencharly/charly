package spec

import (
	"fmt"
	"os"
	"strconv"
)

// tunnel_resolve.go — the pure tunnel-config RESOLUTION value-transforms, promoted from
// sdk/deploykit (#55 U5 value-type consolidation, unblocked by U3's spec.ParsePortMapping).
// ResolveTunnelConfig / TunnelConfigFromMetadata turn a TunnelYAML (or image-label metadata)
// into a ready-to-execute TunnelConfig; parseHostPorts / buildPortMapping / resolveProto are
// their pure helpers. Every dependency is a spec value type + spec.ParsePortMapping — no
// *Config/*Candy live graph (ResolveTunnelConfig's CandyReader/[]string params are unused), no
// host I/O beyond a stderr diagnostic — so they are spec-hosted contract transforms an
// import-clean charly file / plugin reaches without an sdk mechanism-kit import. sdk/deploykit
// keeps re-export forwarders so its callers compile unchanged. This also RETIRED the dead
// FUNCTIONAL duplicate in sdk/kit/tunnel_metadata.go — a stale copy of these helpers (lacking the
// live ResolveTunnelConfig; zero production callers), R3.

// parseHostPorts extracts host-side ports from image port mappings via the canonical
// ParsePortMapping. Unparseable entries are reported on stderr — silent skipping was the root
// cause of an unrelated bug where tunnel rules vanished without a diagnostic.
func parseHostPorts(boxPorts []string) []int {
	var result []int
	for _, mapping := range boxPorts {
		p, ok := ParsePortMapping(mapping)
		if !ok {
			fmt.Fprintf(os.Stderr,
				"Warning: ignoring unparseable port mapping %q (expected forms: \"P\", \"H:C\", \"IP:H:C\")\n",
				mapping)
			continue
		}
		result = append(result, p.Host)
	}
	return result
}

// buildPortMapping builds a host→container port map from image port mappings.
// Same loud-failure policy as parseHostPorts — see comment above.
func buildPortMapping(boxPorts []string) map[int]int {
	m := make(map[int]int, len(boxPorts))
	for _, mapping := range boxPorts {
		p, ok := ParsePortMapping(mapping)
		if !ok {
			fmt.Fprintf(os.Stderr,
				"Warning: ignoring unparseable port mapping %q (expected forms: \"P\", \"H:C\", \"IP:H:C\")\n",
				mapping)
			continue
		}
		m[p.Host] = p.Container
	}
	return m
}

// resolveProto returns the backend scheme for a container port, defaulting to "http".
// portProtos is string-keyed (the OCI-label wire form, P2B reshape) — index by the port as a string.
func resolveProto(containerPort int, portProtos map[string]string) string {
	if portProtos != nil {
		if pp, ok := portProtos[strconv.Itoa(containerPort)]; ok {
			return pp
		}
	}
	return "http"
}

// ResolveTunnelConfig resolves a TunnelYAML into a TunnelConfig with defaults applied.
// portProtos maps container port -> protocol ("http" or "tcp") from candy PortSpec data.
// boxPorts is the list of image port mappings (e.g. "18789:18789", "443:18789").
func ResolveTunnelConfig(t *TunnelYAML, boxName string, dns string, _ map[string]CandyReader, _ []string, portProtos map[string]string, boxPorts []string) *TunnelConfig {
	if t == nil {
		return nil
	}

	cfg := &TunnelConfig{
		Provider: t.Provider,
		BoxName:  boxName,
	}

	hostPorts := parseHostPorts(boxPorts)
	hostToContainer := buildPortMapping(boxPorts)

	// Determine public set
	publicSet := make(map[int]bool)
	publicHostnames := make(map[int]string)
	if t.Public.All {
		for _, p := range hostPorts {
			publicSet[p] = true
		}
	}
	for _, p := range t.Public.Ports {
		publicSet[p] = true
	}
	for p, h := range t.Public.PortMap {
		publicSet[p] = true
		publicHostnames[p] = h
	}

	// Determine private set ("all" means all remaining ports not already public)
	privateSet := make(map[int]bool)
	if t.Private.All {
		for _, p := range hostPorts {
			if !publicSet[p] {
				privateSet[p] = true
			}
		}
	}
	for _, p := range t.Private.Ports {
		privateSet[p] = true
	}

	// Build TunnelPort slice (ordered by image port order)
	for _, hp := range hostPorts {
		if !publicSet[hp] && !privateSet[hp] {
			continue // port not tunneled
		}
		cp := hp
		if c, ok := hostToContainer[hp]; ok {
			cp = c
		}
		proto := resolveProto(cp, portProtos)
		cfg.Ports = append(cfg.Ports, TunnelPort{
			Port:        hp,
			BackendPort: hp,
			Protocol:    proto,
			Public:      publicSet[hp],
			Hostname:    publicHostnames[hp],
		})
	}

	// Cloudflare defaults
	if cfg.Provider == "cloudflare" {
		cfg.TunnelName = t.Tunnel
		if cfg.TunnelName == "" {
			cfg.TunnelName = "charly-" + boxName
		}
		cfg.Hostname = dns
	}

	return cfg
}

// TunnelConfigFromMetadata creates a TunnelConfig from image label metadata.
// Unlike ResolveTunnelConfig, this doesn't need candy access since the tunnel
// configuration is already stored in the label.
func TunnelConfigFromMetadata(meta *BoxMetadata) *TunnelConfig {
	if meta == nil || meta.Tunnel == nil {
		return nil
	}

	t := meta.Tunnel
	cfg := &TunnelConfig{
		Provider: t.Provider,
		BoxName:  meta.Box,
	}

	hostPorts := parseHostPorts(meta.Port)
	hostToContainer := buildPortMapping(meta.Port)

	// Determine public set
	publicSet := make(map[int]bool)
	publicHostnames := make(map[int]string)
	if t.Public.All {
		for _, p := range hostPorts {
			publicSet[p] = true
		}
	}
	for _, p := range t.Public.Ports {
		publicSet[p] = true
	}
	for p, h := range t.Public.PortMap {
		publicSet[p] = true
		publicHostnames[p] = h
	}

	// Determine private set
	privateSet := make(map[int]bool)
	if t.Private.All {
		for _, p := range hostPorts {
			if !publicSet[p] {
				privateSet[p] = true
			}
		}
	}
	for _, p := range t.Private.Ports {
		privateSet[p] = true
	}

	// Build TunnelPort slice
	for _, hp := range hostPorts {
		if !publicSet[hp] && !privateSet[hp] {
			continue
		}
		cp := hp
		if c, ok := hostToContainer[hp]; ok {
			cp = c
		}
		proto := resolveProto(cp, meta.PortProto)
		cfg.Ports = append(cfg.Ports, TunnelPort{
			Port:        hp,
			BackendPort: hp,
			Protocol:    proto,
			Public:      publicSet[hp],
			Hostname:    publicHostnames[hp],
		})
	}

	// Cloudflare defaults
	if cfg.Provider == "cloudflare" {
		cfg.TunnelName = t.Tunnel
		if cfg.TunnelName == "" {
			cfg.TunnelName = "charly-" + meta.Box
		}
		cfg.Hostname = meta.DNS
	}

	return cfg
}
