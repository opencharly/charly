package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/opencharly/spec/spec"
)

// host_build_cli.go — the generic "cli" F10 host-builder (M4). A lifecycle plugin
// (candy/plugin-deploy-pod / -vm), running ON the host but out-of-process, asks the host to run a
// `charly <argv>` subcommand via Executor.HostBuild("cli", spec.CliRequest{...}). The handler runs
// in the CHARLY process (os.Args[0] IS charly, which owns the terminal), so Capture=false inherits
// the host's stdin/stdout/stderr (the interactive legs: charly shell, logs -f — the "exec lane for
// TTY" doctrine inverted) and Capture=true captures stdout (short results the plugin parses). It is
// the lifecycle counterpart of the "overlay"/"image"/"plugin-binary" host-builders — a generic
// action noun, NOT a provider WORD (the F11 uniform-API gate forbids one). It replaces the in-core
// subprocess plumbing (run_subcommand.go) the compiled-in pod/vm lifecycles used before M4.
const cliBuilderKind = "cli"

// hostBuildCli runs a `charly <argv>` subcommand host-side and returns the CliReply. A non-zero exit
// rides CliReply.Error unless BestEffort. The context is unused (an interactive leg must not be
// deadlined — the host TTY owns its lifetime, like the operator running the command directly).
func hostBuildCli(_ context.Context, req spec.CliRequest, _ buildEngineContext) (spec.CliReply, error) {
	executable, err := os.Executable()
	if err != nil {
		return spec.CliReply{ExitCode: -1, Error: fmt.Sprintf("resolve charly executable: %v", err)}, nil
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return spec.CliReply{ExitCode: -1, Error: fmt.Sprintf("resolve absolute charly executable path: %v", err)}, nil
	}
	return runCliSubcommand(executable, req), nil
}

func runCliSubcommand(executable string, req spec.CliRequest) spec.CliReply {
	cmd := exec.Command(executable, req.Argv...)
	cmd.Stdin = os.Stdin

	var stdoutOnly bytes.Buffer
	var combined *combinedLineCapture
	switch {
	case req.Capture && req.Combined:
		// MERGE stderr into the captured text — a `charly check …` child writes its results to
		// STDERR, so the check-bed plugin's per-step .log needs the combined stream to match the
		// pre-relocation core runCapture (which captured combined output). Each stream gets its
		// OWN writer so os/exec gives the child two distinct pipes; combinedLineCapture reunites
		// them a whole line at a time. Handing ONE writer to both fields instead would make
		// exec's interfaceEqual(Stderr, Stdout) fire and dup a SINGLE pipe onto the child's fd 1
		// and fd 2 — every process in the subtree then writes to one fd with no line discipline,
		// and a partial line from one stream splices into the middle of the other's.
		combined = &combinedLineCapture{}
		cmd.Stdout, cmd.Stderr = combined.stream(), combined.stream()
	case req.Capture:
		cmd.Stdout, cmd.Stderr = &stdoutOnly, os.Stderr
	default:
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}

	err := cmd.Run()

	var reply spec.CliReply
	switch {
	case combined != nil:
		// Safe only here: Run→Wait→awaitGoroutines has joined every copier goroutine, so no
		// stream writer can still be running when the trailing partial lines are flushed.
		reply.Stdout = combined.Text()
	case req.Capture:
		reply.Stdout = stdoutOnly.String()
	}
	reply.ExitCode, reply.Error = cliExitResult(err, req.BestEffort)
	return reply
}

// combinedLineCapture merges a child's stdout and stderr into ONE captured text while keeping
// every LINE intact, so the result can be analysed line-by-line (the check-bed .log files are
// scanned for warning/error lines, and a spliced line both invents a phantom warning and hides a
// real one). Each stream is handed its own stream() writer, which holds back a trailing partial
// line until its newline arrives; only whole lines reach the shared buffer, under the mutex.
//
// Ordering: within EITHER stream, order is exact. ACROSS the two streams, lines land in the order
// they were completed by the two copier goroutines — near-chronological, but two lines emitted at
// nearly the same instant on different streams may swap. That skew is the unavoidable price of
// splitting the fds; the original single-fd wiring kept exact kernel write order but paid for it
// with lines that were not lines.
type combinedLineCapture struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	streams []*streamLineWriter
}

// stream returns a fresh io.Writer for one of the child's output streams. Every call MUST return
// a DISTINCT pointer: two equal writers would collapse back to one shared child fd (see the
// interfaceEqual note in runCliSubcommand).
func (c *combinedLineCapture) stream() *streamLineWriter {
	w := &streamLineWriter{sink: c}
	c.streams = append(c.streams, w)
	return w
}

// emit appends one complete line (newline included) to the shared buffer.
func (c *combinedLineCapture) emit(line []byte) {
	c.mu.Lock()
	c.buf.Write(line)
	c.mu.Unlock()
}

// Text flushes each stream's held-back trailing partial line and returns the merged output. Call
// it only after the child has been waited for — a stream writer must not be running concurrently.
func (c *combinedLineCapture) Text() string {
	for _, w := range c.streams {
		w.flushPartial()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// streamLineWriter is the per-stream io.Writer. os/exec's copier goroutine hands it arbitrary
// chunks that routinely end mid-line, so it buffers the tail and forwards only complete lines.
// Exactly one goroutine writes to a given streamLineWriter, so pending needs no lock of its own.
type streamLineWriter struct {
	sink    *combinedLineCapture
	pending []byte
}

func (w *streamLineWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		nl := bytes.IndexByte(w.pending, '\n')
		if nl < 0 {
			break
		}
		w.sink.emit(w.pending[:nl+1])
		w.pending = w.pending[nl+1:]
	}
	return len(p), nil
}

// flushPartial emits a final line that the child never terminated with a newline.
func (w *streamLineWriter) flushPartial() {
	if len(w.pending) == 0 {
		return
	}
	w.sink.emit(w.pending)
	w.pending = nil
}

// cliExitResult maps an exec error to (exitCode, errString): clean → (0, ""); non-zero exit →
// (code, "" if bestEffort else a message); a spawn failure → (-1, message).
func cliExitResult(err error, bestEffort bool) (int, string) {
	if err == nil {
		return 0, ""
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if bestEffort {
			return ee.ExitCode(), ""
		}
		return ee.ExitCode(), fmt.Sprintf("charly subcommand exited %d", ee.ExitCode())
	}
	return -1, err.Error()
}

// Register the cli host-builder on the F10 HostBuild seam at package-var init (before any init(),
// like the substrate/preresolver registries + the overlay/image builders).
var _ = func() bool {
	registerHostBuilder(cliBuilderKind, typedHostBuilder(cliBuilderKind, hostBuildCli))
	return true
}()
