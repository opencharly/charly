package dns

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeResponse is a canned matchPrefix→exit entry for fakeExec (the ModeBox getent probe).
type fakeResponse struct {
	matchPrefix string
	exit        int
}

// fakeExec is a kit.Executor returning canned RunCapture exit codes by command prefix.
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

// fakeCC is a fake kit.CheckContext for the dns verb: the live (host-side net.LookupIP)
// path needs no Exec() under ModeLive; the ModeBox getent path exercises the Exec leg.
type fakeCC struct {
	mode kit.RunMode
	exec kit.Executor
}

func (c *fakeCC) Exec() kit.Executor { return c.exec }
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

// TestDNSVerb: host-side resolution for a guaranteed-resolvable hostname, and
// expected-unresolvable for a never-assigned TLD. Relocated from
// charly/checkrun_verbs_test.go's TestRunner_DNSPlugin (#55 decoupling cone, Batch D).
func TestDNSVerb(t *testing.T) {
	t.Run("resolvable localhost", func(t *testing.T) {
		res := verb{}.RunVerb(context.Background(), &fakeCC{mode: kit.ModeLive}, &spec.Op{PluginInput: map[string]any{"dns": "localhost"}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
	t.Run("unresolvable as expected", func(t *testing.T) {
		res := verb{}.RunVerb(context.Background(), &fakeCC{mode: kit.ModeLive}, &spec.Op{PluginInput: map[string]any{"dns": "this-host-will-never-exist.invalid", "resolvable": false}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
}

// TestDNSVerb_ModeBox: the in-container getent probe path (ModeBox), deterministic via
// fakeExec — getent exit 0 = resolvable, exit 2 = not. Relocated from
// charly/plugin_dns_relocated_test.go's TestRelocatedDNSVerb_DispatchesViaKit (the
// check-role behavior half; the dispatch wiring stays in charly).
func TestDNSVerb_ModeBox(t *testing.T) {
	t.Run("getent-ok + resolvable:true", func(t *testing.T) {
		cc := &fakeCC{mode: kit.ModeBox, exec: &fakeExec{responses: []fakeResponse{{matchPrefix: "getent hosts", exit: 0}}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"dns": "localhost", "resolvable": true}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
	t.Run("getent-fail + resolvable:false", func(t *testing.T) {
		cc := &fakeCC{mode: kit.ModeBox, exec: &fakeExec{responses: []fakeResponse{{matchPrefix: "getent hosts", exit: 2}}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"dns": "no.such.host.invalid", "resolvable": false}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
}
