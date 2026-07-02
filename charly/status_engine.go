package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// PortMapping is the structured runtime port mapping for one published port.
// Replaces the string-flattened representation that lived on the old
// ContainerPSEntry. Surfaces all the way to DeploymentStatus so renderers and
// host probes can consume it without re-parsing.
type PortMapping struct {
	HostIP   string `json:"host_ip,omitempty"`
	HostPort int    `json:"host_port"`
	CtrPort  int    `json:"container_port"`
	Proto    string `json:"protocol"`
}

// ContainerSnapshot is the cheap, batch-derived view of one charly-* container.
// One snapshot is built from a single `podman ps --format json` row plus the
// matching `podman inspect` blob; both engine calls are batched at the
// EngineClient level (one ps + one inspect per status invocation, not per
// container).
type ContainerSnapshot struct {
	Name        string        // "charly-selkies-desktop-185.52.136.164"
	State       string        // "running" | "exited" | "created" | "paused" | "dead" | "removing"
	Status      string        // human "Up 3 hours"
	Box         string        // base box short-name (filled by Collector after parsing the quadlet description)
	Instance    string        // optional instance suffix, ditto
	ImageRef    string        // full image ref:tag from ps — the deployed artifact identity
	NetworkMode string        // "host" | "bridge" | "container:<id>" | named network
	Ports       []PortMapping // runtime mappings from `podman ps`
	Devices     []string      // /dev/dri/..., nvidia.com/gpu=all, ...
	Mounts      []MountInfo   // live mounts from podman inspect .Mounts (RUNTIME truth — what the container is ACTUALLY mounting, not the OCI label default)
}

// MountInfo represents one live container mount point as reported by
// `podman inspect .Mounts[]`. Source is the host-side path (or volume
// name for type=volume); Destination is the container-side path. Type
// is the engine's mount kind ("bind" / "volume" / "tmpfs"). Used by
// `charly status` to distinguish a `--bind` / `--encrypt` deploy override
// from the image-label default volume backing.
type MountInfo struct {
	Type        string // "bind" | "volume" | "tmpfs"
	Source      string // host path (bind) or volume name (volume)
	Destination string // container path
	Name        string // for type=volume: the named volume; otherwise empty
}

// HostPortFor returns the host IP + host port that maps to the given
// container-side port/proto. Host-networked containers always return
// ("127.0.0.1", ctrPort, true) — there is no NAT mapping but the port is
// reachable on localhost. Returns ("", 0, false) when the port is not
// published.
func (s *ContainerSnapshot) HostPortFor(ctrPort int, proto string) (string, int, bool) {
	if s == nil {
		return "", 0, false
	}
	if s.NetworkMode == "host" {
		return "127.0.0.1", ctrPort, true
	}
	for _, p := range s.Ports {
		if p.CtrPort == ctrPort && (proto == "" || p.Proto == proto || p.Proto == "") {
			ip := p.HostIP
			if ip == "" || ip == "0.0.0.0" || ip == "::" {
				ip = "127.0.0.1"
			}
			return ip, p.HostPort, true
		}
	}
	return "", 0, false
}

// EngineClient is the only place in the status surface that touches
// podman/docker. All other code consumes ContainerSnapshot.
type EngineClient struct {
	bin string // "podman" or "docker"
}

// NewEngineClient builds a client for the given engine name ("podman" or
// "docker", or "auto" which is resolved via EngineBinary).
func NewEngineClient(engine string) *EngineClient {
	return &EngineClient{bin: EngineBinary(engine)}
}

// Bin returns the resolved engine binary name.
func (e *EngineClient) Bin() string { return e.bin }

// SnapshotAll lists charly-* containers (one ps call), then runs one batched
// inspect for the whole set. Returns one ContainerSnapshot per container.
func (e *EngineClient) SnapshotAll(includeAll bool) ([]ContainerSnapshot, error) {
	rows, err := e.runPS(includeAll)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	inspects, err := e.runInspect(names)
	if err != nil {
		// Inspect failures shouldn't blank out the whole snapshot — fall
		// back to ps-only data with empty NetworkMode/Devices.
		inspects = nil
	}
	idx := map[string]engineInspectRow{}
	for _, ir := range inspects {
		idx[strings.TrimPrefix(ir.Name, "/")] = ir
	}
	out := make([]ContainerSnapshot, 0, len(rows))
	for _, r := range rows {
		snap := ContainerSnapshot{
			Name:     r.Name,
			State:    r.State,
			Status:   r.Status,
			ImageRef: r.Image,
			Ports:    r.Ports,
		}
		if ir, ok := idx[r.Name]; ok {
			snap.NetworkMode = ir.NetworkMode
			snap.Devices = ir.Devices
			snap.Mounts = ir.Mounts
		}
		out = append(out, snap)
	}
	return out, nil
}

