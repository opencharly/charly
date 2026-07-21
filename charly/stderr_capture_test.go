package main

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns whatever was
// written. Shared test helper (formerly defined in tunnel_test.go, which relocated to
// sdk/deploykit alongside the tunnel resolution functions it exercised — FLOOR-SLIM
// mechanical batch); arbiter_dispatch_test.go and reverse_ops_test.go also depend on it,
// so it stays here as charly's own copy (R3: one copy per package, not shared across
// the module boundary since these tests are charly-internal).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	_ = w.Close()
	<-done
	return buf.String()
}
