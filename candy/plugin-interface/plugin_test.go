package iface

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
	matchPrefix, stdout string
	exit                int
}

// fakeExec is a kit.Executor returning canned RunCapture output by command prefix (the
// `ip -o addr show` / `ip -o link show` probes).
type fakeExec struct{ responses []fakeResponse }

func (f *fakeExec) RunCapture(_ context.Context, cmd string) (string, string, int, error) {
	for _, r := range f.responses {
		if strings.HasPrefix(cmd, r.matchPrefix) || strings.Contains(cmd, r.matchPrefix) {
			return r.stdout, "", r.exit, nil
		}
	}
	return "", "no fake response for: " + cmd, 127, nil
}
func (f *fakeExec) Kind() string { return "container" }

// fakeCC is a fake kit.CheckContext exercising the interface verb's Exec leg.
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

// TestInterfaceVerb: presence + MTU check. Relocated from
// charly/checkrun_verbs_test.go's TestRunner_Interface (#55 decoupling cone, Batch D).
func TestInterfaceVerb(t *testing.T) {
	cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
		{matchPrefix: "ip -o addr show 'eth0'", stdout: "2: eth0 inet 10.0.0.5/24\n", exit: 0},
		{matchPrefix: "ip -o link show 'eth0'", stdout: "1500\n", exit: 0},
	}}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"interface": "eth0", "mtu": 1500, "addrs": []any{"10.0.0.5/24"}}})
	if res.Status != kit.StatusPass {
		t.Errorf("got %+v", res)
	}
}

// TestInterfaceVerb_Absent: an empty `ip -o addr show` probe (interface not present)
// must FAIL. Relocated from charly/plugin_interface_relocated_test.go's
// TestRelocatedInterfaceVerb_DispatchesViaKit (the check-role behavior half; the
// dispatch wiring stays in charly).
func TestInterfaceVerb_Absent(t *testing.T) {
	cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
		{matchPrefix: "ip -o addr show", stdout: "", exit: 0},
	}}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"interface": "nonexistent"}})
	if res.Status != kit.StatusFail {
		t.Errorf("expected fail for an absent interface, got %+v", res)
	}
}
