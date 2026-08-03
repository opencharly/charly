package command

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// fakeExec is a kit.Executor returning a canned stdout + exit — the in-container command
// RunCapture leg.
type fakeExec struct {
	stdout string
	exit   int
}

func (f *fakeExec) RunCapture(context.Context, string) (string, string, int, error) {
	return f.stdout, "", f.exit, nil
}
func (f *fakeExec) Kind() string { return "container" }

// fakeCC is a minimal kit.CheckContext exercising the command verb's Exec + Mode legs.
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

func runCommandVerb(exit int, input map[string]any, exitStatus *int) kit.Result {
	cc := &fakeCC{mode: kit.ModeLive, exec: &fakeExec{exit: exit}}
	return verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: input, ExitStatus: exitStatus})
}

// TestCommandVerb_ExpectNonZero_PassesOnFailure proves expect_non_zero PASSES when the command
// exits non-zero (whatever the code) — the assertion that had no expression before this field.
func TestCommandVerb_ExpectNonZero_PassesOnFailure(t *testing.T) {
	for _, exit := range []int{1, 2, 127} {
		res := runCommandVerb(exit, map[string]any{"command": "false", "expect_non_zero": true}, nil)
		if res.Status != kit.StatusPass {
			t.Fatalf("expect_non_zero exit=%d: want pass, got %v: %s", exit, res.Status, res.Message)
		}
	}
}

// TestCommandVerb_ExpectNonZero_FailsOnSuccess proves expect_non_zero FAILS when the command
// unexpectedly succeeds (exit 0) — the negative arm that makes the field a real assertion.
func TestCommandVerb_ExpectNonZero_FailsOnSuccess(t *testing.T) {
	res := runCommandVerb(0, map[string]any{"command": "true", "expect_non_zero": true}, nil)
	if res.Status != kit.StatusFail {
		t.Fatalf("expect_non_zero exit=0: want fail, got %v: %s", res.Status, res.Message)
	}
}

// TestCommandVerb_ExactCodeUnaffected proves the historic exit_status behaviour is unchanged when
// expect_non_zero is absent: default asserts 0, and an explicit exit_status asserts that code.
func TestCommandVerb_ExactCodeUnaffected(t *testing.T) {
	if res := runCommandVerb(0, map[string]any{"command": "true"}, nil); res.Status != kit.StatusPass {
		t.Fatalf("default exit_status=0, exit=0: want pass, got %v: %s", res.Status, res.Message)
	}
	if res := runCommandVerb(1, map[string]any{"command": "false"}, nil); res.Status != kit.StatusFail {
		t.Fatalf("default exit_status=0, exit=1: want fail, got %v: %s", res.Status, res.Message)
	}
	want3 := 3
	if res := runCommandVerb(3, map[string]any{"command": "exit 3"}, &want3); res.Status != kit.StatusPass {
		t.Fatalf("exit_status=3, exit=3: want pass, got %v: %s", res.Status, res.Message)
	}
}

// runCommandVerbOut is runCommandVerb's stdout-carrying variant, for matcher assertions
// (Stdout equals/contains/matches) rather than a bare exit-status check.
func runCommandVerbOut(stdout string, exit int, input map[string]any) kit.Result {
	cc := &fakeCC{mode: kit.ModeLive, exec: &fakeExec{stdout: stdout, exit: exit}}
	return verb{}.RunVerb(context.Background(), cc, &spec.Op{PluginInput: input})
}

// TestCommandVerb_StdoutMatchers: exit/stdout matcher evaluation. Relocated from
// charly/checkrun_test.go's TestRunner_CommandVerb (#55 decoupling cone, Batch D) —
// mirrors candy/plugin-port and candy/plugin-http's own test pattern (R3), and reuses this
// file's OWN existing fakeCC/fakeExec/runCommandVerb fixtures (extended with a stdout
// field) rather than introducing a parallel fixture shape.
func TestCommandVerb_StdoutMatchers(t *testing.T) {
	t.Run("exit ok stdout equals", func(t *testing.T) {
		op := map[string]any{"command": "redis-cli ping"}
		res := verb{}.RunVerb(context.Background(), &fakeCC{mode: kit.ModeLive, exec: &fakeExec{stdout: "PONG\n", exit: 0}},
			&spec.Op{PluginInput: op, Stdout: spec.MatcherList{{Op: "equals", Value: "PONG"}}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})

	t.Run("stdout contains list", func(t *testing.T) {
		op := map[string]any{"command": "status"}
		res := verb{}.RunVerb(context.Background(), &fakeCC{mode: kit.ModeLive, exec: &fakeExec{stdout: "ready ok running", exit: 0}},
			&spec.Op{PluginInput: op, Stdout: spec.MatcherList{{Op: "contains", Value: []any{"ready", "ok"}}}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})

	t.Run("exit mismatch", func(t *testing.T) {
		res := runCommandVerbOut("", 2, map[string]any{"command": "fail-cmd"})
		if res.Status != kit.StatusFail || !strings.Contains(res.Message, "exit=2") {
			t.Errorf("expected exit failure, got %+v", res)
		}
	})

	t.Run("matches regex", func(t *testing.T) {
		op := map[string]any{"command": "uptime"}
		res := verb{}.RunVerb(context.Background(), &fakeCC{mode: kit.ModeLive, exec: &fakeExec{stdout: "load average: 0.12 0.34 0.56\n", exit: 0}},
			&spec.Op{PluginInput: op, Stdout: spec.MatcherList{{Op: "matches", Value: `load average: [\d.]+`}}})
		if res.Status != kit.StatusPass {
			t.Errorf("expected pass, got %+v", res)
		}
	})
}
