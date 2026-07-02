package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/alecthomas/kong"

	"github.com/opencharly/charly/charly/plugin/kit"
)

// command.go is the command:tmux leg of this plugin — the externalized `charly tmux …` CLI,
// ported OUT of charly's core (the deleted charly/tmux.go + charly/plugin_command_tmux.go) so
// the welded venue/executor resolver coupling no longer links into the `charly tmux` surface.
// It owns the ENTIRE 8-leaf `charly tmux` grammar verbatim (shell / cmd / run / attach / list /
// capture / send / kill) — the leaf STRUCTS are byte-identical to the former core command, so
// `charly tmux …` parses exactly as before; only each leaf's RUN body changed: it no longer
// calls the core resolveCheckVenue + DeployExecutor (which STAYS core — 12 callers), it
// RE-EXPRESSES the leaf through SANCTIONED `charly` CLI verbs (the CLI is the only operational
// interface, R4):
//
//   - non-interactive (cmd / run / list / capture / send / kill) → `charly cmd <box> 'tmux …'`
//     runs tmux on the venue's container via the existing host→container exec delegation. Old
//     core path: resolveCheckVenue → ContainerChain → `<engine> exec <c> bash -c 'tmux …'`; the
//     shell-back path: `charly cmd` → resolveContainer → `<engine> exec <c> sh -c 'tmux …'` — the
//     SAME container, SAME default exec user, SAME `/tmp/tmux-<uid>/default` socket, so the tmux
//     session a `run` creates is the session a later `list`/`capture` sees.
//   - interactive (shell / attach) → `charly shell <box> -c 'tmux …'` opens a real TTY in the
//     container. The plugin IS the process (charly syscall.Exec'd it in CLI mode, so it owns
//     charly's terminal stdio/TTY natively), so the inner `charly shell` sees a real terminal and
//     allocates `<engine> exec -it` — the TTY flows end-to-end, exactly as the former core
//     `syscall.Exec(<engine> exec -it … tmux …)` did. (The former core already REQUIRED a
//     container for shell/attach — a VM target errored, pointing at `charly vm ssh` — so the
//     container-scoped shell-back is faithful.)
//
// Dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly tmux <args…>`, charly RESOLVES this plugin's binary (host-built from source, or baked
// into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the pass-through tokens
// after the `tmux` word, in CLI mode (the go-plugin handshake cookie is stripped, so sdk.Main
// runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to charly's own executable — so
// every shell-back re-enters the SAME charly binary that dispatched the plugin.

// cliMain is the CLI-mode entry point (sdk.Main calls it when charly fork/exec'd this plugin as a
// command passthrough). It parses the pass-through tokens against TmuxCmd and dispatches the
// selected subcommand via kctx.Run() (each leaf carries its own Run handler). Returns the process
// exit code.
func cliMain(args []string) int {
	var grp TmuxCmd
	parser, err := kong.New(&grp,
		kong.Name("tmux"),
		kong.Description("charly tmux — manage tmux sessions inside running containers"),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-tmux: build kong parser: %v\n", err)
		return 1
	}
	kctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plugin-tmux: parse `charly tmux %v`: %v\n", args, err)
		return 1
	}
	if err := kctx.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "charly tmux: %v\n", err)
		return 1
	}
	return 0
}

// charlyBin returns the host charly binary the dispatch seam stamped into CHARLY_BIN, falling
// back to `charly` on PATH (e.g. if the plugin binary is run directly, off the dispatch path).
func charlyBin() string {
	if b := os.Getenv("CHARLY_BIN"); b != "" {
		return b
	}
	return "charly"
}

// tmuxScript shell-quotes each tmux argument and joins them into a single `tmux …` command
// string, so an argument containing spaces / shell metacharacters (a `send-keys -l "<command>"`
// payload, a session name, a `-F "#{session_name}"` format) survives intact when carried as the
// single command string `charly cmd`/`charly shell -c` runs through `sh -c`. Mirrors the former
// core tmuxArgs (kit.ShellQuote is the SAME single-quoter the core uses — R3).
func tmuxScript(args ...string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = kit.ShellQuote(a)
	}
	return "tmux " + strings.Join(quoted, " ")
}

// cmdArgv builds the `charly cmd [--no-notify] <box> [-i <instance>] <script>` argv — the
// non-interactive shell-back that runs <script> on the venue's container via the host→container
// exec delegation. notify maps 1:1 onto `charly cmd`'s own --notify/--no-notify (the sanctioned
// completion notification; internal helper calls always pass notify=false).
func cmdArgv(box, instance, script string, notify bool) []string {
	argv := []string{charlyBin(), "cmd"}
	if !notify {
		argv = append(argv, "--no-notify")
	}
	argv = append(argv, box)
	if instance != "" {
		argv = append(argv, "-i", instance)
	}
	return append(argv, script)
}

