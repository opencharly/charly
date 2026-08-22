package container

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStreamLoad_PipesPayload asserts the primitive actually MOVES the bytes — the
// property every image-delivery path in the tree rests on. It discriminates: a
// StreamLoad that started both commands but never wired the pipe would leave the
// destination file empty and fail here.
func TestStreamLoad_PipesPayload(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out")
	const payload = "layer-blob-bytes"

	save := exec.Command("printf", "%s", payload)
	load := exec.Command("sh", "-c", "cat > "+dst)

	if err := StreamLoad(save, load); err != nil {
		t.Fatalf("StreamLoad: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("destination = %q, want %q", got, payload)
	}
}

// TestStreamLoad_ReportsSaveFailure asserts a failing SAVE side is surfaced rather
// than masked by a load side that exits 0 regardless. This is the real hazard: the
// load process happily reports success on a short stream, so a caller that only
// watched the load exit code would call a truncated transfer a success.
func TestStreamLoad_ReportsSaveFailure(t *testing.T) {
	save := exec.Command("sh", "-c", "exit 3")
	load := exec.Command("cat")

	err := StreamLoad(save, load)
	if err == nil {
		t.Fatal("StreamLoad returned nil for a failing save side")
	}
	if !strings.Contains(err.Error(), "save failed") {
		t.Fatalf("error %q does not name the save side", err)
	}
}

// TestStreamLoad_ReportsLoadFailure asserts a failing LOAD side is surfaced even
// when the save side succeeds.
func TestStreamLoad_ReportsLoadFailure(t *testing.T) {
	save := exec.Command("printf", "x")
	load := exec.Command("sh", "-c", "cat >/dev/null; exit 4")

	err := StreamLoad(save, load)
	if err == nil {
		t.Fatal("StreamLoad returned nil for a failing load side")
	}
	if !strings.Contains(err.Error(), "load failed") {
		t.Fatalf("error %q does not name the load side", err)
	}
}

// TestStreamLoad_EarlyLoadExitDoesNotHang is the regression guard for a HANG, not a
// wrong result — the failure mode a timeout-less caller can never recover from.
//
// save.StdoutPipe() leaves the read end open IN THE PARENT until save.Wait() returns.
// So if the load side exits before draining, nothing ever gives save an EPIPE: it
// blocks once the kernel pipe buffer fills, and save.Wait() blocks behind it forever.
// StreamLoad closes the parent's copy once the load child has inherited its own, which
// turns that deadlock into a named error.
//
// The payload must exceed the pipe buffer (64 KiB on Linux) for the write to block at
// all; 50 MiB is comfortably past it. Without the fix this test does not fail — it
// hangs until the go test timeout, which is exactly why it is written with its own
// deadline instead of a plain call.
func TestStreamLoad_EarlyLoadExitDoesNotHang(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		save := exec.Command("sh", "-c", "head -c 52428800 /dev/zero")
		load := exec.Command("sh", "-c", "exit 0") // exits without reading a byte
		done <- StreamLoad(save, load)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("StreamLoad returned nil though the load side never read the stream")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("StreamLoad hung: the load side exited early and save never saw EPIPE")
	}
}
