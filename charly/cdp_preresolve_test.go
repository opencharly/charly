package main

import (
	"encoding/json"
	"testing"
)

// These tests cover the CDP helper retained HOST-side after the `cdp` declarative verb
// moved out-of-process (candy/plugin-cdp): the devToolsTab JSON shape (decoded by the
// status-probe path). The core's minimal CDP WebSocket client + the tab-index resolver were
// deleted when the LAST in-core `--from-cdp` consumer (the `wl` verb) externalized into
// candy/plugin-wl — so their tests went with them. The declarative cdp methods' tests live in
// the plugin module (candy/plugin-cdp/methods_test.go).

func TestDevToolsTabWebSocketField(t *testing.T) {
	jsonData := `{"id":"ABC123","title":"Test Page","url":"https://example.com","type":"page","webSocketDebuggerUrl":"ws://localhost:9222/devtools/page/ABC123"}`
	var tab devToolsTab
	if err := json.Unmarshal([]byte(jsonData), &tab); err != nil {
		t.Fatalf("failed to unmarshal devToolsTab: %v", err)
	}
	if tab.ID != "ABC123" {
		t.Errorf("ID = %q, want %q", tab.ID, "ABC123")
	}
	if tab.WebSocketDebuggerURL != "ws://localhost:9222/devtools/page/ABC123" {
		t.Errorf("WebSocketDebuggerURL = %q, want %q", tab.WebSocketDebuggerURL, "ws://localhost:9222/devtools/page/ABC123")
	}
}
