package main

// plugin_served_provenance_test.go — the LOUD-provenance contract for a plugin's SERVED
// artifact, plus the reliability of a local override against a prebuilt binary.
//
// RCA this pins (RC3): two ~20-minute bed runs were discarded because the run log could not say
// which plugin binary served the verbs. The only line naming the plugin resolved a TAG
// ("Resolved @github.com/opencharly/plugin-adb -> v2026.251.0449 (latest tag)"), while the bytes
// that actually executed came from a default-pin build; the served binary was identified only
// afterwards, by reading strings out of it. Each case below fails on the old behaviour.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/proc"
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

func TestLocalOverrideRootFor_MatchesOnlyConfiguredRoots(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	outside := t.TempDir()
	t.Setenv(proc.RepoOverrideEnv, "opencharly/plugin-h4="+root+",github.com/opencharly/other="+other)

	for _, tc := range []struct {
		name string
		dir  string
		want bool
	}{
		{"the override root itself", root, true},
		{"a candy dir inside the root", filepath.Join(root, "candy", "plugin-h4"), true},
		{"the other configured root", other, true},
		{"an unrelated directory", outside, false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotRoot, ok := localOverrideRootFor(tc.dir)
			if ok != tc.want {
				t.Fatalf("localOverrideRootFor(%q) = (%q, %v), want ok=%v", tc.dir, gotRoot, ok, tc.want)
			}
			if ok && gotRoot != root && gotRoot != other {
				t.Fatalf("localOverrideRootFor(%q) root = %q, want %q or %q", tc.dir, gotRoot, root, other)
			}
		})
	}

	t.Setenv(proc.RepoOverrideEnv, "")
	if _, ok := localOverrideRootFor(root); ok {
		t.Fatal("an UNSET override must never report a local override")
	}
}

// A directory whose NAME merely shares a prefix with the root is NOT inside it:
// /tmp/tree-sibling must not be classified as /tmp/tree.
func TestLocalOverrideRootFor_NoPrefixConfusion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	sibling := root + "-sibling"
	for _, d := range []string{root, sibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(proc.RepoOverrideEnv, "opencharly/plugin-h4="+root)
	if gotRoot, ok := localOverrideRootFor(sibling); ok {
		t.Fatalf("a prefix-sharing SIBLING %q was classified as inside %q (root=%q)", sibling, root, gotRoot)
	}
	if _, ok := localOverrideRootFor(root); !ok {
		t.Fatalf("the override root %q itself must match", root)
	}
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

func TestPluginServedLine_DistinguishesEveryServedArtifact(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "plugin-h4")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginStampPath(bin), []byte("0123456789abcdef0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Setenv(proc.RepoOverrideEnv, "opencharly/plugin-h4="+root)
	const ref = "github.com/opencharly/plugin-h4/candy/plugin-h4"

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

	overridden := pluginServedLine(ref, filepath.Join(root, "candy", "plugin-h4"), bin, false)
	if !strings.Contains(overridden, "using LOCAL OVERRIDE "+root) || !strings.Contains(overridden, "serving "+bin) {
		t.Errorf("overridden-plugin line = %q, want the override root AND the served binary", overridden)
	}

	// The three must not render identically — that indistinguishability IS the defect.
	seen := map[string]string{}
	for name, line := range map[string]string{"built": built, "baked": baked, "override": overridden} {
		if prev, dup := seen[line]; dup {
			t.Errorf("%s and %s render the SAME provenance line: %q", name, prev, line)
		}
		seen[line] = name
	}
}

func TestReportPluginServed_WritesTheProvenanceLine(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "plugin-h4")
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(proc.RepoOverrideEnv, "")
	got := captureStderr(t, func() { reportPluginServed("plugin-h4", "/src/plugin-h4", bin) })
	if !strings.Contains(got, "plugin plugin-h4: served from "+bin) {
		t.Fatalf("reportPluginServed did not print the provenance line; stderr:\n%s", got)
	}
}

// TestResolvePluginBinary_LocalOverrideBeatsBaked is the RELIABILITY half: with
// CHARLY_REPO_OVERRIDE matching a plugin's repo, the served binary must come from the overridden
// tree — a prebuilt (baked) binary on the search path must not win, and its bypass must be loud.
func TestResolvePluginBinary_LocalOverrideBeatsBaked(t *testing.T) {
	bakedDir := t.TempDir()
	bakedBin := filepath.Join(bakedDir, "plugin-h4")
	if err := os.WriteFile(bakedBin, []byte("prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeProviders(t, bakedDir, "plugin-h4", "verb:h4")
	t.Setenv("CHARLY_PLUGIN_DIR", bakedDir)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// A dev/worktree binary: the FHS plugin dir of an installed package is OFF the search, so
	// the only candidate here is the baked dir this test owns.
	savedExe := packagedInstallExe
	packagedInstallExe = filepath.Join(t.TempDir(), "charly")
	t.Cleanup(func() { packagedInstallExe = savedExe })

	// The overridden working tree. Deliberately NOT a Go module: the build must FAIL, which is
	// how this test proves the overridden SOURCE was taken rather than the baked binary.
	srcDir := t.TempDir()

	// CONTROL: with no override, the baked binary serves (the pre-existing behaviour).
	t.Setenv(proc.RepoOverrideEnv, "")
	got, err := resolvePluginBinary(context.Background(), srcDir, "plugin-h4")
	if err != nil || got != bakedBin {
		t.Fatalf("control: without an override the baked binary must serve; got (%q, %v), want %q", got, err, bakedBin)
	}

	// OVERRIDE: the operator pointed the plugin's repo at a local tree — the baked binary must
	// NOT serve, the attempt must go to the overridden source, and the bypass must be named.
	t.Setenv(proc.RepoOverrideEnv, "github.com/opencharly/plugin-h4="+srcDir)
	var gotBin string
	var gotErr error
	stderr := captureStderr(t, func() {
		gotBin, gotErr = resolvePluginBinary(context.Background(), srcDir, "plugin-h4")
	})
	if gotBin == bakedBin {
		t.Fatalf("a baked binary served a plugin whose repo is LOCALLY OVERRIDDEN — the override is not reliable (got %q)", gotBin)
	}
	if gotErr == nil || !strings.Contains(gotErr.Error(), srcDir) {
		t.Fatalf("the overridden SOURCE was not the build input; got (%q, %v), want a build failure naming %q", gotBin, gotErr, srcDir)
	}
	if !strings.Contains(stderr, "ignoring baked binary "+bakedBin) || !strings.Contains(stderr, srcDir) {
		t.Fatalf("the bypassed baked binary was not named loudly; stderr:\n%s", stderr)
	}
}
