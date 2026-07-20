package status

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/opencharly/sdk/spec"
)

// render.go — moved VERBATIM from charly/status_render.go (P14a chunk 2b), minus
// formatTunnelSummary — a COLLECTION helper (DeploymentStatus.Tunnel is already a plain string by
// the time it reaches this file) that now lives in candy/plugin-substrate's status_flat.go (K6,
// the former charly/status_collector.go). Every cell helper here reads only
// spec.DeploymentStatus / spec.PortMapping / spec.ToolStatus
// fields — no core type crosses the boundary.

// RenderTable writes the multi-row aligned table.
//
// Columns: KIND  IMAGE  STATUS  PORTS  TUNNEL  DEVICES  TOOLS
// KIND names the substrate (pod / vm / k8s / local / android). IMAGE merges
// image + instance ("image/instance") so a multi-instance deployment is
// visually distinct. Nested children render as indented IMAGE-cell rows.
func RenderTable(w io.Writer, ss []spec.DeploymentStatus) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KIND\tIMAGE\tSTATUS\tPORTS\tTUNNEL\tDEVICES\tTOOLS")
	for _, s := range ss {
		renderTableRow(tw, s, "")
	}
	return tw.Flush()
}

// renderTableRow writes one row (with an optional IMAGE-cell prefix for nested
// indentation) and recurses into its nested children.
func renderTableRow(tw io.Writer, s spec.DeploymentStatus, prefix string) {
	_, _ = fmt.Fprintf(tw, "%s\t%s%s\t%s\t%s\t%s\t%s\t%s\n",
		cellKind(s.Kind),
		prefix, cellBox(s),
		s.Status,
		cellPorts(s.Ports),
		cellTunnel(s.Tunnel),
		cellDevices(s.Devices),
		cellTools(s.Tools),
	)
	for _, child := range s.Nested {
		renderTableRow(tw, *child, prefix+"  └─ ")
	}
}

// RenderDetail writes the single-image detail view (key: value).
func RenderDetail(w io.Writer, s spec.DeploymentStatus) error {
	if s.Kind != "" {
		_, _ = fmt.Fprintf(w, "Kind:      %s\n", s.Kind)
	}
	_, _ = fmt.Fprintf(w, "Image:     %s\n", s.Image)
	if s.ImageRef != "" {
		_, _ = fmt.Fprintf(w, "Image ref: %s\n", s.ImageRef)
	}
	if s.Instance != "" {
		_, _ = fmt.Fprintf(w, "Instance:  %s\n", s.Instance)
	}
	if s.Uptime != "" {
		_, _ = fmt.Fprintf(w, "Status:    %s (%s)\n", s.Status, s.Uptime)
	} else {
		_, _ = fmt.Fprintf(w, "Status:    %s\n", s.Status)
	}
	_, _ = fmt.Fprintf(w, "Container: %s\n", s.Container)
	if len(s.Secrets) > 0 {
		_, _ = fmt.Fprintf(w, "Secrets:   %s\n", strings.Join(s.Secrets, ", "))
	}
	_, _ = fmt.Fprintf(w, "Mode:      %s\n", s.RunMode)
	if len(s.Ports) > 0 {
		_, _ = fmt.Fprintf(w, "Ports:     %s\n", longPorts(s.Ports))
	}
	if len(s.Devices) > 0 {
		_, _ = fmt.Fprintf(w, "Devices:   %s\n", strings.Join(s.Devices, ", "))
	}
	if td := cellToolsDetail(s.Tools); td != "" {
		_, _ = fmt.Fprintf(w, "Tools:     %s\n", td)
	}
	for i, v := range s.Volumes {
		if i == 0 {
			_, _ = fmt.Fprintf(w, "Volumes:   %s\n", v)
		} else {
			_, _ = fmt.Fprintf(w, "           %s\n", v)
		}
	}
	if s.Network != "" {
		_, _ = fmt.Fprintf(w, "Network:   %s\n", s.Network)
	}
	if s.Tunnel != "" {
		_, _ = fmt.Fprintf(w, "Tunnel:    %s\n", s.Tunnel)
	}
	for i, child := range s.Nested {
		label := "Nested:"
		if i > 0 {
			label = "       "
		}
		_, _ = fmt.Fprintf(w, "%-10s %s %s (%s)\n", label, cellKind(child.Kind), cellBox(*child), child.Status)
	}
	return nil
}

