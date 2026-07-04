package cdp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// methods.go is the cdp method dispatcher + the CDP-protocol client layer, moved from
// charly/cdp.go. Every method was refactored from a CLI Run() that PRINTED to stdout/
// stderr into a function that RETURNS the captured stdout string — so provider.go can
// feed the output through the shared sdk matcher pipeline + sdk.RunArtifactValidators
// (a host-side matcher step does not run for an out-of-process verb). The
// DevTools HTTP surface (/json), the per-tab CDP WebSocket dispatch, and the deep-query
// shadow-DOM helper are unchanged, so a bed authored against the in-tree verb passes
// unchanged. The endpoint is host-pre-resolved (charly/cdp_preresolve.go) — the plugin
// needs no podman / venue resolution at all.
//
// Three CLI-only extras did NOT move: `cdp click --vnc`/`--wl` (cross-verb click delivery
// via VNC/wlrctl — the declarative `cdp: click` keeps its Input.dispatchMouseEvent path),
// the `coords` method's best-effort sway-window-offset line (its CDP/viewport output
// stays), and diagnoseCDP (the host ss/supervisorctl error helper).

const defaultCdpWaitTimeout = 30 * time.Second

// deepQueryJS recursively searches through shadow DOM boundaries to find an element
// matching a CSS selector (document.querySelector only searches the light DOM).
const deepQueryJS = `function deepQuery(sel, root) {
  root = root || document;
  var matches = root.querySelectorAll(sel);
  for (var m = 0; m < matches.length; m++) {
    var r = matches[m].getBoundingClientRect();
    if (r.width > 0 && r.height > 0) return matches[m];
  }
  var all = root.querySelectorAll('*');
  for (var i = 0; i < all.length; i++) {
    if (all[i].shadowRoot) {
      var el = deepQuery(sel, all[i].shadowRoot);
      if (el) return el;
    }
  }
  return null;
}`

