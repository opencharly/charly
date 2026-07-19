package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/targetkit"
	"github.com/opencharly/sdk/testkit"
	"google.golang.org/grpc"
)

type fixtureStream struct{ frames []*pb.ChannelFrame }

func (*fixtureStream) Context() context.Context        { return context.Background() }
func (*fixtureStream) Recv() (*pb.ChannelFrame, error) { return nil, io.EOF }
func (s *fixtureStream) Send(frame *pb.ChannelFrame) error {
	s.frames = append(s.frames, frame)
	return nil
}

func TestPiGRPCHelperProcess(t *testing.T) {
	if os.Getenv("CHARLY_PI_GRPC_HELPER") != "1" {
		return
	}
	if err := targetkit.ServeStdio(os.Stdin, os.Stdout, func(server *grpc.Server) {
		pb.RegisterProviderServer(server, NewProvider())
	}); err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(2)
	}
	os.Exit(0)
}

func TestPiOutputProjectsSessionReferenceAndSettledState(t *testing.T) {
	stream := &fixtureStream{}
	sender := &piSender{stream: stream, request: "request", next: 1, replay: sdk.NewReplayBuffer(32, 1<<20)}
	input := bytes.NewBufferString("" +
		`{"id":"state-1","type":"response","command":"get_state","success":true,"data":{"model":{"id":"fixture"},"thinkingLevel":"off","isStreaming":false,"isCompacting":false,"steeringMode":"one-at-a-time","followUpMode":"one-at-a-time","sessionFile":"/sessions/pi.jsonl","sessionId":"019f7596-4cba-7972-a0de-07238ad01957","autoCompactionEnabled":true,"messageCount":0,"pendingMessageCount":0}}` + "\n" +
		`{"type":"agent_end"}` + "\n")
	if err := forwardPiOutput(input, sender); err != nil {
		t.Fatal(err)
	}
	var session, settled bool
	for _, frame := range stream.frames {
		if frame.GetName() == "session" && bytes.Contains(frame.GetPayloadJson(), []byte("/sessions/pi.jsonl")) {
			session = true
		}
		if frame.GetName() == "settled" {
			settled = true
		}
	}
	if !session || !settled {
		t.Fatalf("frames missing session/settled: %#v", stream.frames)
	}
}

func TestPiRPCBoundaryRejectsUnknownCommandsAndIncompleteState(t *testing.T) {
	if err := validatePiJSON("#PiRPCCommand", []byte(`{"type":"invented_command"}`)); err == nil {
		t.Fatal("unknown Pi RPC command passed CUE validation")
	}
	incomplete := []byte(`{"type":"response","command":"get_state","success":true,"data":{"sessionFile":"/sessions/pi.jsonl"}}`)
	if err := validatePiJSON("#PiRPCStateResponse", incomplete); err == nil {
		t.Fatal("incomplete Pi state response passed CUE validation")
	}
}

func TestPiEnvironmentUsesOfficialOrchestratorCLIAdapter(t *testing.T) {
	got := piEnv(map[string]any{
		"session_file": "/sessions/pi.jsonl", "orchestrator": true, "orchestrator_instance": "review",
	})
	want := map[string]bool{
		"CHARLY_PI_SESSION_FILE=/sessions/pi.jsonl": true,
		"CHARLY_PI_ORCHESTRATOR=1":                  true,
		"CHARLY_PI_ORCHESTRATOR_INSTANCE=review":    true,
	}
	for _, value := range got {
		delete(want, value)
	}
	if len(want) != 0 {
		t.Fatalf("missing env: %#v (got %v)", want, got)
	}
}