// RenderJSON writes the structured output. For the multi-image flow this is
// an array of DeploymentStatus; for the single-image flow callers should pass
// a one-element slice and the caller decides whether to unwrap. The kind and
// nested fields are part of the encoded shape.
func RenderJSON(w io.Writer, ss []spec.DeploymentStatus) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ss)
}

// RenderJSONOne writes one deployment's status as a single object (matches
// the single-image detail JSON shape).
func RenderJSONOne(w io.Writer, s spec.DeploymentStatus) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// --- Cell formatters ---

// cellKind returns the substrate token for the KIND column, or "-" when unset.
func cellKind(k spec.SubstrateKind) string {
	if k == "" {
		return "-"
	}
	return string(k)
}

// cellBox returns "image" or "image/instance". The slash-separated form
// matches deploykit.DeployKey(): both charly.yml and `charly ... -i <inst>` use it, so the
// table label aligns with the operator's mental model.
func cellBox(s spec.DeploymentStatus) string {
	if s.Instance == "" {
		return s.Image
	}
	return s.Image + "/" + s.Instance
}

// cellPorts returns a sorted, comma-joined list of host ports, or "-".
func cellPorts(p []spec.PortMapping) string {
	if len(p) == 0 {
		return "-"
	}
	seen := map[int]bool{}
	var nums []int
	for _, m := range p {
		if m.HostPort > 0 && !seen[m.HostPort] {
			seen[m.HostPort] = true
			nums = append(nums, m.HostPort)
		}
	}
	if len(nums) == 0 {
		return "-"
	}
	sort.Ints(nums)
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ",")
}

// cellDevices collapses raw device paths to compact tokens (gpu/dri/...).
func cellDevices(devices []string) string {
	if len(devices) == 0 {
		return "-"
	}
	seen := map[string]bool{}
	var tokens []string
	for _, d := range devices {
		var token string
		switch {
		case strings.Contains(d, "nvidia.com/gpu"):
			token = "gpu"
		case strings.Contains(d, "/dev/nvidia") || strings.Contains(d, "nvidia"):
			token = "gpu"
		case strings.Contains(d, "/dev/kfd"):
			token = "kfd"
		case strings.Contains(d, "/dev/dri"):
			token = "dri"
		case strings.Contains(d, "/dev/kvm"):
			token = "kvm"
		case strings.Contains(d, "/dev/fuse"):
			token = "fuse"
		case strings.Contains(d, "/dev/net/tun"):
			token = "tun"
		default:
			token = filepath.Base(d)
		}
		if !seen[token] {
			seen[token] = true
			tokens = append(tokens, token)
		}
	}
	sort.Strings(tokens)
	return strings.Join(tokens, ",")
}

// cellTools renders the compact TOOLS column entries: "name" for socket-based
// probes, "name:port" for port-based probes. Status="-" entries are filtered.
func cellTools(tools []spec.ToolStatus) string {
	sorted := sortTools(tools)
	var parts []string
	for _, t := range sorted {
		if t.Status == "-" {
			continue
		}
		if t.Port > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", t.Name, t.Port))
		} else {
			parts = append(parts, t.Name)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// cellToolsDetail is the verbose form used in the single-image detail view.
func cellToolsDetail(tools []spec.ToolStatus) string {
	sorted := sortTools(tools)
	var parts []string
	for _, t := range sorted {
		if t.Status == "-" {
			continue
		}
		if t.Port > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d (%s)", t.Name, t.Port, t.Status))
		} else {
			parts = append(parts, fmt.Sprintf("%s (%s)", t.Name, t.Status))
		}
	}
	return strings.Join(parts, ", ")
}

// cellTunnel returns the tunnel summary or "-" placeholder for the table.
func cellTunnel(t string) string {
	if t == "" {
		return "-"
	}
	return t
}

// longPorts renders structured PortMappings as "H:C/proto, ..." for the
// detail view (where the operator wants the full host-to-container picture).
func longPorts(p []spec.PortMapping) string {
	if len(p) == 0 {
		return ""
	}
	parts := make([]string, 0, len(p))
	for _, m := range p {
		s := fmt.Sprintf("%d:%d", m.HostPort, m.CtrPort)
		if m.Proto != "" {
			s += "/" + m.Proto
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// sortTools returns tools alphabetically by name. Stable enough for the
// renderer; not allocating a copy when the slice is empty.
func sortTools(tools []spec.ToolStatus) []spec.ToolStatus {
	if len(tools) == 0 {
		return nil
	}
	out := make([]spec.ToolStatus, len(tools))
	copy(out, tools)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
