package file

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeExec is a kit.Executor returning canned RunCapture output by command prefix (the
// file-existence/stat probe + `cat` content read).
type fakeExec struct {
	responses []fakeResponse
}

type fakeResponse struct {
	matchPrefix, stdout string
}

func (f *fakeExec) RunCapture(_ context.Context, cmd string) (string, string, int, error) {
	for _, r := range f.responses {
		if strings.HasPrefix(cmd, r.matchPrefix) || strings.Contains(cmd, r.matchPrefix) {
			return r.stdout, "", 0, nil
		}
	}
	return "", "no fake response for: " + cmd, 127, nil
}
func (f *fakeExec) Kind() string { return "container" }

// fakeCC is a fake kit.CheckContext exercising the file verb's Exec leg.
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

// TestFileVerb: exists pass, mode mismatch, filetype check, missing file, bare-scalar
// contains-as-substring. Relocated from charly/checkrun_test.go's TestRunner_FileVerb (#55
// decoupling cone, Batch D) — mirrors candy/plugin-port and candy/plugin-http's own test
// pattern (R3).
func TestFileVerb(t *testing.T) {
	t.Run("exists true, mode ok", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|755|root|root\n"},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"file": "/usr/bin/redis-server", "exists": true, "mode": "0755", "filetype": "file"}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})

	t.Run("mode mismatch", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|755|root|root\n"},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"file": "/x", "mode": "0644"}})
		if res.Status != kit.StatusFail || !strings.Contains(res.Message, "mode") {
			t.Errorf("expected mode failure, got %+v", res)
		}
	})

	t.Run("absent as expected", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=0||||\n"},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"file": "/nope", "exists": false}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass for absent-as-expected, got %+v", res)
		}
	})

	t.Run("exists false but file present", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|644|root|root\n"},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"file": "/x", "exists": false}})
		if res.Status != kit.StatusFail {
			t.Errorf("expected fail, got %+v", res)
		}
	})

	t.Run("contains bare scalar defaults to substring", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|644|root|root\n"},
			{matchPrefix: "cat ", stdout: "line one\nfsfreeze-hook.d here\nline three\n"},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"file": "/etc/x", "contains": "fsfreeze-hook.d"}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass (bare-scalar contains = substring), got %+v", res)
		}
	})

	t.Run("owner match", func(t *testing.T) {
		cc := &fakeCC{exec: &fakeExec{responses: []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|644|root|root\n"},
		}}}
		res := verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: map[string]any{"file": "/etc/hostname", "exists": true, "mode": "644", "owner": "root", "filetype": "file"}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass (owner match), got %+v", res)
		}
	})
}

// TestFileVerb_RenderProvisionScript: the ACT role renders an idempotent RUNTIME
// file-creation — mkdir + cat-heredoc (content-bearing) + chmod. Relocated from
// charly/plugin_file_relocated_test.go's TestRelocatedFileVerb_DispatchesViaKit (the
// act-role behavior half; the dispatch wiring stays in charly).
func TestFileVerb_RenderProvisionScript(t *testing.T) {
	script, ok := verb{}.RenderProvisionScript(
		&spec.Op{PluginInput: map[string]any{"file": "/etc/motd", "mode": "644"}, Content: "hello"}, nil)
	if !ok || !strings.Contains(script, "/etc/motd") || !strings.Contains(script, "chmod") {
		t.Fatalf("act: want a file-creation script, got ok=%v %q", ok, script)
	}
}

// TestDecodeContainsList covers the file verb's contains-default codec — relocated with
// the verb into this candy. A BARE scalar element defaults to Op="contains" (substring
// match), while an explicit single-operator map keeps its authored operator. The input is
// the gengotypes-degraded `any` shape a `plugin_input.contains` decodes to.
func TestDecodeContainsList(t *testing.T) {
	tests := []struct {
		name      string
		in        any
		wantOps   []string
		wantValue []any
	}{
		{"bare scalar promotes to contains", "foo", []string{"contains"}, []any{"foo"}},
		{"bare sequence promotes each element to contains", []any{"foo", "bar"}, []string{"contains", "contains"}, []any{"foo", "bar"}},
		{"explicit equals map keeps Op=equals", map[string]any{"equals": "foo"}, []string{"equals"}, []any{"foo"}},
		{"explicit matches map keeps Op=matches", map[string]any{"matches": "^prefix"}, []string{"matches"}, []any{"^prefix"}},
		{"explicit not_contains map keeps Op=not_contains", []any{map[string]any{"not_contains": "nope"}}, []string{"not_contains"}, []any{"nope"}},
		{"mixed sequence: explicit equals + bare scalar", []any{map[string]any{"equals": "foo"}, "bar"}, []string{"equals", "contains"}, []any{"foo", "bar"}},
		{"real-world marker list defaults to contains", []any{"charly-fixture-web-content-marker"}, []string{"contains"}, []any{"charly-fixture-web-content-marker"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeContainsList(tc.in)
			if len(got) != len(tc.wantOps) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(tc.wantOps), got)
			}
			for i := range got {
				if got[i].Op != tc.wantOps[i] {
					t.Errorf("[%d].Op = %q, want %q", i, got[i].Op, tc.wantOps[i])
				}
				if !reflect.DeepEqual(got[i].Value, tc.wantValue[i]) {
					t.Errorf("[%d].Value = %v (%T), want %v (%T)", i, got[i].Value, got[i].Value, tc.wantValue[i], tc.wantValue[i])
				}
			}
		})
	}
}

// TestDecodeContainsList_Nil ensures an absent contains decodes to a nil list.
func TestDecodeContainsList_Nil(t *testing.T) {
	if got := decodeContainsList(nil); got != nil {
		t.Errorf("decodeContainsList(nil) = %v, want nil", got)
	}
}
