package user

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeExec is a kit.Executor returning canned RunCapture output (the getent passwd probe).
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

// fakeCC is a fake kit.CheckContext exercising the user verb's Exec leg.
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

// TestUserVerb / TestUserVerb_UIDMismatch: getent output parsing. Relocated from
// charly/checkrun_verbs_test.go's TestRunner_User / TestRunner_User_UIDMismatch (#55
// decoupling cone, Batch D).
func TestUserVerb(t *testing.T) {
	cc := &fakeCC{exec: &fakeExec{matchPrefix: "getent passwd 'alice'", stdout: "alice:x:1000:1000:Alice:/home/alice:/bin/bash\n", exit: 0}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"user": "alice", "uid": 1000, "home": "/home/alice"}})
	if res.Status != kit.StatusPass {
		t.Errorf("expected pass, got %+v", res)
	}
}

func TestUserVerb_UIDMismatch(t *testing.T) {
	cc := &fakeCC{exec: &fakeExec{matchPrefix: "getent passwd 'alice'", stdout: "alice:x:1500:1500:Alice:/home/alice:/bin/bash\n", exit: 0}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"user": "alice", "uid": 1000}})
	if res.Status != kit.StatusFail {
		t.Errorf("expected fail, got %+v", res)
	}
}

// TestUserVerb_RenderProvisionScript: the ACT role renders an idempotent
// `id || useradd` with the given uid/home/shell. Relocated from
// charly/plugin_user_relocated_test.go's TestRelocatedUserVerb_DispatchesViaKit (the
// act-role behavior half; the dispatch wiring stays in charly).
func TestUserVerb_RenderProvisionScript(t *testing.T) {
	script, ok := verb{}.RenderProvisionScript(
		&spec.Op{PluginInput: map[string]any{"user": "svc", "uid": 1500, "home": "/home/svc", "shell": "/bin/sh"}}, nil)
	if !ok || !strings.Contains(script, "useradd") || !strings.Contains(script, "svc") {
		t.Fatalf("act: want a useradd script, got ok=%v %q", ok, script)
	}
}
