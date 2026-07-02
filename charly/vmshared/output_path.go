package vmshared

// Shared "`-` means stdout" convention for file-output args across
// charly verbs. Used by the screenshot-writing check verbs (the cdp/vnc/libvirt
// screenshot plugin verbs, all out-of-process) and anything else that writes a
// binary artifact.
//
// Guidelines for callers:
//   - Human-readable status messages (byte counts, dimensions) go to
//     stderr, not stdout — otherwise they pollute piped binary output.
//   - When the returned writer is an *os.File (i.e. path != "-"),
//     callers own closing it.

import (
	"io"
	"os"
)

// writerForPath opens `path` for writing, or returns os.Stdout when
// path is "-". The returned writer is an io.Closer when it is a
// real file; callers MUST type-assert and close in that case.
//
// Panics are avoided — on error, os.Create's error bubbles up to
// caller as a nil writer + non-nil error. (Signature returns only a
// Writer so callers can skip error handling for the common path;
// use openOutputPath when you want the error.)
func writerForPath(path string) io.Writer {
	if path == "-" {
		return os.Stdout
	}
	f, err := os.Create(path)
	if err != nil {
		// Fall back: write to /dev/null equivalent so callers don't
		// crash. The error would have been visible if they used
		// openOutputPath. This covers convenience helpers where the
		// caller wants a no-op writer on failure.
		return io.Discard
	}
	return f
}

// openOutputPath is the error-returning form of writerForPath.
// Callers that care about permission errors should use this.
// When path is "-", returns os.Stdout and a nil close func.
func openOutputPath(path string) (io.Writer, func() error, error) {
	if path == "-" {
		return os.Stdout, func() error { return nil }, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}
