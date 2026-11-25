package main

import (
	"context"
	"errors"
	"testing"

	"github.com/opencharly/spec/spec"
)

// captureTarget is a LifecycleTarget that captures: the shape pluginDeployTarget has.
type captureTarget struct {
	spec.LifecycleTarget
	out  string
	code int
	ran  []string
}

func (c *captureTarget) shellCapture(_ context.Context, cmd []string) (string, int, error) {
	c.ran = cmd
	return c.out, c.code, nil
}

func (c *captureTarget) Shell(context.Context, []string) error {
	panic("a capturing target must not be relayed to Shell")
}

// relayTarget is a LifecycleTarget without the capture capability.
type relayTarget struct {
	spec.LifecycleTarget
	err error
	ran []string
}

func (r *relayTarget) Shell(_ context.Context, cmd []string) error {
	r.ran = cmd
	return r.err
}

// TestRunServiceCapture proves the two arms: a capturing target hands back the table and the exit
// code without printing, and a non-capturing one keeps the relay-and-error behaviour.
func TestRunServiceCapture(t *testing.T) {
	argv := []string{"podman", "exec", "immich-cdi", "supervisorctl", "status"}

	capt := &captureTarget{out: "immich-db-init EXITED Nov 24\n", code: 3}
	out, code, captured, err := runServiceCapture(capt, argv)
	if err != nil {
		t.Fatalf("capturing target returned %v", err)
	}
	if !captured || out != capt.out || code != 3 {
		t.Fatalf("capture = (%q, %d, %v), want (%q, 3, true)", out, code, captured, capt.out)
	}
	if len(capt.ran) != len(argv) || capt.ran[0] != argv[0] {
		t.Fatalf("argv reached the target as %v", capt.ran)
	}

	relayErr := errors.New(`command "service": exit code 3`)
	relay := &relayTarget{err: relayErr}
	out, code, captured, err = runServiceCapture(relay, argv)
	if !errors.Is(err, relayErr) {
		t.Fatalf("relaying target must keep its error, got %v", err)
	}
	if captured || out != "" || code != 0 {
		t.Fatalf("a relaying target must not report a captured reply, got (%q, %d, %v)", out, code, captured)
	}
	if len(relay.ran) != len(argv) {
		t.Fatalf("argv did not reach the relaying target: %v", relay.ran)
	}
}
