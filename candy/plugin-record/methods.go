package record

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// methods.go is the record method dispatcher + the venue-driving layer, ported from
// charly/record.go (the deleted host-side RecordCmd). The 4-method surface
// (list/start/stop/cmd) was refactored from CLI Run() methods that PRINTED status to
// stderr into functions that RETURN the captured output string — so provider.go can feed
// the output through the shared sdk matcher pipeline + the artifact validators (a host-side
// matcher step does not run for an out-of-process verb). Every in-container
// action (the asciinema/wf-recorder tmux session, the .mode metadata, the recording pull)
// runs over the host executor reverse channel (sdk.Executor.RunCapture / GetFile) instead
// of the in-proc DeployExecutor the host-side RecordCmd used, so a bed authored against the
// in-tree verb passes unchanged.

const recordingDir = "/tmp/charly-recordings"

// requiredModifiers mirrors the in-tree recordMethods required-field specs (the host's
// validate-time + runtime required-modifier check keyed off the former in-proc live-verb seam,
// which an external verb is not — so the check moves HERE, at dispatch). stop needs
// an artifact path (where the recording is copied); cmd needs the text line to send.
var requiredModifiers = map[string][]string{
	"stop": {"artifact"},
	"cmd":  {"text"},
}

func modifierZero(op *spec.Op, name string) bool {
	switch name {
	case "artifact":
		return op.Artifact == ""
	case "text":
		return op.Text == ""
	}
	return false
}

// dispatch runs one record method against the venue (over the host executor reverse
// channel) and returns its captured output. A returned error is the verb FAILING (the
// in-tree CLI Run() returning an error → exit 1); provider.go maps it through the
// exit_status / stderr matchers.
func dispatch(ctx context.Context, ex *sdk.Executor, op *spec.Op) (string, error) {
	method := string(op.Record)
	if err := sdk.CheckRequiredModifiers(method, op, requiredModifiers, modifierZero); err != nil {
		return "", err
	}
	switch method {
	case "list":
		return recordList(ctx, ex)
	case "start":
		return recordStart(ctx, ex, op)
	case "stop":
		return recordStop(ctx, ex, op)
	case "cmd":
		return recordCmd(ctx, ex, op)
	}
	return "", fmt.Errorf("unknown record method %q", method)
}

// ---------------------------------------------------------------------------
// Methods (ported from charly/record.go's RecordCmd Run() methods)
// ---------------------------------------------------------------------------

// recordStart starts a recording session on the venue. Detects the recorder tool +
// mode (asciinema terminal / pixelflux-record|wf-recorder desktop), creates the output
// directory, and launches the recorder in a detached tmux session.
func recordStart(ctx context.Context, ex *sdk.Executor, op *spec.Op) (string, error) {
	if err := checkTmuxInstalled(ctx, ex); err != nil {
		return "", err
	}
	name := recordName(op)
	session := recordSessionName(name)
	if tmuxHasSession(ctx, ex, session) {
		return "", fmt.Errorf("recording %q already active (session %s); stop it first", name, session)
	}
	tool, mode, err := resolveMode(ctx, ex, op.RecordMode)
	if err != nil {
		return "", err
	}
	if err := ex.VenueRunSilent(ctx, "mkdir -p "+recordingDir); err != nil {
		return "", fmt.Errorf("creating recording directory: %w", err)
	}
	outFile := recordingFilePath(name, mode)
	var recorderCmd string
	switch tool {
	case "asciinema":
		recorderCmd = fmt.Sprintf("asciinema rec %s", kit.ShellQuote(outFile))
	case "pixelflux-record":
		recorderCmd = fmt.Sprintf("pixelflux-record %s --fps %d", kit.ShellQuote(outFile), recordFps(op))
		if op.RecordAudio {
			recorderCmd += " --audio"
		}
	case "wf-recorder":
		recorderCmd = fmt.Sprintf(
			"env XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-/tmp} WAYLAND_DISPLAY=${WAYLAND_DISPLAY:-wayland-0} wf-recorder -f %s",
			kit.ShellQuote(outFile))
		if op.RecordAudio {
			recorderCmd += " --audio"
		}
		recorderCmd += fmt.Sprintf(" -r %d", recordFps(op))
	}
	if err := execTmux(ctx, ex, "new-session", "-d", "-s", session, recorderCmd); err != nil {
		return "", fmt.Errorf("starting recording session: %w", err)
	}
	// Write mode metadata for stop (best-effort).
	modeFile := recordingDir + "/" + name + ".mode"
	_ = ex.VenueRunSilent(ctx, fmt.Sprintf("printf '%%s' %s > %s", kit.ShellQuote(mode), kit.ShellQuote(modeFile)))
	return fmt.Sprintf("Recording started (mode: %s, tool: %s, session: %s); output: %s", mode, tool, session, outFile), nil
}

