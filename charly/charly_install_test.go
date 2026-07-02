package main

import (
	"context"
	"strings"
	"testing"
)

// recordingExecutor is a DeployExecutor that returns a single canned RunCapture
// stdout (the guest's `charly version` probe output) and records every PutFile call,
// so EnsureCharlyInVenue's decision (use the system charly vs deliver a /tmp copy) can be
// asserted without a real pod/VM. Distinct from checkrun_test.go's fakeExecutor.
//
// tmpExists models the idempotency probe ("/tmp/charly-<calver> version"): when
// false (default) the probe reports "absent" (exit 1) so a fresh copy is
// delivered; when true it reports "present" (exit 0) so the prior copy is reused.
type recordingExecutor struct {
	captureOut  string
	tmpExists   bool
	putFiles    []putFileCall
	runSystems  []string
	runCaptures []string
}

type putFileCall struct {
	local, remote string
	mode          uint32
	ownerRoot     bool
}

func (e *recordingExecutor) RunCapture(_ context.Context, script string) (string, string, int, error) {
	e.runCaptures = append(e.runCaptures, script)
	// The idempotency probe ("/tmp/charly-<calver> version") reports whether a prior
	// /tmp copy already exists and runs; model it via tmpExists.
	if strings.Contains(script, "/tmp/charly-") {
		if e.tmpExists {
			return "", "", 0, nil
		}
		return "", "", 1, nil
	}
	return e.captureOut, "", 0, nil
}
func (e *recordingExecutor) PutFile(_ context.Context, local, remote string, mode uint32, ownerRoot bool, _ EmitOpts) error {
	e.putFiles = append(e.putFiles, putFileCall{local, remote, mode, ownerRoot})
	return nil
}
func (e *recordingExecutor) RunSystem(_ context.Context, script string, _ EmitOpts) error {
	e.runSystems = append(e.runSystems, script)
	return nil
}
func (e *recordingExecutor) Venue() string                                         { return "rec" }
func (e *recordingExecutor) Kind() string                                          { return "vm" }
func (e *recordingExecutor) RunUser(_ context.Context, _ string, _ EmitOpts) error { return nil }
func (e *recordingExecutor) RunBuilder(_ context.Context, _ BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (e *recordingExecutor) GetFile(_ context.Context, _ string, _ bool, _ EmitOpts) ([]byte, error) {
	return nil, nil
}
func (e *recordingExecutor) ResolveHome(_ context.Context, _ string) (string, error) {
	return "/home/fake", nil
}

// TestEnsureCharlyInVenue covers the generic venue-agnostic charly resolver: use the
// venue's SYSTEM charly when it is current (>= host by CalVer; never shadow, never
// downgrade); deliver the host binary to a /tmp path (outside $PATH) ONLY when
// the venue charly is absent or older.
func TestEnsureCharlyInVenue(t *testing.T) {
	saved := BuildCalVer
	defer func() { BuildCalVer = saved }()
	BuildCalVer = "2026.154.1000" // host identity

	tests := []struct {
		name       string
		captureOut string // guest `charly version` probe output
		wantCmd    string // "charly" or the /tmp path
		wantPush   bool   // scp of a /tmp copy
	}{
		{"venue older → /tmp copy", "2026.154.0900\n", "/tmp/charly-2026.154.1000", true},
		{"venue absent → /tmp copy", "", "/tmp/charly-2026.154.1000", true},
		{"venue equal → system charly (no scp)", "2026.154.1000\n", "charly", false},
		{"venue strictly newer → system charly (no downgrade)", "2026.155.0010\n", "charly", false},
		{"venue 'unknown' (unstamped) → /tmp copy", "unknown\n", "/tmp/charly-2026.154.1000", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ex := &recordingExecutor{captureOut: tt.captureOut}
			cmd, err := EnsureCharlyInVenue(context.Background(), ex, EmitOpts{})
			if err != nil {
				t.Fatalf("EnsureCharlyInVenue: %v", err)
			}
			if cmd != tt.wantCmd {
				t.Errorf("charlyCmd = %q, want %q", cmd, tt.wantCmd)
			}
			gotPush := false
			for _, p := range ex.putFiles {
				if strings.HasPrefix(p.remote, "/tmp/charly-") {
					gotPush = true
					if p.remote != tt.wantCmd {
						t.Errorf("pushed to %q, want %q", p.remote, tt.wantCmd)
					}
					if p.ownerRoot || p.mode != 0o755 {
						t.Errorf("temp push ownerRoot=%v mode=%o, want non-root + 0755", p.ownerRoot, p.mode)
					}
				}
				if p.remote == "/usr/local/bin/charly" {
					t.Errorf("must NEVER write /usr/local/bin/charly (shadows the system charly); got %+v", p)
				}
			}
			if gotPush != tt.wantPush {
				t.Errorf("temp push=%v, want %v (putFiles=%+v)", gotPush, tt.wantPush, ex.putFiles)
			}
		})
	}
}

// TestEnsureCharlyInVenue_Idempotent verifies that when a prior /tmp copy already
// exists and runs (the venue charly is absent, but /tmp/charly-<calver> is present), the
// resolver reuses it WITHOUT re-transferring the 27 MB binary.
func TestEnsureCharlyInVenue_Idempotent(t *testing.T) {
	saved := BuildCalVer
	defer func() { BuildCalVer = saved }()
	BuildCalVer = "2026.154.1000"

	ex := &recordingExecutor{captureOut: "", tmpExists: true} // venue charly absent, prior /tmp copy present
	cmd, err := EnsureCharlyInVenue(context.Background(), ex, EmitOpts{})
	if err != nil {
		t.Fatalf("EnsureCharlyInVenue: %v", err)
	}
	if cmd != "/tmp/charly-2026.154.1000" {
		t.Errorf("charlyCmd = %q, want the reused /tmp path", cmd)
	}
	if len(ex.putFiles) != 0 {
		t.Errorf("reused prior copy must NOT re-transfer; got PutFile calls %+v", ex.putFiles)
	}
}
