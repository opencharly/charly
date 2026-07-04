package cdp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests cover the plugin-side cdp helpers moved from charly/cdp.go +
// charly/browser_cdp.go: jsQuote (JS string-literal quoting), the cdpMessage wire form,
// and resolveTabWS's numeric-index/UUID dual-path contract.

func TestJsQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`hello`, `"hello"`},
		{`he said "hi"`, `"he said \"hi\""`},
		{`line1\nline2`, `"line1\\nline2"`},
		{`<script>alert('xss')</script>`, `"\u003cscript\u003ealert('xss')\u003c/script\u003e"`},
		{``, `""`},
	}
	for _, tt := range tests {
		if got := jsQuote(tt.input); got != tt.want {
			t.Errorf("jsQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCDPMessageSerialization(t *testing.T) {
	params, _ := json.Marshal(map[string]string{"expression": "1+1"})
	msg := cdpMessage{ID: 1, Method: "Runtime.evaluate", Params: params}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal cdpMessage: %v", err)
	}
	var decoded cdpMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal cdpMessage: %v", err)
	}
	if decoded.ID != 1 || decoded.Method != "Runtime.evaluate" {
		t.Errorf("decoded = %+v, want ID 1 / Runtime.evaluate", decoded)
	}

	errMsg := cdpMessage{ID: 2, Error: &cdpError{Code: -32601, Message: "method not found"}}
	if errMsg.Error.Error() != "CDP error -32601: method not found" {
		t.Errorf("cdpError.Error() = %q", errMsg.Error.Error())
	}

	noMethod, _ := json.Marshal(cdpMessage{ID: 4})
	if strings.Contains(string(noMethod), `"method"`) {
		t.Error("expected method to be omitted from JSON when empty")
	}
}

func TestResolveTabWS_NumericIndex(t *testing.T) {
	type tabFixture struct {
		ID                   string
		Type                 string
		WebSocketDebuggerURL string
	}
	cases := []struct {
		name       string
		tabs       []tabFixture
		input      string
		wantSubstr string
		wantErr    string
	}{
		{"numeric 1", []tabFixture{{ID: "a", Type: "page", WebSocketDebuggerURL: "ws://x/a"}}, "1", "ws://x/a", ""},
		{"numeric skips non-page", []tabFixture{
			{ID: "sw", Type: "service_worker", WebSocketDebuggerURL: "ws://x/sw"},
			{ID: "p", Type: "page", WebSocketDebuggerURL: "ws://x/p"},
		}, "1", "ws://x/p", ""},
		{"uuid match", []tabFixture{{ID: "abc", Type: "page", WebSocketDebuggerURL: "ws://x/abc"}}, "abc", "ws://x/abc", ""},
		{"zero not valid", []tabFixture{{ID: "f", Type: "page", WebSocketDebuggerURL: "ws://x/f"}}, "0", "", "tab 0 not found"},
		{"out of range", []tabFixture{{ID: "f", Type: "page", WebSocketDebuggerURL: "ws://x/f"}}, "5", "", "tab 5 not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/json" {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(tc.tabs)
			}))
			defer srv.Close()
			got, err := resolveTabWS(srv.URL, tc.input)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantSubstr {
				t.Errorf("got %q, want %q", got, tc.wantSubstr)
			}
		})
	}
}
