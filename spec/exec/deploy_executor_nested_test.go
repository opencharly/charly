package exec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

// deploy_executor_nested_test.go — tests for NestedExecutor
// composition (jump generation, Venue() accumulation).

func TestNestedExecutor_VenueChains(t *testing.T) {
	local := ShellExecutor{}
	if local.Venue() != VenueLocal {
		t.Errorf("ShellExecutor.Venue = %q, want %q", local.Venue(), VenueLocal)
	}

	n1 := &NestedExecutor{
		Parent: local,
		Jump:   NestedJump{Kind: JumpPodmanExec, Target: "mybox"},
	}
	if !strings.Contains(n1.Venue(), "podman-exec:mybox") {
		t.Errorf("NestedExecutor Venue missing jump description: %q", n1.Venue())
	}
	if !strings.Contains(n1.Venue(), VenueLocal) {
		t.Errorf("NestedExecutor Venue missing parent venue: %q", n1.Venue())
	}

	// Stacked: container-in-host-in-ssh.
	n2 := &NestedExecutor{
		Parent: n1,
		Jump:   NestedJump{Kind: JumpSSH, Target: "user@guest:2222"},
	}
	v := n2.Venue()
	if !strings.Contains(v, "ssh:user@guest:2222") {
		t.Errorf("stacked Venue missing SSH jump: %q", v)
	}
	if !strings.Contains(v, "podman-exec:mybox") {
		t.Errorf("stacked Venue missing parent jump: %q", v)
	}
}

func TestWrapWithJump_Podman(t *testing.T) {
	j := NestedJump{Kind: JumpPodmanExec, Target: "mybox"}
	out, err := wrapWithJump(j, "echo hi", false)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !strings.Contains(out, "podman exec -i") {
		t.Errorf("wrapped missing `podman exec -i`: %s", out)
	}
	if !strings.Contains(out, "'mybox'") {
		t.Errorf("wrapped missing quoted target: %s", out)
	}
	if !strings.Contains(out, "echo hi") {
		t.Errorf("wrapped missing script body: %s", out)
	}
}

func TestWrapWithJump_PodmanRoot(t *testing.T) {
	j := NestedJump{Kind: JumpPodmanExec, Target: "mybox"}
	out, err := wrapWithJump(j, "id", true)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	// Root mode escalates via sudo, and the escalation must wrap the shell
	// PROBE (not a bare bash) so the jump still starts on a busybox base.
	if !strings.Contains(out, "sudo sh -c") {
		t.Errorf("root mode missing sudo escalation: %s", out)
	}
	if !strings.Contains(out, "exec bash") {
		t.Errorf("root mode lost the bash hand-off: %s", out)
	}
}

// TestWrapWithJump_ShellProbeNotHardcodedBash guards the busybox-base
// regression: a hardcoded `bash` on the far side of a jump fails before the
// script is read on any base without bash (Alpine), surfacing as the OCI
// runtime's "attempted to invoke a command that was not found" (exit 127).
// The emitted command must invoke `sh` and hand off to bash conditionally.
func TestWrapWithJump_ShellProbeNotHardcodedBash(t *testing.T) {
	for _, tc := range []struct {
		name string
		jump NestedJump
	}{
		{"podman", NestedJump{Kind: JumpPodmanExec, Target: "mybox"}},
		{"docker", NestedJump{Kind: JumpDockerExec, Target: "mybox"}},
		{"ssh", NestedJump{Kind: JumpSSH, Target: "user@host.invalid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := wrapWithJump(tc.jump, "true", false)
			if err != nil {
				t.Fatalf("wrap: %v", err)
			}
			if !strings.Contains(out, "command -v bash") {
				t.Errorf("shell is not probed — a base without bash cannot start: %s", out)
			}
			if !strings.Contains(out, "exec sh") {
				t.Errorf("no sh fallback for a busybox base: %s", out)
			}
			// The jump must not invoke bash directly as the interpreter.
			for _, bad := range []string{"'mybox' bash", "host.invalid bash"} {
				if strings.Contains(out, bad) {
					t.Errorf("interpreter is hardcoded bash (%q): %s", bad, out)
				}
			}
		})
	}
}

