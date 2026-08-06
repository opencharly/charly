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
	stderr      string
	exit        int
}

// fakeExec is a kit.Executor returning canned RunCapture output by command prefix (the
// rpm/dpkg/pacman probe).
type fakeExec struct{ responses []fakeResponse }

func (f *fakeExec) RunCapture(_ context.Context, cmd string) (string, string, int, error) {
	for _, r := range f.responses {
		if strings.HasPrefix(cmd, r.matchPrefix) || strings.Contains(cmd, r.matchPrefix) {
			return r.stdout, r.stderr, r.exit, nil
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

// TestPackageVerb_InfraFailureNotContentFalse proves the package verb distinguishes a
// genuine "not installed" (the probe ran, printed ABSENT) from an EXEC/INFRA failure (the
// podman exec died — store-write error exit 255, killed signal — so no INSTALLED/ABSENT
// token was printed). The infra failure must surface as such, NEVER as the false content
// verdict "installed=false, want true" (the check-{debian,jupyter-ml}-coder
// store-contention mislabel). Relocated from charly/plugin_package_relocated_test.go's
// TestPackageVerb_InfraFailureNotContentFalse (the check-role behavior half; the dispatch
// wiring stays in charly).
func TestPackageVerb_InfraFailureNotContentFalse(t *testing.T) {
	run := func(fe *fakeExec, wantInstalled bool) kit.Result {
		return verb{}.RunVerb(context.Background(), &fakeCC{exec: fe},
			&spec.Op{PluginInput: map[string]any{"package": "bash", "installed": wantInstalled}})
	}

	// Genuine absent: probe printed ABSENT, exit 0. installed:false → pass.
	if res := run(&fakeExec{responses: []fakeResponse{{matchPrefix: "if rpm", stdout: "ABSENT", exit: 0}}}, false); res.Status != kit.StatusPass {
		t.Fatalf("absent-as-expected: want pass, got %v: %s", res.Status, res.Message)
	}
	// Genuine absent but wanted installed → a real content FAIL (installed=false).
	if res := run(&fakeExec{responses: []fakeResponse{{matchPrefix: "if rpm", stdout: "ABSENT", exit: 0}}}, true); res.Status != kit.StatusFail || !strings.Contains(res.Message, "installed=false") {
		t.Fatalf("absent-but-wanted: want a content fail 'installed=false', got %v: %s", res.Status, res.Message)
	}
	// INFRA failure: exec died (exit 255, store error, no token). Must NOT be
	// reported as installed=false — surfaced as an exec/infra failure.
	res := run(&fakeExec{responses: []fakeResponse{{matchPrefix: "if rpm", stdout: "", exit: 255, stderr: "saving container state: writing container"}}}, true)
	if res.Status != kit.StatusFail {
		t.Fatalf("infra failure: want fail, got %v", res.Status)
	}
	if strings.Contains(res.Message, "installed=false") {
		t.Fatalf("infra failure MUST NOT be a false content verdict; got: %s", res.Message)
	}
	if !strings.Contains(res.Message, "exec/infra failure") {
		t.Fatalf("infra failure must be labeled as such; got: %s", res.Message)
	}
}

// TestPackageVerb_RenderProvisionScript: the ACT role renders the install shell under
// whichever package manager the live target runs. Relocated from
// charly/plugin_package_relocated_test.go's TestRelocatedPackageVerb_DispatchesViaKit (the
// act-role behavior half; the dispatch wiring stays in charly).
func TestPackageVerb_RenderProvisionScript(t *testing.T) {
	script, ok := verb{}.RenderProvisionScript(&spec.Op{PluginInput: map[string]any{"package": "bash"}}, []string{"fedora"})
	if !ok || !strings.Contains(script, "dnf install") || !strings.Contains(script, "pacman -S") {
		t.Fatalf("act: want an install shell, got ok=%v %q", ok, script)
	}
}

// TestPackageVerb_StepProvider: the TYPED-STEP role names the SystemPackages step kind and
// decodes plugin_input (package + cross-distro package_map) into the StepDescriptor the
// host materializer consumes. Relocated from charly/plugin_package_relocated_test.go's
// TestRelocatedPackageVerb_DispatchesViaKit (the step-role behavior half; the dispatch
// wiring + the materializer stay in charly).
func TestPackageVerb_StepProvider(t *testing.T) {
	got := verb{}.StepKind()
	if got != kit.StepKindSystemPackages {
		t.Fatalf("StepKind = %v, want StepKindSystemPackages", got)
	}
	desc := verb{}.ConstructStepDescriptor(&spec.Op{PluginInput: map[string]any{"package": "openssh", "package_map": map[string]any{"fedora": "openssh-server"}}})
	if desc.SystemPackages == nil || desc.SystemPackages.Package != "openssh" || desc.SystemPackages.PackageMap["fedora"] != "openssh-server" {
		t.Fatalf("StepDescriptor = %+v, want Package=openssh PackageMap[fedora]=openssh-server", desc)
	}
}