// devToolsTab represents a Chrome DevTools Protocol tab entry.
type devToolsTab struct {
	ID                   string `json:"id"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// requiredModifiers mirrors the in-tree cdpMethods required-field specs (the host's
// validate-time + runtime required-modifier check keyed off the former in-proc live-verb seam,
// which an external verb is not — so the check moves HERE, at dispatch).
var requiredModifiers = map[string][]string{
	"url":           {"tab"},
	"text":          {"tab"},
	"html":          {"tab"},
	"eval":          {"tab", "expression"},
	"axtree":        {"tab"},
	"coords":        {"tab", "selector"},
	"raw":           {"tab", "method"},
	"wait":          {"tab", "selector"},
	"screenshot":    {"tab", "artifact"},
	"open":          {"url"},
	"close":         {"tab"},
	"click":         {"tab", "selector"},
	"type":          {"tab", "selector", "text"},
	"spa-status":    {"tab"},
	"spa-click":     {"tab", "x", "y"},
	"spa-type":      {"tab", "text"},
	"spa-key":       {"tab", "key"},
	"spa-key-combo": {"tab", "combo"},
	"spa-mouse":     {"tab", "x", "y"},
}

// dispatch runs one cdp method against the host-resolved DevTools endpoint and returns
// its captured output. A returned error is the verb FAILING (the in-tree CLI Run()
// returning an error → exit 1); provider.go maps it through the exit_status / stderr
// matchers. The HTTP methods (status/open/list/close) hit the /json surface directly;
// every other method opens a per-tab CDP WebSocket.
func dispatch(ep *cdpEndpoint, op *spec.Op) (string, error) {
	method := string(op.Cdp)
	if err := sdk.RequireModifiers(method, op, requiredModifiers); err != nil {
		return "", err
	}

	switch method {
	case "status":
		return runStatus(ep)
	case "open":
		return runOpen(ep, op.URL)
	case "list":
		return runList(ep)
	case "close":
		return runClose(ep, op.Tab)
	}

	// WebSocket methods: connect the tab.
	client, err := connectTab(ep.URL, op.Tab)
	if err != nil {
		return "", err
	}
	defer client.Close()

	switch method {
	case "text":
		return runText(client)
	case "html":
		return runHTML(client)
	case "url":
		return runURL(client)
	case "eval":
		return runEval(client, op.Expression)
	case "axtree":
		return runAxtree(client, op.Query)
	case "coords":
		return runCoords(client, op.Selector)
	case "raw":
		return runRaw(client, op.Method, op.Params)
	case "wait":
		return runWait(client, op.Selector, cdpTimeout(op))
	case "screenshot":
		return runScreenshot(client, op.Artifact)
	case "click":
		return runClick(client, op.Selector)
	case "type":
		return runType(client, op.Selector, op.Text)
	case "spa-status":
		return runSpaStatus(client)
	case "spa-click":
		return runSpaClick(client, op.X, op.Y, op.Button)
	case "spa-mouse":
		return runSpaMouse(client, op.X, op.Y)
	case "spa-type":
		return runSpaType(client, op.Text)
	case "spa-key":
		return runSpaKey(client, op.KeyName)
	case "spa-key-combo":
		return runSpaKeyCombo(client, op.Combo)
	}
	return "", fmt.Errorf("unknown cdp method %q", method)
}

// cdpTimeout reads the authored `timeout:` (default 30s) for the wait method.
func cdpTimeout(op *spec.Op) time.Duration {
	if s := string(op.Timeout); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	return defaultCdpWaitTimeout
}

// ---------------------------------------------------------------------------
// HTTP (/json) methods
// ---------------------------------------------------------------------------

// runStatus probes /json/version and reports reachability. Like the in-tree status it
// returns no error even when unreachable (exit 0; a `stdout: contains: ok` matcher is the
// liveness assertion).
func runStatus(ep *cdpEndpoint) (string, error) {
	addr := strings.TrimPrefix(ep.URL, "http://")
	httpc := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpc.Get(ep.URL + "/json/version")
	if err != nil {
		return fmt.Sprintf("CDP:       unreachable (%s)\n", addr), nil
	}
	resp.Body.Close()
	return fmt.Sprintf("CDP:       ok (%s)\n", addr), nil
}

// runOpen creates a new tab navigated to the URL via the DevTools HTTP API.
func runOpen(ep *cdpEndpoint, target string) (string, error) {
	encoded := url.QueryEscape(target)
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("PUT", ep.URL+"/json/new?"+encoded, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("opening URL in Chrome: %w", err)
	}
	defer resp.Body.Close()
	var tab devToolsTab
	if err := json.NewDecoder(resp.Body).Decode(&tab); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}
	return fmt.Sprintf("Opened %s (tab %s)", target, tab.ID), nil
}

// runList lists open page tabs as tab-separated "id\ttitle\turl" lines.
func runList(ep *cdpEndpoint) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(ep.URL + "/json")
	if err != nil {
		return "", fmt.Errorf("failed to connect to Chrome DevTools: %w", err)
	}
	defer resp.Body.Close()
	var tabs []devToolsTab
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return "", fmt.Errorf("failed to parse DevTools response: %w", err)
	}
	var b strings.Builder
	for _, tab := range tabs {
		if tab.Type != "page" {
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", tab.ID, truncate(tab.Title, 60, 57), truncate(tab.URL, 80, 77))
	}
	return b.String(), nil
}

// runClose closes a tab by ID.
func runClose(ep *cdpEndpoint, tabID string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(ep.URL + "/json/close/" + tabID)
	if err != nil {
		return "", fmt.Errorf("failed to close tab: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to close tab %s (HTTP %d)", tabID, resp.StatusCode)
	}
	return fmt.Sprintf("Closed tab %s", tabID), nil
}

// ---------------------------------------------------------------------------
// WebSocket (per-tab CDP) methods
// ---------------------------------------------------------------------------

func runText(client *CDPClient) (string, error) {
	text, err := cdpEvaluate(client, `document.body.innerText`)
	if err != nil {
		return "", fmt.Errorf("getting page text: %w", err)
	}
	return text, nil
}

func runHTML(client *CDPClient) (string, error) {
	html, err := cdpEvaluate(client, `document.documentElement.outerHTML`)
	if err != nil {
		return "", fmt.Errorf("getting page HTML: %w", err)
	}
	return html, nil
}

func runURL(client *CDPClient) (string, error) {
	result, err := cdpEvaluate(client, `JSON.stringify({title: document.title, url: location.href})`)
	if err != nil {
		return "", fmt.Errorf("getting page URL: %w", err)
	}
	var info struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal([]byte(result), &info); err != nil {
		return result, nil
	}
	return fmt.Sprintf("Title: %s\nURL:   %s\n", info.Title, info.URL), nil
}

func runEval(client *CDPClient, expression string) (string, error) {
	result, err := cdpEvaluate(client, expression)
	if err != nil {
		return "", fmt.Errorf("evaluating expression: %w", err)
	}
	return result, nil
}

func runAxtree(client *CDPClient, query string) (string, error) {
	result, err := client.Call("Accessibility.getFullAXTree", nil)
	if err != nil {
		return "", fmt.Errorf("getting accessibility tree: %w", err)
	}
	if query == "" {
		var pretty json.RawMessage
		if err := json.Unmarshal(result, &pretty); err == nil {
			out, _ := json.MarshalIndent(pretty, "", "  ")
			return string(out), nil
		}
		return string(result), nil
	}
	var tree struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(result, &tree); err != nil {
		return string(result), nil
	}
	q := strings.ToLower(query)
	var matches []json.RawMessage
	for _, node := range tree.Nodes {
		if strings.Contains(strings.ToLower(string(node)), q) {
			matches = append(matches, node)
		}
	}
	out, err := json.MarshalIndent(matches, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling filtered tree: %w", err)
	}
	return string(out), nil
}

// runCoords reports an element's viewport + CDP-desktop coordinates. The in-tree CLI's
// best-effort sway-window-rect line is DROPPED (CLI-only); the CDP/viewport output stays.
func runCoords(client *CDPClient, selector string) (string, error) {
	js := fmt.Sprintf(`(function() {
		%s
		const el = deepQuery(%s);
		if (!el) return JSON.stringify({error: 'Element not found'});
		el.scrollIntoViewIfNeeded();
		const rect = el.getBoundingClientRect();
		return JSON.stringify({x: rect.x, y: rect.y, cx: rect.x + rect.width/2, cy: rect.y + rect.height/2, w: rect.width, h: rect.height});
	})()`, deepQueryJS, jsQuote(selector))

	result, err := cdpEvaluate(client, js)
	if err != nil {
		return "", fmt.Errorf("finding element: %w", err)
	}
	var rect struct {
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		CX    float64 `json:"cx"`
		CY    float64 `json:"cy"`
		W     float64 `json:"w"`
		H     float64 `json:"h"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal([]byte(result), &rect); err != nil {
		return "", fmt.Errorf("parsing element position: %w", err)
	}
	if rect.Error != "" {
		return "", fmt.Errorf("%s", rect.Error)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Element:  %s (%.0fx%.0f)\n", selector, rect.W, rect.H)
	fmt.Fprintf(&b, "Viewport: x=%.0f y=%.0f  center=(%.0f, %.0f)\n", rect.X, rect.Y, rect.CX, rect.CY)
	if offset, err := cdpGetWindowOffset(client); err == nil {
		desktopX := rect.CX + offset.ScreenX
		desktopY := rect.CY + offset.ScreenY + offset.ChromeHeight
		fmt.Fprintf(&b, "Desktop:  x=%.0f y=%.0f  center=(%.0f, %.0f)  (via window.screenX/screenY, chromeHeight=%.0f)\n",
			rect.X+offset.ScreenX, rect.Y+offset.ScreenY+offset.ChromeHeight, desktopX, desktopY, offset.ChromeHeight)
	}
	return b.String(), nil
}

func runRaw(client *CDPClient, method, paramsJSON string) (string, error) {
	var params any
	if paramsJSON != "" {
		var raw json.RawMessage
		if err := json.Unmarshal([]byte(paramsJSON), &raw); err != nil {
			return "", fmt.Errorf("invalid JSON params: %w", err)
		}
		params = raw
	}
	result, err := client.Call(method, params)
	if err != nil {
		return "", err
	}
	var pretty json.RawMessage
	if err := json.Unmarshal(result, &pretty); err != nil {
		return string(result), nil
	}
	out, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return string(result), nil
	}
	return string(out), nil
}