func TestNativePiProviderOverRealSSHAndGRPC(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("OpenSSH client unavailable")
	}
	runner := filepath.Join(t.TempDir(), "pi-runner")
	script := `#!/bin/sh
IFS= read -r state
printf '%s\n' '{"id":"state","type":"response","command":"get_state","success":true,"data":{"model":{"id":"fixture"},"thinkingLevel":"off","isStreaming":false,"isCompacting":false,"steeringMode":"one-at-a-time","followUpMode":"one-at-a-time","sessionFile":"/sessions/ssh-pi.jsonl","sessionId":"019f7596-4cba-7972-a0de-07238ad01957","autoCompactionEnabled":true,"messageCount":0,"pendingMessageCount":0}}'
IFS= read -r prompt
printf '%s\n' '{"type":"agent_end"}'
`
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	server := testkit.StartSSHProcessServer(t, func(_ string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPiGRPCHelperProcess$")
		cmd.Env = append(os.Environ(), "CHARLY_PI_GRPC_HELPER=1", "CHARLY_PI_RUNNER="+runner)
		return cmd
	})
	t.Setenv("HOME", server.Home)
	target := spec.TargetSpec{Hops: []spec.TargetHop{
		{Transport: "ssh", Address: server.Address, User: "agent", Port: server.Port, IdentityFile: server.IdentityFile, Options: spec.StrMap{
			"IdentitiesOnly": "yes", "LogLevel": "ERROR", "StrictHostKeyChecking": "no", "UserKnownHostsFile": "/dev/null",
		}},
		{Transport: "grpc"},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, client, err := targetkit.DialProvider(ctx, target, targetkit.DialOptions{Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close SSH Pi connection: %v", err)
		}
	})
	id := spec.UUIDv7("0198f140-6b7a-7b90-8a10-010203040506")
	request := spec.AgentRunRequest{ID: id, SessionID: id, RequestID: id, IdempotencyKey: "ssh-pi", Prompt: "respond through SSH"}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := sdk.OpenProviderChannel(ctx, client, &pb.ChannelFrame{
		Kind: sdk.ChannelOpen, RequestId: string(id), Class: "agent-runtime", Reserved: "pi", Op: "run", PayloadJson: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	gate := sdk.NewSequenceGate(1)
	var session, settled, exited bool
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
		session = session || frame.GetName() == "session" && bytes.Contains(frame.GetPayloadJson(), []byte("/sessions/ssh-pi.jsonl"))
		settled = settled || frame.GetName() == "settled"
	}
	if !session || !settled {
		t.Fatalf("Pi SSH/gRPC frames missing session/settled: session=%v settled=%v", session, settled)
	}
}

// The cancel-path shutdown regression gate: the old watchPiProcessGroup opened
// with a raw SIGKILL to the process group, so a cancel-then-Wait always read
// "signal: killed". The graceful-first ladder (kit.ShutdownProcessGroup) opens
// with the stdin EOF a stdio-carried runner answers — a plain `sh` reading
// stdin must therefore reap with a CLEAN exit status after a cancel.
func TestCancelShutdownLadderOpensWithStdinEOF(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	process, err := startPiProcess(ctx, sh, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitErr := process.cmd.Wait()
	close(process.reaped) // unblock the ladder's final reap-wait
	if waitErr != nil {
		t.Fatalf("cancel-path Wait = %v, want a clean stdin-EOF exit (graceful ladder, no opening force-kill)", waitErr)
	}
}

// A runner that ignores its stdin EOF must die by the ladder's SIGTERM rung —
// never by an opening SIGKILL. `sleep` reads no stdin and keeps SIGTERM's
// default disposition, so after the first ProcessShutdownGrace (the production
// constant inside the ladder, not a test-side poll) the group terminates by
// SIGTERM and Wait reports "signal: terminated"; the old code reported
// "signal: killed".
func TestCancelShutdownLadderEscalatesToSIGTERMNotSIGKILL(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep unavailable")
	}
	runner := filepath.Join(t.TempDir(), "ignores-stdin-eof")
	if err := os.WriteFile(runner, []byte("#!/bin/sh\nexec sleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	process, err := startPiProcess(ctx, runner, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitErr := process.cmd.Wait()
	close(process.reaped)
	if waitErr == nil || waitErr.Error() != "signal: terminated" {
		t.Fatalf("cancel-path Wait = %v, want %q (SIGTERM escalation, not SIGKILL)", waitErr, "signal: terminated")
	}
}

// Once Wait has reaped the child the watcher is DISARMED: a late cancel — the
// deferred cancel in OpenChannel after a clean stdin-EOF exit is the real
// instance — returns promptly without walking the ladder's signal rungs, so no
// signal can ever land on the now-recyclable pgid.
func TestWatcherDisarmedAfterReap(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.Command(sh)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	process := &piProcess{cmd: cmd, stdin: stdin, reaped: make(chan struct{})}
	exited := make(chan struct{})
	go func() {
		watchPiProcessGroup(ctx, process)
		close(exited)
	}()
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	close(process.reaped)
	cancel() // the late cancel: disarmed, no ladder
	<-exited // the watcher must return promptly
}
