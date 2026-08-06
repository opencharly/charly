package examplerunverb

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeCC is a fake kit.CheckContext for the examplerunverb verb: RunVerb reads only the
// live run mode off the context (no Exec leg).
type fakeCC struct{ mode kit.RunMode }

func (c *fakeCC) Exec() kit.Executor { return nil }
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

// TestExamplerunverbVerb: echoes the marker + the live run mode read off the CheckContext
// (proving the verb reaches engine state an out-of-process Invoke could not). Relocated
// from charly/plugin_examplerunverb_relocated_test.go's
// TestRelocatedExamplerunverbVerb_DispatchesViaKit (the check-role behavior half; the
// dispatch wiring stays in charly).
func TestExamplerunverbVerb(t *testing.T) {
	res := verb{}.RunVerb(context.Background(), &fakeCC{mode: kit.ModeLive},
		&spec.Op{PluginInput: map[string]any{"marker": "runverb-xyz"}})
	if res.Status != kit.StatusPass {
		t.Fatalf("want pass, got %v: %s", res.Status, res.Message)
	}
	if !strings.Contains(res.Message, "runverb-xyz") || !strings.Contains(res.Message, "mode=live") {
		t.Fatalf("message %q missing marker or live mode (proves it read the live CheckContext)", res.Message)
	}
}
