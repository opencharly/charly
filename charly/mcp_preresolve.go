package main

import (
	"fmt"
	"github.com/opencharly/sdk/kit"
	"net/url"
	"strings"
)

// mcp_preresolve.go is the HOST-side half of the externalized `mcp` verb. The
// out-of-process candy/plugin-mcp provider speaks the Model Context Protocol on
// the wire but owns NONE of charly's container-inspection / OCI-label / port-mapping
// machinery — so the host does the deployment → container → image-metadata →
// mcp_provides resolution (resolveContainer + ExtractMetadata + the helpers below)
// and the container-network → host-routable URL rewrite (InspectContainer +
// rewriteMCPURLForHost), handing the plugin a plain DIALABLE endpoint (plus the
// metadata-only entry list the `servers` method needs) via the CheckEnv. This is the
// mcp analogue of preresolveSpiceEndpoint (spice_preresolve.go) and
// preresolveKubeCluster (k8s_config.go): the out-of-process plugin cannot reach
// core's podman engine / project loader, so the host pre-resolves before marshaling.

// McpEnv is the host-resolved MCP check context shipped to the out-of-process
// candy/plugin-mcp provider via CheckEnv.Substrate. It carries BOTH halves the verb needs:
//
//   - Entries: every resolved mcp_provides entry, for the metadata-only `servers`
//     method (it lists declared servers and never dials).
//   - URL / Transport / Name: the single picked, host-routable dial endpoint for
//     every other method (ping / list-* / call / read), which connects over the wire.
//
// nil for every non-mcp verb. The plugin just reads it — no podman, no OCI labels.
type McpEnv struct {
	Entries   []MCPProvideEntry `json:"entries,omitempty"` // all declared servers (the `servers` method)
	URL       string            `json:"url,omitempty"`     // the picked, host-routable dial URL
	Transport string            `json:"transport,omitempty"`
	Name      string            `json:"name,omitempty"`
}

// preresolveMcpEndpoint resolves a `mcp:` op's target deployment (r.Box) to the
// dialable MCP endpoint + the declared-server list host-side. Returns:
//   - env:   the resolved context (nil for a non-mcp op or a box-mode / no-box run —
//     the plugin's own no-endpoint skip then fires);
//   - early: a pre-dispatch CheckResult to return immediately (a FAIL when resolution
//     errors); nil to proceed to dispatch.
//
// For `mcp: servers` only the Entries are needed (no dial); every other method also
// fills the picked URL/Transport/Name. Mirrors preresolveSpiceEndpoint, minus the
// SSH tunnel (mcp has no remote-hypervisor side channel — the rewrite to a published
// host port is the whole host-routability story).
func (r *Runner) preresolveMcpEndpoint(c *Op) (env *McpEnv, early *CheckResult) {
	// Non-mcp op, or no live container context (box-mode / empty box) → nothing to
	// resolve; the plugin's own box-mode / no-endpoint skip handles the degenerate cases.
	if c.Plugin != "mcp" || r.Mode == RunModeBox || r.Box == "" {
		return nil, nil
	}
	method := kit.InputStr(c, "method")

	engine, containerName, err := resolveContainer(r.Box, r.Instance)
	if err != nil {
		res := failf(c, "mcp: %s: %v", method, err)
		return nil, &res
	}
	imageRef, err := containerImageRef(engine, containerName)
	if err != nil {
		res := failf(c, "mcp: %s: %v", method, err)
		return nil, &res
	}
	meta, err := ExtractMetadata(engine, imageRef)
	if err != nil {
		res := failf(c, "mcp: %s: %v", method, err)
		return nil, &res
	}
	if meta == nil || len(meta.MCPProvide) == 0 {
		res := failf(c, "mcp: %s: box %q declares no mcp_provides", method, r.Box)
		return nil, &res
	}

	// Build the full declared-server list (the metadata the `servers` method emits).
	entries := make([]MCPProvideEntry, 0, len(meta.MCPProvide))
	for _, p := range meta.MCPProvide {
		entries = append(entries, MCPProvideEntry{
			Name:      p.Name,
			URL:       resolveContainerNameTemplate(p.URL, containerName),
			Transport: p.Transport,
			Source:    r.Box,
		})
	}
	entries = podAwareMCPProvides(entries, r.Box, containerName)
	env = &McpEnv{Entries: entries}

	// `servers` is metadata-only — no dial, so no inspect / URL rewrite needed.
	if method == "servers" {
		return env, nil
	}

	// Every other method dials a single picked server: resolve it, inspect the
	// container, and rewrite its container-network URL to a host-routable one.
	entry, err := resolveMCPEntry(meta, r.Box, containerName, kit.InputStr(c, "mcp_name"))
	if err != nil {
		res := failf(c, "mcp: %s: %v", method, err)
		return nil, &res
	}
	inspection, err := InspectContainer(engine, containerName)
	if err != nil {
		res := failf(c, "mcp: %s: inspecting container %s: %v", method, containerName, err)
		return nil, &res
	}
	rewritten, err := rewriteMCPURLForHost(entry.URL, containerName, inspection)
	if err != nil {
		res := failf(c, "mcp: %s: %v", method, err)
		return nil, &res
	}
	env.URL = rewritten
	env.Transport = entry.Transport
	env.Name = entry.Name
	return env, nil
}

