package dns

import (
	"context"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeCC is a fake kit.CheckContext for the dns verb's live (host-side net.LookupIP) path —
// no Exec() is needed under ModeLive.
type fakeCC struct{}

func (c *fakeCC) Exec() kit.Executor { return nil }
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

// TestDNSVerb: host-side resolution for a guaranteed-resolvable hostname, and
// expected-unresolvable for a never-assigned TLD. Relocated from
// charly/checkrun_verbs_test.go's TestRunner_DNSPlugin (#55 decoupling cone, Batch D).
func TestDNSVerb(t *testing.T) {
	t.Run("resolvable localhost", func(t *testing.T) {
		res := verb{}.RunVerb(context.Background(), &fakeCC{}, &spec.Op{PluginInput: map[string]any{"dns": "localhost"}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
	t.Run("unresolvable as expected", func(t *testing.T) {
		res := verb{}.RunVerb(context.Background(), &fakeCC{}, &spec.Op{PluginInput: map[string]any{"dns": "this-host-will-never-exist.invalid", "resolvable": false}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
}
