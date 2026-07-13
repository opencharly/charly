package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/enginekit"
	"github.com/opencharly/sdk/spec"
)

// status_test.go — HOST-side `charly status` collection + probe tests. The
// render-side tests (cell formatters, table/JSON rendering) moved to
// candy/plugin-status/render_test.go alongside the code they exercise
// (status_render.go relocated wholesale, minus formatTunnelSummary which
// stayed here — a COLLECTION helper, not a render one).

// --- formatTunnelSummary (a collection helper — stays host) ---

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

// NOTE: the parsePS / parseDockerPortString white-box tests (formerly here)
// moved to the enginekit package with the engine-parsing code they exercise
// (chunk 1 relocated those functions to sdk/enginekit as unexported symbols).

// --- Snapshot HostPortFor ---

func TestHostPortFor_Bridge(t *testing.T) {
	snap := &enginekit.ContainerSnapshot{
		NetworkMode: "charly",
		Ports: []spec.PortMapping{
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
	snap := &enginekit.ContainerSnapshot{NetworkMode: "host"}
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
		engine: enginekit.NewEngineClient("podman"),
	}
	snap := &enginekit.ContainerSnapshot{
		Name:        "charly-selkies-desktop-w",
		Box:         "selkies-desktop",
		Instance:    "w",
		State:       "running",
		Ports:       []spec.PortMapping{{HostIP: "127.0.0.1", HostPort: 9240, CtrPort: 9222, Proto: "tcp"}},
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
