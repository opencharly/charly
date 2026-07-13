package check

// stream.go — stream-json stdout parser for the AI runner (P12: relocated verbatim
// from charly/check_runner_stream.go — pure tee-then-parse pipeline, no host
// coupling).
//
// When an AI's `output_format: stream-json` is set, its stdout emits
// newline-delimited JSON (one event per line). This file owns the parser side: a
// tee-then-parse pipeline that simultaneously (1) writes raw bytes through to
// iter<k>/runner.ndjson AND (2) splits the byte stream on newlines, json.Unmarshal's
// each line, and appends a RunnerEvent to a slice owned by the caller via OnEvent —
// under the caller's mutex.
//
// Malformed JSON lines do NOT abort the parser: the line is captured as a
// parse_error RunnerEvent and parsing continues.

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// streamJSONSink wraps a stdout io.Writer pipeline that tees raw bytes to disk
// (byte-exact runner.ndjson) and parses each newline-delimited line as JSON,
// dispatching to onEvent on a separate goroutine.
type streamJSONSink struct {
	ndjsonFile *os.File
	pipeWriter *io.PipeWriter
	parserWG   sync.WaitGroup
	closed     bool
	mu         sync.Mutex // guards closed
}

// newStreamJSONSink opens ndjsonPath for byte-exact writes and starts a parser
// goroutine that reads from an internal pipe and invokes onEvent for each line. The
// returned sink should be passed as the runner's cmd.Stdout, then Closed once the
// runner exits. onEvent is called from the parser goroutine — the caller serializes
// via its own mutex.
func newStreamJSONSink(ndjsonPath string, onEvent func(RunnerEvent)) (*streamJSONSink, error) {
	f, err := os.Create(ndjsonPath)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	s := &streamJSONSink{
		ndjsonFile: f,
		pipeWriter: pw,
	}
	s.parserWG.Go(func() {
		defer pr.Close() //nolint:errcheck
		scanner := bufio.NewScanner(pr)
		// Up to 8 MiB per line — claude can emit large tool-result blobs inside one
		// assistant message. Default 64 KiB is too small for the long-tail.
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			ev := parseStreamJSONLine(line)
			if onEvent != nil {
				onEvent(ev)
			}
		}
		if err := scanner.Err(); err != nil && onEvent != nil {
			onEvent(RunnerEvent{
				AtUTC: time.Now().UTC().Format(time.RFC3339),
				Raw: map[string]any{
					"_scanner_error": err.Error(),
				},
			})
		}
	})
	return s, nil
}

// Write implements io.Writer. Called by exec/cmd.Run with each chunk of stdout
// bytes; tees to the on-disk ndjson file AND forwards to the parser pipe.
func (s *streamJSONSink) Write(p []byte) (int, error) {
	if _, err := s.ndjsonFile.Write(p); err != nil {
		// On disk-write failure, still feed the parser so events aren't lost.
		return s.pipeWriter.Write(p)
	}
	return s.pipeWriter.Write(p)
}

// Close finishes the parser side and closes both the ndjson file and the pipe. Safe
// to call once; subsequent calls no-op.
func (s *streamJSONSink) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	_ = s.pipeWriter.Close()
	s.parserWG.Wait()
	return s.ndjsonFile.Close()
}

// parseStreamJSONLine converts one NDJSON line to a RunnerEvent. Malformed lines
// yield a parse_error event rather than failing.
func parseStreamJSONLine(line []byte) RunnerEvent {
	now := time.Now().UTC().Format(time.RFC3339)
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return RunnerEvent{
			AtUTC: now,
			Raw: map[string]any{
				"_parse_error": err.Error(),
				"_line":        string(line),
			},
		}
	}
	ev := RunnerEvent{
		AtUTC: now,
		Raw:   raw,
	}
	if t, ok := raw["type"].(string); ok {
		ev.Type = t
	}
	return ev
}
