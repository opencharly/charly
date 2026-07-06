package main

// cdp_preresolve.go — the last host-side residue of the `cdp` verb. Since the endpoint-seam
// cutover (H part 2) the out-of-process candy/plugin-cdp resolves its own dialable DevTools
// base URL via the GENERIC host-endpoint reverse-leg (CheckContextService.ResolveEndpoint →
// resolveVerbEndpoint), so the former host-side cdp endpoint preresolver is gone. Only the
// DevTools /json decode struct — which the host's own status-probe path (status_probes.go)
// still uses — remains here.

// devToolsTab represents a Chrome DevTools Protocol tab entry. Retained because the
// status-probe path (status_probes.go) decodes /json into it.
type devToolsTab struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}
