package main

// plugin_served_provenance_test.go — the LOUD-provenance contract for a plugin's SERVED
// artifact, plus the SHIPPED proof that the host-build arm really builds and serves the candy's
// own source (not a prebuilt binary).
//
// RCA this pins (RC3): two ~20-minute bed runs were discarded because the run log could not say
// which plugin binary served the verbs. The only line naming the plugin resolved a TAG
// ("Resolved @github.com/opencharly/plugin-adb -> v2026.251.0449 (latest tag)"), while the bytes
// that actually executed came from a different build; the served binary was identified only
// afterwards, by reading strings out of it. Each case below fails on the old behaviour.

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs fn with os.Stderr redirected to a pipe and returns everything written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	if cerr := w.Close(); cerr != nil {
		t.Fatal(cerr)
	}
	os.Stderr = old
	return <-done
}

// isolatePluginSearch makes the baked-plugin search path OWNED by the test: the build cache
// lands under a temp XDG_CACHE_HOME, $CHARLY_PLUGIN_DIR points at bakedDir (or an empty temp dir
// when the test needs NO baked hit), and the FHS baked dir is off the search because the running
// binary is faked to a dev/worktree path (packagedInstall() == false — issue #328).
func isolatePluginSearch(t *testing.T, bakedDir string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if bakedDir == "" {
		bakedDir = t.TempDir()
	}
	t.Setenv("CHARLY_PLUGIN_DIR", bakedDir)
	savedExe := packagedInstallExe
	packagedInstallExe = filepath.Join(t.TempDir(), "charly")
	t.Cleanup(func() { packagedInstallExe = savedExe })
}

// writeMinimalPluginModule writes a REAL, buildable Go module at <root>/candy/<name> — the
// shape an out-of-process plugin candy has on disk (its own go.mod + a main package). It is the
// source the host-build arm must compile and serve.
func writeMinimalPluginModule(t *testing.T, root, name string) string {
	t.Helper()
	srcDir := filepath.Join(root, "candy", name)
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/" + name + "\n\ngo 1.26\n"
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	mainGo := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
	return srcDir
}

func TestPluginArtifactID_StampBeatsMtime(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "plugin-h4")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := pluginArtifactID(bin); !strings.Contains(got, "mtime") {
		t.Fatalf("a binary with no stamp sidecar must identify by size+mtime, got %q", got)
	}
	const stamp = "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(pluginStampPath(bin), []byte(stamp+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pluginArtifactID(bin); got != "stamp "+stamp[:16] {
		t.Fatalf("pluginArtifactID with a stamp = %q, want %q", got, "stamp "+stamp[:16])
	}
	if got := pluginArtifactID(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("an unreadable binary must identify as empty, got %q", got)
	}
}

// TestPluginServedLine_DistinguishesEveryServedArtifact is the defect itself: a host-built
// plugin and a baked one must NOT render the same provenance line.
func TestPluginServedLine_DistinguishesEveryServedArtifact(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "plugin-h4")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginStampPath(bin), []byte("0123456789abcdef0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const ref = "plugin-h4"

	built := pluginServedLine(ref, "/src/plugin-h4", bin, false)
	for _, want := range []string{"plugin " + ref + ":", "served from " + bin, "build stamp 0123456789abcdef", "source /src/plugin-h4"} {
		if !strings.Contains(built, want) {
			t.Errorf("built-plugin line %q is missing %q", built, want)
		}
	}

	baked := pluginServedLine(ref, "", bin, true)
	if !strings.Contains(baked, "served from baked binary "+bin) || !strings.Contains(baked, "build stamp ") {
		t.Errorf("baked-plugin line = %q, want the baked binary path + a build id", baked)
	}

	if built == baked {
		t.Errorf("a host-built plugin and a baked one render the SAME provenance line: %q", built)
	}
}

func TestReportPluginServed_WritesTheProvenanceLine(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "plugin-h4")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	isolatePluginSearch(t, "")
	got := captureStderr(t, func() { reportPluginServed("plugin-h4", "/src/plugin-h4", bin) })
	if !strings.Contains(got, "plugin plugin-h4: served from "+bin) {
		t.Fatalf("reportPluginServed did not print the provenance line; stderr:\n%s", got)
	}
	if !strings.Contains(got, "source /src/plugin-h4") {
		t.Fatalf("the provenance line must name the SOURCE tree that served the build; stderr:\n%s", got)
	}
}

