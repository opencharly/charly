package main

import "testing"

// TestSupervisordStdoutLogging guards the supervisord stdout_logfile mapping:
// services that don't set stdout: must keep the historical /dev/fd/1 default
// (backward compatible), file:<path> must yield a rotating dedicated log, and
// none must be /dev/null. Fails without the supervisordLog `none`/`/dev/fd/1`
// fix and the new supervisordLogMaxbytes helper.
func TestSupervisordStdoutLogging(t *testing.T) {
	fns := serviceRenderFuncs()
	logf, ok := fns["supervisordLog"].(func(string) string)
	if !ok {
		t.Fatal("supervisordLog helper missing")
	}
	maxb, ok := fns["supervisordLogMaxbytes"].(func(string) string)
	if !ok {
		t.Fatal("supervisordLogMaxbytes helper missing")
	}
	cases := []struct{ in, wantLog, wantMax string }{
		{"", "/dev/fd/1", "0"},        // unset → container stdout, no rotation (unchanged)
		{"journal", "/dev/fd/1", "0"}, // explicit journal → same
		{"none", "/dev/null", "0"},    // discard
		{"file:/home/user/.local/share/selkies/selkies.log", "/home/user/.local/share/selkies/selkies.log", "10MB"},
	}
	for _, c := range cases {
		if got := logf(c.in); got != c.wantLog {
			t.Errorf("supervisordLog(%q) = %q, want %q", c.in, got, c.wantLog)
		}
		if got := maxb(c.in); got != c.wantMax {
			t.Errorf("supervisordLogMaxbytes(%q) = %q, want %q", c.in, got, c.wantMax)
		}
	}
}
