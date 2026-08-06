package service

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
	stdout      string
	exit        int
}

// fakeExec is a kit.Executor returning canned RunCapture output by command prefix (the
// supervisorctl/systemctl probe).
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

// fakeCC is a fake kit.CheckContext exercising the service verb's Exec leg.
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

// TestServiceVerb: running true/false. Relocated from charly/checkrun_verbs_test.go's
// TestRunner_Service (#55 decoupling cone, Batch D) — mirrors candy/plugin-port and
// candy/plugin-http's own test pattern (R3).
func TestServiceVerb(t *testing.T) {
	t.Run("running true via supervisorctl", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "supervisorctl status 'jupyter'", exit: 0},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"service": "jupyter", "running": true}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})

	t.Run("running mismatch", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "supervisorctl status 'jupyter'", exit: 1},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"service": "jupyter", "running": true}})
		if res.Status != kit.StatusFail {
			t.Errorf("expected fail, got %+v", res)
		}
	})
}

// TestServiceVerb_RenderProvisionScript: the ACT role renders the enable + start shell
// under whichever init the live target runs. Relocated from
// charly/plugin_service_relocated_test.go's TestRelocatedServiceVerb_DispatchesViaKit (the
// act-role behavior half; the dispatch wiring stays in charly).
func TestServiceVerb_RenderProvisionScript(t *testing.T) {
	script, ok := verb{}.RenderProvisionScript(&spec.Op{PluginInput: map[string]any{"service": "nginx"}}, nil)
	if !ok || !strings.Contains(script, "systemctl enable") || !strings.Contains(script, "supervisorctl") {
		t.Fatalf("act: want an enable shell, got ok=%v %q", ok, script)
	}
}

// TestServiceVerb_StepProvider: the TYPED-STEP role names the ServicePackaged step kind
// and decodes plugin_input into the StepDescriptor the host materializer consumes.
// Relocated from charly/plugin_service_relocated_test.go's
// TestRelocatedServiceVerb_DispatchesViaKit (the step-role behavior half; the dispatch
// wiring + the materializer stay in charly).
func TestServiceVerb_StepProvider(t *testing.T) {
	got := verb{}.StepKind()
	if got != kit.StepKindServicePackaged {
		t.Fatalf("StepKind = %v, want StepKindServicePackaged", got)
	}
	desc := verb{}.ConstructStepDescriptor(&spec.Op{PluginInput: map[string]any{"service": "nginx"}})
	if desc.ServicePackaged == nil || desc.ServicePackaged.Unit != "nginx" || !desc.ServicePackaged.Enable {
		t.Fatalf("StepDescriptor = %+v, want Unit=nginx Enable=true", desc)
	}
}
