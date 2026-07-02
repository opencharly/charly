package main

// tunnel.go is the RESOLUTION half of the tunnel subsystem. The EXECUTION leg (the
// tailscale serve/funnel commands + the cloudflared tunnel lifecycle) was externalized
// to candy/plugin-tunnel (verb:tunnel) — the C16b core-externalization cutover; the core
// adapter that forwards TunnelStart / TunnelStop / cloudflareTunnelSetup over that verb
// lives in tunnel_plugin.go. What stays here is the PURE, execution-free surface with a
// SECOND in-core consumer beyond the deploy path (like GenerateK8sKustomize staying core):
//
//   - the wire types TunnelConfig / TunnelPort — consumed by the quadlet emitter
//     (quadlet.go: QuadletConfig.Tunnel + the ExecStartPost=tailscale serve emission) AND
//     marshaled across the process boundary to the plugin (hence the json tags);
//   - the pure helpers schemeTarget / tailscaleFlag / isTCPFamily / TunnelPort.backend /
//     ValidPublicPorts — the quadlet emitter builds the SAME tailscale command string from
//     them;
//   - the config-path helpers tunnelConfigDir / tunnelConfigPath — the quadlet emitter's
//     generateTunnelUnit references the cloudflared config path in the systemd unit;
//   - the resolution ResolveTunnelConfig / TunnelConfigFromMetadata / parseHostPorts /
//     buildPortMapping / resolveProto — turn a charly.yml TunnelYAML (or image-label
//     metadata) into a ready-to-execute TunnelConfig.

import (
	"fmt"
	"os"
	"path/filepath"
)

// TunnelPort represents a single port to tunnel with its protocol and access scope.
// The json tags are the wire contract with candy/plugin-tunnel's params.TunnelPort.
type TunnelPort struct {
	Port        int    `json:"port"`         // Tailscale HTTPS listen port (must be valid serve port)
	BackendPort int    `json:"backend_port"` // Localhost backend port (0 means same as Port)
	Protocol    string `json:"protocol"`     // backend scheme: "http", "https", "https+insecure", "tcp", "tls-terminated-tcp", "ssh", "rdp", "smb"
	Public      bool   `json:"public"`       // true = internet-accessible, false = private (tailnet-only)
	Hostname    string `json:"hostname"`     // cloudflare: per-port hostname (from map form)
}

// backend returns the localhost backend port, defaulting to Port if BackendPort is zero.
func (tp TunnelPort) backend() int {
	if tp.BackendPort != 0 {
		return tp.BackendPort
	}
	return tp.Port
}

// TunnelConfig is the resolved, ready-to-execute tunnel configuration. The json tags are
// the wire contract with candy/plugin-tunnel's params.TunnelConfig (tunnel_plugin.go
// marshals it as the {method, config} envelope's config field).
type TunnelConfig struct {
	Provider   string       `json:"provider"`    // "tailscale" or "cloudflare"
	TunnelName string       `json:"tunnel_name"` // cloudflare: tunnel name
	Hostname   string       `json:"hostname"`    // cloudflare: default hostname (from image dns field)
	BoxName    string       `json:"box_name"`    // for PID file naming
	Ports      []TunnelPort `json:"ports"`       // all tunneled ports with access scope
}

// schemeTarget returns the backend URL for a given scheme and port.
func schemeTarget(scheme string, port int) string {
	switch scheme {
	case "tcp", "tls-terminated-tcp":
		return fmt.Sprintf("tcp://127.0.0.1:%d", port)
	default:
		return fmt.Sprintf("%s://127.0.0.1:%d", scheme, port)
	}
}

// tailscaleFlag returns the tailscale serve/funnel flag for a scheme.
// Returns "--https", "--tcp", or "--tls-terminated-tcp".
func tailscaleFlag(scheme string) string {
	switch scheme {
	case "tcp":
		return "--tcp"
	case "tls-terminated-tcp":
		return "--tls-terminated-tcp"
	default:
		return "--https"
	}
}

// isTCPFamily returns true for schemes that use TCP-style forwarding (not HTTP proxy).
func isTCPFamily(scheme string) bool {
	switch scheme {
	case "tcp", "tls-terminated-tcp":
		return true
	default:
		return false
	}
}

// ValidPublicPorts are the allowed external ports for Tailscale public access.
var ValidPublicPorts = map[int]bool{443: true, 8443: true, 10000: true}

// tunnelConfigDir returns ~/.config/charly/tunnels/. Retained in core because the quadlet
// emitter (generateTunnelUnit) references the cloudflared config path via tunnelConfigPath;
// candy/plugin-tunnel keeps its OWN copy to WRITE the config/PID files there.
func tunnelConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "charly", "tunnels"), nil
}

// tunnelConfigPath returns the cloudflared config file path for a tunnel. Referenced by
// quadlet.go's generateTunnelUnit (the ExecStart=cloudflared --config <path> line).
func tunnelConfigPath(name string) (string, error) {
	dir, err := tunnelConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".yml"), nil
}

// parseHostPorts extracts host-side ports from image port mappings via the
// canonical ParsePortMapping. Unparseable entries are reported on stderr —
// silent skipping was the root cause of an unrelated bug where tunnel rules
// vanished without a diagnostic.
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
func resolveProto(containerPort int, portProtos map[int]string) string {
	if portProtos != nil {
		if pp, ok := portProtos[containerPort]; ok {
			return pp
		}
	}
	return "http"
}

// ResolveTunnelConfig resolves a TunnelYAML into a TunnelConfig with defaults applied.
// portProtos maps container port -> protocol ("http" or "tcp") from candy PortSpec data.
// boxPorts is the list of image port mappings (e.g. "18789:18789", "443:18789").
func ResolveTunnelConfig(t *TunnelYAML, boxName string, dns string, _ map[string]*Candy, _ []string, portProtos map[int]string, boxPorts []string) *TunnelConfig {
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
