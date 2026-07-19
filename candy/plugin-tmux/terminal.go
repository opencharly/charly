package tmux

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

const tmuxPane = "run:0.0"

var errCloseRequested = errors.New("plugin-tmux: acknowledged close requested")

type channelSender struct {
	mu      sync.Mutex
	stream  sdk.ProviderChannel
	request string
	next    uint64
	replay  *sdk.ReplayBuffer
}

func (s *channelSender) send(frame *pb.ChannelFrame) error {
	return s.sendWithPayload(frame, nil)
}

func (s *channelSender) sendWithPayload(frame *pb.ChannelFrame, payload func(uint64) ([]byte, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	frame.RequestId = s.request
	if frame.Kind != sdk.ChannelAck && frame.Sequence == 0 {
		frame.Sequence = s.next
		s.next++
	}
	if payload != nil {
		var err error
		frame.PayloadJson, err = payload(frame.Sequence)
		if err != nil {
			return err
		}
	}
	if frame.Kind != sdk.ChannelAck {
		if err := s.replay.Add(frame); err != nil {
			return fmt.Errorf("plugin-tmux: preserving unacknowledged terminal evidence: %w", err)
		}
	}
	return s.stream.Send(frame)
}

func (s *channelSender) acknowledge(sequence uint64) { s.replay.Acknowledge(sequence) }

func (s *channelSender) replayFrom(sequence uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	frames, err := s.replay.ReplayFrom(sequence)
	if err != nil {
		return err
	}
	for _, frame := range frames {
		if err := s.stream.Send(frame); err != nil {
			return err
		}
	}
	return nil
}

type tmuxChannel struct {
	profile              spec.TerminalProfile
	agentRuntime         bool
	operation            string
	socket               string
	sender               *channelSender
	control              *exec.Cmd
	controlIn            io.WriteCloser
	controlOut           io.ReadCloser
	controlErr           io.ReadCloser
	closed               bool
	closing              bool
	hadInput             bool
	settled              bool
	settledRE            *regexp.Regexp
	promptRE             *regexp.Regexp
	promptReady          chan struct{}
	promptOnce           sync.Once
	promptEchoReady      chan struct{}
	promptEchoOnce       sync.Once
	promptEchoMu         sync.Mutex
	promptEchoText       string
	promptSubmitted      atomic.Bool
	launchPending        bool
	pendingExit          *int32
	exitBarrierRequested bool
	exitBarrierCommand   string
	screen               *vt.SafeEmulator
}

func openTmuxChannel(open *pb.ChannelFrame, stream sdk.ProviderChannel, initialPrompt string) (returnErr error) {
	if open.GetReserved() != "tmux" || (open.GetClass() != "terminal" && open.GetClass() != "agent-runtime") {
		return fmt.Errorf("plugin-tmux: unsupported channel %s:%s", open.GetClass(), open.GetReserved())
	}
	profile, err := decodeTmuxProfile(open.GetPayloadJson())
	if err != nil {
		return err
	}
	ch := newTmuxChannel(open, stream, profile)
	screenDrain := drainTerminalResponses(ch.screen)
	defer func() {
		if err := screenDrain.stop(); err != nil {
			returnErr = errors.Join(returnErr, err)
		}
	}()
	if err := ch.configureSettledPattern(); err != nil {
		return err
	}
	detached, err := ch.start(stream)
	if err != nil {
		return err
	}
	if err := ch.synchronizeScreen(detached); err != nil {
		return err
	}
	return ch.wait(stream, screenDrain.failure, initialPrompt, detached)
}

func decodeTmuxProfile(payload []byte) (spec.TerminalProfile, error) {
	var profile spec.TerminalProfile
	if err := json.Unmarshal(payload, &profile); err != nil {
		return profile, fmt.Errorf("plugin-tmux: decode CUE TerminalProfile: %w", err)
	}
	if err := sdk.ValidateGenerated("#TerminalProfile", profile); err != nil {
		return profile, fmt.Errorf("plugin-tmux: %w", err)
	}
	if len(profile.Entrypoint) == 0 {
		return profile, errors.New("plugin-tmux: terminal profile requires entrypoint")
	}
	if profile.Cols == 0 {
		profile.Cols = 120
	}
	if profile.Rows == 0 {
		profile.Rows = 40
	}
	if profile.Cols < 1 || profile.Cols > 1000 || profile.Rows < 1 || profile.Rows > 1000 {
		return profile, fmt.Errorf("plugin-tmux: invalid terminal size %dx%d", profile.Cols, profile.Rows)
	}
	return profile, nil
}

func newTmuxChannel(open *pb.ChannelFrame, stream sdk.ProviderChannel, profile spec.TerminalProfile) *tmuxChannel {
	channel := &tmuxChannel{
		profile:         profile,
		agentRuntime:    open.GetClass() == "agent-runtime",
		operation:       open.GetOp(),
		socket:          "charly-" + safeID(open.GetRequestId()),
		sender:          &channelSender{stream: stream, request: open.GetRequestId(), next: open.GetAckSequence() + 1, replay: sdk.NewReplayBuffer(4096, 16<<20)},
		promptReady:     make(chan struct{}),
		promptEchoReady: make(chan struct{}),
		screen:          vt.NewSafeEmulator(int(profile.Cols), int(profile.Rows)),
	}
	channel.promptSubmitted.Store(true)
	return channel
}

type terminalResponseDrain struct {
	failure chan error
	done    chan struct{}
	closer  io.Closer
}

func drainTerminalResponses(screen *vt.SafeEmulator) *terminalResponseDrain {
	drain := &terminalResponseDrain{failure: make(chan error, 1), done: make(chan struct{})}
	closer, ok := screen.InputPipe().(io.Closer)
	if !ok {
		drain.failure <- errors.New("terminal emulator input pipe cannot be closed")
		close(drain.done)
		return drain
	}
	drain.closer = closer
	go func() {
		defer close(drain.done)
		if _, err := io.Copy(io.Discard, screen); err != nil {
			drain.failure <- fmt.Errorf("terminal emulator response drain: %w", err)
		}
	}()
	return drain
}

// stop closes the emulator's response pipe directly and joins the reader.
// x/vt's Emulator.Close mutates its unsynchronized closed flag while Read
// inspects it, so calling Close concurrently with the required drain races.
// Closing the pipe writer is the event-driven EOF boundary and leaves no
// response goroutine behind.
func (d *terminalResponseDrain) stop() error {
	var closeErr error
	if d.closer != nil {
		closeErr = d.closer.Close()
	}
	<-d.done
	select {
	case drainErr := <-d.failure:
		return errors.Join(closeErr, drainErr)
	default:
		return closeErr
	}
}

func (c *tmuxChannel) configureSettledPattern() error {
	pattern := settledPattern(c.profile)
	if pattern != "" {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("plugin-tmux: settled regex: %w", err)
		}
		c.settledRE = compiled
	}
	prompt := initialPromptPattern(c.profile)
	if prompt == "" {
		return nil
	}
	compiled, err := regexp.Compile(prompt)
	if err != nil {
		return fmt.Errorf("plugin-tmux: prompt regex: %w", err)
	}
	c.promptRE = compiled
	return nil
}

