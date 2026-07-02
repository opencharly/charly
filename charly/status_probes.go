package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// ToolStatus is one row of the live tool-probe result. Status="-" means the
// tool isn't configured in this container; it gets filtered before render.
type ToolStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Port   int    `json:"port,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Probe is the union root. Implementations are either HostProbe (network
// probe from the operator host) or GuestProbe (runs inside the container).
type Probe interface {
	Name() string
}

// HostProbe runs from the operator's machine — no `podman exec`. Port
// resolution comes from ContainerSnapshot, NOT from a per-probe `podman port`
// shell-out. ctx allows the collector to bound probe duration.
type HostProbe interface {
	Probe
	ProbeHost(ctx context.Context, snap *ContainerSnapshot) ToolStatus
}

// GuestProbe runs INSIDE a container. The collector batches every applicable
// GuestProbe for one container into a single `podman exec sh -c '<concat>'`
// invocation. Each probe contributes a self-contained shell snippet that must
// not propagate non-zero exit (guard tool checks with `command -v X && X ...`
// patterns). Output is split per-probe by the batcher; each section is fed to
// Parse.
type GuestProbe interface {
	Probe
	Snippet() string
	Parse(stdout string) ToolStatus
}

// hostProbes / guestProbes are the registered probe sets. Stateless singletons.
var (
	hostProbes  = []HostProbe{cdpProbe{}, vncProbe{}}
	guestProbes = []GuestProbe{
		supervisordProbe{},
		dbusProbe{},
		charlyProbe{},
		wlProbe{},
		swayProbe{},
	}
)

// --- Guest probes ---

type supervisordProbe struct{}

func (supervisordProbe) Name() string { return "supervisord" }
func (supervisordProbe) Snippet() string {
	// supervisorctl exits 3 when any service isn't RUNNING but still emits
	// the table on stdout. Catch that with `|| true` so the snippet never
	// signals failure to the outer shell. Tag the first stdout line with
	// PRESENT=1 so Parse can tell "not installed" from "running but
	// every service is FATAL".
	return `command -v supervisorctl >/dev/null 2>&1 || exit 0
echo PRESENT=1
supervisorctl status 2>&1 || true`
}
func (supervisordProbe) Parse(stdout string) ToolStatus {
	ts := ToolStatus{Name: "supervisord", Status: "-"}
	stdout = strings.TrimSpace(stdout)
	if stdout == "" || !strings.HasPrefix(stdout, "PRESENT=1") {
		return ts
	}
	ts.Status = "ok"
	body := strings.TrimPrefix(stdout, "PRESENT=1")
	body = strings.TrimSpace(body)
	if body == "" {
		return ts
	}
	if strings.Contains(body, "no such file") || strings.Contains(body, "refused") {
		ts.Status = "unreachable"
		return ts
	}
	lines := strings.Split(body, "\n")
	running := 0
	total := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		total++
		if strings.Contains(line, "RUNNING") {
			running++
		}
	}
	if total > 0 {
		ts.Detail = fmt.Sprintf("%d/%d running", running, total)
	}
	return ts
}

type dbusProbe struct{}

func (dbusProbe) Name() string { return "dbus" }
func (dbusProbe) Snippet() string {
	// One snippet, four checks. Echos lines like "DAEMON=swaync" only for
	// daemons that are actually running, so Parse can build the detail.
	return `pgrep -x dbus-daemon >/dev/null 2>&1 || exit 0
echo DBUS=1
for d in swaync mako dunst; do
  pgrep -x "$d" >/dev/null 2>&1 && echo "DAEMON=$d"
done
true`
}
func (dbusProbe) Parse(stdout string) ToolStatus {
	ts := ToolStatus{Name: "dbus", Status: "-"}
	if !strings.Contains(stdout, "DBUS=1") {
		return ts
	}
	ts.Status = "ok"
	var daemons []string
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		if d, ok := strings.CutPrefix(line, "DAEMON="); ok {
			daemons = append(daemons, d)
		}
	}
	if len(daemons) > 0 {
		ts.Detail = "notify:" + strings.Join(daemons, ",")
	}
	return ts
}

type charlyProbe struct{}

func (charlyProbe) Name() string { return "charly" }
func (charlyProbe) Snippet() string {
	return `command -v charly >/dev/null 2>&1 || exit 0
echo CHARLY=1
charly version 2>/dev/null || true`
}
func (charlyProbe) Parse(stdout string) ToolStatus {
	ts := ToolStatus{Name: "charly", Status: "-"}
	if !strings.Contains(stdout, "CHARLY=1") {
		return ts
	}
	ts.Status = "ok"
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stdout), "CHARLY=1"))
	if body != "" {
		ts.Detail = body
	}
	return ts
}

type wlProbe struct{}

func (wlProbe) Name() string { return "wl" }
func (wlProbe) Snippet() string {
	// All three core-tool checks in one snippet. Each present tool writes
	// its own marker line; Parse returns "-" when none are present.
	return `for t in wtype wlrctl grim pixelflux-screenshot; do
  command -v "$t" >/dev/null 2>&1 && echo "WL=$t"