// runCmd runs a `charly cmd …` shell-back, streaming stdout/stderr/stdin to the operator's
// terminal (the same streaming `charly cmd` already does), and returns the exit error.
func runCmd(box, instance, script string, notify bool) error {
	argv := cmdArgv(box, instance, script, notify)
	c := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv[0] is CHARLY_BIN (the dispatching charly), args are shell-quoted
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}

// probeCmd runs a `charly cmd --no-notify …` shell-back with output discarded and reports
// whether it exited 0 — the availability/has-session probe primitive (never notifies, never
// prints). Mirrors the former core venueRunSilent.
func probeCmd(box, instance, script string) bool {
	argv := cmdArgv(box, instance, script, false)
	c := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv[0] is CHARLY_BIN, args are shell-quoted
	c.Stdout = &bytes.Buffer{}
	c.Stderr = &bytes.Buffer{}
	return c.Run() == nil
}

// shellExec REPLACES this process with `charly shell <box> -c 'tmux …'` (the interactive TTY
// shell-back) via syscall.Exec, so the inner charly inherits the plugin's terminal stdio/TTY
// natively and the tmux session is interactive. On success this never returns; only a PRE-exec
// failure (binary missing) returns an error.
func shellExec(box, instance, script string) error {
	bin, err := exec.LookPath(charlyBin())
	if err != nil {
		return fmt.Errorf("resolving charly binary %q: %w", charlyBin(), err)
	}
	argv := []string{"charly", "shell", box}
	if instance != "" {
		argv = append(argv, "-i", instance)
	}
	argv = append(argv, "-c", script)
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil { //nolint:gosec // bin is CHARLY_BIN, args are shell-quoted
		return fmt.Errorf("exec %s: %w", bin, err)
	}
	return nil // unreachable: syscall.Exec replaced the process image
}

// checkTmuxInstalled verifies tmux is on PATH inside the venue's container, returning the
// friendly former-core error otherwise (faithful to the leaves that pre-checked it).
func checkTmuxInstalled(box, instance string) error {
	if !probeCmd(box, instance, "command -v tmux >/dev/null 2>&1") {
		return fmt.Errorf("tmux is not installed in container %s (add the tmux candy to your box)", box)
	}
	return nil
}

// tmuxHasSession reports whether a named tmux session exists in the venue's container.
func tmuxHasSession(box, instance, session string) bool {
	return probeCmd(box, instance, tmuxScript("has-session", "-t", session))
}

// TmuxCmd manages tmux sessions inside running containers. The leaf structs are byte-identical to
// the former core command (charly/tmux.go) so `charly tmux …` parses exactly as before.
type TmuxCmd struct {
	Attach  TmuxAttachCmd  `cmd:"" help:"Attach to an existing tmux session (interactive)"`
	Capture TmuxCaptureCmd `cmd:"" help:"Capture pane output from a session"`
	Cmd     TmuxCmdCmd     `cmd:"" help:"Send a command to a tmux session (with notification)"`
	Kill    TmuxKillCmd    `cmd:"" help:"Kill a tmux session"`
	List    TmuxListCmd    `cmd:"" help:"List active tmux sessions"`
	Run     TmuxRunCmd     `cmd:"" help:"Start a command in a new detached tmux session"`
	Send    TmuxSendCmd    `cmd:"" help:"Send keys to a running session"`
	Shell   TmuxShellCmd   `cmd:"" help:"Persistent shell — creates or reattaches to a tmux session"`
}

// TmuxShellCmd creates or reattaches to a persistent shell session (interactive). The shell-back
// `charly shell <box> -c 'tmux new-session -A -s <session>'` attaches when the session exists and
// creates+attaches otherwise (the `-A` flag is the attach-or-create idiom — the same form the
// former core used in its VM hint).
type TmuxShellCmd struct {
	Box      string `arg:"" help:"Box name"`
	Session  string `short:"s" long:"session" default:"shell" help:"Session name (default: shell)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxShellCmd) Run() error {
	if err := checkTmuxInstalled(c.Box, c.Instance); err != nil {
		return err
	}
	return shellExec(c.Box, c.Instance, tmuxScript("new-session", "-A", "-s", c.Session))
}

// TmuxCmdCmd sends a command to an existing tmux session with a desktop notification. The
// notification fires via `charly cmd`'s OWN sanctioned completion notification (the --notify flag
// maps 1:1); the former core's bespoke "sent to <session>" gdbus call is replaced by the
// sanctioned verb (the CLI is the only operational interface — no re-implemented gdbus snippet).
type TmuxCmdCmd struct {
	Box      string `arg:"" help:"Box name"`
	Command  string `arg:"" help:"Command to send"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
	Notify   bool   `long:"notify" negatable:"" default:"true" help:"Send desktop notification (--no-notify to disable)"`
}