// observeInitialPrompt turns tmux control output into the semantic readiness event.
// There is no capture polling, retry, sleep, or guessed deadline: tmux owns the
// terminal event stream and the channel context owns cancellation.
func (c *tmuxChannel) observeInitialPrompt() {
	if !c.agentRuntime || c.promptRE == nil || c.promptReady == nil {
		return
	}
	if c.promptRE.MatchString(c.plainScreen()) {
		c.promptOnce.Do(func() { close(c.promptReady) })
	}
}

func compactTerminalText(value string) string { return strings.Join(strings.Fields(value), " ") }

// observePromptEcho is the delivery acknowledgement between bracketed paste
// and Enter. Interactive agents may deliberately treat Enter received during
// paste processing as a newline. Waiting for the terminal's own rendered echo
// gives us an event-driven ordering boundary without a guessed delay.
func (c *tmuxChannel) observePromptEcho() {
	c.promptEchoMu.Lock()
	expected := c.promptEchoText
	c.promptEchoMu.Unlock()
	if expected == "" {
		return
	}
	if strings.Contains(compactTerminalText(c.plainScreen()), expected) {
		c.promptEchoOnce.Do(func() { close(c.promptEchoReady) })
	}
}

func (c *tmuxChannel) awaitInitialPrompt(ctx context.Context, controlDone, screenFailure <-chan error) error {
	if !c.agentRuntime || c.promptRE == nil {
		return nil
	}
	if err := c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "waiting-for-prompt"}); err != nil {
		return fmt.Errorf("plugin-tmux: report semantic prompt wait: %w", err)
	}
	fmt.Fprintf(os.Stderr, "plugin-tmux: profile %q waiting for semantic prompt event on socket %s\n", c.profile.Name, c.socket)
	select {
	case <-c.promptReady:
		fmt.Fprintf(os.Stderr, "plugin-tmux: profile %q semantic prompt ready on socket %s\n", c.profile.Name, c.socket)
		if err := c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "prompt-ready"}); err != nil {
			return fmt.Errorf("plugin-tmux: report semantic prompt readiness: %w", err)
		}
		return nil
	case err := <-controlDone:
		if err == nil {
			err = errors.New("tmux control stream ended")
		}
		return fmt.Errorf("plugin-tmux: profile %q ended before semantic prompt: %w", c.profile.Name, err)
	case err := <-screenFailure:
		return fmt.Errorf("plugin-tmux: profile %q terminal emulator failed before semantic prompt: %w", c.profile.Name, err)
	case <-ctx.Done():
		return fmt.Errorf("plugin-tmux: profile %q semantic prompt wait canceled: %w", c.profile.Name, ctx.Err())
	}
}