func runWait(client *CDPClient, selector string, timeout time.Duration) (string, error) {
	js := fmt.Sprintf(`(function() { %s; return deepQuery(%s) !== null; })()`, deepQueryJS, jsQuote(selector))
	deadline := time.Now().Add(timeout)
	for {
		result, err := cdpEvaluate(client, js)
		if err != nil {
			return "", fmt.Errorf("checking for element: %w", err)
		}
		if result == "true" {
			return fmt.Sprintf("Element %s found", selector), nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout waiting for element %s after %s", selector, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func runScreenshot(client *CDPClient, artifact string) (string, error) {
	// Bring the tab to foreground and force a repaint before capture (a background tab
	// returns a blank surface). Errors here are non-fatal.
	_, _ = client.Call("Page.bringToFront", map[string]any{})

	result, err := client.Call("Page.captureScreenshot", map[string]any{"format": "png", "fromSurface": true})
	if err != nil {
		return "", fmt.Errorf("capturing screenshot: %w", err)
	}
	var sr struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(result, &sr); err != nil {
		return "", fmt.Errorf("parsing screenshot result: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(sr.Data)
	if err != nil {
		return "", fmt.Errorf("decoding screenshot data: %w", err)
	}
	if err := os.WriteFile(artifact, data, 0o644); err != nil {
		return "", fmt.Errorf("writing screenshot to %s: %w", artifact, err)
	}
	return fmt.Sprintf("Screenshot saved to %s (%d bytes)", artifact, len(data)), nil
}

// runClick finds an element (piercing shadow DOM), scrolls it into view, and clicks its
// center via Input.dispatchMouseEvent. The in-tree CLI's `--vnc`/`--wl` cross-verb click
// delivery is DROPPED (CLI-only); this keeps the declarative CDP click path.
func runClick(client *CDPClient, selector string) (string, error) {
	js := fmt.Sprintf(`(function() {
		%s
		const el = deepQuery(%s);
		if (!el) return JSON.stringify({error: 'Element not found'});
		el.scrollIntoViewIfNeeded();
		const rect = el.getBoundingClientRect();
		return JSON.stringify({x: rect.x + rect.width/2, y: rect.y + rect.height/2});
	})()`, deepQueryJS, jsQuote(selector))

	result, err := cdpEvaluate(client, js)
	if err != nil {
		return "", fmt.Errorf("finding element: %w", err)
	}
	var coords struct {
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
		Error string  `json:"error"`
	}
	if err := json.Unmarshal([]byte(result), &coords); err != nil {
		return "", fmt.Errorf("parsing element position: %w", err)
	}
	if coords.Error != "" {
		return "", fmt.Errorf("%s", coords.Error)
	}

	mouseParams := map[string]any{"x": coords.X, "y": coords.Y, "button": "left", "clickCount": 1}
	mouseParams["type"] = "mousePressed"
	if _, err := client.Call("Input.dispatchMouseEvent", mouseParams); err != nil {
		return "", fmt.Errorf("dispatching mousePressed: %w", err)
	}
	mouseParams["type"] = "mouseReleased"
	if _, err := client.Call("Input.dispatchMouseEvent", mouseParams); err != nil {
		return "", fmt.Errorf("dispatching mouseReleased: %w", err)
	}

	// Report new page state (best-effort).
	info, err := cdpEvaluate(client, `JSON.stringify({title: document.title, url: location.href})`)
	if err != nil {
		return fmt.Sprintf("Clicked element at (%.0f, %.0f)", coords.X, coords.Y), nil
	}
	var page struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal([]byte(info), &page); err == nil {
		return fmt.Sprintf("Clicked element at (%.0f, %.0f)\nTitle: %s\nURL:   %s\n", coords.X, coords.Y, page.Title, page.URL), nil
	}
	return fmt.Sprintf("Clicked element at (%.0f, %.0f)", coords.X, coords.Y), nil
}

func runType(client *CDPClient, selector, text string) (string, error) {
	js := fmt.Sprintf(`(function() {
		%s
		const el = deepQuery(%s);
		if (!el) return 'Element not found';
		el.scrollIntoViewIfNeeded();
		el.focus();
		el.value = '';
		return 'ok';
	})()`, deepQueryJS, jsQuote(selector))

	result, err := cdpEvaluate(client, js)
	if err != nil {
		return "", fmt.Errorf("focusing element: %w", err)
	}
	if result != "ok" {
		return "", fmt.Errorf("%s", result)
	}

	for _, ch := range text {
		key := string(ch)
		if err := cdpDispatchKeyEvent(client, "keyDown", key); err != nil {
			return "", fmt.Errorf("dispatching keyDown: %w", err)
		}
		if err := cdpDispatchKeyEvent(client, "char", key); err != nil {
			return "", fmt.Errorf("dispatching char: %w", err)
		}
		if err := cdpDispatchKeyEvent(client, "keyUp", key); err != nil {
			return "", fmt.Errorf("dispatching keyUp: %w", err)
		}
	}
	return fmt.Sprintf("Typed text into %s", selector), nil
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// connectTab resolves the WebSocket URL for a tab and opens a CDPClient.
func connectTab(devtoolsURL, tabID string) (*CDPClient, error) {
	wsURL, err := resolveTabWS(devtoolsURL, tabID)
	if err != nil {
		return nil, err
	}
	return NewCDPClient(wsURL)
}

// resolveTabWS fetches /json and returns the WebSocket debugger URL for the tab. A
// numeric tabID is a 1-based index into the type:page tabs; a non-numeric tabID falls
// through to a UUID match.
func resolveTabWS(devtoolsURL, tabID string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(devtoolsURL + "/json")
	if err != nil {
		return "", fmt.Errorf("fetching tab list: %w", err)
	}
	defer resp.Body.Close()

	var tabs []devToolsTab
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return "", fmt.Errorf("parsing tab list: %w", err)
	}

	if idx, err := strconv.Atoi(tabID); err == nil && idx >= 1 {
		var pages []devToolsTab
		for _, tab := range tabs {
			if tab.Type == "page" {
				pages = append(pages, tab)
			}
		}
		if idx <= len(pages) {
			tab := pages[idx-1]
			if tab.WebSocketDebuggerURL == "" {
				return "", fmt.Errorf("tab #%d has no WebSocket debugger URL", idx)
			}
			return tab.WebSocketDebuggerURL, nil
		}
	}

	for _, tab := range tabs {
		if tab.ID == tabID {
			if tab.WebSocketDebuggerURL == "" {
				return "", fmt.Errorf("tab %s has no WebSocket debugger URL", tabID)
			}
			return tab.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("tab %s not found", tabID)
}

// cdpEvaluate calls Runtime.evaluate and returns the result value as a string.
func cdpEvaluate(client *CDPClient, expression string) (string, error) {
	result, err := client.Call("Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}

	var checkResult struct {
		Result struct {
			Type        string          `json:"type"`
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &checkResult); err != nil {
		return "", fmt.Errorf("parsing evaluate result: %w", err)
	}
	if checkResult.ExceptionDetails != nil {
		return "", fmt.Errorf("JavaScript exception: %s", checkResult.ExceptionDetails.Text)
	}

	if checkResult.Result.Type == "string" {
		var s string
		if err := json.Unmarshal(checkResult.Result.Value, &s); err != nil {
			return string(checkResult.Result.Value), nil
		}
		return s, nil
	}
	if checkResult.Result.Type == "undefined" || checkResult.Result.Value == nil {
		if checkResult.Result.Description != "" {
			return checkResult.Result.Description, nil
		}
		return "", nil
	}
	return string(checkResult.Result.Value), nil
}

// cdpDispatchKeyEvent sends a single CDP Input.dispatchKeyEvent.
func cdpDispatchKeyEvent(client *CDPClient, eventType, key string) error {
	params := map[string]any{"type": eventType, "key": key}
	if eventType == "char" {
		params["text"] = key
	}
	_, err := client.Call("Input.dispatchKeyEvent", params)
	return err
}

// windowOffset holds Chrome's position on the desktop.
type windowOffset struct {
	ScreenX      float64 `json:"screenX"`
	ScreenY      float64 `json:"screenY"`
	ChromeHeight float64 `json:"chromeHeight"`
}

// cdpGetWindowOffset queries the Chrome window's desktop position and chrome UI height.
func cdpGetWindowOffset(client *CDPClient) (windowOffset, error) {
	result, err := cdpEvaluate(client, `JSON.stringify({screenX: window.screenX, screenY: window.screenY, chromeHeight: window.outerHeight - window.innerHeight})`)
	if err != nil {
		return windowOffset{}, fmt.Errorf("querying window offset: %w", err)
	}
	var offset windowOffset
	if err := json.Unmarshal([]byte(result), &offset); err != nil {
		return windowOffset{}, fmt.Errorf("parsing window offset: %w", err)
	}
	return offset, nil
}

// jsQuote returns a JavaScript string literal for use in evaluated expressions.
func jsQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// truncate shortens s to max chars, appending "..." after keep chars when it overflows.
func truncate(s string, max, keep int) string {
	if len(s) > max {
		return s[:keep] + "..."
	}
	return s
}
