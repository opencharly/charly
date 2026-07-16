package tunnelverb

// tunnel_exec.go is the EXECUTION LEG of the tunnel subsystem, externalized out of
// charly's core (charly/tunnel.go kept the pure RESOLUTION; the schemeTarget/
// tailscaleFlag/isTCPFamily helpers the quadlet emitter shares moved to sdk/deploykit
// with the emitter in P11). It runs the actual
// tailscale serve/funnel commands and the cloudflared tunnel lifecycle, stopping at the
// exec/auth boundary. The pure argv-building helpers here are the plugin's own copies of
// the core helpers (a cross-process-boundary duplication of a few tiny pure functions,
// like plugin-secrets' resolveSecretBackend — NOT in-module duplication): ONE argv
// builder (tailscaleStartArgv / tailscaleStopArgv) feeds BOTH the live exec AND the
// creds-free `plan` dry-run, so the dry-run proves the EXACT command the exec would run.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/opencharly/charly/candy/plugin-tunnel/params"
)

// --- pure helpers (plugin-local copies of the core helpers) ---

// backend returns the localhost backend port, defaulting to Port if BackendPort is zero.
// (params.TunnelPort ports are int64 — the gengotypes rendering of the CUE int.)
func backend(tp params.TunnelPort) int64 {
	if tp.BackendPort != 0 {
		return tp.BackendPort
	}
	return tp.Port
}

// schemeTarget returns the backend URL for a given scheme and port.
func schemeTarget(scheme string, port int64) string {
	switch scheme {
	case "tcp", "tls-terminated-tcp":
		return fmt.Sprintf("tcp://127.0.0.1:%d", port)
	default:
		return fmt.Sprintf("%s://127.0.0.1:%d", scheme, port)
	}
}

// tailscaleFlag returns the tailscale serve/funnel flag for a scheme
// ("--https", "--tcp", or "--tls-terminated-tcp").
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

// --- tailscale argv builders (shared by exec AND the plan dry-run — R3) ---

// serveVerb picks the tailscale sub-command: "funnel" (public/internet) or "serve"
// (private/tailnet-only).
func serveVerb(public bool) string {
	if public {
		return "funnel"
	}
	return "serve"
}

// tailscaleStartArgv builds the argv `tailscale serve|funnel --bg <flag>=<port> <target>`
// the start path runs (and the plan dry-run asserts).
func tailscaleStartArgv(tp params.TunnelPort) []string {
	return []string{
		"tailscale", serveVerb(tp.Public), "--bg",
		tailscaleFlag(tp.Protocol) + "=" + strconv.FormatInt(tp.Port, 10),
		schemeTarget(tp.Protocol, backend(tp)),
	}
}

// tailscaleStopArgv builds the argv `tailscale serve|funnel <flag>=<port> off`.
func tailscaleStopArgv(tp params.TunnelPort) []string {
	return []string{
		"tailscale", serveVerb(tp.Public),
		tailscaleFlag(tp.Protocol) + "=" + strconv.FormatInt(tp.Port, 10),
		"off",
	}
}

// --- dispatch: start / stop ---

// tunnelStart dispatches to the appropriate provider's start path.
func tunnelStart(cfg params.TunnelConfig) error {
	switch cfg.Provider {
	case "tailscale":
		for _, tp := range cfg.Ports {
			if err := tailscaleOneStart(tp); err != nil {
				return err
			}
		}
		return nil
	case "cloudflare":
		return cloudflareTunnelStart(cfg)
	default:
		return fmt.Errorf("unknown tunnel provider: %s", cfg.Provider)
	}
}

// tunnelStop dispatches to the appropriate provider's stop path.
func tunnelStop(cfg params.TunnelConfig) error {
	switch cfg.Provider {
	case "tailscale":
		for _, tp := range cfg.Ports {
			if err := tailscaleOneStop(tp); err != nil {
				return err
			}
		}
		return nil
	case "cloudflare":
		return cloudflareTunnelStop(cfg)
	default:
		return fmt.Errorf("unknown tunnel provider: %s", cfg.Provider)
	}
}

// --- tailscale serve/funnel (private = tailnet-only, public = internet-accessible) ---

func accessLabel(public bool) string {
	if public {
		return "public"
	}
	return "private"
}

func tailscaleOneStart(tp params.TunnelPort) error {
	if tp.Protocol == "udp" {
		fmt.Fprintf(os.Stderr, "Warning: port %d (UDP) cannot be tunneled — tailscale %s only supports TCP/HTTPS. UDP traffic works directly between tailnet nodes.\n", tp.Port, serveVerb(tp.Public))
		return nil
	}
	argv := tailscaleStartArgv(tp)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s tunnel on port %d failed: %w", accessLabel(tp.Public), tp.Port, err)
	}
	if tp.Public {
		fmt.Fprintf(os.Stderr, "Port %d: public (internet-accessible)\n", tp.Port)
	} else {
		proto := "https"
		if isTCPFamily(tp.Protocol) {
			proto = "tcp"
		}
		fmt.Fprintf(os.Stderr, "Port %d: private (tailnet-only, %s)\n", tp.Port, proto)
	}
	return nil
}

