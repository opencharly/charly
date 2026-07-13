package status

import (
	"bytes"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// render_test.go — moved from charly/status_test.go's "--- Cell formatters ---" and
// "--- Renderers ---" sections (P14a chunk 2b): the PURE render tests that exercise the code now
// living in render.go. formatTunnelSummary's test STAYED host (it's a collection helper).

// --- Cell formatters ---

func TestCellPorts(t *testing.T) {
	tests := []struct {
		name string
		in   []spec.PortMapping
		want string
	}{
		{"empty", nil, "-"},
		{"single", []spec.PortMapping{{HostPort: 5900, CtrPort: 5900, Proto: "tcp"}}, "5900"},
		{
			"multiple sorted",
			[]spec.PortMapping{
				{HostPort: 8080, CtrPort: 8080, Proto: "tcp"},
				{HostPort: 5900, CtrPort: 5900, Proto: "tcp"},
				{HostPort: 18789, CtrPort: 18789, Proto: "tcp"},
			},
			"5900,8080,18789",
		},
		{
			"dedup duplicate host ports",
			[]spec.PortMapping{
				{HostPort: 9222, CtrPort: 9222, Proto: "tcp"},
				{HostPort: 9222, CtrPort: 9222, Proto: "udp"},
			},
			"9222",
		},
		{"udp counts", []spec.PortMapping{{HostPort: 47998, CtrPort: 47998, Proto: "udp"}}, "47998"},
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
		in   []spec.ToolStatus
		want string
	}{
		{"empty", nil, "-"},
		{"all unconfigured", []spec.ToolStatus{{Name: "cdp", Status: "-"}}, "-"},
		{"one ok with port", []spec.ToolStatus{{Name: "cdp", Status: "ok", Port: 9222}}, "cdp:9222"},
		{"socket tool", []spec.ToolStatus{{Name: "sway", Status: "ok"}}, "sway"},
		{
			"mixed sorted",
			[]spec.ToolStatus{
				{Name: "cdp", Status: "ok", Port: 9222},
				{Name: "vnc", Status: "ok", Port: 5900},
				{Name: "sway", Status: "ok"},
				{Name: "wl", Status: "ok"},
			},
			"cdp:9222,sway,vnc:5900,wl",
		},
		{
			"remapped port + unreachable",
			[]spec.ToolStatus{
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
	tools := []spec.ToolStatus{
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

func TestCellBox(t *testing.T) {
	tests := []struct {
		name string
		in   spec.DeploymentStatus
		want string
	}{
		{"image only", spec.DeploymentStatus{Image: "redis"}, "redis"},
		{"image+instance", spec.DeploymentStatus{Image: "selkies-desktop", Instance: "work"}, "selkies-desktop/work"},
		{"hyphen in image", spec.DeploymentStatus{Image: "check-sway-browser-vnc-pod"}, "check-sway-browser-vnc-pod"},
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

// --- Renderers ---

func TestRenderTable_HasColumns(t *testing.T) {
	statuses := []spec.DeploymentStatus{
		{
			Image:    "selkies-desktop",
			Instance: "work",
			Status:   "running",
			Ports:    []spec.PortMapping{{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"}},
			Tunnel:   "tailscale (all ports)",
			Tools:    []spec.ToolStatus{{Name: "cdp", Status: "ok", Port: 9240}},
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
	statuses := []spec.DeploymentStatus{
		{
			Image: "x",
			Ports: []spec.PortMapping{{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"}},
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