// recordStop stops a recording session, copies the produced recording off the venue, and
// writes it to op.Artifact (the host path) so the provider's RunArtifactValidators can read
// it. The artifact requirement is enforced by checkRequiredModifiers before dispatch.
func recordStop(ctx context.Context, ex *sdk.Executor, op *spec.Op) (string, error) {
	name := recordName(op)
	session := recordSessionName(name)
	if !tmuxHasSession(ctx, ex, session) {
		return "", fmt.Errorf("no active recording %q (session %s not found)", name, session)
	}
	mode := readRecordingMode(ctx, ex, name)

	// Graceful stop: asciinema exits its shell, video recorders take SIGINT.
	if mode == "terminal" {
		_ = execTmux(ctx, ex, "send-keys", "-t", session, "exit", "Enter")
	} else {
		_ = execTmux(ctx, ex, "send-keys", "-t", session, "C-c")
	}

	// Bounded readiness probe: wait for the session to exit gracefully, then force-kill.
	stopped := false
	for range 10 {
		time.Sleep(500 * time.Millisecond)
		if !tmuxHasSession(ctx, ex, session) {
			stopped = true
			break
		}
	}
	if !stopped {
		_ = execTmux(ctx, ex, "kill-session", "-t", session)
	}
	cleanupModeFile(ctx, ex, name)

	outFile := recordingFilePath(name, mode)
	// Pull the recording off the venue (over the reverse channel) and write it to the host
	// artifact path BEFORE the provider's RunArtifactValidators reads it.
	data, err := ex.GetFile(ctx, outFile, false)
	if err != nil {
		return "", fmt.Errorf("copying recording: %w (file: %s)", err, outFile)
	}
	if err := os.WriteFile(op.Artifact, data, 0o644); err != nil {
		return "", fmt.Errorf("writing recording to %s: %w", op.Artifact, err)
	}
	return fmt.Sprintf("Recording stopped (mode: %s); saved %d bytes to %s", mode, len(data), op.Artifact), nil
}