func (c *tmuxChannel) announce(detached bool) error {
	name := "running"
	if detached {
		name = "reattached"
	}
	return c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: name})
}

func (c *tmuxChannel) synchronizeScreen(reconcileHistory bool) error {
	snapshot, err := c.captureScreen()
	if err != nil {
		return err
	}
	mode := c.profile.Transcript
	if mode == "" {
		mode = "both"
	}
	if reconcileHistory && (mode == "raw" || mode == "both") && len(snapshot) > 0 {
		if err := c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelTerminal, Data: snapshot}); err != nil {
			return err
		}
	}
	if _, err := c.screen.Write(append([]byte("\x1b[H\x1b[2J"), snapshot...)); err != nil {
		return fmt.Errorf("terminal emulator initial screen: %w", err)
	}
	c.observeInitialPrompt()
	return c.sendScreen(sdk.ChannelResync)
}

func (c *tmuxChannel) injectPrompt(ctx context.Context, initialPrompt string, controlDone, screenFailure <-chan error) error {
	if initialPrompt == "" {
		return nil
	}
	c.promptSubmitted.Store(false)
	c.promptEchoMu.Lock()
	c.promptEchoText = compactTerminalText(initialPrompt)
	c.promptEchoMu.Unlock()
	paste := spec.TerminalInput{Kind: "paste", Text: initialPrompt}
	pastePayload, err := encodeTerminalInput(paste)
	if err != nil {
		return err
	}
	if err := c.input(ctx, &pb.ChannelFrame{Kind: sdk.ChannelStdin, Name: "paste", Data: []byte(initialPrompt), PayloadJson: pastePayload}); err != nil {
		return err
	}
	if err := c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "waiting-for-prompt-echo"}); err != nil {
		return fmt.Errorf("plugin-tmux: report prompt echo wait: %w", err)
	}
	fmt.Fprintf(os.Stderr, "plugin-tmux: profile %q waiting for rendered prompt echo on socket %s\n", c.profile.Name, c.socket)
	select {
	case <-c.promptEchoReady:
		fmt.Fprintf(os.Stderr, "plugin-tmux: profile %q rendered prompt echo ready on socket %s\n", c.profile.Name, c.socket)
		if err := c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "prompt-echo-ready"}); err != nil {
			return fmt.Errorf("plugin-tmux: report prompt echo readiness: %w", err)
		}
	case err := <-controlDone:
		if err == nil {
			err = errors.New("tmux control stream ended")
		}
		return fmt.Errorf("plugin-tmux: profile %q ended before rendered prompt echo: %w", c.profile.Name, err)
	case err := <-screenFailure:
		return fmt.Errorf("plugin-tmux: profile %q terminal emulator failed before rendered prompt echo: %w", c.profile.Name, err)
	case <-ctx.Done():
		return fmt.Errorf("plugin-tmux: profile %q prompt echo wait canceled: %w", c.profile.Name, ctx.Err())
	}
	enter := spec.TerminalInput{Kind: "key", Key: "enter"}
	enterPayload, err := encodeTerminalInput(enter)
	if err != nil {
		return err
	}
	// The rendered prompt echo is the paste-complete boundary. Arm semantic
	// completion before Enter so output from a fast child cannot arrive in the
	// gap between key delivery and the state transition.
	c.promptSubmitted.Store(true)
	if err := c.input(ctx, &pb.ChannelFrame{Kind: sdk.ChannelTerminal, Name: "enter", PayloadJson: enterPayload}); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "plugin-tmux: profile %q submitted prompt after rendered echo on socket %s\n", c.profile.Name, c.socket)
	return nil
}

func (c *tmuxChannel) wait(stream sdk.ProviderChannel, screenFailure <-chan error, initialPrompt string, detached bool) error {
	controlDone := make(chan error, 1)
	go func() { controlDone <- c.readControl() }()
	if c.launchPending {
		if err := c.launchEntrypoint(); err != nil {
			return err
		}
	}
	if initialPrompt != "" {
		if err := c.awaitInitialPrompt(stream.Context(), controlDone, screenFailure); err != nil {
			return err
		}
	}
	if err := c.announce(detached); err != nil {
		return fmt.Errorf("plugin-tmux: announce terminal readiness: %w", err)
	}
	if err := c.injectPrompt(stream.Context(), initialPrompt, controlDone, screenFailure); err != nil {
		return fmt.Errorf("plugin-tmux: inject initial prompt: %w", err)
	}
	inputDone := make(chan error, 1)
	go func() { inputDone <- c.readInput(stream) }()

	select {
	case err := <-controlDone:
		if c.closed {
			err = errors.Join(err, c.killServer())
		}
		return err
	case err := <-inputDone:
		if errors.Is(err, errCloseRequested) {
			return c.finishControl(controlDone, false, c.killServer())
		}
		// EOF/cancellation detaches: the run-owned tmux server is deliberately
		// preserved for deterministic reattachment and incident evidence.
		if c.profile.Persistence == "none" {
			return c.finishControl(controlDone, false, errors.Join(err, c.killServer()))
		}
		return c.finishControl(controlDone, true, err)
	case err := <-screenFailure:
		if c.profile.Persistence == "none" {
			return c.finishControl(controlDone, false, errors.Join(err, c.killServer()))
		}
		return c.finishControl(controlDone, true, err)
	case <-stream.Context().Done():
		err := stream.Context().Err()
		if c.profile.Persistence == "none" {
			err = errors.Join(err, c.killServer())
		}
		return errors.Join(err, <-controlDone)
	}
}

