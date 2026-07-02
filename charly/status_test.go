package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// --- Cell formatters ---

func TestCellPorts(t *testing.T) {
	tests := []struct {
		name string
		in   []PortMapping
		want string
	}{
		{"empty", nil, "-"},
		{"single", []PortMapping{{HostPort: 5900, CtrPort: 5900, Proto: "tcp"}}, "5900"},
		{
			"multiple sorted",
			[]PortMapping{
				{HostPort: 8080, CtrPort: 8080, Proto: "tcp"},
				{HostPort: 5900, CtrPort: 5900, Proto: "tcp"},
				{HostPort: 18789, CtrPort: 18789, Proto: "tcp"},
			},
			"5900,8080,18789",
		},
		{
			"dedup duplicate host ports",
			[]PortMapping{
				{HostPort: 9222, CtrPort: 9222, Proto: "tcp"},
				{HostPort: 9222, CtrPort: 9222, Proto: "udp"},
			},
			"9222",
		},
		{"udp counts", []PortMapping{{HostPort: 47998, CtrPort: 47998, Proto: "udp"}}, "47998"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cellPorts(tt.in); got != tt.want {
				t.Errorf("cellPorts() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCellDevices(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, "-"},
		{"gpu only", []string{"nvidia.com/gpu=all"}, "gpu"},
		{"dri only", []string{"/dev/dri/renderD128"}, "dri"},
		{"gpu+dri", []string{"nvidia.com/gpu=all", "/dev/dri/renderD128"}, "dri,gpu"},
		{"gpu+dri+kvm", []string{"nvidia.com/gpu=all", "/dev/dri/renderD128", "/dev/kvm"}, "dri,gpu,kvm"},
		{"dedup dri", []string{"/dev/dri/renderD128", "/dev/dri/card0"}, "dri"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cellDevices(tt.in); got != tt.want {
				t.Errorf("cellDevices() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCellTools(t *testing.T) {
	tests := []struct {
		name string
		in   []ToolStatus
		want string
	}{
		{"empty", nil, "-"},
		{"all unconfigured", []ToolStatus{{Name: "cdp", Status: "-"}}, "-"},
		{"one ok with port", []ToolStatus{{Name: "cdp", Status: "ok", Port: 9222}}, "cdp:9222"},
		{"socket tool", []ToolStatus{{Name: "sway", Status: "ok"}}, "sway"},
		{
			"mixed sorted",
			[]ToolStatus{
				{Name: "cdp", Status: "ok", Port: 9222},
				{Name: "vnc", Status: "ok", Port: 5900},
				{Name: "sway", Status: "ok"},
				{Name: "wl", Status: "ok"},
			},
			"cdp:9222,sway,vnc:5900,wl",
		},
		{
			"remapped port + unreachable",
			[]ToolStatus{
				{Name: "cdp", Status: "ok", Port: 9223},
				{Name: "vnc", Status: "unreachable", Port: 5901},
			},
			"cdp:9223,vnc:5901",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cellTools(tt.in); got != tt.want {
				t.Errorf("cellTools() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCellToolsDetail(t *testing.T) {
	tools := []ToolStatus{
		{Name: "cdp", Status: "ok", Port: 9222},
		{Name: "vnc", Status: "ok", Port: 5900},
		{Name: "sway", Status: "ok"},
		{Name: "wl", Status: "ok"},
	}
	got := cellToolsDetail(tools)
	want := "cdp:9222 (ok), sway (ok), vnc:5900 (ok), wl (ok)"
	if got != want {
		t.Errorf("cellToolsDetail() = %q, want %q", got, want)
	}
}

func TestCellImage(t *testing.T) {
	tests := []struct {
		name string
		in   DeploymentStatus
		want string
	}{
		{"image only", DeploymentStatus{Image: "redis"}, "redis"},
		{"image+instance", DeploymentStatus{Image: "selkies-desktop", Instance: "work"}, "selkies-desktop/work"},
		{"hyphen in image", DeploymentStatus{Image: "check-sway-browser-vnc-pod"}, "check-sway-browser-vnc-pod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cellBox(tt.in); got != tt.want {
				t.Errorf("cellBox() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCellTunnel(t *testing.T) {
	if got := cellTunnel(""); got != "-" {
		t.Errorf("empty tunnel = %q, want %q", got, "-")
	}
	if got := cellTunnel("tailscale (all ports)"); got != "tailscale (all ports)" {
		t.Errorf("non-empty tunnel passthrough = %q", got)
	}
}

func TestFormatTunnelSummary(t *testing.T) {
	tests := []struct {
		name string
		in   *TunnelYAML
		want string
	}{
		{"nil", nil, ""},
		{"tailscale all", &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}}, "tailscale (all ports)"},
		{"cloudflare all", &TunnelYAML{Provider: "cloudflare", Public: PortScope{All: true}}, "cloudflare (all ports)"},
		{"provider only", &TunnelYAML{Provider: "tailscale"}, "tailscale"},
		{"explicit ports", &TunnelYAML{Provider: "tailscale", Private: PortScope{Ports: []int{8080, 9000}}}, "tailscale (ports 8080,9000)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTunnelSummary(tt.in); got != tt.want {
				t.Errorf("formatTunnelSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Engine ps parsing ---

func TestParsePS_Podman(t *testing.T) {
	in := `[{"Names":["charly-ollama"],"State":"running","Status":"Up 3 hours","Ports":[{"host_ip":"127.0.0.1","container_port":11434,"host_port":11434,"range":1,"protocol":"tcp"}]}]`
	rows, err := parsePS(in)
	if err != nil {
		t.Fatalf("parsePS: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Name != "charly-ollama" || rows[0].State != "running" {
		t.Errorf("name/state = %q/%q", rows[0].Name, rows[0].State)
	}
	if len(rows[0].Ports) != 1 || rows[0].Ports[0].HostPort != 11434 || rows[0].Ports[0].CtrPort != 11434 {
		t.Errorf("ports = %+v", rows[0].Ports)
	}
}

func TestParsePS_Podman_PortRange(t *testing.T) {
	in := `[{"Names":["charly-x"],"State":"running","Status":"Up","Ports":[{"host_ip":"127.0.0.1","container_port":8000,"host_port":8000,"range":3,"protocol":"tcp"}]}]`
	rows, err := parsePS(in)
	if err != nil {
		t.Fatalf("parsePS: %v", err)
	}
	if len(rows[0].Ports) != 3 {
		t.Fatalf("expected range expansion to 3 mappings, got %d", len(rows[0].Ports))
	}
	if rows[0].Ports[2].HostPort != 8002 || rows[0].Ports[2].CtrPort != 8002 {
		t.Errorf("range mapping[2] = %+v, want host=8002 ctr=8002", rows[0].Ports[2])
	}
}

func TestParsePS_DockerNDJSON(t *testing.T) {
	in := `{"Names":"charly-ollama","State":"running","Status":"Up 3 hours","Ports":"127.0.0.1:11434->11434/tcp"}` + "\n" +
		`{"Names":"charly-jupyter","State":"running","Status":"Up 1 hour","Ports":"127.0.0.1:8888->8888/tcp, 0.0.0.0:5900->5900/tcp"}`
	rows, err := parsePS(in)
	if err != nil {
		t.Fatalf("parsePS: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d, want 2", len(rows))
	}
	if rows[0].Ports[0].HostPort != 11434 || rows[0].Ports[0].HostIP != "127.0.0.1" {
		t.Errorf("row0 port[0] = %+v", rows[0].Ports[0])
	}
	if len(rows[1].Ports) != 2 || rows[1].Ports[1].HostPort != 5900 {
		t.Errorf("row1 ports = %+v", rows[1].Ports)
	}
}

func TestParseDockerPortString_IPv6(t *testing.T) {
	out := parseDockerPortString("[::]:8080->8080/tcp")
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1", len(out))
	}
	if out[0].HostIP != "::" || out[0].HostPort != 8080 || out[0].CtrPort != 8080 {
		t.Errorf("ipv6 mapping = %+v", out[0])
	}
}

func TestParseDockerPortString_Unpublished(t *testing.T) {
	out := parseDockerPortString("80/tcp")
	if len(out) != 0 {
		t.Errorf("unpublished port should be skipped, got %+v", out)
	}
}

// --- Snapshot HostPortFor ---

func TestHostPortFor_Bridge(t *testing.T) {
	snap := &ContainerSnapshot{
		NetworkMode: "charly",
		Ports: []PortMapping{
			{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"},
			{HostIP: "0.0.0.0", HostPort: 5900, CtrPort: 5900, Proto: "tcp"},
		},
	}
	ip, port, ok := snap.HostPortFor(9222, "tcp")
	if !ok || ip != "127.0.0.1" || port != 9240 {
		t.Errorf("9222: ok=%v ip=%q port=%d", ok, ip, port)
	}
	ip, port, ok = snap.HostPortFor(5900, "tcp")
	if !ok || ip != "127.0.0.1" || port != 5900 {
		t.Errorf("5900 (0.0.0.0 → 127.0.0.1): ok=%v ip=%q port=%d", ok, ip, port)
	}
	if _, _, ok := snap.HostPortFor(9999, "tcp"); ok {
		t.Errorf("9999 should not be published")
	}
}

func TestHostPortFor_HostNetwork(t *testing.T) {
	snap := &ContainerSnapshot{NetworkMode: "host"}
	ip, port, ok := snap.HostPortFor(9222, "tcp")
	if !ok || ip != "127.0.0.1" || port != 9222 {
		t.Errorf("host-net 9222: ok=%v ip=%q port=%d", ok, ip, port)
	}
}

// --- parsePortStrings (deploy.yml + image label fallback) ---

func TestParsePortStrings(t *testing.T) {
	in := []string{"8888:8888", "127.0.0.1:9240:9222/tcp", "[::1]:5900:5900"}
	out := parsePortStrings(in)
	if len(out) != 3 {
		t.Fatalf("got %d, want 3 — IPv4-prefixed form must parse", len(out))
	}
	if out[0].HostPort != 8888 || out[0].CtrPort != 8888 {
		t.Errorf("[0] = %+v", out[0])
	}
	if out[1].HostIP != "127.0.0.1" || out[1].HostPort != 9240 || out[1].CtrPort != 9222 || out[1].Proto != "tcp" {
		t.Errorf("[1] = %+v", out[1])
	}
	if out[2].HostIP != "[::1]" || out[2].HostPort != 5900 || out[2].CtrPort != 5900 {
		t.Errorf("[2] = %+v", out[2])
	}
}

// --- Probe Snippet/Parse ---

func TestSupervisordProbe_Parse(t *testing.T) {
	got := supervisordProbe{}.Parse("PRESENT=1\nfoo                              RUNNING   pid 1, uptime 0:01:00\nbar                              FATAL     Exited too quickly\n")
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.Detail != "1/2 running" {
		t.Errorf("detail = %q, want '1/2 running'", got.Detail)
	}
}

func TestSupervisordProbe_NotInstalled(t *testing.T) {
	got := supervisordProbe{}.Parse("")
	if got.Status != "-" {
		t.Errorf("empty stdout should be '-', got %q", got.Status)
	}
}

func TestDbusProbe_WithDaemons(t *testing.T) {
	got := dbusProbe{}.Parse("DBUS=1\nDAEMON=swaync\nDAEMON=mako\n")
	if got.Status != "ok" {
		t.Errorf("status = %q", got.Status)
	}
	if got.Detail != "notify:swaync,mako" {
		t.Errorf("detail = %q", got.Detail)
	}
}

func TestDbusProbe_NotPresent(t *testing.T) {
	got := dbusProbe{}.Parse("")
	if got.Status != "-" {
		t.Errorf("status = %q, want '-'", got.Status)
	}
}

func TestCharlyProbe_Present(t *testing.T) {
	got := charlyProbe{}.Parse("CHARLY=1\n2026.05.02-1234\n")
	if got.Status != "ok" || got.Detail != "2026.05.02-1234" {
		t.Errorf("got %+v", got)
	}
}

func TestWlProbe_Mixed(t *testing.T) {
	got := wlProbe{}.Parse("WL=wtype\nWL=wlrctl\nWL=grim\n")
	if got.Status != "ok" {
		t.Errorf("status = %q", got.Status)
	}
	if got.Detail != "wtype,wlrctl,grim" {
		t.Errorf("detail = %q", got.Detail)
	}
}

func TestWlProbe_OnlyOneScreenshot(t *testing.T) {
	got := wlProbe{}.Parse("WL=wtype\nWL=grim\nWL=pixelflux-screenshot\n")
	if got.Detail != "wtype,grim" {
		t.Errorf("expected only one screenshot tool, got %q", got.Detail)
	}
}

func TestSwayProbe_Outputs(t *testing.T) {
	body := `[{"name":"HEADLESS-1","current_mode":{"width":1920,"height":1080}}]`
	got := swayProbe{}.Parse("SWAY=1\n" + body)
	if got.Status != "ok" {
		t.Errorf("status = %q", got.Status)
	}
	if got.Detail != "HEADLESS-1 1920x1080" {
		t.Errorf("detail = %q", got.Detail)
	}
}

// --- Probe batcher ---

func TestSplitProbeSections(t *testing.T) {
	stdout := "\n===PROBE:supervisord===\nPRESENT=1\nfoo RUNNING pid 1\n===PROBE_END:supervisord===\n" +
		"\n===PROBE:dbus===\nDBUS=1\nDAEMON=swaync\n===PROBE_END:dbus===\n"
	sections := splitProbeSections(stdout)
	if !strings.Contains(sections["supervisord"], "PRESENT=1") {
		t.Errorf("supervisord section missing payload: %q", sections["supervisord"])
	}
	if !strings.Contains(sections["dbus"], "DAEMON=swaync") {
		t.Errorf("dbus section missing payload: %q", sections["dbus"])
	}
}

// --- Renderers ---

func TestRenderTable_HasColumns(t *testing.T) {
	statuses := []DeploymentStatus{
		{
			Image:    "selkies-desktop",
			Instance: "work",
			Status:   "running",
			Ports:    []PortMapping{{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"}},
			Tunnel:   "tailscale (all ports)",
			Tools:    []ToolStatus{{Name: "cdp", Status: "ok", Port: 9240}},
			RunMode:  "quadlet",
		},
	}
	var buf bytes.Buffer
	if err := RenderTable(&buf, statuses); err != nil {
		t.Fatalf("RenderTable: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "IMAGE") || !strings.Contains(out, "TUNNEL") {
		t.Errorf("table missing IMAGE/TUNNEL header columns:\n%s", out)
	}
	if !strings.Contains(out, "selkies-desktop/work") {
		t.Errorf("instance not merged into IMAGE cell:\n%s", out)
	}
	if !strings.Contains(out, "9240") {
		t.Errorf("host port not rendered:\n%s", out)
	}
	if !strings.Contains(out, "tailscale (all ports)") {
		t.Errorf("tunnel summary missing:\n%s", out)
	}
}

func TestRenderJSON_StructuredPorts(t *testing.T) {
	statuses := []DeploymentStatus{
		{
			Image: "x",
			Ports: []PortMapping{{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"}},
		},
	}
	var buf bytes.Buffer
	if err := RenderJSON(&buf, statuses); err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"host_port": 9240`) {
		t.Errorf("structured port object missing:\n%s", out)
	}
}

// --- Collector lookup helpers ---

func TestCollector_LookupDeploy_KeyShapes(t *testing.T) {
	c := &Collector{
		deploy: &BundleConfig{
			Bundle: map[string]BundleNode{
				"selkies-desktop":      {Port: []string{"3000:3000"}},
				"selkies-desktop/work": {Port: []string{"3001:3000"}, Tunnel: &TunnelYAML{Provider: "tailscale", Private: PortScope{All: true}}},
				"weird-joined-name":    {Port: []string{"7777:7777"}},
			},
		},
	}
	// Base image, no instance — direct hit.
	dn, ok := c.lookupDeploy("selkies-desktop", "", "charly-selkies-desktop")
	if !ok || len(dn.Port) == 0 {
		t.Errorf("base lookup failed: ok=%v ports=%v", ok, dn.Port)
	}
	// Image + instance — deployKey form.
	dn, ok = c.lookupDeploy("selkies-desktop", "work", "charly-selkies-desktop-work")
	if !ok || dn.Tunnel == nil || dn.Tunnel.Provider != "tailscale" {
		t.Errorf("instance lookup failed: ok=%v tunnel=%+v", ok, dn.Tunnel)
	}
	// Joined-name fallback.
	dn, ok = c.lookupDeploy("", "", "charly-weird-joined-name")
	if !ok || len(dn.Port) == 0 {
		t.Errorf("joined-name lookup failed: ok=%v", ok)
	}
}

// --- collectOne uses base image name for image-label fallback ---

func TestCollector_CollectOne_UsesBaseImageForLabels(t *testing.T) {
	// Smoke check: an empty Collector + a snapshot with Box set should
	// not panic and should populate Ports from runtime snapshot. Exercising
	// the full image-label fallback would require mocking
	// ResolveNewestLocalCalVer/ExtractMetadata; that's covered indirectly
	// by R10. This test pins the data-flow invariant.
	c := &Collector{
		rt:     &ResolvedRuntime{RunMode: "quadlet"},
		engine: &EngineClient{bin: "podman"},
	}
	snap := &ContainerSnapshot{
		Name:        "charly-selkies-desktop-w",
		Box:         "selkies-desktop",
		Instance:    "w",
		State:       "running",
		Ports:       []PortMapping{{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"}},
		NetworkMode: "charly",
	}
	cs := c.collectOne(context.Background(), snap)
	if cs.Image != "selkies-desktop" || cs.Instance != "w" {
		t.Errorf("image/instance not preserved: %q/%q", cs.Image, cs.Instance)
	}
	if len(cs.Ports) != 1 || cs.Ports[0].HostPort != 9240 {
		t.Errorf("runtime ports not surfaced: %+v", cs.Ports)
	}
}

// --- statusFromState ---

func TestStatusFromState(t *testing.T) {
	cases := map[string]string{
		"running": "running",
		"exited":  "stopped",
		"created": "stopped",
		"paused":  "paused",
		"dead":    "dead",
		"":        "stopped",
		"weird":   "weird",
	}
	for in, want := range cases {
		if got := statusFromState(in); got != want {
			t.Errorf("statusFromState(%q) = %q, want %q", in, got, want)
		}
	}
}
