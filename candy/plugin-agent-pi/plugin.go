package pi

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/opencharly/charly/candy/plugin-agent-pi/params"
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

var piSchema struct {
	sync.Once
	validator *sdk.SchemaValidator
	err       error
}

func validatePi(definition string, value any) error {
	piSchema.Do(func() {
		piSchema.validator, piSchema.err = sdk.NewSchemaValidator(schemaFS, "schema")
	})
	if piSchema.err != nil {
		return fmt.Errorf("plugin-agent-pi: %w", piSchema.err)
	}
	if err := piSchema.validator.Validate(definition, value); err != nil {
		return fmt.Errorf("plugin-agent-pi: %w", err)
	}
	return nil
}

func validatePiJSON(definition string, payload []byte) error {
	piSchema.Do(func() {
		piSchema.validator, piSchema.err = sdk.NewSchemaValidator(schemaFS, "schema")
	})
	if piSchema.err != nil {
		return fmt.Errorf("plugin-agent-pi: %w", piSchema.err)
	}
	if err := piSchema.validator.ValidateJSON(definition, payload); err != nil {
		return fmt.Errorf("plugin-agent-pi: %w", err)
	}
	return nil
}

func encodePi(definition string, value any) ([]byte, error) {
	if err := validatePi(definition, value); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("plugin-agent-pi: encode %s: %w", definition, err)
	}
	return payload, nil
}

func NewProvider() pb.ProviderServer { return &provider{} }

func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.199.1330", []sdk.ProvidedCapability{{Class: "agent-runtime", Word: "pi"}}, schemaFS)
}

type provider struct{ pb.UnimplementedProviderServer }

func (provider) Invoke(context.Context, *pb.InvokeRequest) (*pb.InvokeReply, error) {
	return nil, errors.New("plugin-agent-pi: agent runs require Provider.Channel")
}

func (p provider) Channel(stream pb.Provider_ChannelServer) error {
	open, err := sdk.ReceiveChannelOpen(stream)
	if err != nil {
		return err
	}
	return p.OpenChannel(open, stream)
}

func (provider) OpenChannel(open *pb.ChannelFrame, stream sdk.ProviderChannel) error {
	if open.GetClass() != "agent-runtime" || open.GetReserved() != "pi" {
		return fmt.Errorf("plugin-agent-pi: unsupported channel %s:%s", open.GetClass(), open.GetReserved())
	}
	request, err := decodeAgentRunRequest(open.GetPayloadJson())
	if err != nil {
		return err
	}
	runner := os.Getenv("CHARLY_PI_RUNNER")
	if runner == "" {
		runner = "charly-pi-agent-runner"
	}
	path, err := exec.LookPath(runner)
	if err != nil {
		return fmt.Errorf("plugin-agent-pi: find runner: %w", err)
	}
	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	process, err := startPiProcess(ctx, path, request.Params)
	if err != nil {
		return err
	}
	sender := &piSender{stream: stream, request: open.GetRequestId(), next: open.GetAckSequence() + 1, replay: sdk.NewReplayBuffer(4096, 32<<20)}
	return runPiProcess(ctx, cancel, stream, process, sender, request)
}

func decodeAgentRunRequest(payload []byte) (spec.AgentRunRequest, error) {
	var request spec.AgentRunRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return request, fmt.Errorf("plugin-agent-pi: decode CUE AgentRunRequest: %w", err)
	}
	if err := sdk.ValidateGenerated("#AgentRunRequest", request); err != nil {
		return request, fmt.Errorf("plugin-agent-pi: %w", err)
	}
	return request, nil
}

type piProcess struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	killFailure <-chan error
}

func startPiProcess(ctx context.Context, path string, environment map[string]any) (*piProcess, error) {
	cmd := exec.CommandContext(ctx, path)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(cmd.Environ(), piEnv(environment)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Join(err, stdin.Close())
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, errors.Join(err, stdin.Close(), stdout.Close())
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(err, stdin.Close(), stdout.Close(), stderr.Close())
	}
	killFailure := make(chan error, 1)
	go watchPiProcessGroup(ctx, cmd, killFailure)
	return &piProcess{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, killFailure: killFailure}, nil
}

