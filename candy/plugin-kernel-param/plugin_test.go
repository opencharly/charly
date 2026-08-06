package kernelparam

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeExec is a kit.Executor returning canned RunCapture output (the /proc/sys cat probe).
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

// fakeCC is a fake kit.CheckContext exercising the kernel-param verb's Exec leg.
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

// TestKernelParamVerb: scalar match via the value matcher. Relocated from
// charly/checkrun_verbs_test.go's TestRunner_KernelParam (#55 decoupling cone, Batch D). The
// CHECK reads /proc/sys/<key-as-slashes> directly (no procps-ng `sysctl` dependency).
func TestKernelParamVerb(t *testing.T) {
	cc := &fakeCC{exec: &fakeExec{matchPrefix: "cat '/proc/sys/net/ipv4/ip_forward'", stdout: "1\n", exit: 0}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"kernel-param": "net.ipv4.ip_forward", "value": "1"}})
	if res.Status != kit.StatusPass {
		t.Errorf("got %+v", res)
	}
}

// TestKernelParamVerb_ListValue: the value matcher also accepts the gengotypes-degraded
// LIST encoding (`value: [Linux]`). Relocated from
// charly/plugin_kernel_param_relocated_test.go's TestRelocatedKernelParamVerb_DispatchesViaKit
// (the check-role behavior half; the dispatch wiring stays in charly).
func TestKernelParamVerb_ListValue(t *testing.T) {
	cc := &fakeCC{exec: &fakeExec{matchPrefix: "/proc/sys", stdout: "Linux\n", exit: 0}}
	res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"kernel-param": "kernel.ostype", "value": []any{"Linux"}}})
	if res.Status != kit.StatusPass {
		t.Errorf("got %+v", res)
	}
}

// TestKernelParamVerb_RenderProvisionScript: the ACT role renders `sysctl -w key=value`.
// Relocated from charly/plugin_kernel_param_relocated_test.go's
// TestRelocatedKernelParamVerb_DispatchesViaKit (the act-role behavior half; the
// dispatch wiring stays in charly).
func TestKernelParamVerb_RenderProvisionScript(t *testing.T) {
	script, ok := verb{}.RenderProvisionScript(
		&spec.Op{PluginInput: map[string]any{"kernel-param": "vm.swappiness", "value": []any{10}}}, nil)
	if !ok || !strings.Contains(script, "sysctl -w") || !strings.Contains(script, "swappiness") {
		t.Fatalf("act: want a sysctl -w script, got ok=%v %q", ok, script)
	}
}