func (c *tmuxChannel) finishControl(controlDone <-chan error, detach bool, result error) error {
	if detach {
		if _, err := io.WriteString(c.controlIn, "detach-client\n"); err != nil {
			result = errors.Join(result, fmt.Errorf("detach tmux control client: %w", err))
		}
	}
	return errors.Join(result, <-controlDone)
}

func (c *tmuxChannel) start(stream sdk.ProviderChannel) (bool, error) {
	exists := exec.Command("tmux", "-L", c.socket, "has-session", "-t", "run").Run() == nil
	if !exists {
		args := []string{
			"-L", c.socket, "-f", "/dev/null",
			"set-option", "-g", "history-limit", "100000", ";",
			"set-option", "-gw", "remain-on-exit", "on", ";",
			"new-session", "-d", "-s", "run", "-x", strconv.FormatInt(c.profile.Cols, 10), "-y", strconv.FormatInt(c.profile.Rows, 10),
		}
		if c.profile.WorkingDir != "" {
			args = append(args, "-c", c.profile.WorkingDir)
		}
		launch := exec.Command("tmux", args...)
		launch.Env = append(launch.Environ(), envPairs(c.profile.Env)...)
		if out, err := launch.CombinedOutput(); err != nil {
			return false, fmt.Errorf("tmux new-session: %s: %w", strings.TrimSpace(string(out)), err)
		}
		c.launchPending = true
	} else {
		cols, rows, err := tmuxWindowSize(c.socket)
		if err != nil {
			return true, err
		}
		c.profile.Cols, c.profile.Rows = cols, rows
		c.screen.Resize(int(cols), int(rows))
	}
	args := []string{"-L", c.socket, "-C", "attach-session", "-t", "run"}
	c.control = exec.CommandContext(stream.Context(), "tmux", args...)
	c.control.Env = append(c.control.Environ(), envPairs(c.profile.Env)...)
	stdin, err := c.control.StdinPipe()
	if err != nil {
		return exists, err
	}
	stdout, err := c.control.StdoutPipe()
	if err != nil {
		return exists, err
	}
	stderr, err := c.control.StderrPipe()
	if err != nil {
		return exists, err
	}
	if err := c.control.Start(); err != nil {
		return exists, err
	}
	c.controlIn = stdin
	c.controlOut = stdout
	c.controlErr = stderr
	if _, err := io.WriteString(c.controlIn, "refresh-client -B 'charly-pane:%*:#{pane_dead}:#{pane_dead_status}'\n"); err != nil {
		return exists, fmt.Errorf("tmux pane lifecycle subscription: %w", err)
	}
	return exists, nil
}

// launchEntrypoint establishes an explicit startup boundary for short-lived
// processes. The tmux control client and its reader are attached before the
// run pane is replaced, so even a process that writes and exits immediately is
// observed by the protocol. No polling interval or guessed delay is involved.
func (c *tmuxChannel) launchEntrypoint() error {
	entry := make([]string, len(c.profile.Entrypoint))
	for i, arg := range c.profile.Entrypoint {
		entry[i] = kit.ShellQuote(arg)
	}
	command := "exec " + strings.Join(entry, " ")
	fmt.Fprintf(os.Stderr, "plugin-tmux: profile %q launching entrypoint after control attachment on socket %s\n", c.profile.Name, c.socket)
	if err := runTmux(c.socket, "respawn-pane", "-k", "-t", tmuxPane, command); err != nil {
		return fmt.Errorf("plugin-tmux: launch entrypoint after control attachment: %w", err)
	}
	c.launchPending = false
	return nil
}

func tmuxWindowSize(socket string) (int64, int64, error) {
	output, err := exec.Command("tmux", "-L", socket, "display-message", "-p", "-t", "run:0", "#{window_width} #{window_height}").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("tmux window size: %w", err)
	}
	fields := strings.Fields(string(output))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("tmux window size: malformed output %q", strings.TrimSpace(string(output)))
	}
	cols, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("tmux window width %q: %w", fields[0], err)
	}
	rows, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("tmux window height %q: %w", fields[1], err)
	}
	if cols < 1 || rows < 1 || cols > 1000 || rows > 1000 {
		return 0, 0, fmt.Errorf("tmux window size: invalid dimensions %dx%d", cols, rows)
	}
	return cols, rows, nil
}

