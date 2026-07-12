package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// recordingScpExec is a DeployExecutor fake that records the PutFile +
// ResolveHome arguments scpToVmExec produces, so the tilde-resolution / mode /
// USER-owned-delivery wiring is verified without a live VM.
type recordingScpExec struct {
	homeReturn string
	homeCalls  int

	putCalled bool
	putLocal  string
	putRemote string
	putMode   uint32
	putRoot   bool
}

func (r *recordingScpExec) Venue() string                                     { return "ssh://fake" }
func (r *recordingScpExec) RunSystem(context.Context, string, EmitOpts) error { return nil }
func (r *recordingScpExec) RunUser(context.Context, string, EmitOpts) error   { return nil }
func (r *recordingScpExec) RunBuilder(context.Context, BuilderRunOpts) ([]byte, error) {
	return nil, nil
}
func (r *recordingScpExec) GetFile(context.Context, string, bool, EmitOpts) ([]byte, error) {
	return nil, nil
}
func (r *recordingScpExec) RunCapture(context.Context, string) (string, string, int, error) {
	return "", "", 0, nil
}
func (r *recordingScpExec) Kind() string { return "vm" }
func (r *recordingScpExec) ResolveHome(context.Context, string) (string, error) {
	r.homeCalls++
	return r.homeReturn, nil
}
func (r *recordingScpExec) PutFile(_ context.Context, local, remote string, mode uint32, root bool, _ EmitOpts) error {
	r.putCalled = true
	r.putLocal = local
	r.putRemote = remote
	r.putMode = mode
	r.putRoot = root
	return nil
}

// srcFile writes a temp file with the given perm and returns its absolute path.
func srcFile(t *testing.T, perm os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cred.json")
	if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.Chmod(p, perm); err != nil {
		t.Fatalf("chmod src: %v", err)
	}
	return p
}

func TestScpToVmExec_TildeResolvesAgainstGuestHome(t *testing.T) {
	src := srcFile(t, 0o644)
	fe := &recordingScpExec{homeReturn: "/home/ai"}

	if err := scpToVmExec(context.Background(), fe, src, "~/.claude/.credentials.json", ""); err != nil {
		t.Fatalf("scpToVmExec: %v", err)
	}
	if fe.homeCalls != 1 {
		t.Errorf("ResolveHome calls = %d, want 1 (tilde dst must resolve guest $HOME)", fe.homeCalls)
	}
	if want := "/home/ai/.claude/.credentials.json"; fe.putRemote != want {
		t.Errorf("PutFile remote = %q, want %q", fe.putRemote, want)
	}
	if fe.putLocal != src {
		t.Errorf("PutFile local = %q, want %q", fe.putLocal, src)
	}
	if fe.putRoot {
		t.Error("PutFile ownerRoot = true, want false (credentials must be USER-owned)")
	}
	if fe.putMode != 0o644 {
		t.Errorf("PutFile mode = %o, want 0644 (preserved from source)", fe.putMode)
	}
}

func TestScpToVmExec_AbsoluteDstSkipsHomeResolve(t *testing.T) {
	src := srcFile(t, 0o600)
	fe := &recordingScpExec{homeReturn: "/home/ai"}

	if err := scpToVmExec(context.Background(), fe, src, "/etc/foo/bar.json", ""); err != nil {
		t.Fatalf("scpToVmExec: %v", err)
	}
	if fe.homeCalls != 0 {
		t.Errorf("ResolveHome calls = %d, want 0 (absolute dst must not resolve $HOME)", fe.homeCalls)
	}
	if fe.putRemote != "/etc/foo/bar.json" {
		t.Errorf("PutFile remote = %q, want /etc/foo/bar.json", fe.putRemote)
	}
}

func TestScpToVmExec_ModeStringOverridesSource(t *testing.T) {
	src := srcFile(t, 0o644)
	fe := &recordingScpExec{homeReturn: "/home/ai"}

	if err := scpToVmExec(context.Background(), fe, src, "/tmp/x", "0600"); err != nil {
		t.Fatalf("scpToVmExec: %v", err)
	}
	if fe.putMode != 0o600 {
		t.Errorf("PutFile mode = %o, want 0600 (modeStr override)", fe.putMode)
	}
}

func TestScpToVmExec_DirectorySourceRejected(t *testing.T) {
	fe := &recordingScpExec{}
	if err := scpToVmExec(context.Background(), fe, t.TempDir(), "/tmp/x", ""); err == nil {
		t.Fatal("scpToVmExec on a directory source: want error, got nil")
	}
	if fe.putCalled {
		t.Error("PutFile was called for a directory source; want early rejection")
	}
}