// recordList lists active recording sessions on the venue as a tab-aligned table. A missing
// tmux server / no sessions is NOT an error — it returns "No active recordings" (mirroring
// the in-tree RecordListCmd, which printed that and returned nil).
func recordList(ctx context.Context, ex *sdk.Executor) (string, error) {
	out, err := captureTmux(ctx, ex, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return "No active recordings", nil
	}
	var recordings []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "record-") {
			recordings = append(recordings, line)
		}
	}
	if len(recordings) == 0 {
		return "No active recordings", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-20s %-10s %s\n", "NAME", "MODE", "FILE")
	for _, session := range recordings {
		recName := strings.TrimPrefix(session, "record-")
		mode := readRecordingMode(ctx, ex, recName)
		file := recordingFilePath(recName, mode)
		fmt.Fprintf(&b, "%-20s %-10s %s\n", recName, mode, file)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// recordCmd sends a text line into the recording's tmux session (it and its output become
// part of a terminal recording). The notification the in-tree RecordCmdCmd sent is dropped —
// it was a best-effort cosmetic side-effect with no bearing on the check verdict.
func recordCmd(ctx context.Context, ex *sdk.Executor, op *spec.Op) (string, error) {
	name := recordName(op)
	session := recordSessionName(name)
	if !tmuxHasSession(ctx, ex, session) {
		return "", fmt.Errorf("no active recording %q (session %s not found)", name, session)
	}
	if err := sendTmuxCommand(ctx, ex, session, op.Text); err != nil {
		return "", err
	}
	return fmt.Sprintf("Sent to %s: %s", session, op.Text), nil
}

// ---------------------------------------------------------------------------
// Mode + tool detection (ported from charly/record.go)
// ---------------------------------------------------------------------------

// resolveMode determines the recording tool + mode from the authored record_mode and the
// tools available on the venue. An empty record_mode means "auto" (the in-tree CLI default).
func resolveMode(ctx context.Context, ex *sdk.Executor, modeFlag string) (tool, mode string, err error) {
	switch modeFlag {
	case "terminal":
		if !ex.VenueHasTool(ctx, "asciinema") {
			return "", "", fmt.Errorf("terminal recording requires asciinema (add the asciinema candy)")
		}
		return "asciinema", "terminal", nil
	case "desktop":
		t, derr := detectDesktopRecorder(ctx, ex)
		if derr != nil {
			return "", "", derr
		}
		return t, "desktop", nil
	default: // "" or "auto"
		return detectRecordTool(ctx, ex)
	}
}

// detectDesktopRecorder finds the best available desktop recording tool on the venue.
func detectDesktopRecorder(ctx context.Context, ex *sdk.Executor) (string, error) {
	if ex.VenueHasTool(ctx, "pixelflux-record") {
		return "pixelflux-record", nil
	}
	if ex.VenueHasTool(ctx, "wf-recorder") {
		return "wf-recorder", nil
	}
	return "", fmt.Errorf("no desktop recorder available (need pixelflux-record or wf-recorder)")
}

// detectRecordTool probes the venue for available recording tools (auto mode): desktop
// recorders first (more specific), then the terminal asciinema fallback.
func detectRecordTool(ctx context.Context, ex *sdk.Executor) (tool, mode string, err error) {
	if ex.VenueHasTool(ctx, "pixelflux-record") {
		return "pixelflux-record", "desktop", nil
	}
	if ex.VenueHasTool(ctx, "wf-recorder") {
		return "wf-recorder", "desktop", nil
	}
	if ex.VenueHasTool(ctx, "asciinema") {
		return "asciinema", "terminal", nil
	}
	return "", "", fmt.Errorf("no recording tool available (need asciinema, pixelflux-record, or wf-recorder)")
}

// ---------------------------------------------------------------------------
// Helpers (ported from charly/record.go + tmux.go + check_venue.go, retargeted at the
// sdk.Executor reverse channel)
// ---------------------------------------------------------------------------

func recordName(op *spec.Op) string {
	if op.RecordName != "" {
		return op.RecordName
	}
	return "default"
}

func recordFps(op *spec.Op) int {
	if op.RecordFps > 0 {
		return op.RecordFps
	}
	return 30 // the in-tree RecordStartCmd.Fps default
}

func recordSessionName(name string) string { return "record-" + name }

func recordingFilePath(name, mode string) string {
	ext := ".mp4"
	if mode == "terminal" {
		ext = ".cast"
	}
	return recordingDir + "/" + name + ext
}

// readRecordingMode reads the recording mode from the .mode metadata file on the venue,
// falling back to "desktop" when absent/unreadable.
func readRecordingMode(ctx context.Context, ex *sdk.Executor, name string) string {
	modeFile := recordingDir + "/" + name + ".mode"
	out, err := ex.VenueCapture(ctx, "cat "+kit.ShellQuote(modeFile))
	if err == nil {
		mode := strings.TrimSpace(out)
		if mode == "terminal" || mode == "desktop" {
			return mode
		}
	}
	return "desktop"
}

func cleanupModeFile(ctx context.Context, ex *sdk.Executor, name string) {
	modeFile := recordingDir + "/" + name + ".mode"
	_ = ex.VenueRunSilent(ctx, "rm -f "+kit.ShellQuote(modeFile))
}

// --- tmux helpers ---

func tmuxArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = kit.ShellQuote(a)
	}
	return strings.Join(quoted, " ")
}

func checkTmuxInstalled(ctx context.Context, ex *sdk.Executor) error {
	if !ex.VenueHasTool(ctx, "tmux") {
		return fmt.Errorf("tmux is not installed on the target (add the tmux candy to your box, or install it on the host/VM)")
	}
	return nil
}

func tmuxHasSession(ctx context.Context, ex *sdk.Executor, session string) bool {
	return ex.VenueRunSilent(ctx, "tmux has-session -t "+kit.ShellQuote(session)) == nil
}

func execTmux(ctx context.Context, ex *sdk.Executor, args ...string) error {
	return ex.VenueRunSilent(ctx, "tmux "+tmuxArgs(args))
}

func captureTmux(ctx context.Context, ex *sdk.Executor, args ...string) (string, error) {
	out, err := ex.VenueCapture(ctx, "tmux "+tmuxArgs(args))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func sendTmuxCommand(ctx context.Context, ex *sdk.Executor, session, command string) error {
	if err := execTmux(ctx, ex, "send-keys", "-t", session, "-l", command); err != nil {
		return fmt.Errorf("sending command to session %s: %w", session, err)
	}
	if err := execTmux(ctx, ex, "send-keys", "-t", session, "Enter"); err != nil {
		return fmt.Errorf("sending Enter to session %s: %w", session, err)
	}
	return nil
}