func (c *tmuxChannel) readControl() (returnErr error) {
	if os.Getenv("CHARLY_TMUX_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "plugin-tmux: socket %s control reader attached\n", c.socket)
	}
	stderrDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(c.controlErr)
		for scanner.Scan() {
			if err := c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStderr, Data: append([]byte(nil), scanner.Bytes()...)}); err != nil {
				stderrDone <- errors.Join(err, c.controlOut.Close())
				return
			}
		}
		stderrDone <- scanner.Err()
	}()
	defer func() {
		closeErr := normalizeControlClose(c.controlIn.Close())
		waitErr := c.control.Wait()
		stderrErr := normalizeControlClose(<-stderrDone)
		if closeErr != nil {
			closeErr = fmt.Errorf("close tmux control input: %w", closeErr)
		}
		if waitErr != nil {
			waitErr = fmt.Errorf("wait for tmux control client: %w", waitErr)
		}
		if stderrErr != nil {
			stderrErr = fmt.Errorf("read tmux control stderr: %w", stderrErr)
		}
		returnErr = errors.Join(returnErr, closeErr, waitErr, stderrErr)
	}()
	scanner := bufio.NewScanner(c.controlOut)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if os.Getenv("CHARLY_TMUX_DEBUG") == "1" && !strings.HasPrefix(line, "%output ") && !strings.HasPrefix(line, "%extended-output ") {
			fmt.Fprintf(os.Stderr, "plugin-tmux: socket %s control: %s\n", c.socket, line)
		}
		done, err := c.handleControlLine(line)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return normalizeControlClose(err)
	}
	return nil
}

func normalizeControlClose(err error) error {
	if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil
	}
	return err
}

func (c *tmuxChannel) handleControlLine(line string) (bool, error) {
	switch {
	case strings.HasPrefix(line, "%begin "):
		if c.exitBarrierRequested && c.exitBarrierCommand == "" {
			command, err := controlCommandNumber(line)
			if err != nil {
				return false, err
			}
			c.exitBarrierCommand = command
			fmt.Fprintf(os.Stderr, "plugin-tmux: socket %s final-output barrier command %s started\n", c.socket, command)
		}
		return false, nil
	case strings.HasPrefix(line, "%end "):
		if c.exitBarrierCommand == "" {
			return false, nil
		}
		command, err := controlCommandNumber(line)
		if err != nil {
			return false, err
		}
		if command != c.exitBarrierCommand {
			return false, nil
		}
		if c.pendingExit == nil {
			return false, errors.New("plugin-tmux: exit drain barrier completed without a pane status")
		}
		fmt.Fprintf(os.Stderr, "plugin-tmux: socket %s final-output barrier command %s completed; reporting exit %d\n", c.socket, command, *c.pendingExit)
		return true, c.sendPaneExit(*c.pendingExit, true)
	case strings.HasPrefix(line, "%error "):
		if c.exitBarrierCommand == "" {
			return false, nil
		}
		command, err := controlCommandNumber(line)
		if err != nil {
			return false, err
		}
		if command == c.exitBarrierCommand {
			fmt.Fprintf(os.Stderr, "plugin-tmux: socket %s final-output barrier command %s failed\n", c.socket, command)
			return false, fmt.Errorf("plugin-tmux: final output drain barrier command %s failed", command)
		}
		return false, nil
	case strings.HasPrefix(line, "%output ") || strings.HasPrefix(line, "%extended-output "):
		return c.handleControlOutput(line)
	case strings.HasPrefix(line, "%subscription-changed charly-pane "):
		dead, code, err := parsePaneSubscription(line)
		if err != nil || !dead {
			return false, err
		}
		// A format subscription observes pane death but is not itself an
		// output boundary. Submit a control command after the observation and
		// finish only at its matching %end block. The tmux control protocol
		// forbids notifications inside command blocks, giving us an explicit,
		// event-driven drain boundary without polling or a guessed delay.
		c.pendingExit = &code
		if c.exitBarrierRequested {
			return false, nil
		}
		c.exitBarrierRequested = true
		fmt.Fprintf(os.Stderr, "plugin-tmux: socket %s observed pane exit %d; requesting final-output barrier\n", c.socket, code)
		if _, err := io.WriteString(c.controlIn, "display-message -p -t run:0.0 '#{pane_dead_status}'\n"); err != nil {
			return false, fmt.Errorf("plugin-tmux: request final output drain barrier: %w", err)
		}
		return false, nil
	case strings.HasPrefix(line, "%exit"):
		if c.pendingExit != nil {
			return true, c.sendPaneExit(*c.pendingExit, true)
		}
		return true, nil
	default:
		return false, nil
	}
}

func controlCommandNumber(line string) (string, error) {
	fields := strings.Fields(line)
	if len(fields) != 4 || (fields[0] != "%begin" && fields[0] != "%end" && fields[0] != "%error") {
		return "", fmt.Errorf("plugin-tmux: malformed control command boundary %q", line)
	}
	if _, err := strconv.ParseUint(fields[2], 10, 64); err != nil {
		return "", fmt.Errorf("plugin-tmux: malformed control command number in %q: %w", line, err)
	}
	return fields[2], nil
}

