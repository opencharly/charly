package process

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeResponse is a canned matchPrefix→output entry for fakeExec.
type fakeResponse struct {
	matchPrefix string
	exit        int
}

// fakeExec is a kit.Executor returning canned RunCapture output by command prefix (the
// pgrep probe).
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

// fakeCC is a fake kit.CheckContext exercising the process verb's Exec leg.
type fakeCC struct{ exec kit.Executor }

func (c *fakeCC) Exec() kit.Executor { return c.exec }
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

// TestProcessVerb: pgrep exit status. Relocated from charly/checkrun_verbs_test.go's
// TestRunner_ProcessPlugin (#55 decoupling cone, Batch D) — mirrors candy/plugin-port and
// candy/plugin-http's own test pattern (R3).
func TestProcessVerb(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{{matchPrefix: "pgrep -x 'redis-server'", exit: 0}}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"process": "redis-server"}})
		if res.Status != kit.StatusPass {
			t.Errorf("got %+v", res)
		}
	})
	t.Run("expected absent", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{{matchPrefix: "pgrep -x 'worm'", exit: 1}}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"process": "worm", "running": false}})
		if res.Status != kit.StatusPass {
			t.Errorf("got %+v", res)
		}
	})
	t.Run("absent but wanted running", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{{matchPrefix: "pgrep -x 'absent'", exit: 1}}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"process": "absent", "running": true}})
		if res.Status != kit.StatusFail {
			t.Errorf("got %+v", res)
		}
	})
}
