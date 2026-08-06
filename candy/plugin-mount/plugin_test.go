package mount

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeExec is a kit.Executor returning canned RunCapture output (the findmnt probe).
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

// fakeCC is a fake kit.CheckContext exercising the mount verb's Exec leg.
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

// TestMountVerb: findmnt parsing + filesystem check. Relocated from
// charly/checkrun_verbs_test.go's TestRunner_Mount (#55 decoupling cone, Batch D).
func TestMountVerb(t *testing.T) {
	cc := &fakeCC{exec: &fakeExec{
		matchPrefix: "findmnt -n -o SOURCE,FSTYPE,OPTIONS '/data'",
		stdout:      "/dev/sda1 ext4 rw,relatime\n", exit: 0,
	}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"mount": "/data", "filesystem": "ext4", "mount_source": "/dev/sda1"}})
	if res.Status != kit.StatusPass {
		t.Errorf("got %+v", res)
	}
}

// TestMountVerb_RenderProvisionScript: the ACT role renders an idempotent
// `findmnt || mount` with the source + filesystem. Relocated from
// charly/plugin_mount_relocated_test.go's TestRelocatedMountVerb_DispatchesViaKit (the
// act-role behavior half; the dispatch wiring stays in charly).
func TestMountVerb_RenderProvisionScript(t *testing.T) {
	script, ok := verb{}.RenderProvisionScript(
		&spec.Op{PluginInput: map[string]any{"mount": "/data", "mount_source": "/dev/sdb1", "filesystem": "ext4"}}, nil)
	if !ok || !strings.Contains(script, "mount") || !strings.Contains(script, "/dev/sdb1") {
		t.Fatalf("act: want a findmnt||mount script, got ok=%v %q", ok, script)
	}
}
