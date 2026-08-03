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

// fakeCC is a fake kit.CheckContext for the addr verb's live (host-side dial) path — no
// Exec() is needed under ModeLive.
type fakeCC struct{}

func (c *fakeCC) Exec() kit.Executor { return nil }
func (c *fakeCC) Mode() kit.RunMode  { return kit.ModeLive }
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

	res := verb{}.RunVerb(context.Background(), &fakeCC{}, &spec.Op{PluginInput: map[string]any{"addr": u}})
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
	res = verb{}.RunVerb(context.Background(), &fakeCC{}, &spec.Op{PluginInput: map[string]any{"addr": addr, "reachable": false}})
	if res.Status != kit.StatusPass {
		t.Errorf("expected unreachable-as-expected, got %+v", res)
	}
}