func watchPiProcessGroup(ctx context.Context, cmd *exec.Cmd, result chan<- error) {
	<-ctx.Done()
	if cmd.Process == nil {
		result <- nil
		return
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		err = nil
	}
	result <- err
}

func runPiProcess(ctx context.Context, cancel context.CancelFunc, stream sdk.ProviderChannel, process *piProcess, sender *piSender, request spec.AgentRunRequest) (returnErr error) {
	defer func() {
		if returnErr != nil && !errors.Is(returnErr, io.EOF) {
			cancel()
		}
		returnErr = finishPiProcess(ctx, stream, process, sender, returnErr)
	}()
	if err := sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "running"}); err != nil {
		return err
	}
	stateLine, err := encodePi("#PiGetStateCommand", params.PiGetStateCommand{Id: string(request.RequestID) + "-state", Type: "get_state"})
	if err != nil {
		return err
	}
	if _, err := process.stdin.Write(append(stateLine, '\n')); err != nil {
		return err
	}
	if request.Prompt != "" {
		line, err := encodePi("#PiPromptCommand", params.PiPromptCommand{Id: string(request.RequestID), Type: "prompt", Message: request.Prompt})
		if err != nil {
			return err
		}
		if _, err := process.stdin.Write(append(line, '\n')); err != nil {
			return err
		}
	}

	done := make(chan error, 2)
	go func() { done <- forwardPiOutput(process.stdout, sender) }()
	go func() { done <- forwardPiInput(stream, process.stdin, sender) }()
	stderrFailure := make(chan error, 1)
	go func() {
		if err := forwardPiErrors(process.stderr, sender); err != nil {
			stderrFailure <- err
		}
	}()
	select {
	case returnErr = <-done:
	case returnErr = <-stderrFailure:
	case <-ctx.Done():
		returnErr = ctx.Err()
	}
	return returnErr
}

func finishPiProcess(ctx context.Context, stream sdk.ProviderChannel, process *piProcess, sender *piSender, runErr error) error {
	if closeErr := process.stdin.Close(); closeErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("plugin-agent-pi: close runner input: %w", closeErr))
	}
	if waitErr := process.cmd.Wait(); runErr == nil && waitErr != nil {
		runErr = waitErr
	}
	if ctx.Err() != nil {
		if killErr := <-process.killFailure; killErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("plugin-agent-pi: kill runner process group: %w", killErr))
		}
	}
	if runErr != nil && !errors.Is(runErr, io.EOF) && stream.Context().Err() == nil {
		return errors.Join(runErr, sender.send(&pb.ChannelFrame{Kind: sdk.ChannelError, Error: runErr.Error()}))
	}
	return sender.send(&pb.ChannelFrame{Kind: sdk.ChannelExit})
}

type piSender struct {
	mu      sync.Mutex
	stream  sdk.ProviderChannel
	request string
	next    uint64
	replay  *sdk.ReplayBuffer
}

func (s *piSender) send(frame *pb.ChannelFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	frame.RequestId = s.request
	if frame.Kind != sdk.ChannelAck {
		frame.Sequence = s.next
		s.next++
		if err := s.replay.Add(frame); err != nil {
			return fmt.Errorf("plugin-agent-pi: preserving unacknowledged agent evidence: %w", err)
		}
	}
	return s.stream.Send(frame)
}

func (s *piSender) acknowledge(sequence uint64) { s.replay.Acknowledge(sequence) }

