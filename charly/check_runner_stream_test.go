package main

// harness_runner_stream_test.go — unit tests for the stream-json
// parser pipeline. These tests run in-process (no real claude binary)
// by feeding canned NDJSON through streamJSONSink.Write.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStreamJSONSink_ParsesValidNDJSON(t *testing.T) {
	dir := t.TempDir()
	ndjsonPath := filepath.Join(dir, "runner.ndjson")

	var mu sync.Mutex
	var events []RunnerEvent
	sink, err := newStreamJSONSink(ndjsonPath, func(ev RunnerEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("newStreamJSONSink: %v", err)
	}

	input := `{"type":"system","subtype":"init","session_id":"abc"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}
{"type":"result","total_cost_usd":0.0123}
`
	if _, err := sink.Write([]byte(input)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	wantTypes := []string{"system", "assistant", "result"}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Errorf("event[%d].Type = %q, want %q", i, events[i].Type, want)
		}
		if events[i].AtUTC == "" {
			t.Errorf("event[%d].AtUTC empty", i)
		}
		if events[i].Raw == nil {
			t.Errorf("event[%d].Raw nil", i)
		}
	}
	if events[0].Raw["session_id"] != "abc" {
		t.Errorf("event[0].Raw[session_id] = %v, want abc", events[0].Raw["session_id"])
	}

	// Tee-to-disk verification: the on-disk NDJSON should be byte-exact
	// to the input.
	got, err := os.ReadFile(ndjsonPath)
	if err != nil {
		t.Fatalf("read ndjson: %v", err)
	}
	if string(got) != input {
		t.Errorf("on-disk NDJSON mismatch.\ngot:\n%s\nwant:\n%s", got, input)
	}
}

func TestStreamJSONSink_MalformedLineSurvivesAsParseError(t *testing.T) {
	dir := t.TempDir()
	ndjsonPath := filepath.Join(dir, "runner.ndjson")

	var mu sync.Mutex
	var events []RunnerEvent
	sink, err := newStreamJSONSink(ndjsonPath, func(ev RunnerEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("newStreamJSONSink: %v", err)
	}

	input := `{"type":"system","subtype":"init"}
this is not valid json {
{"type":"result","ok":true}
`
	if _, err := sink.Write([]byte(input)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("expected 3 events (one parse_error), got %d", len(events))
	}
	if events[0].Type != "system" {
		t.Errorf("event[0].Type = %q, want system", events[0].Type)
	}
	// Middle event should be a parse_error envelope.
	if events[1].Type != "" {
		t.Errorf("malformed event Type = %q, want empty", events[1].Type)
	}
	if errMsg, _ := events[1].Raw["_parse_error"].(string); errMsg == "" {
		t.Errorf("expected _parse_error key, got %v", events[1].Raw)
	}
	if line, _ := events[1].Raw["_line"].(string); !strings.Contains(line, "not valid") {
		t.Errorf("expected _line to carry raw bytes, got %v", events[1].Raw["_line"])
	}
	if events[2].Type != "result" {
		t.Errorf("event[2].Type = %q, want result (parser must continue past malformed line)", events[2].Type)
	}
}

func TestStreamJSONSink_EmptyLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	ndjsonPath := filepath.Join(dir, "runner.ndjson")

	var mu sync.Mutex
	var events []RunnerEvent
	sink, err := newStreamJSONSink(ndjsonPath, func(ev RunnerEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("newStreamJSONSink: %v", err)
	}

	// Some claude builds emit a trailing newline after the result
	// event; the parser must not produce a spurious empty event.
	input := `{"type":"system"}


{"type":"result"}

`
	if _, err := sink.Write([]byte(input)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("expected 2 events (blank lines skipped), got %d", len(events))
	}
}

func TestStreamJSONSink_ChunkedWritesAcrossLineBoundary(t *testing.T) {
	// Real exec/cmd writes stdout in chunks that don't necessarily
	// align with newlines. The bufio.Scanner inside the sink must
	// stitch them back together.
	dir := t.TempDir()
	ndjsonPath := filepath.Join(dir, "runner.ndjson")

	var mu sync.Mutex
	var events []RunnerEvent
	sink, err := newStreamJSONSink(ndjsonPath, func(ev RunnerEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("newStreamJSONSink: %v", err)
	}

	chunks := []string{
		`{"type":"sys`,
		`tem","subtype":"init"}` + "\n" + `{"type":"asst`,
		`","msg":"hi"}` + "\n",
	}
	for _, c := range chunks {
		if _, err := sink.Write([]byte(c)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != "system" {
		t.Errorf("event[0].Type = %q, want system", events[0].Type)
	}
	if events[1].Type != "asst" {
		t.Errorf("event[1].Type = %q, want asst", events[1].Type)
	}
}

func TestParseStreamJSONLine_TopLevelTypeFieldExtracted(t *testing.T) {
	ev := parseStreamJSONLine([]byte(`{"type":"tool_use","name":"Read","input":{"file_path":"/x"}}`))
	if ev.Type != "tool_use" {
		t.Errorf("Type = %q, want tool_use", ev.Type)
	}
	if got, _ := ev.Raw["name"].(string); got != "Read" {
		t.Errorf("Raw[name] = %v, want Read", ev.Raw["name"])
	}
}

func TestParseStreamJSONLine_NoTypeField(t *testing.T) {
	ev := parseStreamJSONLine([]byte(`{"foo":"bar"}`))
	if ev.Type != "" {
		t.Errorf("Type = %q, want empty (no top-level type field)", ev.Type)
	}
	if got, _ := ev.Raw["foo"].(string); got != "bar" {
		t.Errorf("Raw[foo] = %v, want bar", ev.Raw["foo"])
	}
}