func TestWrapWithJump_SSH(t *testing.T) {
	j := NestedJump{
		Kind:   JumpSSH,
		Target: "arch@guest.invalid:2224",
	}
	out, err := wrapWithJump(j, "uname -a", false)
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if !strings.Contains(out, "ssh") {
		t.Errorf("missing ssh invocation: %s", out)
	}
	if !strings.Contains(out, "arch@guest.invalid") {
		t.Errorf("missing target: %s", out)
	}
	if !strings.Contains(out, "-p 2224") {
		t.Errorf("missing port flag: %s", out)
	}
	// Identity and host-key policy remain config-owned; the fixed LogLevel
	// option only keeps local OpenSSH diagnostics out of command stderr.
	if strings.Contains(out, "-i ") {
		t.Errorf("unexpected `-i` flag — ssh-config supplies IdentityFile: %s", out)
	}
	if strings.Contains(out, "StrictHostKeyChecking") {
		t.Errorf("unexpected StrictHostKeyChecking override — ssh-config decides: %s", out)
	}
}

func TestWrapWithJump_VirshConsoleRejected(t *testing.T) {
	j := NestedJump{Kind: JumpVirshConsole, Target: "mydomain"}
	_, err := wrapWithJump(j, "anything", false)
	if err == nil {
		t.Fatal("expected error for JumpVirshConsole (not supported for scripted exec)")
	}
}

func TestParseSSHTarget(t *testing.T) {
	cases := []struct {
		in   string
		user string
		host string
		port int
	}{
		{"arch@127.0.0.1:2224", "arch", "127.0.0.1", 2224},
		{"guest.invalid", "", "guest.invalid", 0},
		{"root@host", "root", "host", 0},
		{"host:22", "", "host", 22},
	}
	for _, tc := range cases {
		u, h, p := parseSSHTarget(tc.in)
		if u != tc.user || h != tc.host || p != tc.port {
			t.Errorf("parseSSHTarget(%q) = (%q,%q,%d), want (%q,%q,%d)",
				tc.in, u, h, p, tc.user, tc.host, tc.port)
		}
	}
}

func TestCopyIntoJumpCommand_PodmanChownChmod(t *testing.T) {
	j := NestedJump{Kind: JumpPodmanExec, Target: "box"}
	cmd, err := copyIntoJumpCommand(j, "/tmp/stage", "/usr/local/bin/foo", 0o755, true)
	if err != nil {
		t.Fatalf("copyIntoJumpCommand: %v", err)
	}
	if !strings.Contains(cmd, "podman cp") {
		t.Errorf("missing podman cp: %s", cmd)
	}
	if !strings.Contains(cmd, "chown root:root") {
		t.Errorf("root ownership requested but chown missing: %s", cmd)
	}
	// fmtOctal renders modes as 0NNN (4-digit) octal — 0755 → "00755".
	if !strings.Contains(cmd, "chmod 00755") {
		t.Errorf("mode 0755 requested but chmod missing: %s", cmd)
	}
}

func TestNestedContainerName(t *testing.T) {
	if got := NestedContainerName("stack.web.db"); got != "stack_web_db" {
		t.Errorf("got %q, want stack_web_db", got)
	}
	if got := NestedContainerName("stack"); got != "stack" {
		t.Errorf("got %q, want stack", got)
	}
}

func TestSSHExecutor_Venue(t *testing.T) {
	e := &SSHExecutor{User: "arch", Host: "127.0.0.1", Port: 2224}
	want := "ssh://arch@127.0.0.1:2224"
	if e.Venue() != want {
		t.Errorf("Venue = %q, want %q", e.Venue(), want)
	}
}

// ---------------------------------------------------------------------------
// 2026-04-27 cutover: heredoc-delim uniqueness + env-var propagation
// ---------------------------------------------------------------------------

