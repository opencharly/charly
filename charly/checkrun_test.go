package main

import (
	"context"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// fakeExecutor records calls and returns canned results by command-prefix.
// Keeps tests hermetic — no actual podman/docker exec happens.
type fakeExecutor struct {
	responses []fakeResponse
	calls     []string
}

type fakeResponse struct {
	matchPrefix string
	stdout      string
	stderr      string
	exit        int
	err         error
}

// fakeExecutor implements DeployExecutor (post-2026-04 cutover). Only
// RunCapture is exercised by the tests below; the other interface methods
// return zero values so the type still satisfies the interface contract.
func (f *fakeExecutor) RunInteractive(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (f *fakeExecutor) RunStream(context.Context, string) (int, error) {
	return -1, spec.ErrNotSupported
}
func (f *fakeExecutor) RunCapture(ctx context.Context, cmd string) (string, string, int, error) {
	f.calls = append(f.calls, cmd)
	for _, r := range f.responses {
		if strings.HasPrefix(cmd, r.matchPrefix) || strings.Contains(cmd, r.matchPrefix) {
			return r.stdout, r.stderr, r.exit, r.err
		}
	}
	return "", "no fake response registered for: " + cmd, 127, nil
}

func (f *fakeExecutor) Kind() string  { return "fake" }
func (f *fakeExecutor) Venue() string { return "fake" }
func (f *fakeExecutor) ResolveHome(_ context.Context, _ string) (string, error) {
	return "/home/fake", nil
}

// Stubs satisfying the rest of DeployExecutor — never called by these tests.
func (f *fakeExecutor) RunSystem(_ context.Context, _ string, _ EmitOpts) error { return nil }
func (f *fakeExecutor) RunUser(_ context.Context, _ string, _ EmitOpts) error   { return nil }
func (f *fakeExecutor) RunBuilder(_ context.Context, _ BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (f *fakeExecutor) PutFile(_ context.Context, _, _ string, _ uint32, _ bool, _ EmitOpts) error {
	return nil
}
func (f *fakeExecutor) GetFile(_ context.Context, _ string, _ bool, _ EmitOpts) ([]byte, error) {
	return nil, nil
}

func newFakeRunner(t *testing.T, mode RunMode, box ...string) (*kit.Runner, *fakeExecutor) {
	t.Helper()
	fake := &fakeExecutor{}
	cfg := kit.RunnerConfig{Exec: fake, Mode: mode, Env: map[string]string{}}
	if len(box) > 0 {
		cfg.Box = box[0]
	}
	r := newCheckRunner(cfg)
	return r, fake
}

// file plugin verb — exists pass, mode mismatch, filetype check, missing file. Now a
// dedicated built-in plugin unit dispatched IN-PROCESS via the CheckVerbProvider RunVerb
// path (TestMain loads its schema); authored as plugin: file + plugin_input (the
// file-exclusive fields + the shared mode ride plugin_input).
func TestRunner_FileVerb(t *testing.T) {
	t.Run("exists true, mode ok", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|755|root|root\n"},
		}
		results := r.Run(context.Background(), []spec.Op{
			{Plugin: "file", PluginInput: map[string]any{"file": "/usr/bin/redis-server", "exists": true, "mode": "0755", "filetype": "file"}},
		})
		if len(results) != 1 || results[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", results[0])
		}
	})

	t.Run("mode mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|755|root|root\n"},
		}
		results := r.Run(context.Background(), []spec.Op{
			{Plugin: "file", PluginInput: map[string]any{"file": "/x", "mode": "0644"}},
		})
		if results[0].Status != TestFail || !strings.Contains(results[0].Message, "mode") {
			t.Errorf("expected mode failure, got %+v", results[0])
		}
	})

	t.Run("absent as expected", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=0||||\n"},
		}
		results := r.Run(context.Background(), []spec.Op{
			{Plugin: "file", PluginInput: map[string]any{"file": "/nope", "exists": false}},
		})
		if results[0].Status != TestPass {
			t.Errorf("expected pass for absent-as-expected, got %+v", results[0])
		}
	})

	t.Run("exists false but file present", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|644|root|root\n"},
		}
		results := r.Run(context.Background(), []spec.Op{
			{Plugin: "file", PluginInput: map[string]any{"file": "/x", "exists": false}},
		})
		if results[0].Status != TestFail {
			t.Errorf("expected fail, got %+v", results[0])
		}
	})

	t.Run("contains bare scalar defaults to substring", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "if [ -e", stdout: "exists=1|regular file|644|root|root\n"},
			{matchPrefix: "cat ", stdout: "line one\nfsfreeze-hook.d here\nline three\n"},
		}
		results := r.Run(context.Background(), []spec.Op{
			{Plugin: "file", PluginInput: map[string]any{"file": "/etc/x", "contains": "fsfreeze-hook.d"}},
		})
		if results[0].Status != TestPass {
			t.Errorf("expected pass (bare-scalar contains = substring), got %+v", results[0])
		}
	})
}