func (c *TmuxCmdCmd) Run() error {
	if err := checkTmuxInstalled(c.Box, c.Instance); err != nil {
		return err
	}
	if !tmuxHasSession(c.Box, c.Instance, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'charly tmux list' to see sessions)", c.Session, c.Box)
	}
	// One shell-back fires one notification: send the literal command text, then Enter.
	script := tmuxScript("send-keys", "-t", c.Session, "-l", c.Command) +
		"; " + tmuxScript("send-keys", "-t", c.Session, "Enter")
	if err := runCmd(c.Box, c.Instance, script, c.Notify); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Sent to %s: %s\n", c.Session, c.Command)
	return nil
}

// TmuxRunCmd starts a command in a new detached tmux session.
type TmuxRunCmd struct {
	Box      string `arg:"" help:"Box name"`
	Command  string `arg:"" help:"Command to run in the session"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxRunCmd) Run() error {
	if err := checkTmuxInstalled(c.Box, c.Instance); err != nil {
		return err
	}
	if tmuxHasSession(c.Box, c.Instance, c.Session) {
		return fmt.Errorf("tmux session %q already exists in %s (use 'charly tmux attach' or 'charly tmux kill')", c.Session, c.Box)
	}
	if err := runCmd(c.Box, c.Instance, tmuxScript("new-session", "-d", "-s", c.Session, c.Command), false); err != nil {
		return fmt.Errorf("starting tmux session: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Started tmux session %q in %s\n", c.Session, c.Box)
	return nil
}

// TmuxAttachCmd attaches to an existing tmux session interactively.
type TmuxAttachCmd struct {
	Box      string `arg:"" help:"Box name"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxAttachCmd) Run() error {
	if err := checkTmuxInstalled(c.Box, c.Instance); err != nil {
		return err
	}
	if !tmuxHasSession(c.Box, c.Instance, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'charly tmux list' to see sessions)", c.Session, c.Box)
	}
	return shellExec(c.Box, c.Instance, tmuxScript("attach-session", "-t", c.Session))
}

// TmuxListCmd lists active tmux sessions in a container. No sessions is not an error — it prints
// an informational message (faithful to the former core).
type TmuxListCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxListCmd) Run() error {
	if err := checkTmuxInstalled(c.Box, c.Instance); err != nil {
		return err
	}
	if err := runCmd(c.Box, c.Instance, tmuxScript("list-sessions"), false); err != nil {
		// tmux exits non-zero when no server/sessions exist — not a real error.
		fmt.Fprintf(os.Stderr, "No tmux sessions in %s\n", c.Box)
	}
	return nil
}

// TmuxCaptureCmd captures pane output from a session.
type TmuxCaptureCmd struct {
	Box      string `arg:"" help:"Box name"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Lines    int    `short:"n" long:"lines" default:"0" help:"Number of history lines (0 = visible pane only)"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxCaptureCmd) Run() error {
	if !tmuxHasSession(c.Box, c.Instance, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'charly tmux list' to see sessions)", c.Session, c.Box)
	}
	args := []string{"capture-pane", "-t", c.Session, "-p"}
	if c.Lines > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", c.Lines))
	}
	return runCmd(c.Box, c.Instance, tmuxScript(args...), false)
}

// TmuxSendCmd sends keys to a running tmux session.
type TmuxSendCmd struct {
	Box      string   `arg:"" help:"Box name"`
	Keys     []string `arg:"" help:"Keys to send (use tmux key names for special keys)"`
	Session  string   `short:"s" long:"session" required:"" help:"Session name"`
	Literal  bool     `short:"l" long:"literal" help:"Send keys literally (disable key name lookup)"`
	Enter    bool     `long:"enter" help:"Append Enter key after the keys"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxSendCmd) Run() error {
	if !tmuxHasSession(c.Box, c.Instance, c.Session) {
		return fmt.Errorf("tmux session %q not found in %s (use 'charly tmux list' to see sessions)", c.Session, c.Box)
	}
	args := []string{"send-keys", "-t", c.Session}
	if c.Literal {
		args = append(args, "-l")
	}
	args = append(args, c.Keys...)
	if c.Enter {
		args = append(args, "Enter")
	}
	return runCmd(c.Box, c.Instance, tmuxScript(args...), false)
}

// TmuxKillCmd kills a tmux session.
type TmuxKillCmd struct {
	Box      string `arg:"" help:"Box name"`
	Session  string `short:"s" long:"session" required:"" help:"Session name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *TmuxKillCmd) Run() error {
	if err := runCmd(c.Box, c.Instance, tmuxScript("kill-session", "-t", c.Session), false); err != nil {
		return fmt.Errorf("killing tmux session %q: %w", c.Session, err)
	}
	fmt.Fprintf(os.Stderr, "Killed tmux session %q in %s\n", c.Session, c.Box)
	return nil
}