// TestNestedExecutor_ThreeLevelNesting_DelimitersUnique verifies the
// heredoc delim collision fix: at 3 levels of nesting (outer → mid →
// inner), each wrap layer must use a DIFFERENT delim or the outer
// shell terminates its heredoc on the first occurrence and the
// trailing closing delims become bare commands → exit 127. (The outer shell is
// not necessarily bash — see wrapWithJump; heredoc handling is POSIX, so the
// collision and the fix are the same either way.)
func TestNestedExecutor_ThreeLevelNesting_DelimitersUnique(t *testing.T) {
	innerJump := NestedJump{Kind: JumpPodmanExec, Target: "deepest"}
	midJump := NestedJump{Kind: JumpPodmanExec, Target: "middle"}
	outerJump := NestedJump{Kind: JumpPodmanExec, Target: "outermost"}

	innerScript := `echo hello`
	midScript, err := wrapWithJump(innerJump, innerScript, false)
	if err != nil {
		t.Fatalf("inner wrap: %v", err)
	}
	outerScript, err := wrapWithJump(midJump, midScript, false)
	if err != nil {
		t.Fatalf("mid wrap: %v", err)
	}
	final, err := wrapWithJump(outerJump, outerScript, false)
	if err != nil {
		t.Fatalf("outer wrap: %v", err)
	}

	// Collect delim closing tokens at line start (open delims appear
	// in `<<'<delim>'` form on the heredoc-opening line and don't
	// start with the delim text). Three nesting levels → three
	// distinct close-delims at line start, each appearing exactly
	// once (the matching close for its open). Also verify each
	// distinct close-delim's open delim appears exactly once
	// elsewhere in the script (in `<<'<delim>'` form).
	closeDelims := map[string]int{}
	for line := range strings.SplitSeq(final, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "CHARLY_NESTED_SCRIPT_EOF") && !strings.Contains(l, " ") {
			closeDelims[l]++
		}
	}
	if len(closeDelims) < 3 {
		t.Errorf("expected at least 3 distinct close-delims for 3-level nesting; got %d distinct: %v\n--- script ---\n%s",
			len(closeDelims), closeDelims, final)
	}
	for d, n := range closeDelims {
		if n != 1 {
			t.Errorf("close-delim %q appears %d times, want 1", d, n)
		}
		// And its matching open should appear exactly once.
		openMarker := "<<'" + d + "'"
		if got := strings.Count(final, openMarker); got != 1 {
			t.Errorf("open marker %q appears %d times, want 1", openMarker, got)
		}
	}
}

// TestNestedExecutor_EnvVarsPropagated_XdgRuntimeDir verifies the
// load-bearing libvirt-session-socket fix: when XDG_RUNTIME_DIR is
// set in the parent environ, wrapWithJump emits a `--env
// XDG_RUNTIME_DIR=...` flag in the podman exec invocation so the
// libvirt session-socket lookup succeeds inside the nested container.
func TestNestedExecutor_EnvVarsPropagated_XdgRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/home/user/.local/share/charly-runtime")
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")

	wrapped, err := wrapWithJump(NestedJump{Kind: JumpPodmanExec, Target: "vm"}, `charly check libvirt info vm`, false)
	if err != nil {
		t.Fatalf("wrapWithJump: %v", err)
	}
	if !strings.Contains(wrapped, "--env") {
		t.Errorf("expected `--env` flag in podman exec invocation; got:\n%s", wrapped)
	}
	if !strings.Contains(wrapped, "XDG_RUNTIME_DIR=/home/user/.local/share/charly-runtime") {
		t.Errorf("expected XDG_RUNTIME_DIR propagation; got:\n%s", wrapped)
	}
}

// TestNestedExecutor_EnvVarsPropagated_DisplayWayland verifies that
// the other display-related env vars in the allowlist are also
// propagated.
func TestNestedExecutor_EnvVarsPropagated_DisplayWayland(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("DISPLAY", ":1")
	t.Setenv("WAYLAND_DISPLAY", "wayland-2")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")

	wrapped, err := wrapWithJump(NestedJump{Kind: JumpPodmanExec, Target: "charly-fixture-desktop"}, `charly check live fixture-desktop --filter wl`, false)
	if err != nil {
		t.Fatalf("wrapWithJump: %v", err)
	}
	for _, expect := range []string{"DISPLAY=:1", "WAYLAND_DISPLAY=wayland-2"} {
		if !strings.Contains(wrapped, expect) {
			t.Errorf("expected propagation of %q; got:\n%s", expect, wrapped)
		}
	}
}