// ---------------------------------------------------------------------------
// Host-side MCP resolution helpers (relocated from the deleted mcp_client.go).
// They stay host-side because each needs charly's image metadata / container
// inspection / port-mapping data an out-of-process plugin cannot reach.
// ---------------------------------------------------------------------------

// resolveMCPEntry picks the target MCPProvideEntry for a given image.
//
// Inputs:
//   - meta:     image metadata from ExtractMetadata (provides meta.MCPProvide)
//   - image:    logical image name (for pod-aware self-match)
//   - ctrName:  running container name (for template substitution)
//   - wantName: optional discriminator when the image provides multiple servers
//
// Returns a single MCPProvideEntry with template fields resolved (no
// {{.ContainerName}} left) and with same-image URLs rewritten to localhost
// (pod-aware path). Matches the pattern used by injectMCPProvides at
// charly config time.
func resolveMCPEntry(meta *BoxMetadata, image, ctrName, wantName string) (MCPProvideEntry, error) {
	if meta == nil || len(meta.MCPProvide) == 0 {
		return MCPProvideEntry{}, fmt.Errorf("box %q declares no mcp_provides", image)
	}

	entries := make([]MCPProvideEntry, 0, len(meta.MCPProvide))
	for _, p := range meta.MCPProvide {
		entries = append(entries, MCPProvideEntry{
			Name:      p.Name,
			URL:       resolveContainerNameTemplate(p.URL, ctrName),
			Transport: p.Transport,
			Source:    image,
		})
	}

	resolved := podAwareMCPProvides(entries, image, ctrName)
	return pickMCPEntry(resolved, wantName)
}

// pickMCPEntry disambiguates by name. Empty wantName auto-picks when there
// is exactly one entry; errors when there are multiple with a clear listing.
func pickMCPEntry(entries []MCPProvideEntry, wantName string) (MCPProvideEntry, error) {
	switch {
	case len(entries) == 0:
		return MCPProvideEntry{}, fmt.Errorf("no mcp_provides entries to pick from")
	case wantName != "":
		for _, e := range entries {
			if e.Name == wantName {
				return e, nil
			}
		}
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		return MCPProvideEntry{}, fmt.Errorf("no mcp_provides entry named %q (available: %s)", wantName, strings.Join(names, ", "))
	case len(entries) == 1:
		return entries[0], nil
	default:
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		return MCPProvideEntry{}, fmt.Errorf("image provides multiple mcp servers; use mcp_name (available: %s)", strings.Join(names, ", "))
	}
}

// resolveContainerNameTemplate substitutes the {{.ContainerName}} template
// token with the running container name. No full Go-template engine needed —
// the only placeholder charly emits into mcp_provides URLs is {{.ContainerName}}.
func resolveContainerNameTemplate(raw, ctrName string) string {
	if ctrName == "" {
		return raw
	}
	return strings.ReplaceAll(raw, "{{.ContainerName}}", ctrName)
}

// rewriteMCPURLForHost rewrites a container-network URL to a host-routable
// one using the published port-mapping from a container inspection. If the
// URL's host is not the container name, it is returned unchanged — the user
// may have set an explicit external URL.
func rewriteMCPURLForHost(rawURL, ctrName string, inspect *ContainerInspection) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("mcp URL is empty")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parsing mcp URL %q: %w", rawURL, err)
	}
	// Host alias forms we accept: the bare container name, or an already-
	// rewritten "localhost" (pod-aware). Leave anything else alone.
	host := u.Hostname()
	if host != ctrName && host != "localhost" && host != "127.0.0.1" {
		return rawURL, nil
	}
	if u.Port() == "" {
		return "", fmt.Errorf("mcp URL %q has no port (cannot map to a host port)", rawURL)
	}
	if inspect == nil {
		return "", fmt.Errorf("no container inspection; cannot resolve host port for %q", rawURL)
	}
	hostPort, ok := lookupHostPort(inspect, u.Port())
	if !ok {
		return "", fmt.Errorf("container port %s/tcp is not published to a host port; declare `ports: [%s:%s]` in the image or run the test from inside the pod", u.Port(), u.Port(), u.Port())
	}
	u.Host = "127.0.0.1:" + hostPort
	return u.String(), nil
}

// lookupHostPort reads the first host-side port binding for a given container
// port. Mirrors the logic in mergeRuntimeVars (testvars.go:200-213).
// Host-networked containers have an empty NetworkSettings.Ports — but every
// container port IS a host port, so just return it verbatim in that case.
func lookupHostPort(inspect *ContainerInspection, containerPort string) (string, bool) {
	if inspect == nil {
		return "", false
	}
	if inspect.IsHostNetworked() {
		return containerPort, true
	}
	for key, binds := range inspect.NetworkSettings.Ports {
		portStr := key
		if before, _, ok := strings.Cut(key, "/"); ok {
			portStr = before
		}
		if portStr != containerPort {
			continue
		}
		if len(binds) == 0 {
			continue
		}
		if hp := binds[0].HostPort; hp != "" {
			return hp, true
		}
	}
	return "", false
}
