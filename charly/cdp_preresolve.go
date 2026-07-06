package main

import "github.com/opencharly/sdk/kit"

// cdp_preresolve.go is the HOST-side residue of the externalized `cdp` verb. The
// out-of-process candy/plugin-cdp provider speaks the Chrome DevTools Protocol on the
// wire (HTTP /json + the CDP WebSocket) but owns NONE of charly's venue / port-mapping
// machinery — so the host does the deployment → venue → host-reachable-DevTools-port
// resolution (cdpDevTools → resolveCheckEndpoint, opening any ssh -L forward for a
// VM/ssh venue) and hands the plugin a plain DIALABLE DevTools base URL via the
// CheckEnv. This is the cdp analogue of preresolveMcpEndpoint (mcp_preresolve.go) and
// preresolveSpiceEndpoint (spice_preresolve.go): the plugin cannot reach core's podman
// engine / project loader, so the host pre-resolves before marshaling.
//
// charly's core no longer carries ANY CDP WebSocket client: the live-container `wl` verb
// (the LAST in-core `--from-cdp` consumer, whose viewport→desktop coordinate translation
// opened a CDP WebSocket to read window.screenX/screenY) externalized into candy/plugin-wl,
// dropping that CLI-only translation entirely — so the core's minimal CDP WebSocket client
// (golang.org/x/net/websocket) was deleted and x/net/websocket left charly's core. Only these
// endpoint-resolution helpers remain host-side (no WebSocket, just HTTP-port resolution).

// CdpEnv is the host-resolved, DIALABLE Chrome DevTools endpoint shipped to the
// out-of-process candy/plugin-cdp provider via CheckEnv.Substrate. URL is the host-reachable
// DevTools base URL ("http://127.0.0.1:NNNN") that maps to the in-venue CDP port 9222 —
// a container published-port mapping, or a forwarded local address for a VM/ssh venue.
// The plugin just dials it (the /json HTTP surface + the per-tab CDP WebSocket); it
// needs no podman, no venue resolution, no OCI labels.
type CdpEnv struct {
	URL string `json:"url"` // host-reachable DevTools base URL, e.g. "http://127.0.0.1:39001"
}

// preresolveCdpEndpoint resolves a `cdp:` op's target deployment (r.Box) to the dialable
// DevTools base URL host-side. Returns:
//   - env:     the resolved endpoint (nil for a non-cdp op or a box-mode / no-box run —
//     the plugin's own no-endpoint skip then fires);
//   - cleanup: closes any opened ssh -L forward (ALWAYS non-nil — defer it
//     unconditionally); it must outlive the plugin's Invoke (the forward carries the
//     live CDP HTTP/WebSocket connection), so invokeVerbProvider defers it across Invoke;
//   - early:   a pre-dispatch CheckResult to return immediately (a FAIL when the
//     DevTools endpoint cannot be resolved — e.g. CDP port 9222 not published); nil to
//     proceed to dispatch.
//
// Mirrors preresolveMcpEndpoint's early-FAIL-on-resolution-error semantics; like
// preresolveSpiceEndpoint it also returns a cleanup because cdpDevTools opens a
// CheckEndpoint (an ssh -L forward for a VM/ssh venue) the host must release.
func (r *Runner) preresolveCdpEndpoint(c *Op) (env *CdpEnv, cleanup func(), early *CheckResult) {
	noop := func() {}
	// Non-cdp op, or no live container context (box-mode / empty box) → nothing to
	// resolve; the plugin's own box-mode / no-endpoint skip handles the degenerate cases.
	if c.Plugin != "cdp" || r.Mode == RunModeBox || r.Box == "" {
		return nil, noop, nil
	}
	devtoolsURL, ep, err := cdpDevTools(r.Box, r.Instance)
	if err != nil {
		res := failf(c, "cdp: %s: %v", kit.InputStr(c, "method"), err)
		return nil, noop, &res
	}
	return &CdpEnv{URL: devtoolsURL}, ep.Close, nil
}

// ---------------------------------------------------------------------------
// Host-side CDP endpoint-resolution helper retained for the cdp endpoint
// pre-resolution above. The DECLARATIVE cdp methods (open/list/text/eval/screenshot/
// click/…) and ALL CDP WebSocket dialing moved to the out-of-process candy/plugin-cdp;
// only this venue→host-reachable-DevTools-port resolution stays host-side.
// ---------------------------------------------------------------------------

// devToolsTab represents a Chrome DevTools Protocol tab entry. Retained because the
// status-probe path (status_probes.go) decodes /json into it.
type devToolsTab struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// cdpDevTools resolves the venue (container / VM / ssh / local) and a host-reachable
// DevTools base URL for the in-venue CDP port 9222 — a container port mapping, or an
// ssh -L forward for VM/ssh venues. The caller MUST close the returned endpoint when done.
func cdpDevTools(box, instance string) (devtoolsURL string, ep *CheckEndpoint, err error) {
	venue, err := resolveCheckVenue(box, instance)
	if err != nil {
		return "", nil, err
	}
	ep, err = resolveCheckEndpoint(venue, 9222)
	if err != nil {
		return "", nil, err
	}
	return "http://" + ep.Addr, ep, nil
}
