package tmux

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/agentkit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/targetkit"
	"github.com/opencharly/sdk/testkit"
	"google.golang.org/grpc"
)

func TestTmuxGRPCHelperProcess(t *testing.T) {
	if os.Getenv("CHARLY_TMUX_GRPC_HELPER") != "1" {
		return
	}
	err := targetkit.ServeStdio(os.Stdin, os.Stdout, func(server *grpc.Server) {
		pb.RegisterProviderServer(server, NewProvider())
	})
	if err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(2)
	}
	os.Exit(0)
}

func TestDecodeControlOutput(t *testing.T) {
	got, err := decodeControlOutput(`%output %0 hello\015\012unicode-✓`)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("hello\r\nunicode-✓")
	if !bytes.Equal(got, want) {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestTypedTerminalInputDiscriminator(t *testing.T) {
	payload, err := json.Marshal(spec.TerminalInput{Kind: "paste", Text: "one\ntwo"})
	if err != nil {
		t.Fatal(err)
	}
	frame := &pb.ChannelFrame{Kind: sdk.ChannelStdin, Name: "paste", Data: []byte("one\ntwo"), PayloadJson: payload}
	input, err := decodeTerminalInput(frame)
	if err != nil {
		t.Fatal(err)
	}
	if input.Kind != "paste" || input.Text != "one\ntwo" {
		t.Fatalf("decoded input = %#v", input)
	}
	frame.Name = "text"
	if _, err := decodeTerminalInput(frame); err == nil {
		t.Fatal("mismatched CUE payload and protobuf discriminator accepted")
	}
	if _, err := decodeTerminalInput(&pb.ChannelFrame{Kind: sdk.ChannelStdin, Name: "text", Data: []byte("legacy")}); err == nil {
		t.Fatal("terminal input without the CUE-generated payload accepted")
	}
	commandPayload, err := encodeTerminalInput(spec.TerminalInput{Kind: "command", Text: "printf COMPLETE"})
	if err != nil {
		t.Fatal(err)
	}
	command, err := decodeTerminalInput(&pb.ChannelFrame{Kind: sdk.ChannelStdin, Name: "command", Data: []byte("printf COMPLETE"), PayloadJson: commandPayload})
	if err != nil || command.Kind != "command" {
		t.Fatalf("typed command input = %#v, error %v", command, err)
	}
}

func TestCommandInputReturnsAfterShellCompletionEvent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	socket := fmt.Sprintf("charly-command-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	if output, err := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "run", "-x", "80", "-y", "24", "sh").CombinedOutput(); err != nil {
		t.Fatalf("create command fixture: %s: %v", output, err)
	}
	channel := &tmuxChannel{socket: socket, sender: &channelSender{request: "0198f140-6b7a-7b90-8a10-123456789abc"}}
	if err := channel.runCommandInput(context.Background(), "printf COMMAND-EVENT-COMPLETE", 1); err != nil {
		t.Fatal(err)
	}
	screen, err := channel.captureScreen()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(screen, []byte("COMMAND-EVENT-COMPLETE")) {
		t.Fatalf("command completion event preceded shell output: %q", screen)
	}
}

func TestChannelSenderReplaysUnacknowledgedFrames(t *testing.T) {
	stream := &testChannel{ctx: context.Background(), notify: make(chan struct{}, 1)}
	sender := &channelSender{stream: stream, request: "request", next: 1, replay: sdk.NewReplayBuffer(4, 4096)}
	if err := sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "two"}); err != nil {
		t.Fatal(err)
	}
	stream.mu.Lock()
	stream.frames = nil
	stream.mu.Unlock()
	if err := sender.replayFrom(1); err != nil {
		t.Fatal(err)
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.frames) != 2 || stream.frames[0].GetSequence() != 1 || stream.frames[1].GetSequence() != 2 {
		t.Fatalf("replayed frames = %#v", stream.frames)
	}
}

