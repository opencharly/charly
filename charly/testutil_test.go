package main

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns whatever
// was written. Shared by arbiter_dispatch_test.go and reverse_ops_test.go
// (K4 lane B: formerly defined in tunnel_test.go, which moved wholesale to
// sdk/kit/tunnel_metadata_test.go along with the port-mapping functions it tested;
// this generic capture helper stayed behind for its other charly-core callers).
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