func tailscaleOneStop(tp params.TunnelPort) error {
	if tp.Protocol == "udp" {
		return nil // UDP ports are not tunneled
	}
	argv := tailscaleStopArgv(tp)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s tunnel stop on port %d failed: %w", accessLabel(tp.Public), tp.Port, err)
	}
	if tp.Public {
		fmt.Fprintf(os.Stderr, "Port %d: public tunnel disabled\n", tp.Port)
	} else {
		proto := "https"
		if isTCPFamily(tp.Protocol) {
			proto = "tcp"
		}
		fmt.Fprintf(os.Stderr, "Port %d: private tunnel disabled (%s)\n", tp.Port, proto)
	}
	return nil
}

// --- cloudflare tunnel lifecycle ---

// tunnelConfigDir returns ~/.config/charly/tunnels/ (the plugin's own copy of the core
// path helper — it WRITES the config/PID files here; the core keeps its copy because the
// quadlet emitter references tunnelConfigPath in the systemd unit).
func tunnelConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "charly", "tunnels"), nil
}

func tunnelPIDPath(name string) (string, error) {
	dir, err := tunnelConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".pid"), nil
}

func tunnelConfigPath(name string) (string, error) {
	dir, err := tunnelConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".yml"), nil
}

type cloudflaredTunnel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// cloudflareTunnelSetup creates the tunnel, writes config YAML, and routes DNS. Called by
// charly config (quadlet mode) for setup-only via the core adapter, and by
// cloudflareTunnelStart (direct mode). Returns the tunnel name and config file path.
func cloudflareTunnelSetup(cfg params.TunnelConfig) (tunnelName, configPath string, err error) {
	name := cfg.TunnelName

	uuid, err := findCloudflaredTunnel(name)
	if err != nil {
		return "", "", err
	}

	if uuid == "" {
		uuid, err = createCloudflaredTunnel(name)
		if err != nil {
			return "", "", err
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("determining home directory: %w", err)
	}
	credsFile := filepath.Join(home, ".cloudflared", uuid+".json")
	if _, err := os.Stat(credsFile); err != nil {
		return "", "", fmt.Errorf("credentials file not found at %s (run 'cloudflared tunnel login' first): %w", credsFile, err)
	}

	configDir, err := tunnelConfigDir()
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return "", "", fmt.Errorf("creating tunnel config dir: %w", err)
	}

	cfgPath, err := tunnelConfigPath(name)
	if err != nil {
		return "", "", err
	}

	var ingress strings.Builder
	for _, tp := range cfg.Ports {
		if tp.Protocol == "udp" {
			fmt.Fprintf(os.Stderr, "Warning: port %d (UDP) skipped in Cloudflare tunnel — cloudflared only supports HTTP/WebSocket\n", tp.Port)
			continue
		}
		hostname := tp.Hostname
		if hostname == "" {
			hostname = cfg.Hostname // fallback to image dns
		}
		fmt.Fprintf(&ingress, "  - hostname: %s\n    service: %s://localhost:%d\n", hostname, tp.Protocol, tp.Port)
	}
	ingress.WriteString("  - service: http_status:404\n")

	configContent := fmt.Sprintf("tunnel: %s\ncredentials-file: %s\ningress:\n%s", uuid, credsFile, ingress.String())

	if err := os.WriteFile(cfgPath, []byte(configContent), 0600); err != nil {
		return "", "", fmt.Errorf("writing tunnel config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Wrote tunnel config %s\n", cfgPath)

	hostnames := collectUniqueHostnames(cfg)
	for _, h := range hostnames {
		fmt.Fprintf(os.Stderr, "Routing DNS %s → tunnel %s\n", h, name)
		dnsCmd := exec.Command("cloudflared", "tunnel", "route", "dns", "--overwrite-dns", name, h)
		dnsCmd.Stdout = os.Stderr
		dnsCmd.Stderr = os.Stderr
		if err := dnsCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: DNS route for %s failed (may already exist): %v\n", h, err)
		}
	}

	return name, cfgPath, nil
}

// cloudflareTunnelStart sets up the tunnel (create, config, DNS) then starts the
// cloudflared process (direct mode). In quadlet mode, the core calls setup and the
// systemd service runs cloudflared.
func cloudflareTunnelStart(cfg params.TunnelConfig) error {
	name, cfgPath, err := cloudflareTunnelSetup(cfg)
	if err != nil {
		return err
	}

	cmd := exec.Command("cloudflared", "tunnel", "--config", cfgPath, "run", name)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting cloudflared: %w", err)
	}

	pidPath, err := tunnelPIDPath(name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Cloudflare Tunnel %s started (PID %d)\n", name, cmd.Process.Pid)
	return nil
}

