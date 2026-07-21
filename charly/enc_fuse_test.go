package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// withFuseConf points deploykit.FuseConfPath at a temp file containing body (or removes it when
// body == "\x00"), restoring the original path after the test. FuseAllowOtherEnabled itself moved
// to sdk/deploykit (Cutover B unit 2, enc_probe.go) — its own dedicated test coverage lives there
// now (TestFuseAllowOtherEnabled); this file keeps ONLY the mock-pointing helper, since
// TestEncExecViaPlugin_AllowOtherPreflight below still needs it to exercise charly-core's
// encExecViaPlugin preflight.
func withFuseConf(t *testing.T, body string) {
	t.Helper()
	orig := deploykit.FuseConfPath
	t.Cleanup(func() { deploykit.FuseConfPath = orig })
	if body == "\x00" {
		deploykit.FuseConfPath = filepath.Join(t.TempDir(), "absent-fuse.conf")
		return
	}
	p := filepath.Join(t.TempDir(), "fuse.conf")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	deploykit.FuseConfPath = p
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
