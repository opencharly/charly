package ollama

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fakeOllama is an httptest.Server scripted with per-path handlers. It records the last
// request body per path so tests can assert what the plugin sent.
type fakeOllama struct {
	*httptest.Server
	bodies map[string][]byte
}

func newFakeOllama(t *testing.T, handlers map[string]http.HandlerFunc) *fakeOllama {
	t.Helper()
	f := &fakeOllama{bodies: map[string][]byte{}}
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				body, _ := io.ReadAll(r.Body)
				f.bodies[r.URL.Path] = body
			}
			h(w, r)
		})
	}
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what was written.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = orig
	_ = w.Close()
	out, _ := io.ReadAll(r)
	return string(out), runErr
}

func jsonReply(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

func streamReply(lines ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
	}
}

func TestParseGlobalFlags(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantHost string
		wantRest []string
	}{
		{"none", []string{"list"}, "", []string{"list"}},
		{"long flag", []string{"--server", "http://x:1", "list"}, "http://x:1", []string{"list"}},
		{"long equals", []string{"list", "--server=http://x:1"}, "http://x:1", []string{"list"}},
		{"short flag", []string{"-s", "http://x:2", "ps"}, "http://x:2", []string{"ps"}},
		{"short equals", []string{"-s=http://x:3", "ps"}, "http://x:3", []string{"ps"}},
		{"flag after args", []string{"pull", "m", "--server", "http://x:4"}, "http://x:4", []string{"pull", "m"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, rest := parseGlobalFlags(tc.args)
			if opts.host != tc.wantHost {
				t.Errorf("host = %q, want %q", opts.host, tc.wantHost)
			}
			if strings.Join(rest, " ") != strings.Join(tc.wantRest, " ") {
				t.Errorf("rest = %v, want %v", rest, tc.wantRest)
			}
		})
	}
}

func TestResolveHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	if got := resolveHost(""); got != defaultHost {
		t.Errorf("default = %q, want %q", got, defaultHost)
	}
	t.Setenv("OLLAMA_HOST", "http://env-host:9999")
	if got := resolveHost(""); got != "http://env-host:9999" {
		t.Errorf("env = %q", got)
	}
	if got := resolveHost("http://flag-host:1111"); got != "http://flag-host:1111" {
		t.Errorf("flag beats env = %q", got)
	}
	if got := resolveHost("plain-host:11434"); got != "http://plain-host:11434" {
		t.Errorf("scheme prepend = %q", got)
	}
	// The candy's server-bind value maps to loopback for client use.
	if got := resolveHost("0.0.0.0"); got != "http://127.0.0.1" {
		t.Errorf("0.0.0.0 maps to loopback = %q", got)
	}
	if got := resolveHost("0.0.0.0:11434"); got != "http://127.0.0.1:11434" {
		t.Errorf("0.0.0.0:port maps to loopback = %q", got)
	}
}

func TestList(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/tags": jsonReply(map[string]any{"models": []map[string]any{
			{"name": "llama3:latest", "model": "llama3:latest", "modified_at": "2026-08-17T00:00:00Z", "size": 4_000_000_000, "digest": "abc"},
		}}),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"list", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "llama3:latest") || !strings.Contains(out, "NAME") || !strings.Contains(out, "3.7 GiB") || !strings.Contains(out, "ago") {
		t.Errorf("unexpected list output: %q", out)
	}
}

func TestPS(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/ps": jsonReply(map[string]any{"models": []map[string]any{
			{"name": "llama3:latest", "model": "llama3:latest", "size": 4_000_000_000, "size_vram": 4_000_000_000, "expires_at": "2999-01-01T00:00:00Z"},
		}}),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"ps", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "llama3:latest") || !strings.Contains(out, "VRAM") {
		t.Errorf("unexpected ps output: %q", out)
	}
}

func TestVersion(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/version": jsonReply(map[string]any{"version": "0.32.14"}),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"version", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0.32.14") {
		t.Errorf("unexpected version output: %q", out)
	}
}

func TestRemove(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/delete": jsonReply(map[string]any{}),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"rm", "llama3:latest", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deleted llama3:latest") {
		t.Errorf("unexpected rm output: %q", out)
	}
	var body map[string]any
	if err := json.Unmarshal(f.bodies["/api/delete"], &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "llama3:latest" {
		t.Errorf("delete body = %v", body)
	}
}