// collectUniqueHostnames returns deduplicated hostnames from tunnel config ports.
func collectUniqueHostnames(cfg params.TunnelConfig) []string {
	seen := make(map[string]bool)
	var result []string
	for _, tp := range cfg.Ports {
		h := tp.Hostname
		if h == "" {
			h = cfg.Hostname
		}
		if h != "" && !seen[h] {
			seen[h] = true
			result = append(result, h)
		}
	}
	return result
}

func cloudflareTunnelStop(cfg params.TunnelConfig) error {
	name := cfg.TunnelName

	pidPath, err := tunnelPIDPath(name)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no PID file, nothing to stop
		}
		return fmt.Errorf("reading PID file: %w", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		_ = os.Remove(pidPath)
		return fmt.Errorf("invalid PID in %s: %w", pidPath, err)
	}

	switch err := syscall.Kill(pid, syscall.SIGTERM); {
	case err == nil:
		fmt.Fprintf(os.Stderr, "Stopped cloudflared tunnel %s (PID %d)\n", name, pid)
	case errors.Is(err, syscall.ESRCH):
		// Benign: the recorded PID is already gone (a prior stop already succeeded, the
		// process exited on its own, or this is a second/double teardown run) — the
		// intent ("make sure the tunnel is stopped") is already satisfied. Not a
		// warning: labeling an already-achieved end state a "warning" trains readers
		// to skim past real ones.
		fmt.Fprintf(os.Stderr, "note: cloudflared tunnel %s (PID %d) already stopped\n", name, pid)
	default:
		fmt.Fprintf(os.Stderr, "Warning: could not signal PID %d: %v\n", pid, err)
	}

	_ = os.Remove(pidPath)
	return nil
}

func findCloudflaredTunnel(name string) (string, error) {
	cmd := exec.Command("cloudflared", "tunnel", "list", "-o", "json")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("listing cloudflare tunnels: %w", err)
	}

	var tunnels []cloudflaredTunnel
	if err := json.Unmarshal(output, &tunnels); err != nil {
		return "", fmt.Errorf("parsing tunnel list: %w", err)
	}

	for _, t := range tunnels {
		if t.Name == name {
			return t.ID, nil
		}
	}
	return "", nil
}

func createCloudflaredTunnel(name string) (string, error) {
	cmd := exec.Command("cloudflared", "tunnel", "create", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("creating cloudflare tunnel: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	outputStr := string(output)
	if _, after, ok := strings.Cut(outputStr, "with id "); ok {
		uuid := strings.TrimSpace(after)
		if nlIdx := strings.IndexAny(uuid, "\n\r "); nlIdx != -1 {
			uuid = uuid[:nlIdx]
		}
		return uuid, nil
	}

	return "", fmt.Errorf("could not parse tunnel UUID from output: %s", outputStr)
}

// --- plan: the creds-free dry-run ---

// tunnelPlan builds the argv the start path WOULD run (without exec) and, when `expect`
// is set, asserts it matches — proving registry dispatch + the TunnelConfig wire
// round-trip + the moved command-building logic with ZERO tailscale/cloudflare creds.
func tunnelPlan(cfg params.TunnelConfig, expect []string) pluginCheckResult {
	lines, err := planArgvLines(cfg)
	if err != nil {
		return pluginCheckResult{Status: "fail", Message: err.Error()}
	}
	got := strings.Join(lines, "\n")
	if len(expect) > 0 {
		want := strings.Join(expect, "\n")
		if got != want {
			return pluginCheckResult{Status: "fail", Message: fmt.Sprintf("tunnel plan argv mismatch:\nwant:\n%s\ngot:\n%s", want, got)}
		}
	}
	return pluginCheckResult{Status: "pass", Message: fmt.Sprintf("tunnel plan (%s):\n%s", cfg.Provider, got)}
}

// planArgvLines renders the deterministic, creds-free command lines the tunnel start path
// would run: for tailscale, the exact `tailscale serve|funnel …` argv (via the SAME
// builder the exec uses); for cloudflare, the ingress rules setup would write plus the run
// command (no $HOME-dependent config path, so the dry-run stays host-independent).
func planArgvLines(cfg params.TunnelConfig) ([]string, error) {
	var lines []string
	switch cfg.Provider {
	case "tailscale":
		for _, tp := range cfg.Ports {
			if tp.Protocol == "udp" {
				continue // UDP is never tunneled
			}
			lines = append(lines, strings.Join(tailscaleStartArgv(tp), " "))
		}
	case "cloudflare":
		for _, tp := range cfg.Ports {
			if tp.Protocol == "udp" {
				continue
			}
			hostname := tp.Hostname
			if hostname == "" {
				hostname = cfg.Hostname
			}
			lines = append(lines, fmt.Sprintf("ingress %s -> %s://localhost:%d", hostname, tp.Protocol, tp.Port))
		}
		name := cfg.TunnelName
		if name == "" {
			name = "charly-" + cfg.BoxName
		}
		lines = append(lines, fmt.Sprintf("cloudflared tunnel run %s", name))
	default:
		return nil, fmt.Errorf("unknown tunnel provider: %s", cfg.Provider)
	}
	return lines, nil
}