func (c *tmuxChannel) handleControlOutput(line string) (bool, error) {
	data, err := decodeControlOutput(line)
	if err != nil {
		return false, err
	}
	mode := c.profile.Transcript
	if mode == "" {
		mode = "both"
	}
	if mode == "raw" || mode == "both" {
		if err := c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelTerminal, Data: data}); err != nil {
			return false, err
		}
	}
	if _, err := c.screen.Write(data); err != nil {
		return false, fmt.Errorf("terminal emulator: %w", err)
	}
	c.observeInitialPrompt()
	c.observePromptEcho()
	screen := c.plainScreen()
	if mode == "screen" || mode == "both" {
		if err := c.sendScreen(sdk.ChannelStatus); err != nil {
			return false, err
		}
	}
	if !c.hadInput || !c.promptSubmitted.Load() || c.settled || c.settledRE == nil || !c.settledRE.MatchString(screen) {
		return false, nil
	}
	c.settled = true
	if err := c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "settled"}); err != nil {
		return false, err
	}
	// A proven semantic adapter is an explicit completion boundary for an agent
	// runtime. End this client while preserving the run-owned tmux server.
	return c.agentRuntime, nil
}

func (c *tmuxChannel) sendPaneExit(code int32, refresh bool) error {
	if refresh {
		snapshot, err := c.captureScreen()
		if err != nil {
			return err
		}
		if _, err := c.screen.Write(append([]byte("\x1b[H\x1b[2J"), snapshot...)); err != nil {
			return fmt.Errorf("terminal emulator final screen: %w", err)
		}
		if err := c.sendScreen(sdk.ChannelResync); err != nil {
			return err
		}
	}
	c.closed = code == 0 && terminalOperationOwnsNaturalExit(c.operation)
	return c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelExit, ExitCode: code})
}

func terminalOperationOwnsNaturalExit(operation string) bool {
	return operation == "run" || operation == "attach"
}

func (c *tmuxChannel) readInput(stream sdk.ProviderChannel) error {
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if frame.GetReplayFrom() != 0 {
			if err := c.sender.replayFrom(frame.GetReplayFrom()); err != nil {
				return c.sendScreen(sdk.ChannelResync)
			}
			continue
		}
		if err := c.input(stream.Context(), frame); err != nil {
			return errors.Join(err, c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelError, Error: err.Error()}))
		}
		if frame.GetSequence() != 0 {
			if err := c.sender.send(&pb.ChannelFrame{Kind: sdk.ChannelAck, AckSequence: frame.GetSequence()}); err != nil {
				return err
			}
		}
		// A close mutation is complete only after its acknowledgement is on the
		// channel. Killing the tmux server inside input() would tear down control
		// mode first and leave the controller with an ambiguous EOF.
		if c.closing {
			return errCloseRequested
		}
	}
}