func TestTmuxReattachUsesLiveWindowSize(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	socket := fmt.Sprintf("charly-resize-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	if output, err := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "run", "-x", "80", "-y", "24", "sh").CombinedOutput(); err != nil {
		t.Fatalf("create fixture: %s: %v", output, err)
	}
	if err := runTmux(socket, "resize-window", "-t", "run:0", "-x", "90", "-y", "30"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream := &testChannel{ctx: ctx, notify: make(chan struct{}, 1)}
	channel := &tmuxChannel{
		profile: spec.TerminalProfile{Cols: 80, Rows: 24},
		socket:  socket,
		screen:  vt.NewSafeEmulator(80, 24),
	}
	defer channel.screen.Close() //nolint:errcheck
	exists, err := channel.start(stream)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = channel.control.Wait()
	if !exists || channel.profile.Cols != 90 || channel.profile.Rows != 30 || channel.screen.Width() != 90 || channel.screen.Height() != 30 {
		t.Fatalf("reattached state = exists:%v profile:%dx%d screen:%dx%d, want true/90x30/90x30", exists, channel.profile.Cols, channel.profile.Rows, channel.screen.Width(), channel.screen.Height())
	}
}

func TestCaptureScreenReconcilesDetachedHistory(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	socket := fmt.Sprintf("charly-history-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	command := "printf 'DETACHED-EVIDENCE-OK\\n'; seq 1 40; sleep 30"
	if output, err := exec.Command("tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "run", "-x", "80", "-y", "5", "sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("create history fixture: %s: %v", output, err)
	}
	stream := &testChannel{ctx: context.Background(), notify: make(chan struct{}, 1)}
	channel := &tmuxChannel{
		profile: spec.TerminalProfile{Cols: 80, Rows: 5, Transcript: "both"},
		socket:  socket,
		sender:  &channelSender{stream: stream, request: "0198f140-6b7a-7b90-8a10-102030405060", next: 1, replay: sdk.NewReplayBuffer(16, 1<<20)},
		screen:  vt.NewSafeEmulator(80, 5),
	}
	drain := drainTerminalResponses(channel.screen)
	defer func() {
		if err := drain.stop(); err != nil {
			t.Errorf("stop terminal response drain: %v", err)
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		screen, err := channel.captureScreen()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(screen, []byte("DETACHED-EVIDENCE-OK")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("detached history missing from capture: %q", screen)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := channel.synchronizeScreen(true); err != nil {
		t.Fatal(err)
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.frames) == 0 || stream.frames[0].GetKind() != sdk.ChannelTerminal || !bytes.Contains(stream.frames[0].GetData(), []byte("DETACHED-EVIDENCE-OK")) {
		t.Fatalf("reconciled frames missing detached evidence: %#v", stream.frames)
	}
}

func TestVirtualScreenHandlesAltScreenUnicodeCursorAndResize(t *testing.T) {
	ch := &tmuxChannel{
		profile: spec.TerminalProfile{Cols: 12, Rows: 4},
		screen:  vt.NewSafeEmulator(12, 4),
	}
	drain := drainTerminalResponses(ch.screen)
	defer func() {
		if err := drain.stop(); err != nil {
			t.Errorf("stop terminal response drain: %v", err)
		}
	}()
	if _, err := ch.screen.Write([]byte("main界\x1b[2;3HX")); err != nil {
		t.Fatal(err)
	}
	main := ch.plainScreen()
	if !strings.Contains(main, "main界") || !strings.Contains(main, "  X") {
		t.Fatalf("main screen = %q", main)
	}
	if _, err := ch.screen.Write([]byte("\x1b[?1049h\x1b[2J\x1b[Halternate✓")); err != nil {
		t.Fatal(err)
	}
	if alt := ch.plainScreen(); !strings.Contains(alt, "alternate✓") || strings.Contains(alt, "main界") {
		t.Fatalf("alternate screen = %q", alt)
	}
	if _, err := ch.screen.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatal(err)
	}
	if restored := ch.plainScreen(); !strings.Contains(restored, "main界") {
		t.Fatalf("restored screen = %q", restored)
	}
	ch.screen.Resize(20, 6)
	if ch.screen.Width() != 20 || ch.screen.Height() != 6 {
		t.Fatalf("resized screen = %dx%d", ch.screen.Width(), ch.screen.Height())
	}
}

type testChannel struct {
	ctx    context.Context
	mu     sync.Mutex
	frames []*pb.ChannelFrame
	notify chan struct{}
	input  chan *pb.ChannelFrame
}

func (s *testChannel) Context() context.Context { return s.ctx }
func (s *testChannel) Recv() (*pb.ChannelFrame, error) {
	if s.input == nil {
		<-s.ctx.Done()
		return nil, io.EOF
	}
	select {
	case <-s.ctx.Done():
		return nil, io.EOF
	case frame, ok := <-s.input:
		if !ok {
			return nil, io.EOF
		}
		return frame, nil
	}
}

func TestConcurrentTmuxRunsIsolateTypedPasteAndHighVolumeOutput(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	type fixture struct {
		id      string
		own     string
		foreign string
		stream  *testChannel
		done    chan error
	}
	fixtures := []*fixture{
		{id: "0198f140-6b7a-7b90-8a10-aabbccddeeff", own: "ALPHA", foreign: "BRAVO"},
		{id: "0198f140-6b7a-7b90-8a10-ffeeddccbbaa", own: "BRAVO", foreign: "ALPHA"},
	}
	for _, item := range fixtures {
		profile, err := json.Marshal(spec.TerminalProfile{
			Name: "isolation", Entrypoint: []string{"sh", "-c", `IFS= read -r first; IFS= read -r second; printf 'PASTE:%s|%s\n' "$first" "$second"; i=0; while [ "$i" -lt 256 ]; do printf '%s-%03d\n' "$first" "$i"; i=$((i+1)); done`},
			Cols: 100, Rows: 30, Persistence: "none", Transcript: "both",
		})
		if err != nil {
			t.Fatal(err)
		}
		item.stream = &testChannel{ctx: ctx, notify: make(chan struct{}, 1), input: make(chan *pb.ChannelFrame, 2)}
		item.done = make(chan error, 1)
		go func(item *fixture, profile []byte) {
			item.done <- openTmuxChannel(&pb.ChannelFrame{
				Kind: sdk.ChannelOpen, RequestId: item.id, Class: "terminal", Reserved: "tmux", Op: "run", PayloadJson: profile,
			}, item.stream, "")
		}(item, profile)
	}
	for _, item := range fixtures {
		if err := waitForTestFrame(ctx, item.stream, func(frame *pb.ChannelFrame) bool {
			return frame.GetKind() == sdk.ChannelStatus && frame.GetName() == "running"
		}); err != nil {
			t.Fatal(err)
		}
		input := spec.TerminalInput{Kind: "paste", Text: item.own + "\nsecond-line\n"}
		payload, err := encodeTerminalInput(input)
		if err != nil {
			t.Fatal(err)
		}
		item.stream.input <- &pb.ChannelFrame{Kind: sdk.ChannelStdin, Name: "paste", Data: []byte(input.Text), PayloadJson: payload, Sequence: 1}
	}
	for _, item := range fixtures {
		select {
		case err := <-item.done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		item.stream.mu.Lock()
		frames := append([]*pb.ChannelFrame(nil), item.stream.frames...)
		item.stream.mu.Unlock()
		var output bytes.Buffer
		var previous uint64
		for _, frame := range frames {
			if frame.GetKind() != sdk.ChannelAck {
				if frame.GetSequence() <= previous {
					t.Fatalf("run %s sequence %d followed %d", item.id, frame.GetSequence(), previous)
				}
				previous = frame.GetSequence()
			}
			if frame.GetKind() == sdk.ChannelTerminal {
				output.Write(frame.GetData())
			}
		}
		got := output.String()
		if !strings.Contains(got, "PASTE:"+item.own+"|second-line") || !strings.Contains(got, item.own+"-000") || !strings.Contains(got, item.own+"-255") {
			t.Fatalf("run %s did not preserve typed paste and high-volume boundaries: %q", item.id, got)
		}
		if strings.Contains(got, item.foreign+"-") || strings.Contains(got, "PASTE:"+item.foreign) {
			t.Fatalf("run %s leaked output from %s: %q", item.id, item.foreign, got)
		}
		if err := exec.Command("tmux", "-L", "charly-"+safeID(item.id), "has-session", "-t", "run").Run(); err == nil {
			t.Fatalf("non-persistent isolated tmux server %s survived clean completion", item.id)
		}
	}
}

func waitForTestFrame(ctx context.Context, stream *testChannel, predicate func(*pb.ChannelFrame) bool) error {
	for {
		stream.mu.Lock()
		frames := append([]*pb.ChannelFrame(nil), stream.frames...)
		stream.mu.Unlock()
		for _, frame := range frames {
			if predicate(frame) {
				return nil
			}
		}
		select {
		case <-stream.notify:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (s *testChannel) Send(frame *pb.ChannelFrame) error {
	s.mu.Lock()
	s.frames = append(s.frames, frame)
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
	return nil
}

func TestTmuxChannelLifecycle(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	profile, err := json.Marshal(spec.TerminalProfile{
		Name: "fixture", Entrypoint: []string{"sh", "-c", "IFS= read -r line; printf 'CHARLY_TMUX_MARKER:%s\\n' \"$line\"; exit 7"}, Cols: 80, Rows: 24, Persistence: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream := &testChannel{ctx: ctx, notify: make(chan struct{}, 1), input: make(chan *pb.ChannelFrame, 1)}
	requestID := string(agentkit.NewID())
	done := make(chan error, 1)
	go func() {
		done <- openTmuxChannel(&pb.ChannelFrame{
			Kind: sdk.ChannelOpen, RequestId: requestID, Class: "terminal", Reserved: "tmux", Op: "launch", PayloadJson: profile,
		}, stream, "")
	}()
	if err := waitForTestFrame(ctx, stream, func(frame *pb.ChannelFrame) bool {
		return frame.GetKind() == sdk.ChannelStatus && frame.GetName() == "running"
	}); err != nil {
		select {
		case openErr := <-done:
			t.Fatalf("tmux channel ended before readiness: %v", openErr)
		default:
			t.Fatal(err)
		}
	}
	input := spec.TerminalInput{Kind: "paste", Text: "ready\n"}
	payload, err := encodeTerminalInput(input)
	if err != nil {
		t.Fatal(err)
	}
	stream.input <- &pb.ChannelFrame{Kind: sdk.ChannelStdin, Name: "paste", Data: []byte(input.Text), PayloadJson: payload, Sequence: 1}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	var sawMarker, sawExit bool
	for _, frame := range stream.frames {
		if bytes.Contains(frame.GetData(), []byte("CHARLY_TMUX_MARKER")) {
			sawMarker = true
		}
		if frame.GetKind() == sdk.ChannelExit && frame.GetExitCode() == 7 {
			sawExit = true
		}
	}
	if !sawMarker || !sawExit {
		t.Fatalf("frames did not preserve output+exit: marker=%v exit=%v frames=%v", sawMarker, sawExit, stream.frames)
	}
}

func TestCloseAcknowledgesBeforeKillingRunOwnedServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	profile, err := json.Marshal(spec.TerminalProfile{Name: "close-order", Entrypoint: []string{"sh"}, Cols: 80, Rows: 24, Persistence: "detach", Transcript: "both"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream := &testChannel{ctx: ctx, notify: make(chan struct{}, 1), input: make(chan *pb.ChannelFrame, 1)}
	requestID := string(agentkit.NewID())
	done := make(chan error, 1)
	go func() {
		done <- openTmuxChannel(&pb.ChannelFrame{Kind: sdk.ChannelOpen, RequestId: requestID, Class: "terminal", Reserved: "tmux", Op: "launch", PayloadJson: profile}, stream, "")
	}()
	if err := waitForTestFrame(ctx, stream, func(frame *pb.ChannelFrame) bool {
		return frame.GetKind() == sdk.ChannelStatus && frame.GetName() == "running"
	}); err != nil {
		t.Fatal(err)
	}
	payload, err := encodeTerminalInput(spec.TerminalInput{Kind: "close"})
	if err != nil {
		t.Fatal(err)
	}
	stream.input <- &pb.ChannelFrame{Kind: sdk.ChannelCancel, Name: "close", PayloadJson: payload, Sequence: 1}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	stream.mu.Lock()
	frames := append([]*pb.ChannelFrame(nil), stream.frames...)
	stream.mu.Unlock()
	var acknowledged bool
	for _, frame := range frames {
		if frame.GetKind() == sdk.ChannelAck && frame.GetAckSequence() == 1 {
			acknowledged = true
		}
	}
	if !acknowledged {
		t.Fatalf("close ended without acknowledgement: %#v", frames)
	}
	if err := exec.Command("tmux", "-L", "charly-"+safeID(requestID), "has-session", "-t", "run").Run(); err == nil {
		t.Fatal("run-owned tmux server survived acknowledged close")
	}
}

func TestNaturalExitCleanupBelongsToOwningTerminalOperation(t *testing.T) {
	for _, test := range []struct {
		operation string
		closed    bool
	}{
		{operation: "run", closed: true},
		{operation: "attach", closed: true},
		{operation: "launch", closed: false},
		{operation: "snapshot", closed: false},
		{operation: "control", closed: false},
	} {
		t.Run(test.operation, func(t *testing.T) {
			if got := terminalOperationOwnsNaturalExit(test.operation); got != test.closed {
				t.Fatalf("owns natural exit = %v, want %v", got, test.closed)
			}
		})
	}
}

func TestTmuxChannelOverRealSSHAndGRPC(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("OpenSSH client unavailable")
	}
	server := testkit.StartSSHProcessServer(t, func(_ string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTmuxGRPCHelperProcess$")
		cmd.Env = append(os.Environ(), "CHARLY_TMUX_GRPC_HELPER=1")
		return cmd
	})
	t.Setenv("HOME", server.Home)
	target := spec.TargetSpec{Hops: []spec.TargetHop{
		{Transport: "ssh", Address: server.Address, User: "agent", Port: server.Port, IdentityFile: server.IdentityFile, Options: spec.StrMap{
			"IdentitiesOnly": "yes", "LogLevel": "ERROR", "StrictHostKeyChecking": "no", "UserKnownHostsFile": "/dev/null",
		}},
		{Transport: "grpc"}, {Transport: "tmux"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, client, err := targetkit.DialProvider(ctx, target, targetkit.DialOptions{Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close SSH tmux connection: %v", err)
		}
	})
	requestID := spec.UUIDv7("0198f140-6b7a-7b90-8a10-112233445566")
	profile, err := json.Marshal(spec.TerminalProfile{
		Name: "ssh-grpc-tmux", Entrypoint: []string{"sh", "-c", "printf SSH-GRPC-TMUX-LIVE; exit 0"},
		Cols: 80, Rows: 24, Persistence: "none", Transcript: "both",
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := sdk.OpenProviderChannel(ctx, client, &pb.ChannelFrame{
		Kind: sdk.ChannelOpen, RequestId: string(requestID), Class: "terminal", Reserved: "tmux", Op: "run", PayloadJson: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	gate := sdk.NewSequenceGate(1)
	var marker, exited bool
	for !exited {
		frame, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if err := gate.Accept(frame); err != nil {
			t.Fatal(err)
		}
		exited = frame.GetKind() == sdk.ChannelExit && frame.GetExitCode() == 0
		if !exited {
			if err := stream.Send(&pb.ChannelFrame{Kind: sdk.ChannelAck, AckSequence: frame.GetSequence()}); err != nil {
				t.Fatal(err)
			}
		}
		marker = marker || bytes.Contains(frame.GetData(), []byte("SSH-GRPC-TMUX-LIVE"))
	}
	if !marker {
		t.Fatal("tmux output did not cross the real SSH gRPC channel")
	}
}

func TestTmuxAgentRuntimeInjectsPromptAndSettlesWithoutKillingSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	requestID := spec.UUIDv7("0198f140-6b7a-7b90-8a10-010203040506")
	profile := spec.TerminalProfile{
		Name: "fixture-agent", Entrypoint: []string{"sh", "-c", "read line; printf 'AGENT-GOT:%s\\n' \"$line\"; exec sh"},
		Cols: 80, Rows: 24, Persistence: "detach", Transcript: "both",
		Readiness: map[string]any{"settled_regex": `AGENT-GOT:hello`},
	}
	request := spec.AgentRunRequest{
		ID: requestID, SessionID: requestID, RequestID: requestID,
		IdempotencyKey: "fixture", Prompt: "hello", Params: map[string]any{"terminal_profile": profile},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream := &testChannel{ctx: ctx, notify: make(chan struct{}, 1)}
	if err := (provider{}).OpenChannel(&pb.ChannelFrame{
		Kind: sdk.ChannelOpen, RequestId: string(requestID), Class: "agent-runtime", Reserved: "tmux", Op: "run", PayloadJson: payload,
	}, stream); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", "charly-"+safeID(string(requestID)), "kill-server").Run() })
	stream.mu.Lock()
	defer stream.mu.Unlock()
	var sawPrompt, sawSettled bool
	for _, frame := range stream.frames {
		if bytes.Contains(frame.GetData(), []byte("AGENT-GOT:hello")) {
			sawPrompt = true
		}
		if frame.GetKind() == sdk.ChannelStatus && frame.GetName() == "settled" {
			sawSettled = true
		}
	}
	if !sawPrompt || !sawSettled {
		t.Fatalf("agent runtime frames missing prompt/settled: prompt=%v settled=%v frames=%v", sawPrompt, sawSettled, stream.frames)
	}
	if err := exec.Command("tmux", "-L", "charly-"+safeID(string(requestID)), "has-session", "-t", "run").Run(); err != nil {
		t.Fatalf("settled agent session was not preserved for follow-up: %v", err)
	}
}

func TestSemanticAdapterPatternsAndProfileAllowlists(t *testing.T) {
	for _, tc := range []struct{ name, settled, prompt string }{
		{"claude-code", "done\n❯\u00a0\n", "done\n❯\u00a0Try fixing a bug\n"},
		{"codex", "done\n›\u00a0\n", "done\n›\u00a0Ask Codex anything\n"},
		{"gemini", "done\nType your message\n", "done\nType your message here\n"},
	} {
		profile := spec.TerminalProfile{SemanticAdapter: tc.name}
		for label, fixture := range map[string]struct{ pattern, screen string }{
			"settled": {settledPattern(profile), tc.settled},
			"prompt":  {initialPromptPattern(profile), tc.prompt},
		} {
			re, err := regexp.Compile(fixture.pattern)
			if err != nil {
				t.Fatal(err)
			}
			if !re.MatchString(fixture.screen) {
				t.Errorf("%s %s pattern did not match", tc.name, label)
			}
			if label == "settled" && re.MatchString(tc.prompt) {
				t.Errorf("%s settled pattern also matched the initial prompt", tc.name)
			}
		}
	}
	if got := compactTerminalText("Reply with exactly\n  THE-MARKER\u00a0 now"); got != "Reply with exactly THE-MARKER now" {
		t.Fatalf("compact terminal text = %q", got)
	}
	if !profileAllows([]string{"enter"}, "enter") || profileAllows([]string{"enter"}, "escape") {
		t.Fatal("profile allowlist mismatch")
	}
}

func TestSafeIDAndAllowlist(t *testing.T) {
	if got := safeID("0198-7ABC/evil;word"); got != "01987abcevilword" {
		t.Fatalf("safeID = %q", got)
	}
	if _, ok := canonicalKeys["$(touch /tmp/no)"]; ok {
		t.Fatal("arbitrary key entered allowlist")
	}
}
