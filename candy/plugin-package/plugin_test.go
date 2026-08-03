package pkgverb

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
// rpm/dpkg/pacman probe).
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

// fakeCC is a fake kit.CheckContext exercising the package verb's Exec + Distros legs.
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

// TestPackageVerb: installed-present, version match, and absent-as-expected paths. Relocated
// from charly/checkrun_verbs_test.go's TestRunner_Package (#55 decoupling cone, Batch D) — the
// in-proc dispatch test now exercises RunVerb directly against a fake kit.CheckContext,
// mirroring candy/plugin-port and candy/plugin-http's own test pattern (R3).
func TestPackageVerb(t *testing.T) {
	t.Run("installed via rpm", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "rpm -q 'redis'", stdout: "INSTALLED", exit: 0},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"package": "redis", "installed": true}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})

	t.Run("installed mismatch", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "rpm -q 'redis'", stdout: "ABSENT", exit: 0},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"package": "redis", "installed": true}})
		if res.Status != kit.StatusFail {
			t.Errorf("expected fail, got %+v", res)
		}
	})

	t.Run("version list match", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "rpm -q 'redis' >/dev/null", stdout: "INSTALLED", exit: 0},
			{matchPrefix: "rpm -q --qf '%{VERSION}", stdout: "7.0.5\n", exit: 0},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"package": "redis", "version": []any{"7.0.5", "7.0.6"}}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
}