// ExecBatched runs `<engine> exec <container> sh -c '<script>'` and returns
// combined stdout. Used by the GuestProbe batcher to run all probes for one
// container in a single exec session.
func (e *EngineClient) ExecBatched(ctx context.Context, container, script string) (string, error) {
	cmd := exec.CommandContext(ctx, e.bin, "exec", container, "sh", "-c", script)
	out, err := cmd.Output()
	return string(out), err
}

// Snapshot returns a ContainerSnapshot for one named container. Used by
// interactive single-container commands (WlStatusCmd and the like) that need
// probe data without enumerating every charly container.
// Two engine calls (one filtered ps + one inspect of just this name).
func (e *EngineClient) Snapshot(name string) (*ContainerSnapshot, error) {
	out, err := exec.Command(e.bin, "ps", "-a", "--filter", "name="+name, "--format", "json", "--no-trunc").Output()
	if err != nil {
		return nil, fmt.Errorf("ps %s: %w", name, err)
	}
	rows, err := parsePS(string(out))
	if err != nil {
		return nil, err
	}
	var row enginePSRow
	for _, r := range rows {
		if r.Name == name {
			row = r
			break
		}
	}
	if row.Name == "" {
		return nil, fmt.Errorf("container %s not found", name)
	}
	snap := &ContainerSnapshot{
		Name:     row.Name,
		State:    row.State,
		Status:   row.Status,
		ImageRef: row.Image,
		Ports:    row.Ports,
	}
	if inspects, err := e.runInspect([]string{name}); err == nil && len(inspects) > 0 {
		snap.NetworkMode = inspects[0].NetworkMode
		snap.Devices = inspects[0].Devices
		snap.Mounts = inspects[0].Mounts
	}
	return snap, nil
}

// --- Internal: ps parsing ---

type enginePSRow struct {
	Name   string
	State  string
	Status string
	Image  string // full image ref:tag as reported by ps
	Ports  []PortMapping
}

func (e *EngineClient) runPS(includeAll bool) ([]enginePSRow, error) {
	args := []string{"ps", "--filter", "name=charly-", "--format", "json", "--no-trunc"}
	if includeAll {
		args = append(args, "-a")
	}
	out, err := exec.Command(e.bin, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	return parsePS(string(out))
}

// podmanPSEntry matches Podman's structured JSON shape.
type podmanPSEntry struct {
	Names  []string     `json:"Names"`
	Status string       `json:"Status"`
	State  string       `json:"State"`
	Image  string       `json:"Image"`
	Ports  []podmanPort `json:"Ports"`
}

type podmanPort struct {
	HostIP        string `json:"host_ip"`
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	Range         int    `json:"range"`
	Protocol      string `json:"protocol"`
}

// dockerPSEntry matches Docker's stringly-typed JSON shape.
type dockerPSEntry struct {
	Names  string `json:"Names"`
	Status string `json:"Status"`
	State  string `json:"State"`
	Image  string `json:"Image"`
	Ports  string `json:"Ports"`
}

// parsePS handles podman's JSON-array shape and docker's NDJSON shape.
func parsePS(data string) ([]enginePSRow, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var pe []podmanPSEntry
		if err := json.Unmarshal([]byte(trimmed), &pe); err == nil {
			out := make([]enginePSRow, 0, len(pe))
			for _, e := range pe {
				out = append(out, enginePSRow{
					Name:   firstName(e.Names),
					State:  e.State,
					Status: e.Status,
					Image:  e.Image,
					Ports:  fromPodmanPorts(e.Ports),
				})
			}
			return out, nil
		}
		var de []dockerPSEntry
		if err := json.Unmarshal([]byte(trimmed), &de); err != nil {
			return nil, fmt.Errorf("parsing ps JSON: %w", err)
		}
		return fromDockerPSRows(de), nil
	}
	// docker ps emits NDJSON by default
	var de []dockerPSEntry
	for line := range strings.SplitSeq(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry dockerPSEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, fmt.Errorf("parsing ps JSON line: %w", err)
		}
		de = append(de, entry)
	}
	return fromDockerPSRows(de), nil
}

func firstName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	// Podman may return comma-joined names — first wins.
	return strings.Split(names[0], ",")[0]
}

func fromPodmanPorts(in []podmanPort) []PortMapping {
	if len(in) == 0 {
		return nil
	}
	out := make([]PortMapping, 0, len(in))
	for _, p := range in {
		span := max(p.Range, 1)
		for i := range span {
			out = append(out, PortMapping{
				HostIP:   p.HostIP,
				HostPort: p.HostPort + i,
				CtrPort:  p.ContainerPort + i,
				Proto:    p.Protocol,
			})
		}
	}
	return out
}

func fromDockerPSRows(rows []dockerPSEntry) []enginePSRow {
	out := make([]enginePSRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, enginePSRow{
			Name:   strings.Split(r.Names, ",")[0],
			State:  r.State,
			Status: r.Status,
			Image:  r.Image,
			Ports:  parseDockerPortString(r.Ports),
		})
	}
	return out
}