// TestResolvePluginBinary_BakedBinaryServesWithoutABuild is the CONTROL: with no source dir to
// build from, a baked binary serves and the line says so. The source dir is deliberately NOT a
// Go module — if the resolver tried to build it, this test would fail.
func TestResolvePluginBinary_BakedBinaryServesWithoutABuild(t *testing.T) {
	bakedDir := t.TempDir()
	bakedBin := filepath.Join(bakedDir, "plugin-h4")
	if err := os.WriteFile(bakedBin, []byte("prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProviders(t, bakedDir, "plugin-h4", "verb:h4")
	isolatePluginSearch(t, bakedDir)

	got, err := resolvePluginBinary(context.Background(), t.TempDir(), "plugin-h4")
	if err != nil || got != bakedBin {
		t.Fatalf("with no buildable source the baked binary must serve; got (%q, %v), want %q", got, err, bakedBin)
	}
	line := captureStderr(t, func() { reportPluginServed("plugin-h4", "", got) })
	if !strings.Contains(line, "served from baked binary "+bakedBin) {
		t.Fatalf("a baked plugin must be named as baked; stderr:\n%s", line)
	}
}

// TestResolvePluginBinary_HostBuildsFromTheCandySource is the SHIPPED proof of the SUCCESSFUL
// host-build arm — the path the fix introduces and the one the deleted ad-hoc harness used to
// cover. It builds a REAL minimal candy module in a temp tree, then asserts the served artifact
// comes FROM that tree: the binary lands in the plugin build cache, carries a content stamp,
// EXECUTES, and the provenance line names both the binary and its source dir. Nothing here is
// mocked — a stub would not catch the failure mode this test exists for (a prebuilt binary
// serving while the log said nothing).
func TestResolvePluginBinary_HostBuildsFromTheCandySource(t *testing.T) {
	if testing.Short() {
		t.Skip("the host-build proof runs a real go build")
	}
	cache := t.TempDir()
	isolatePluginSearch(t, "")
	t.Setenv("XDG_CACHE_HOME", cache)

	srcDir := writeMinimalPluginModule(t, t.TempDir(), "plugin-h4")

	bin, err := resolvePluginBinary(context.Background(), srcDir, "plugin-h4")
	if err != nil {
		t.Fatalf("the host build of a real candy module must SUCCEED: %v", err)
	}
	if wantDir := filepath.Join(cache, "charly", "plugins"); filepath.Dir(bin) != wantDir {
		t.Fatalf("the built binary must land in the plugin build cache %s; got %s", wantDir, bin)
	}
	st, statErr := os.Stat(bin)
	if statErr != nil || st.IsDir() {
		t.Fatalf("the served binary %s must exist as a regular file: %v", bin, statErr)
	}
	if st.Size() == 0 {
		t.Fatalf("the served binary %s is empty — the build produced no bytes", bin)
	}
	if _, err := os.Stat(pluginStampPath(bin)); err != nil {
		t.Errorf("a host-built binary must carry its content stamp beside it: %v", err)
	}
	if id := pluginArtifactID(bin); !strings.HasPrefix(id, "stamp ") {
		t.Errorf("the served artifact id must be the CONTENT stamp, got %q", id)
	}
	// The built artifact is REAL, not a placeholder: it executes.
	if out, err := exec.Command(bin).CombinedOutput(); err != nil {
		t.Fatalf("the built plugin binary must execute: %v\n%s", err, out)
	}
	// And the run log says exactly which bytes executed and which tree they came from.
	line := captureStderr(t, func() { reportPluginServed("plugin-h4", srcDir, bin) })
	for _, want := range []string{"plugin plugin-h4: served from " + bin, "build stamp ", "source " + srcDir} {
		if !strings.Contains(line, want) {
			t.Errorf("provenance line %q is missing %q", line, want)
		}
	}
	if strings.Contains(line, "served from baked binary") {
		t.Errorf("a host-built plugin must not be reported as baked: %q", line)
	}
}
