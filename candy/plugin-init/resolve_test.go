package initkind

import "testing"

// TestRestartMappingFuncs guards the abstract restart: → init-system policy mappings.
func TestRestartMappingFuncs(t *testing.T) {
	funcs := serviceRenderFuncs()

	systemdRestart := funcs["systemdRestart"].(func(string) string)
	if got := systemdRestart("always"); got != "always" {
		t.Errorf("systemdRestart(always) = %q", got)
	}
	if got := systemdRestart("on-failure"); got != "on-failure" {
		t.Errorf("systemdRestart(on-failure) = %q", got)
	}
	if got := systemdRestart("unless-stopped"); got != "always" {
		t.Errorf("systemdRestart(unless-stopped) = %q (want always)", got)
	}
	if got := systemdRestart(""); got != "no" {
		t.Errorf("systemdRestart(empty) = %q (want no)", got)
	}

	supRestart := funcs["supervisordRestart"].(func(string) string)
	if got := supRestart("always"); got != "true" {
		t.Errorf("supervisordRestart(always) = %q", got)
	}
	if got := supRestart("on-failure"); got != "unexpected" {
		t.Errorf("supervisordRestart(on-failure) = %q", got)
	}
	if got := supRestart("no"); got != "false" {
		t.Errorf("supervisordRestart(no) = %q", got)
	}
}

// TestSupervisordStdoutLogging guards the supervisord stdout_logfile mapping:
// unset keeps the historical /dev/fd/1 default, file:<path> yields a rotating
// dedicated log, none is /dev/null.
func TestSupervisordStdoutLogging(t *testing.T) {
	fns := serviceRenderFuncs()
	logf := fns["supervisordLog"].(func(string) string)
	maxb := fns["supervisordLogMaxbytes"].(func(string) string)
	cases := []struct{ in, wantLog, wantMax string }{
		{"", "/dev/fd/1", "0"},
		{"journal", "/dev/fd/1", "0"},
		{"none", "/dev/null", "0"},
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