func (s *piSender) replayFrom(sequence uint64) error {
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

func forwardPiOutput(reader io.Reader, sender *piSender) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if !json.Valid(line) {
			return errors.New("plugin-agent-pi: malformed upstream Pi RPC JSON")
		}
		var event params.PiRPCEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("plugin-agent-pi: decode upstream Pi RPC event: %w", err)
		}
		if err := validatePiJSON("#PiRPCEvent", line); err != nil {
			return err
		}
		name, ok := event["type"].(string)
		if !ok || name == "" {
			return errors.New("plugin-agent-pi: validated event omitted its string type discriminator")
		}
		if err := sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: name, PayloadJson: line}); err != nil {
			return err
		}
		if name == "response" && event["command"] == "get_state" {
			var state params.PiRPCStateResponse
			if err := json.Unmarshal(line, &state); err != nil {
				return fmt.Errorf("plugin-agent-pi: decode Pi state response: %w", err)
			}
			if err := validatePiJSON("#PiRPCStateResponse", line); err != nil {
				return err
			}
			if state.Success && state.Data == nil {
				return errors.New("plugin-agent-pi: successful Pi state response omitted data")
			}
			if state.Data != nil && state.Data.SessionFile != "" {
				binding := spec.AgentSessionBinding{StorageRef: state.Data.SessionFile}
				payload, err := encodeGenerated("#AgentSessionBinding", binding)
				if err != nil {
					return err
				}
				if err := sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "session", PayloadJson: payload}); err != nil {
					return err
				}
			}
		}
		if name == "agent_end" {
			if err := sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStatus, Name: "settled"}); err != nil {
				return err
			}
			return nil
		}
	}
	return scanner.Err()
}

func encodeGenerated(definition string, value any) ([]byte, error) {
	if err := sdk.ValidateGenerated(definition, value); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("plugin-agent-pi: encode %s: %w", definition, err)
	}
	return payload, nil
}

func forwardPiErrors(reader io.Reader, sender *piSender) error {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if err := sender.send(&pb.ChannelFrame{Kind: sdk.ChannelStderr, Data: append([]byte(nil), scanner.Bytes()...)}); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func forwardPiInput(stream sdk.ProviderChannel, writer io.Writer, sender *piSender) error {
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		if frame.GetKind() == sdk.ChannelCancel {
			payload, encodeErr := encodePi("#PiAbortCommand", params.PiAbortCommand{Type: "abort"})
			if encodeErr != nil {
				return encodeErr
			}
			_, err = writer.Write(append(payload, '\n'))
			return err
		}
		if frame.GetKind() == sdk.ChannelAck {
			sender.acknowledge(frame.GetAckSequence())
			continue
		}
		if frame.GetReplayFrom() != 0 {
			if err := sender.replayFrom(frame.GetReplayFrom()); err != nil {
				if sendErr := sender.send(&pb.ChannelFrame{Kind: sdk.ChannelResync, Name: "replay-unavailable", Error: err.Error()}); sendErr != nil {
					return errors.Join(err, sendErr)
				}
				return err
			}
			continue
		}
		if frame.GetKind() != sdk.ChannelStdin || !json.Valid(frame.GetData()) {
			return errors.New("plugin-agent-pi: input must be one upstream Pi RPC JSON object")
		}
		if err := validatePiJSON("#PiRPCCommand", frame.GetData()); err != nil {
			return err
		}
		if _, err := writer.Write(append(append([]byte(nil), frame.GetData()...), '\n')); err != nil {
			return err
		}
		if err := sender.send(&pb.ChannelFrame{Kind: sdk.ChannelAck, AckSequence: frame.GetSequence()}); err != nil {
			return err
		}
	}
}

func piEnv(params map[string]any) []string {
	pairs := [][2]string{{"cwd", "CHARLY_PI_CWD"}, {"session_file", "CHARLY_PI_SESSION_FILE"}, {"session_dir", "CHARLY_PI_SESSION_DIR"}, {"orchestrator_instance", "CHARLY_PI_ORCHESTRATOR_INSTANCE"}}
	out := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		if value, ok := params[pair[0]].(string); ok && value != "" {
			out = append(out, pair[1]+"="+value)
		}
	}
	if value, ok := params["orchestrator"].(bool); ok && value {
		out = append(out, "CHARLY_PI_ORCHESTRATOR=1")
	}
	return out
}
