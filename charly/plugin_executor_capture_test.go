package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
)

// TestExecutorReverse_CaptureAndGetFile proves the new CHECK-VERB reverse-channel legs:
// executorReverseServer.RunCapture returns stdout/stderr/exit separately (a non-zero exit
// rides ExitCode, NOT Error) and GetFile reads a venue file — the wire-backed
// kit.Executor.RunCapture/GetFile surface an out-of-process exec-based check verb
// (record/dbus — and wl when it externalizes) drives over the E3b broker. Backed by a real
// ShellExecutor (host venue).
func TestExecutorReverse_CaptureAndGetFile(t *testing.T) {
	srv := &executorReverseServer{exec: kit.ShellExecutor{}}
	ctx := context.Background()

	rep, err := srv.RunCapture(ctx, &pb.RunRequest{Script: "echo hello; echo oops >&2; exit 3"})
	if err != nil {
		t.Fatalf("RunCapture: %v", err)
	}
	if !strings.Contains(rep.GetStdout(), "hello") {
		t.Errorf("stdout = %q, want contains hello", rep.GetStdout())
	}
	if !strings.Contains(rep.GetStderr(), "oops") {
		t.Errorf("stderr = %q, want contains oops", rep.GetStderr())
	}
	if rep.GetExitCode() != 3 {
		t.Errorf("exit = %d, want 3 (a non-zero exit rides ExitCode)", rep.GetExitCode())
	}
	if rep.GetError() != "" {
		t.Errorf("error = %q, want empty (a non-zero exit is not an execution error)", rep.GetError())
	}

	f := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(f, []byte("filebody"), 0o644); err != nil {
		t.Fatal(err)
	}
	fr, err := srv.GetFile(ctx, &pb.GetFileRequest{Path: f})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if string(fr.GetContent()) != "filebody" {
		t.Errorf("content = %q, want filebody", fr.GetContent())
	}
}