// TestBuildContainerEnvFlags_SkipsHostSessionRuntimeDir guards the fix for the
// nested-toolchain regression surfaced by the check-openclaw-desktop-pod bed:
// the harness forced the host's XDG_RUNTIME_DIR=/run/user/1000 onto its
// `podman exec`, clobbering the pod's baked /tmp and breaking rootless
// podman/buildah/libvirt with "lstat /run/user/1000: no such file or directory".
// Session-env values that reference the host /run/user/<uid> session dir must
// be skipped; non-session values must still propagate.
func TestBuildContainerEnvFlags_SkipsHostSessionRuntimeDir(t *testing.T) {
	for _, k := range containerEnvPropagationKeys {
		t.Setenv(k, "") // hermetic: empty values are skipped by buildContainerEnvFlags
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")

	got := buildContainerEnvFlags()

	if strings.Contains(got, "/run/user/1000") {
		t.Errorf("must NOT forward the host session runtime dir into the container; got: %q", got)
	}
	if strings.Contains(got, "XDG_RUNTIME_DIR") {
		t.Errorf("XDG_RUNTIME_DIR=/run/user/1000 should be skipped entirely; got: %q", got)
	}
	if strings.Contains(got, "DBUS_SESSION_BUS_ADDRESS") {
		t.Errorf("DBUS_SESSION_BUS_ADDRESS referencing /run/user/<uid> should be skipped; got: %q", got)
	}
	if !strings.Contains(got, "DISPLAY=:0") {
		t.Errorf("DISPLAY=:0 (no /run/user ref) should be propagated; got: %q", got)
	}
	if !strings.Contains(got, "WAYLAND_DISPLAY=wayland-0") {
		t.Errorf("WAYLAND_DISPLAY should be propagated; got: %q", got)
	}
}

// TestBuildContainerEnvFlags_PropagatesPinnedRuntimeDir confirms the fix does
// NOT break the libvirt-socket case: an explicitly-pinned XDG_RUNTIME_DIR that
// is NOT under /run/user/<uid> (the charly-runtime location) must still propagate so
// nested `charly check libvirt` finds its socket.
func TestBuildContainerEnvFlags_PropagatesPinnedRuntimeDir(t *testing.T) {
	for _, k := range containerEnvPropagationKeys {
		t.Setenv(k, "")
	}
	t.Setenv("XDG_RUNTIME_DIR", "/home/user/.local/share/charly-runtime")
	got := buildContainerEnvFlags()
	if !strings.Contains(got, "XDG_RUNTIME_DIR=/home/user/.local/share/charly-runtime") {
		t.Errorf("an explicitly-pinned (non-/run/user) XDG_RUNTIME_DIR must be propagated; got: %q", got)
	}
}

// ---------------------------------------------------------------------------
// 2026-06-29 cutover: NestedExecutor.GetFile stages on the PARENT, not the leaf
// ---------------------------------------------------------------------------

// recordingParentExecutor is a DeployExecutor (via embedded ShellExecutor) that
// records the scripts passed to RunUser and the paths passed to GetFile and
// returns canned bytes from GetFile — running nothing. It lets the
// NestedExecutor.GetFile staging composition be asserted without a real
// container or VM.
type recordingParentExecutor struct {
	ShellExecutor
	runUserScripts []string
	getFilePaths   []string
	getFileData    []byte
}

func (r *recordingParentExecutor) RunUser(_ context.Context, script string, _ spec.EmitOpts) error {
	r.runUserScripts = append(r.runUserScripts, script)
	return nil
}

func (r *recordingParentExecutor) GetFile(_ context.Context, path string, _ bool, _ spec.EmitOpts) ([]byte, error) {
	r.getFilePaths = append(r.getFilePaths, path)
	return r.getFileData, nil
}

// TestNestedExecutorGetFile_StagesRedirectOnParent guards the reverse-channel
// fix: NestedExecutor.GetFile must run ONLY `cat <remote>` through the jump and
// apply the staging `>` redirect on the PARENT shell (after the `}` closing the
// jump group), so the stage file lands parent-side where Parent.GetFile reads
// it. The original bug baked the redirect INSIDE wrapWithJump, so the stage was
// written in the leaf container while Parent.GetFile read the parent's separate
// filesystem → "no such file" on every container→host (or VM→host) pull (first
// hit green by the wl-screenshot PNG in check-android-emulator-pod). This test
// FAILS against the pre-fix code (the stage path appears inside the leaf
// heredoc body and `} > ` is absent) and PASSES with the fix, and confirms
// binary content round-trips verbatim.
func TestNestedExecutorGetFile_StagesRedirectOnParent(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n\x00\x01\x02\xde\xad\xbe\xef"
	rec := &recordingParentExecutor{getFileData: []byte(png)}
	n := &NestedExecutor{Parent: rec, Jump: NestedJump{Kind: JumpPodmanExec, Target: "charly-test-pod"}}

	data, err := n.GetFile(context.Background(), "/tmp/charly-wl-screenshot.png", false, spec.EmitOpts{})
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if string(data) != png {
		t.Fatalf("bytes not round-tripped verbatim from the parent stage:\n got %q\nwant %q", data, png)
	}

	// The stage RunUser is the one carrying `cat` + the stage path (the other
	// recorded RunUser is the deferred `rm` cleanup).
	var stage string
	for _, s := range rec.runUserScripts {
		if strings.Contains(s, "cat ") && strings.Contains(s, "charly-nested-get-") {
			stage = s
		}
	}
	if stage == "" {
		t.Fatalf("no stage RunUser recorded; got %v", rec.runUserScripts)
	}

	// Fix signature: the jump-group close + redirect (`} > `) appears right
	// after the heredoc terminator, i.e. the redirect runs on the PARENT.
	if !strings.Contains(stage, "CHARLY_NESTED_SCRIPT_EOF\n} > ") {
		t.Errorf("stage redirect not applied parent-side (expected `} > ` after the heredoc terminator):\n%s", stage)
	}
	// Regression guard: the stage path must NOT appear inside the leaf heredoc
	// body (everything before the closing terminator) — that placement is the
	// pre-fix bug (the redirect wrapped into the leaf, so the file never
	// reached the parent).
	leaf := stage
	if i := strings.Index(stage, "CHARLY_NESTED_SCRIPT_EOF\n}"); i >= 0 {
		leaf = stage[:i]
	}
	if strings.Contains(leaf, "charly-nested-get-") {
		t.Errorf("stage path leaked INSIDE the leaf heredoc (the pre-fix bug):\n%s", stage)
	}

	// Parent.GetFile must pull exactly the parent-side stage path once.
	if len(rec.getFilePaths) != 1 || !strings.HasPrefix(rec.getFilePaths[0], "/tmp/charly-nested-get-") {
		t.Errorf("Parent.GetFile not called once with the parent stage path; got %v", rec.getFilePaths)
	}
}

// ---------------------------------------------------------------------------
// Transport fidelity: the emitted line must survive the hand-off that carries it
// ---------------------------------------------------------------------------

// writeTransportStub writes an executable stub into dir and returns nothing; the
// caller puts dir at the front of PATH so the emitted jump line resolves to it.
func writeTransportStub(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

// TestWrapWithJump_SurvivesTransportReparse is the transport-level counterpart to
// the string assertions above: it EXECUTES the emitted line through stubs that
// model how each transport actually delivers the far-side command, instead of
// pattern-matching the text.
//
// This is the assertion a strings.Contains cannot express. The ssh subtest passes
// the `command -v bash` / `exec sh` text checks while being completely broken,
// because ssh(1) does NOT forward its remote-command arguments as argv — it joins
// them with single spaces into one string that the remote login shell re-parses.
// The parent shell has already stripped the probe's quotes by then, so an
// unquoted probe reaches the far side as bare text and dies with
// `syntax error near unexpected token 'then'` (exit 2) before the script is read.
// Reproduced against a real sshd; the stub reproduces it identically.
//
// Both directions are pinned:
//   - the script actually runs on the far side (NESTED_SCRIPT_RAN), and
//   - the probe took its BASH branch (TOOK=bash-branch), so the busybox fix did
//     not silently downgrade every jump to sh.
//
// The second assertion needs care, and an earlier version of it was VACUOUS.
// Reading $BASH_VERSION on the far side cannot distinguish the branches on a
// host where /bin/sh IS bash: bash invoked as sh still exports BASH_VERSION, so
// "handed off to bash" and "fell back to sh" produce the identical observation
// and the assertion holds even when the probe is forced down `exec sh`. The
// discriminator has to be something only the bash BRANCH can produce, so this
// test puts a marking `bash` on the far side's PATH: reaching it proves
// `command -v bash` succeeded and `exec bash` ran. The ssh stub's login shell is
// invoked by ABSOLUTE path so it cannot pick the marker up first and pre-set it.
func TestWrapWithJump_SurvivesTransportReparse(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		// The stubs model the parent shell and the remote login shell as bash,
		// matching the real-sshd corroboration; without it there is nothing to
		// run the emitted line, so this is a failure, not a skip.
		t.Fatalf("bash not found — the transport stubs need a real bash: %v", err)
	}

	// Hermetic: no --env flags leaking from the developer's environ into the
	// container-jump line (the stubs would have to model them too).
	for _, k := range containerEnvPropagationKeys {
		t.Setenv(k, "")
	}

	binDir := t.TempDir()

	// `podman exec -i [--env K=V …] <name> <cmd> [args…]`: the engine receives
	// real argv and hands it straight to exec(2) inside the container — there is
	// NO second parse. Modelled by dropping podman's own flags and exec'ing the
	// remaining argv unchanged.
	writeTransportStub(t, binDir, "podman", `#!/bin/sh
[ "$1" = exec ] && shift
while [ $# -gt 0 ]; do
  case "$1" in
    -i|-t|-it) shift ;;
    --env) shift 2 ;;
    *) break ;;
  esac
done
shift
exec "$@"
`)

	// ssh(1): local options and the destination are consumed locally, then the
	// REMAINING ARGUMENTS ARE JOINED WITH SPACES into one string handed to the
	// remote login shell, which parses it a second time. `"$*"` is exactly that
	// join; `bash -c` is the remote login shell.
	writeTransportStub(t, binDir, "ssh", `#!/bin/sh
while [ $# -gt 0 ]; do
  case "$1" in
    -o|-p) shift 2 ;;
    -*) shift ;;
    *) break ;;
  esac
done
shift
exec `+bashPath+` -c "$*"
`)

	// The far side's `bash`, marked. The probe reaches this ONLY by taking its
	// bash branch (`command -v bash` succeeded, `exec bash` ran); the sh
	// fallback never touches it. It exports the marker and hands off to the real
	// bash by absolute path, so there is no PATH recursion.
	writeTransportStub(t, binDir, "bash", `#!/bin/sh
PROBE_TOOK=bash-branch
export PROBE_TOOK
exec `+bashPath+` "$@"
`)

	// Root mode escalates through sudo; the stub keeps the test unprivileged
	// while preserving the argv shape sudo passes on.
	writeTransportStub(t, binDir, "sudo", "#!/bin/sh\nexec \"$@\"\n")

	// The far-side script reports that it ran AND which branch of the probe got
	// it there. NOT $BASH_VERSION — see the vacuity note in the doc comment.
	const inner = `echo NESTED_SCRIPT_RAN
echo "TOOK=${PROBE_TOOK:-sh-branch}"`

	for _, tc := range []struct {
		name string
		jump NestedJump
		root bool
	}{
		{"podman", NestedJump{Kind: JumpPodmanExec, Target: "mybox"}, false},
		{"podman-root", NestedJump{Kind: JumpPodmanExec, Target: "mybox"}, true},
		{"docker", NestedJump{Kind: JumpDockerExec, Target: "mybox"}, false},
		{"ssh", NestedJump{Kind: JumpSSH, Target: "user@127.0.0.1:2222"}, false},
		{"ssh-root", NestedJump{Kind: JumpSSH, Target: "user@127.0.0.1:2222"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.jump.Kind == JumpDockerExec {
				// docker takes the same argv-delivery path as podman.
				writeTransportStub(t, binDir, "docker", `#!/bin/sh
[ "$1" = exec ] && shift
while [ $# -gt 0 ]; do
  case "$1" in
    -i|-t|-it) shift ;;
    --env) shift 2 ;;
    *) break ;;
  esac
done
shift
exec "$@"
`)
			}
			line, err := wrapWithJump(tc.jump, inner, tc.root)
			if err != nil {
				t.Fatalf("wrapWithJump: %v", err)
			}
			cmd := exec.Command(bashPath, "-c", line)
			cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the emitted jump line did not survive the %s transport: %v\n--- output ---\n%s\n--- emitted ---\n%s",
					tc.name, err, out, line)
			}
			if !strings.Contains(string(out), "NESTED_SCRIPT_RAN") {
				t.Errorf("far-side script never ran over the %s transport:\n--- output ---\n%s\n--- emitted ---\n%s",
					tc.name, out, line)
			}
			if !strings.Contains(string(out), "TOOK=bash-branch") {
				t.Errorf("probe did not take its bash branch over the %s transport (a bash-bearing far side must still get bash):\n--- output ---\n%s\n--- emitted ---\n%s",
					tc.name, out, line)
			}
		})
	}
}