func (c *tmuxChannel) input(ctx context.Context, frame *pb.ChannelFrame) error {
	input, err := decodeTerminalInput(frame)
	if err != nil {
		return err
	}
	switch input.Kind {
	case "text":
		c.hadInput = true
		c.settled = false
		return runTmux(c.socket, "send-keys", "-t", tmuxPane, "-l", "--", input.Text)
	case "paste":
		c.hadInput = true
		c.settled = false
		load := exec.Command("tmux", "-L", c.socket, "load-buffer", "-")
		load.Stdin = strings.NewReader(input.Text)
		if out, err := load.CombinedOutput(); err != nil {
			return fmt.Errorf("tmux load-buffer: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return runTmux(c.socket, "paste-buffer", "-d", "-t", tmuxPane)
	case "command":
		return c.runCommandInput(ctx, input.Text, frame.GetSequence())
	case "key":
		if !profileAllows(c.profile.Keys, input.Key) {
			return fmt.Errorf("terminal key %q is not enabled by profile", input.Key)
		}
		key, ok := canonicalKeys[input.Key]
		if !ok {
			return fmt.Errorf("terminal key %q is not allowed", input.Key)
		}
		return runTmux(c.socket, "send-keys", "-t", tmuxPane, key)
	case "resize":
		if input.Cols <= 0 || input.Rows <= 0 || input.Cols > 1000 || input.Rows > 1000 {
			return fmt.Errorf("invalid terminal size %dx%d", input.Cols, input.Rows)
		}
		if err := runTmux(c.socket, "resize-window", "-t", "run:0", "-x", strconv.FormatInt(input.Cols, 10), "-y", strconv.FormatInt(input.Rows, 10)); err != nil {
			return err
		}
		c.profile.Cols, c.profile.Rows = input.Cols, input.Rows
		c.screen.Resize(int(input.Cols), int(input.Rows))
		return c.sendScreen(sdk.ChannelStatus)
	case "signal":
		if !profileAllows(c.profile.Signals, input.Signal) {
			return fmt.Errorf("terminal signal %q is not enabled by profile", input.Signal)
		}
		key, ok := signalKeys[input.Signal]
		if !ok {
			return fmt.Errorf("terminal signal %q is not allowed", input.Signal)
		}
		return runTmux(c.socket, "send-keys", "-t", tmuxPane, key)
	case "close":
		c.closed = true
		c.closing = true
		return nil
	case "ack":
		c.sender.acknowledge(frame.GetAckSequence())
		return nil
	default:
		return fmt.Errorf("unsupported terminal input kind %q", input.Kind)
	}
}

func (c *tmuxChannel) runCommandInput(ctx context.Context, command string, sequence uint64) error {
	token := "charly-command-" + safeID(c.sender.request) + "-" + strconv.FormatUint(sequence, 10)
	if err := runTmux(c.socket, "wait-for", "-L", token); err != nil {
		return fmt.Errorf("tmux command completion lock %s: %w", token, err)
	}
	defer func() { _ = runTmux(c.socket, "wait-for", "-U", token) }()
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	waiter := exec.CommandContext(waitCtx, "tmux", "-L", c.socket, "wait-for", "-L", token)
	if err := waiter.Start(); err != nil {
		return fmt.Errorf("tmux command completion waiter %s: %w", token, err)
	}
	signal := "tmux -L " + kit.ShellQuote(c.socket) + " wait-for -U " + kit.ShellQuote(token)
	wrapped := "sh -lc " + kit.ShellQuote(command) + "; " + signal
	fmt.Fprintf(os.Stderr, "plugin-tmux: socket %s command sequence %d waiting for event %s\n", c.socket, sequence, token)
	if err := runTmux(c.socket, "send-keys", "-t", tmuxPane, "-l", "--", wrapped); err != nil {
		cancel()
		return errors.Join(err, waiter.Wait())
	}
	if err := runTmux(c.socket, "send-keys", "-t", tmuxPane, "Enter"); err != nil {
		cancel()
		return errors.Join(err, waiter.Wait())
	}
	if err := waiter.Wait(); err != nil {
		return fmt.Errorf("tmux command completion event %s: %w", token, err)
	}
	fmt.Fprintf(os.Stderr, "plugin-tmux: socket %s command sequence %d completed event %s\n", c.socket, sequence, token)
	return nil
}

func decodeTerminalInput(frame *pb.ChannelFrame) (spec.TerminalInput, error) {
	if frame.GetKind() == sdk.ChannelAck {
		return spec.TerminalInput{Kind: "ack"}, nil
	}
	var input spec.TerminalInput
	if len(frame.GetPayloadJson()) == 0 {
		return input, errors.New("terminal input requires the CUE-generated TerminalInput payload")
	}
	if err := json.Unmarshal(frame.GetPayloadJson(), &input); err != nil {
		return input, fmt.Errorf("decode TerminalInput: %w", err)
	}
	if err := sdk.ValidateGenerated("#TerminalInput", input); err != nil {
		return input, err
	}
	if !terminalInputEnvelopeMatches(input, frame) {
		return input, errors.New("TerminalInput payload does not match ChannelFrame discriminator")
	}
	return input, nil
}

func encodeTerminalInput(input spec.TerminalInput) ([]byte, error) {
	if err := sdk.ValidateGenerated("#TerminalInput", input); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode TerminalInput: %w", err)
	}
	return payload, nil
}

func terminalInputEnvelopeMatches(input spec.TerminalInput, frame *pb.ChannelFrame) bool {
	switch input.Kind {
	case "text", "paste", "command":
		return frame.GetKind() == sdk.ChannelStdin && frame.GetName() == input.Kind && string(frame.GetData()) == input.Text
	case "key":
		return frame.GetKind() == sdk.ChannelTerminal && frame.GetName() == input.Key
	case "resize":
		return frame.GetKind() == sdk.ChannelResize && int64(frame.GetCols()) == input.Cols && int64(frame.GetRows()) == input.Rows
	case "signal":
		return frame.GetKind() == sdk.ChannelSignal && frame.GetName() == input.Signal
	case "close":
		return frame.GetKind() == sdk.ChannelExit || frame.GetKind() == sdk.ChannelCancel
	default:
		return false
	}
}

func (c *tmuxChannel) captureScreen() ([]byte, error) {
	// Include the full retained history. Detached input intentionally returns as
	// soon as tmux acknowledges it, so output may arrive with no controller
	// attached; the next snapshot/reattach is the reconciliation boundary that
	// must make those bytes durable evidence.
	return exec.Command("tmux", "-L", c.socket, "capture-pane", "-p", "-S", "-", "-t", tmuxPane).Output()
}

func (c *tmuxChannel) plainScreen() string { return ansi.Strip(c.screen.Render()) }

func (c *tmuxChannel) sendScreen(kind string) error {
	screen := c.plainScreen()
	pos := c.screen.CursorPosition()
	frame := &pb.ChannelFrame{
		Kind: kind, Name: "screen", Data: []byte(screen),
		Cols: uint32(c.profile.Cols), Rows: uint32(c.profile.Rows),
	}
	return c.sender.sendWithPayload(frame, func(sequence uint64) ([]byte, error) {
		snapshot := spec.TerminalSnapshot{
			RunID: spec.UUIDv7(c.sender.request), Sequence: int64(sequence),
			Cols: c.profile.Cols, Rows: c.profile.Rows, Screen: screen,
			CursorCol: int64(pos.X), CursorRow: int64(pos.Y),
		}
		if err := sdk.ValidateGenerated("#TerminalSnapshot", snapshot); err != nil {
			return nil, err
		}
		payload, err := json.Marshal(snapshot)
		if err != nil {
			return nil, fmt.Errorf("encode TerminalSnapshot: %w", err)
		}
		return payload, nil
	})
}

func (c *tmuxChannel) killServer() error { return runTmux(c.socket, "kill-server") }

func profileAllows(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, allowed := range values {
		if allowed == value {
			return true
		}
	}
	return false
}
func settledPattern(profile spec.TerminalProfile) string {
	if value, ok := profile.Readiness["settled_regex"].(string); ok {
		return value
	}
	switch profile.SemanticAdapter {
	case "claude-code":
		return `(?m)^❯(?:[ \t]|\x{00a0})*$`
	case "codex":
		return `(?m)^›(?:[ \t]|\x{00a0})*$`
	case "gemini":
		return `(?im)^(?:>|type your message)(?:[ \t]|\x{00a0})*$`
	}
	return ""
}

func initialPromptPattern(profile spec.TerminalProfile) string {
	if value, ok := profile.Readiness["prompt_regex"].(string); ok {
		return value
	}
	switch profile.SemanticAdapter {
	case "claude-code":
		return `(?m)^❯(?:[[:space:]]|\x{00a0})`
	case "codex":
		return `(?m)^›(?:[[:space:]]|\x{00a0})`
	case "gemini":
		return `(?im)^(?:>|type your message)(?:[[:space:]]|\x{00a0})`
	}
	return ""
}

func runTmux(socket string, args ...string) error {
	argv := append([]string{"-L", socket}, args...)
	out, err := exec.Command("tmux", argv...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux %s: %s: %w", args[0], strings.TrimSpace(string(out)), err)
	}
	return nil
}

func envPairs(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	// Determinism is useful in process evidence even though environment order is
	// semantically irrelevant.
	slicesSort(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func slicesSort(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func safeID(value string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsDigit(r) || (r >= 'a' && r <= 'z') {
			out.WriteRune(r)
		}
	}
	if out.Len() == 0 {
		return "invalid"
	}
	return out.String()
}

func decodeControlOutput(line string) ([]byte, error) {
	var s string
	if strings.HasPrefix(line, "%extended-output ") {
		separator := strings.Index(line, " : ")
		if separator < 0 {
			return nil, fmt.Errorf("malformed tmux extended control output %q", line)
		}
		s = line[separator+3:]
	} else {
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("malformed tmux control output %q", line)
		}
		s = parts[2]
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] >= '0' && s[i+1] <= '7' && s[i+2] >= '0' && s[i+2] <= '7' && s[i+3] >= '0' && s[i+3] <= '7' {
			value, err := strconv.ParseUint(s[i+1:i+4], 8, 8)
			if err != nil {
				return nil, fmt.Errorf("decode tmux octal output %q: %w", s[i+1:i+4], err)
			}
			out = append(out, byte(value))
			i += 3
			continue
		}
		out = append(out, s[i])
	}
	return out, nil
}

func parsePaneSubscription(line string) (bool, int32, error) {
	separator := strings.LastIndex(line, " : ")
	if separator < 0 {
		return false, 0, fmt.Errorf("malformed tmux pane subscription %q", line)
	}
	value := strings.Split(line[separator+3:], ":")
	if len(value) != 2 {
		return false, 0, fmt.Errorf("malformed tmux pane state %q", line[separator+3:])
	}
	if value[0] == "0" {
		return false, 0, nil
	}
	code, err := strconv.ParseInt(value[1], 10, 32)
	if err != nil {
		return false, 0, fmt.Errorf("malformed tmux pane exit status %q: %w", value[1], err)
	}
	return true, int32(code), nil
}

var canonicalKeys = map[string]string{
	"enter": "Enter", "escape": "Escape", "tab": "Tab", "backspace": "BSpace",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End", "page-up": "PPage", "page-down": "NPage",
	"delete": "DC", "insert": "IC", "ctrl-c": "C-c", "ctrl-d": "C-d",
	"ctrl-z": "C-z", "ctrl-l": "C-l", "ctrl-a": "C-a", "ctrl-e": "C-e",
}

var signalKeys = map[string]string{"interrupt": "C-c", "eof": "C-d", "suspend": "C-z"}
