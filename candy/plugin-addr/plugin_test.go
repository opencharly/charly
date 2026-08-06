package addr

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeResponse is a canned matchPrefix→exit entry for fakeExec (the ModeBox nc probe).
type fakeResponse struct {
	matchPrefix string
	exit        int
}

// fakeExec is a kit.Executor returning canned RunCapture exit codes by command prefix.
type fakeExec struct{ responses []fakeResponse }

func (f *fakeExec) RunCapture(_ context.Context, cmd string) (string, string, int, error) {
	for _, r := range f.responses {
		if strings.HasPrefix(cmd, r.matchPrefix) || strings.Contains(cmd, r.matchPrefix) {
			return "", "", r.exit, nil
		}
	}
	return "", "no fake response for: " + cmd, 127, nil
}
func (f *fakeExec) Kind() string { return "container" }

// fakeCC is a fake kit.CheckContext for the addr verb: the live (host-side dial) path
// needs no Exec() under ModeLive; the ModeBox nc path exercises the Exec leg.
type fakeCC struct {
	mode kit.RunMode
	exec kit.Executor
}

func (c *fakeCC) Exec() kit.Executor { return c.exec }
func (c *fakeCC) Mode() kit.RunMode  { return c.mode }
func (c *fakeCC) HTTPDo(context.Context, kit.HTTPRequest) (kit.HTTPResponse, error) {
	return kit.HTTPResponse{}, nil
}
func (c *fakeCC) ResolveEndpoint(context.Context, int) (string, error) { return "", nil }
func (c *fakeCC) ResolveGraphicsEndpoint(context.Context, string) (kit.GraphicsEndpoint, error) {
	return kit.GraphicsEndpoint{}, nil
}
func (c *fakeCC) ResolveImageLabel(context.Context, string) (string, error) { return "", nil }
func (c *fakeCC) DialTimeout() time.Duration                                { return 3 * time.Second }
func (c *fakeCC) Box() string                                               { return "" }
func (c *fakeCC) Instance() string                                          { return "" }
func (c *fakeCC) Distros() []string                                         { return nil }
func (c *fakeCC) AddBackground(int)                                         {}

// TestAddrVerb: host-side dial against a real httptest listener. Relocated from
// charly/checkrun_verbs_test.go's TestRunner_Addr (#55 decoupling cone, Batch D).
func TestAddrVerb(t *testing.T) {
	srv := httptest.NewServer(nil)
	defer srv.Close()
	u := strings.TrimPrefix(srv.URL, "http://")

	res := verb{}.RunVerb(context.Background(), &fakeCC{mode: kit.ModeLive}, &spec.Op{PluginInput: map[string]any{"addr": u}})
	if res.Status != kit.StatusPass {
		t.Errorf("expected reachable, got %+v", res)
	}

	// Unreachable — pick a high port nothing is on. net.Listen gives us one safely.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close() // free the port
	res = verb{}.RunVerb(context.Background(), &fakeCC{mode: kit.ModeLive}, &spec.Op{PluginInput: map[string]any{"addr": addr, "reachable": false}})
	if res.Status != kit.StatusPass {
		t.Errorf("expected unreachable-as-expected, got %+v", res)
	}
}

// TestAddrVerb_ModeBox: the in-container nc probe path (ModeBox), deterministic via
// fakeExec — nc exit 0 = reachable, exit 1 = not. Relocated from
// charly/plugin_addr_relocated_test.go's TestRelocatedAddrVerb_DispatchesViaKit (the
// check-role behavior half; the dispatch wiring stays in charly).
func TestAddrVerb_ModeBox(t *testing.T) {
	t.Run("nc-up + reachable:true", func(t *testing.T) {
		cc := &fakeCC{mode: kit.ModeBox, exec: &fakeExec{responses: []fakeResponse{{matchPrefix: "nc -z", exit: 0}}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"addr": "127.0.0.1:22", "reachable": true}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
	t.Run("nc-down + reachable:false", func(t *testing.T) {
		cc := &fakeCC{mode: kit.ModeBox, exec: &fakeExec{responses: []fakeResponse{{matchPrefix: "nc -z", exit: 1}}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"addr": "127.0.0.1:1", "reachable": false}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
}