done
true`
}
func (wlProbe) Parse(stdout string) ToolStatus {
	ts := ToolStatus{Name: "wl", Status: "-"}
	var tools []string
	seenScreenshot := false
	for line := range strings.SplitSeq(stdout, "\n") {
		line = strings.TrimSpace(line)
		t := strings.TrimPrefix(line, "WL=")
		if t == line {
			continue
		}
		// Only report one screenshot tool (matches old checkWlStatus).
		if t == "grim" || t == "pixelflux-screenshot" {
			if seenScreenshot {
				continue
			}
			seenScreenshot = true
		}
		tools = append(tools, t)
	}
	if len(tools) == 0 {
		return ts
	}
	ts.Status = "ok"
	ts.Detail = strings.Join(tools, ",")
	return ts
}

type swayProbe struct{}

func (swayProbe) Name() string { return "sway" }
func (swayProbe) Snippet() string {
	// Discover SWAYSOCK then ask sway for its outputs. Empty stdout when
	// sway isn't running.
	return `command -v swaymsg >/dev/null 2>&1 || exit 0
SWAYSOCK=$(ls -t /tmp/sway-ipc.*.sock 2>/dev/null | head -1)
[ -n "$SWAYSOCK" ] || exit 0
export SWAYSOCK
echo SWAY=1
swaymsg -t get_outputs 2>/dev/null || true`
}
func (swayProbe) Parse(stdout string) ToolStatus {
	ts := ToolStatus{Name: "sway", Status: "-"}
	stdout = strings.TrimSpace(stdout)
	if !strings.HasPrefix(stdout, "SWAY=1") {
		return ts
	}
	ts.Status = "ok"
	body := strings.TrimSpace(strings.TrimPrefix(stdout, "SWAY=1"))
	if body == "" {
		return ts
	}
	var outputs []struct {
		Name        string `json:"name"`
		CurrentMode struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"current_mode"`
	}
	if err := json.Unmarshal([]byte(body), &outputs); err == nil && len(outputs) > 0 {
		o := outputs[0]
		ts.Detail = fmt.Sprintf("%s %dx%d", o.Name, o.CurrentMode.Width, o.CurrentMode.Height)
	}
	return ts
}

// --- Host probes ---

// devToolsTab is defined in cdp.go and reused here.

type cdpProbe struct{}

func (cdpProbe) Name() string { return "cdp" }
func (cdpProbe) ProbeHost(ctx context.Context, snap *ContainerSnapshot) ToolStatus {
	ts := ToolStatus{Name: "cdp", Status: "-"}
	ip, port, ok := snap.HostPortFor(9222, "tcp")
	if !ok {
		return ts
	}
	ts.Port = port
	ts.Status = "unreachable"
	url := fmt.Sprintf("http://%s:%d/json", ip, port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ts
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ts
	}
	defer resp.Body.Close()
	var tabs []devToolsTab
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return ts
	}
	ts.Status = "ok"
	ts.Detail = fmt.Sprintf("%d tabs", len(tabs))
	return ts
}

type vncProbe struct{}

func (vncProbe) Name() string { return "vnc" }
func (vncProbe) ProbeHost(ctx context.Context, snap *ContainerSnapshot) ToolStatus {
	ts := ToolStatus{Name: "vnc", Status: "-"}
	ip, port, ok := snap.HostPortFor(5900, "tcp")
	if !ok {
		return ts
	}
	ts.Port = port
	ts.Status = "unreachable"
	addr := fmt.Sprintf("%s:%d", ip, port)
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return ts
	}
	defer conn.Close() //nolint:errcheck
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var banner [12]byte
	if _, err := io.ReadFull(conn, banner[:]); err != nil {
		return ts
	}
	if !strings.HasPrefix(string(banner[:]), "RFB ") {
		return ts
	}
	ts.Status = "ok"
	ts.Detail = strings.TrimSpace(string(banner[:]))
	return ts
}

// --- Batcher ---

const probeStartMarker = "===PROBE:"
const probeEndMarker = "===PROBE_END:"

// runGuestProbes assembles all GuestProbe snippets into one shell script,
// runs it via a single ExecBatched, splits the stdout by per-probe markers,
// and dispatches each section to its probe's Parse. One subprocess per
// container (was 7+).
func runGuestProbes(ctx context.Context, e *EngineClient, container string, probes []GuestProbe) []ToolStatus {
	if len(probes) == 0 {
		return nil
	}
	var b strings.Builder
	for _, p := range probes {
		fmt.Fprintf(&b, "printf '\\n%s%s===\\n'\n( %s ) 2>/dev/null\nprintf '%s%s===\\n'\n",
			probeStartMarker, p.Name(),
			p.Snippet(),
			probeEndMarker, p.Name(),
		)
	}
	out, _ := e.ExecBatched(ctx, container, b.String())
	sections := splitProbeSections(out)
	results := make([]ToolStatus, len(probes))
	for i, p := range probes {
		results[i] = p.Parse(sections[p.Name()])
	}
	return results
}

// splitProbeSections returns a map[probeName]stdout for the markers emitted
// by runGuestProbes' assembled script. Sections that are missing (probe
// produced no output / was clipped) map to "".
func splitProbeSections(out string) map[string]string {
	sections := map[string]string{}
	rest := out
	for {
		startIdx := strings.Index(rest, probeStartMarker)
		if startIdx < 0 {
			break
		}
		afterStart := rest[startIdx+len(probeStartMarker):]
		before, after, ok := strings.Cut(afterStart, "===\n")
		if !ok {
			break
		}
		name := before
		body := after
		endIdx := strings.Index(body, probeEndMarker+name+"===")
		if endIdx < 0 {
			sections[name] = strings.TrimSpace(body)
			break
		}
		sections[name] = strings.TrimSpace(body[:endIdx])
		rest = body[endIdx:]
	}
	return sections
}
