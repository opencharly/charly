package unixgroup

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeExec is a kit.Executor returning canned RunCapture output (the getent group probe).
type fakeExec struct {
	matchPrefix, stdout string
	exit                int
}

func (f *fakeExec) RunCapture(_ context.Context, cmd string) (string, string, int, error) {
	if strings.HasPrefix(cmd, f.matchPrefix) || strings.Contains(cmd, f.matchPrefix) {
		return f.stdout, "", f.exit, nil
	}
	return "", "no fake response for: " + cmd, 127, nil
}
func (f *fakeExec) Kind() string { return "container" }

// fakeCC is a fake kit.CheckContext exercising the unix_group verb's Exec leg.
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

// TestUnixGroupVerb: getent group parsing. Relocated from charly/checkrun_verbs_test.go's
// TestRunner_Group (#55 decoupling cone, Batch D).
func TestUnixGroupVerb(t *testing.T) {
	cc := &fakeCC{exec: &fakeExec{matchPrefix: "getent group 'docker'", stdout: "docker:x:999:alice,bob\n", exit: 0}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"unix_group": "docker", "gid": 999}})
	if res.Status != kit.StatusPass {
		t.Errorf("got %+v", res)
	}
}

// TestUnixGroupVerb_RenderProvisionScript: the ACT role renders an idempotent
// `getent group || groupadd` with the given gid. Relocated from
// charly/plugin_unix_group_relocated_test.go's TestRelocatedUnixGroupVerb_DispatchesViaKit
// (the act-role behavior half; the dispatch wiring stays in charly).
func TestUnixGroupVerb_RenderProvisionScript(t *testing.T) {
	script, ok := verb{}.RenderProvisionScript(
		&spec.Op{PluginInput: map[string]any{"unix_group": "svc", "gid": 1500}}, nil)
	if !ok || !strings.Contains(script, "groupadd") || !strings.Contains(script, "svc") {
		t.Fatalf("act: want a groupadd script, got ok=%v %q", ok, script)
	}
}
