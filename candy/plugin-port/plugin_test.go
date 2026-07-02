package port

import (
	"context"
	"testing"
	"time"

	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/spec"
)

// fakeExec is a kit.Executor returning a canned exit (the in-container ss listening probe).
type fakeExec struct{ exit int }

func (f *fakeExec) RunCapture(context.Context, string) (string, string, int, error) {
	return "", "", f.exit, nil
}
func (f *fakeExec) Kind() string { return "container" }

// fakeCC is a fake kit.CheckContext exercising the port verb's Exec + Mode + DialTimeout legs.
type fakeCC struct {
	mode kit.RunMode
	exec kit.Executor
}

func (c *fakeCC) Exec() kit.Executor { return c.exec }
func (c *fakeCC) Mode() kit.RunMode  { return c.mode }
func (c *fakeCC) HTTPDo(context.Context, kit.HTTPRequest) (kit.HTTPResponse, error) {
	return kit.HTTPResponse{}, nil
}
func (c *fakeCC) DialTimeout() time.Duration { return 3 * time.Second }
func (c *fakeCC) Box() string                { return "" }
func (c *fakeCC) Instance() string           { return "" }
func (c *fakeCC) Distros() []string          { return nil }
func (c *fakeCC) AddBackground(int)          {}

// TestPortVerb_ListeningPass proves the in-container listening probe (cc.Exec, exit 0 =>
// listening) passes — the Exec leg that crosses the reverse channel out-of-process.
func TestPortVerb_ListeningPass(t *testing.T) {
	cc := &fakeCC{mode: kit.ModeLive, exec: &fakeExec{exit: 0}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"port": 6379, "listening": true}})
	if res.Status != kit.StatusPass {
		t.Fatalf("listening: want pass, got %v: %s", res.Status, res.Message)
	}
}

// TestPortVerb_ReachableSkipUnderBox proves a host-side reachability probe SKIPs under
// ModeBox (no host port mapping on a disposable build container).
func TestPortVerb_ReachableSkipUnderBox(t *testing.T) {
	cc := &fakeCC{mode: kit.ModeBox, exec: &fakeExec{}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"port": 6379, "reachable": true}})
	if res.Status != kit.StatusSkip {
		t.Fatalf("reachable under box: want skip, got %v: %s", res.Status, res.Message)
	}
}