func TestShow(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/show": jsonReply(map[string]any{
			"modified_at": "2026-08-17T00:00:00Z",
			"details":     map[string]any{"format": "gguf", "family": "llama", "parameter_size": "8B", "quantization_level": "Q4_0"},
			"modelfile":   "FROM llama3",
		}),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"show", "llama3:latest", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gguf", "llama", "8B", "Q4_0", "FROM llama3"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q: %q", want, out)
		}
	}
}

func TestCopy(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/copy": jsonReply(map[string]any{}),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"cp", "a:latest", "b:latest", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "copied a:latest to b:latest") {
		t.Errorf("unexpected cp output: %q", out)
	}
}

func TestPull(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/pull": streamReply(
			`{"status":"pulling manifest"}`,
			`{"status":"downloading","digest":"sha256:x","total":100,"completed":100}`,
			`{"status":"success"}`,
		),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"pull", "llama3:latest", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pulled llama3:latest") {
		t.Errorf("unexpected pull output: %q", out)
	}
}

func TestPush(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/push": streamReply(
			`{"status":"retrieving manifest"}`,
			`{"status":"pushing","digest":"sha256:y","total":100,"completed":100}`,
			`{"status":"success"}`,
		),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"push", "mine:latest", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pushed mine:latest") {
		t.Errorf("unexpected push output: %q", out)
	}
	var body map[string]any
	if err := json.Unmarshal(f.bodies["/api/push"], &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "mine:latest" {
		t.Errorf("push body = %v", body)
	}
}

func TestPushRequiresModel(t *testing.T) {
	if err := runOllamaCLI([]string{"push"}); err == nil {
		t.Fatal("expected usage error without a model argument")
	}
}

func TestCreate(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/create": streamReply(`{"status":"success"}`),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"create", "mine:latest", "--from", "llama3:latest", "--system", "be nice", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "created mine:latest") {
		t.Errorf("unexpected create output: %q", out)
	}
	var body map[string]any
	if err := json.Unmarshal(f.bodies["/api/create"], &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "mine:latest" || body["from"] != "llama3:latest" || body["system"] != "be nice" {
		t.Errorf("create body = %v", body)
	}
}

func TestCreateRequiresFrom(t *testing.T) {
	if err := runOllamaCLI([]string{"create", "mine:latest"}); err == nil {
		t.Fatal("expected usage error without --from")
	}
}

func TestRunWithPrompt(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/generate": streamReply(
			`{"model":"m","response":"Hello","done":false}`,
			`{"model":"m","response":" world","done":true}`,
		),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"run", "m", "say", "hi", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hello world\n" {
		t.Errorf("generate output = %q", out)
	}
	var body map[string]any
	if err := json.Unmarshal(f.bodies["/api/generate"], &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "m" || body["prompt"] != "say hi" {
		t.Errorf("generate body = %v", body)
	}
}

func TestStop(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/generate": streamReply(`{"model":"m","done":true,"done_reason":"unload"}`),
	})
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"stop", "m", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "stopped m") {
		t.Errorf("unexpected stop output: %q", out)
	}
	var body map[string]any
	if err := json.Unmarshal(f.bodies["/api/generate"], &body); err != nil {
		t.Fatal(err)
	}
	if body["keep_alive"] != "0s" {
		t.Errorf("stop must send keep_alive 0, body = %v", body)
	}
}

func TestChatRepl(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/chat": streamReply(
			`{"model":"m","message":{"role":"assistant","content":"hi"},"done":false}`,
			`{"model":"m","message":{"role":"assistant","content":" there"},"done":true}`,
		),
	})
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(w, "hello")
	fmt.Fprintln(w, "/bye")
	_ = w.Close()
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	out, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"run", "m", "--server", f.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi there") {
		t.Errorf("chat output = %q", out)
	}
}

func TestServerErrorPropagates(t *testing.T) {
	f := newFakeOllama(t, map[string]http.HandlerFunc{
		"/api/tags": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
		},
	})
	_, err := captureStdout(t, func() error {
		return runOllamaCLI([]string{"list", "--server", f.URL})
	})
	if err == nil {
		t.Fatal("expected the server error to propagate")
	}
	if !strings.Contains(err.Error(), "ollama list") {
		t.Errorf("error should name the command: %v", err)
	}
}

func TestUnknownCommand(t *testing.T) {
	if err := runOllamaCLI([]string{"frobnicate"}); err == nil || !strings.Contains(err.Error(), "unknown ollama command") {
		t.Errorf("expected unknown-command error, got %v", err)
	}
}

func TestNoArgsPrintsUsage(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return runOllamaCLI(nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "charly ollama — manage a deployed Ollama server") {
		t.Errorf("usage output = %q", out)
	}
}
