package main

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns whatever
// was written. Used to assert the loud-warning behavior added to parseHostPorts
// and buildPortMapping in 2026-04 — the silent-skip those functions used to
// perform was the root cause of an entire tunnel-emission disappearing
// without any diagnostic to the operator.
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

func TestParseHostPorts_AllForms(t *testing.T) {
	in := []string{
		"8888",                // bare numeric
		"8080:80",             // host:container
		"127.0.0.1:5900:5900", // IPv4 bind prefix (the regression case)
		"[::1]:8080:80",       // IPv6 bind prefix
		"47998:47998/udp",     // proto suffix
		"127.0.0.1:53:53/udp", // bind + proto
	}
	got := parseHostPorts(in)
	want := []int{8888, 8080, 5900, 8080, 47998, 53}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseHostPorts(%v) = %v, want %v", in, got, want)
	}
}

func TestParseHostPorts_LogsOnInvalid(t *testing.T) {
	var got []int
	stderr := captureStderr(t, func() {
		got = parseHostPorts([]string{"bogus", "8888"})
	})
	if !reflect.DeepEqual(got, []int{8888}) {
		t.Errorf("parseHostPorts kept %v, want only [8888]", got)
	}
	if !strings.Contains(stderr, `unparseable port mapping "bogus"`) {
		t.Errorf("expected warning on stderr for %q, got:\n%s", "bogus", stderr)
	}
	// The warning text mentions every accepted form so the operator sees the
	// shape they should have used.
	for _, frag := range []string{"\"P\"", "\"H:C\"", "\"IP:H:C\""} {
		if !strings.Contains(stderr, frag) {
			t.Errorf("expected warning to reference accepted form %s, got:\n%s", frag, stderr)
		}
	}
}

func TestBuildPortMapping_AllForms(t *testing.T) {
	in := []string{
		"8888",
		"8080:80",
		"127.0.0.1:5901:5900",
		"[::1]:9000:90",
		"47998:47998/udp",
	}
	got := buildPortMapping(in)
	want := map[int]int{
		8888:  8888,
		8080:  80,
		5901:  5900,
		9000:  90,
		47998: 47998,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildPortMapping(%v) = %v, want %v", in, got, want)
	}
}

func TestBuildPortMapping_LogsOnInvalid(t *testing.T) {
	var got map[int]int
	stderr := captureStderr(t, func() {
		got = buildPortMapping([]string{"bogus", "8888:80"})
	})
	if !reflect.DeepEqual(got, map[int]int{8888: 80}) {
		t.Errorf("buildPortMapping kept %v, want only map[8888:80]", got)
	}
	if !strings.Contains(stderr, `unparseable port mapping "bogus"`) {
		t.Errorf("expected warning on stderr, got:\n%s", stderr)
	}
}
