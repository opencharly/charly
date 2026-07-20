package httpverb

import (
	"context"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// fakeExec is a kit.Executor returning canned RunCapture output (the ModeBox curl path).
type fakeExec struct {
	out  string
	exit int
}

func (f *fakeExec) RunCapture(context.Context, string) (string, string, int, error) {
	return f.out, "", f.exit, nil
}
func (f *fakeExec) Kind() string { return "image" }

// fakeCC is a fake kit.CheckContext: it records the HTTPDo request + returns a canned
// response (the live path), and hands out a fake Executor (the box path).
type fakeCC struct {
	mode     kit.RunMode
	exec     kit.Executor
	httpResp kit.HTTPResponse
	httpErr  error
	lastReq  kit.HTTPRequest
}

func (c *fakeCC) Exec() kit.Executor { return c.exec }
func (c *fakeCC) Mode() kit.RunMode  { return c.mode }
func (c *fakeCC) HTTPDo(_ context.Context, req kit.HTTPRequest) (kit.HTTPResponse, error) {
	c.lastReq = req
	return c.httpResp, c.httpErr
}
func (c *fakeCC) ResolveEndpoint(context.Context, int) (string, error) { return "", nil }
func (c *fakeCC) ResolveGraphicsEndpoint(context.Context, string) (kit.GraphicsEndpoint, error) {
	return kit.GraphicsEndpoint{}, nil
}
func (c *fakeCC) ResolveClusterContext(context.Context, string) (string, error) { return "", nil }
func (c *fakeCC) ResolveImageLabel(context.Context, string) (string, error)     { return "", nil }
func (c *fakeCC) DialTimeout() time.Duration                                    { return 3 * time.Second }
func (c *fakeCC) Box() string                                                   { return "" }
func (c *fakeCC) Instance() string                                              { return "" }
func (c *fakeCC) Distros() []string                                             { return nil }
func (c *fakeCC) AddBackground(int)                                             {}

// TestHTTPVerb_LiveViaHTTPDo proves the live path builds the request from the op + matches
// the cc.HTTPDo response (status + body), and FAILS on a status mismatch. The verb dials
// via cc.HTTPDo — the leg F2 serves over the reverse channel out-of-process.
func TestHTTPVerb_LiveViaHTTPDo(t *testing.T) {
	cc := &fakeCC{mode: kit.ModeLive, httpResp: kit.HTTPResponse{Status: 200, Body: []byte("service is ready"), HeaderBlob: "X-Charly: yes\n"}}
	op := &spec.Op{PluginInput: map[string]any{"http": "http://svc/ok", "status": 200, "body": []any{map[string]any{"contains": "ready"}}}}
	res := verb{}.RunVerb(context.Background(), cc, op)
	if res.Status != kit.StatusPass {
		t.Fatalf("live status+body: want pass, got %v: %s", res.Status, res.Message)
	}
	if cc.lastReq.URL != "http://svc/ok" {
		t.Errorf("HTTPDo URL = %q, want http://svc/ok", cc.lastReq.URL)
	}

	cc2 := &fakeCC{mode: kit.ModeLive, httpResp: kit.HTTPResponse{Status: 500}}
	res2 := verb{}.RunVerb(context.Background(), cc2, &spec.Op{PluginInput: map[string]any{"http": "http://svc/boom", "status": 200}})
	if res2.Status != kit.StatusFail {
		t.Fatalf("status mismatch: want fail, got %v: %s", res2.Status, res2.Message)
	}
}

// TestHTTPVerb_BoxViaCurl proves the ModeBox path probes via cc.Exec() (curl), matching the
// status code curl reports.
func TestHTTPVerb_BoxViaCurl(t *testing.T) {
	cc := &fakeCC{mode: kit.ModeBox, exec: &fakeExec{out: "200", exit: 0}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"http": "http://svc/", "status": 200}})
	if res.Status != kit.StatusPass {
		t.Fatalf("box curl 200: want pass, got %v: %s", res.Status, res.Message)
	}

	ccBad := &fakeCC{mode: kit.ModeBox, exec: &fakeExec{out: "503", exit: 0}}
	resBad := verb{}.RunVerb(context.Background(), ccBad, &spec.Op{PluginInput: map[string]any{"http": "http://svc/", "status": 200}})
	if resBad.Status != kit.StatusFail {
		t.Fatalf("box curl 503 vs status:200: want fail, got %v", resBad.Status)
	}
}