// parseDockerPortString parses docker ps's flattened port string. Docker emits
// entries separated by ", "; each entry is one of:
//
//	"<bind>:<host>-><container>/<proto>"   (published, IPv4 or [IPv6])
//	"<host>-><container>/<proto>"          (published, no bind)
//	"<container>/<proto>"                  (unpublished — skipped)
func parseDockerPortString(s string) []PortMapping {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []PortMapping
	for item := range strings.SplitSeq(s, ",") {
		item = strings.TrimSpace(item)
		before, after, ok := strings.Cut(item, "->")
		if !ok {
			continue // unpublished
		}
		left, right := before, after
		var bindIP string
		var hostPortStr string
		// Handle "[v6]:port" and "v4:port" and bare "port"
		if strings.HasPrefix(left, "[") {
			if i := strings.Index(left, "]:"); i > 0 {
				bindIP = left[1:i]
				hostPortStr = left[i+2:]
			}
		} else if i := strings.LastIndex(left, ":"); i >= 0 {
			bindIP = left[:i]
			hostPortStr = left[i+1:]
		} else {
			hostPortStr = left
		}
		hostPort, err := strconv.Atoi(strings.TrimSpace(hostPortStr))
		if err != nil || hostPort <= 0 {
			continue
		}
		proto := "tcp"
		ctrStr := right
		if before, after, ok := strings.Cut(right, "/"); ok {
			proto = strings.TrimSpace(after)
			ctrStr = before
		}
		ctrPort, err := strconv.Atoi(strings.TrimSpace(ctrStr))
		if err != nil || ctrPort <= 0 {
			continue
		}
		out = append(out, PortMapping{
			HostIP:   bindIP,
			HostPort: hostPort,
			CtrPort:  ctrPort,
			Proto:    proto,
		})
	}
	return out
}

// --- Internal: inspect parsing ---

type engineInspectRow struct {
	Name        string
	NetworkMode string
	Devices     []string
	Mounts      []MountInfo
}

func (e *EngineClient) runInspect(names []string) ([]engineInspectRow, error) {
	if len(names) == 0 {
		return nil, nil
	}
	args := append([]string{"inspect"}, names...)
	out, err := exec.Command(e.bin, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("inspecting containers: %w", err)
	}
	return parseInspect(out)
}

// parseInspect decodes the array of inspect blobs both engines emit by default.
// We pull only the fields we need and tolerate the docker/podman casing
// differences by indexing into a generic map.
func parseInspect(data []byte) ([]engineInspectRow, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}
	var raws []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raws); err != nil {
		return nil, fmt.Errorf("parsing inspect JSON: %w", err)
	}
	out := make([]engineInspectRow, 0, len(raws))
	for _, r := range raws {
		row := engineInspectRow{Name: stringAt(r, "Name")}
		hc, _ := r["HostConfig"].(map[string]any)
		if hc != nil {
			row.NetworkMode = stringAt(hc, "NetworkMode")
			row.Devices = devicesFromHostConfig(hc)
		}
		row.Mounts = mountsFromInspect(r)
		// CDI / GPU detection — both engines surface this slightly
		// differently; the union covers podman (CDI in HostConfig.Devices /
		// Annotations / nvidia.com/gpu request) and docker (--gpus →
		// HostConfig.DeviceRequests). One scan over the raw map catches all.
		if hasGPU(r) {
			row.Devices = append([]string{"nvidia.com/gpu=all"}, row.Devices...)
		}
		out = append(out, row)
	}
	return out, nil
}

// mountsFromInspect pulls the .Mounts[] array out of a raw inspect blob.
// Both podman and docker emit the same shape: an array of objects with
// Type / Source / Destination / Name keys (Name is empty for type=bind).
// This is the LIVE mount data — what the container is actually bound to,
// independent of the OCI label default volume layout.
func mountsFromInspect(raw map[string]any) []MountInfo {
	mountsAny, ok := raw["Mounts"].([]any)
	if !ok {
		return nil
	}
	out := make([]MountInfo, 0, len(mountsAny))
	for _, m := range mountsAny {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, MountInfo{
			Type:        stringAt(mm, "Type"),
			Source:      stringAt(mm, "Source"),
			Destination: stringAt(mm, "Destination"),
			Name:        stringAt(mm, "Name"),
		})
	}
	return out
}

func stringAt(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// devicesFromHostConfig pulls Devices[].PathOnHost out of a HostConfig blob.
func devicesFromHostConfig(hc map[string]any) []string {
	devs, ok := hc["Devices"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, d := range devs {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if p := stringAt(dm, "PathOnHost"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// hasGPU returns true when any nvidia.com/gpu / Gpus / DeviceRequests marker
// is present in the inspect blob. Cheap string scan over the JSON is enough —
// false positives are harmless (they just add a "gpu" device token).
func hasGPU(raw map[string]any) bool {
	b, _ := json.Marshal(raw)
	s := string(b)
	return strings.Contains(s, "nvidia.com/gpu") ||
		strings.Contains(s, `"Gpus"`) ||
		strings.Contains(s, `"DeviceRequests"`) && strings.Contains(s, "nvidia")
}
