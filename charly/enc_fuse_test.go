package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/spec"
)

// withFuseConf points fuseConfPath at a temp file containing body (or removes it when body ==
// "\x00"), restoring the original path after the test.
func withFuseConf(t *testing.T, body string) {
	t.Helper()
	orig := fuseConfPath
	t.Cleanup(func() { fuseConfPath = orig })
	if body == "\x00" {
		fuseConfPath = filepath.Join(t.TempDir(), "absent-fuse.conf")
		return
	}
	p := filepath.Join(t.TempDir(), "fuse.conf")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fuseConfPath = p
}

func TestFuseAllowOtherEnabled(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"active", "# comment\nuser_allow_other\n#mount_max = 1000\n", true},
		{"active-trailing-space", "  user_allow_other  \n", true},
		{"commented", "#user_allow_other\n", false},
		{"commented-spaced", "# user_allow_other - description line\n", false},
		{"absent", "# nothing here\n#mount_max = 1000\n", false},
		{"missing-file", "\x00", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withFuseConf(t, c.body)
			if got := fuseAllowOtherEnabled(); got != c.want {
				t.Fatalf("fuseAllowOtherEnabled() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestEncExecViaPlugin_AllowOtherPreflight proves the mount-method preflight fails FAST with the
// exact fix when user_allow_other is missing — BEFORE resolving/invoking the plugin (so it needs
// no registered plugin). Locks the C16a follow-up that turns the raw fusermount3 error into an
// actionable one.
func TestEncExecViaPlugin_AllowOtherPreflight(t *testing.T) {
	withFuseConf(t, "# user_allow_other not set here\n")
	for _, m := range []string{spec.EncMethodMount, spec.EncMethodEnsure} {
		err := encExecViaPlugin(spec.EncExecInput{Method: m, BoxName: "x"})
		if err == nil || !strings.Contains(err.Error(), "user_allow_other") {
			t.Fatalf("method %q: want a user_allow_other preflight error, got %v", m, err)
		}
	}
}