// port plugin verb — listening via fake ss output, unreachable via dial timeout,
// host-side dial skip under box-test mode. Now a dedicated built-in plugin unit
// dispatched IN-PROCESS via the CheckVerbProvider RunVerb path (TestMain loads its
// schema); authored as plugin: port + plugin_input.
// NOTE: the `port` verb's in-proc dispatch tests moved to candy/plugin-port/plugin_test.go
// when port went OUT-OF-PROCESS (F2) — they now exercise RunVerb against a fake
// kit.CheckContext. The `command` verb stays compiled-in, so its tests stay here.

// command verb — exit/stdout/stderr matchers.
func TestRunner_CommandVerb(t *testing.T) {
	t.Run("exit ok stdout equals", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "redis-cli ping", stdout: "PONG\n", exit: 0},
		}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli ping"}, Stdout: spec.MatcherList{{Op: "equals", Value: "PONG"}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("stdout contains list", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "status", stdout: "ready ok running", exit: 0},
		}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "command", PluginInput: map[string]any{"command": "status"}, Stdout: spec.MatcherList{{Op: "contains", Value: []any{"ready", "ok"}}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})

	t.Run("exit mismatch", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "fail-cmd", exit: 2},
		}
		res := r.Run(context.Background(), []spec.Op{cmdOp("fail-cmd")})
		if res[0].Status != TestFail || !strings.Contains(res[0].Message, "exit=2") {
			t.Errorf("expected exit failure, got %+v", res[0])
		}
	})

	t.Run("matches regex", func(t *testing.T) {
		r, fake := newFakeRunner(t, RunModeLive)
		fake.responses = []fakeResponse{
			{matchPrefix: "uptime", stdout: "load average: 0.12 0.34 0.56\n", exit: 0},
		}
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "command", PluginInput: map[string]any{"command": "uptime"}, Stdout: spec.MatcherList{{Op: "matches", Value: `load average: [\d.]+`}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v", res[0])
		}
	})
}

// NOTE: the `http` verb went OUT-OF-PROCESS (F2). Its HOST-SIDE request execution (status /
// body / custom-timeout / allow-insecure against a real server) is now tested by
// TestDoHTTPRequest in check_http_test.go (doHTTPRequest is the shared host path both the
// in-proc and the reverse-channel HTTPDo legs use); the verb's request-building + matching
// logic is tested in candy/plugin-http/plugin_test.go (RunVerb against a fake
// kit.CheckContext). The full OUT-OF-PROCESS round trip is the check-pod R10 bed.

// Verifies the runner performs ${VAR} expansion before executing, and
// reports unresolved refs as skip.
func TestRunner_VariableExpansion(t *testing.T) {
	t.Run("expanded", func(t *testing.T) {
		fake := &fakeExecutor{}
		fake.responses = []fakeResponse{
			{matchPrefix: "redis-cli -p 16379", stdout: "PONG\n", exit: 0},
		}
		r := newCheckRunner(kit.RunnerConfig{Exec: fake, Mode: RunModeLive, Env: map[string]string{"HOST_PORT:6379": "16379"}})
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli -p ${HOST_PORT:6379}"}, Stdout: spec.MatcherList{{Op: "equals", Value: "PONG"}}},
		})
		if res[0].Status != TestPass {
			t.Errorf("expected pass, got %+v. fake calls: %v", res[0], fake.calls)
		}
	})

	t.Run("unresolved → skip", func(t *testing.T) {
		r, _ := newFakeRunner(t, RunModeLive)
		res := r.Run(context.Background(), []spec.Op{
			{Plugin: "command", PluginInput: map[string]any{"command": "redis-cli -p ${HOST_PORT:6379}"}},
		})
		if res[0].Status != TestSkip || !strings.Contains(res[0].Message, "unresolved") {
			t.Errorf("expected skip with unresolved, got %+v", res[0])
		}
	})
}

// Skip:true short-circuits before any execution.
func TestRunner_SkipFlag(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeLive)
	res := r.Run(context.Background(), []spec.Op{{Plugin: "command", PluginInput: map[string]any{"command": "anything"}, Skip: true}})
	if res[0].Status != TestSkip {
		t.Errorf("expected skip, got %+v", res[0])
	}
}

// Zero-verb check fails with a clear error at runOne.
func TestRunner_EmptyCheck(t *testing.T) {
	r, _ := newFakeRunner(t, RunModeLive)
	res := r.Run(context.Background(), []spec.Op{{}})
	if res[0].Status != TestFail || !strings.Contains(res[0].Message, "no verb") {
		t.Errorf("expected fail with 'no verb', got %+v", res[0])
	}
}

// The package-main result-list formatters were relocated to sdk/kit in the P12 check-engine
// cutover (kit.FormatStepResults*, exercised via reportSteps); their coverage lives with the
// reporters in sdk/kit. No package-main formatter remains to test here.
