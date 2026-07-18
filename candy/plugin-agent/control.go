package agentkind

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/agentkit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/targetkit"
	"golang.org/x/term"
	"google.golang.org/grpc"
)

const (
	classAgentRuntime = "agent-runtime"
	classTerminal     = "terminal"
)

type AgentCmd struct {
	StateDir   string             `long:"state-dir" env:"CHARLY_AGENT_STATE_DIR" type:"path" help:"Durable state directory"`
	Runtime    AgentRuntimeCmd    `cmd:"" help:"Inspect available runtime capabilities"`
	Session    AgentSessionCmd    `cmd:"" help:"Create and inspect durable sessions"`
	Run        AgentRunCmd        `cmd:"" help:"Start runs and inspect event logs"`
	Followup   AgentFollowupCmd   `cmd:"" help:"Resume a session with a follow-up prompt"`
	Steer      AgentSteerCmd      `cmd:"" help:"Resume a session with steering guidance"`
	Dispatch   AgentDispatchCmd   `cmd:"" help:"Validate and dispatch a generated AgentTeam"`
	Delegate   AgentDelegateCmd   `cmd:"" help:"Delegate work to a named session/target"`
	Team       AgentTeamCmd       `cmd:"" help:"Inspect durable team dispatches"`
	Federation AgentFederationCmd `cmd:"" help:"Delegate to another Charly node without a central service"`
	Terminal   AgentTerminalCmd   `cmd:"" help:"Run or attach to typed terminal channels"`
	Incident   AgentIncidentCmd   `cmd:"" help:"Record and inspect incidents"`
	RCA        AgentRCACmd        `cmd:"" name:"rca" help:"Perform root-cause analysis"`
	Recover    AgentRecoverCmd    `cmd:"" help:"Authorize an explicit recovery decision"`
}

var agentStateDir string

func (c *AgentCmd) AfterApply() error { agentStateDir = c.StateDir; return nil }

func agentStore() (*agentkit.Store, error) {
	dir := agentStateDir
	if dir == "" {
		dir = os.Getenv("CHARLY_AGENT_STATE_DIR")
	}
	return agentkit.OpenStore(dir)
}

type AgentRuntimeCmd struct {
	List   AgentRuntimeListCmd   `cmd:"" help:"List available runtime providers"`
	Status AgentRuntimeStatusCmd `cmd:"" help:"Show one runtime provider's availability"`
}
type AgentRuntimeListCmd struct{}
type AgentRuntimeStatusCmd struct {
	Provider string `arg:""`
	Class    string `long:"class" default:"agent-runtime" enum:"agent-runtime,terminal"`
}

func (*AgentRuntimeListCmd) Run() error {
	capabilities, err := localCapabilities(context.Background())
	if err != nil {
		return err
	}
	var rows []map[string]string
	for _, capability := range capabilities.GetProvided() {
		if capability.GetClass() == classAgentRuntime || capability.GetClass() == classTerminal {
			rows = append(rows, map[string]string{"class": capability.GetClass(), "provider": capability.GetWord()})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i]["class"]+":"+rows[i]["provider"] < rows[j]["class"]+":"+rows[j]["provider"]
	})
	return writeJSON(rows)
}

func (c *AgentRuntimeStatusCmd) Run() error {
	capabilities, err := localCapabilities(context.Background())
	if err != nil {
		return err
	}
	for _, capability := range capabilities.GetProvided() {
		if capability.GetClass() == c.Class && capability.GetWord() == c.Provider {
			return writeJSON(map[string]any{"class": c.Class, "provider": c.Provider, "available": true, "streaming": true})
		}
	}
	return fmt.Errorf("runtime provider %s:%s not found", c.Class, c.Provider)
}

type AgentSessionCmd struct {
	Create AgentSessionCreateCmd `cmd:"" help:"Create a durable runtime session"`
	New    AgentSessionCreateCmd `cmd:"" name:"new" help:"Create a durable runtime session"`
	List   AgentSessionListCmd   `cmd:"" help:"List sessions"`
	Get    AgentSessionGetCmd    `cmd:"" help:"Show one session"`
	Show   AgentSessionGetCmd    `cmd:"" name:"show" help:"Show one session"`
	Close  AgentSessionCloseCmd  `cmd:"" help:"Mark a session closed"`
}
type AgentSessionCreateCmd struct {
	Runtime    string `arg:"" help:"Runtime provider word (for example pi or tmux)"`
	Target     string `long:"target" default:"{}" help:"Deployment, host::deployment, CUE #TargetSpec JSON, or @file"`
	StorageRef string `long:"storage-ref" help:"Runtime-native durable session reference"`
	Profile    string `long:"profile" help:"Terminal profile name, JSON, or @file for terminal-backed runtimes"`
}

func (c *AgentSessionCreateCmd) Run() error {
	target, err := decodeTarget(c.Target)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var profile *spec.TerminalProfile
	if c.Profile != "" {
		value, err := resolveTerminalProfile(c.Profile, target)
		if err != nil {
			return err
		}
		profile = &value
	}
	s := spec.AgentSession{ID: agentkit.NewID(), Runtime: c.Runtime, Target: target, State: "new", CreatedAt: now, UpdatedAt: now, StorageRef: c.StorageRef, TerminalProfile: profile}
	if err := validateGenerated("#AgentSession", s); err != nil {
		return err
	}
	store, err := agentStore()
	if err != nil {
		return err
	}
	if err := store.PutSession(s); err != nil {
		return err
	}
	return writeJSON(s)
}

type AgentSessionListCmd struct{}

func (*AgentSessionListCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.Sessions()
	if err != nil {
		return err
	}
	return writeJSON(v)
}

type AgentSessionGetCmd struct {
	ID string `arg:""`
}

func (c *AgentSessionGetCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.Session(spec.UUIDv7(c.ID))
	if err != nil {
		return err
	}
	return writeJSON(v)
}

type AgentSessionCloseCmd struct {
	ID string `arg:""`
}

func (c *AgentSessionCloseCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.Session(spec.UUIDv7(c.ID))
	if err != nil {
		return err
	}
	v.State = "closed"
	v.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.PutSession(v); err != nil {
		return err
	}
	return writeJSON(v)
}

type AgentRunCmd struct {
	Start  AgentRunStartCmd  `cmd:"" help:"Start or resume a run"`
	List   AgentRunListCmd   `cmd:"" help:"List durable run requests"`
	Show   AgentRunShowCmd   `cmd:"" help:"Show a run and its current event-derived state"`
	Abort  AgentRunAbortCmd  `cmd:"" help:"Request cancellation of an active ephemeral controller"`
	Events AgentRunEventsCmd `cmd:"" help:"Read ordered run events"`
}
type AgentRunStartCmd struct {
	Session        string `arg:""`
	Prompt         string `arg:"" optional:""`
	IdempotencyKey string `long:"idempotency-key"`
	Resume         bool   `long:"resume"`
}

func (c *AgentRunStartCmd) Run() error {
	return executeAgentRun(c.Session, c.Prompt, c.IdempotencyKey, c.Resume, nil)
}

type AgentRunEventsCmd struct {
	RunID string `arg:"" name:"run"`
}
type AgentRunListCmd struct{}

func (*AgentRunListCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.Runs()
	if err != nil {
		return err
	}
	return writeJSON(v)
}

type AgentRunShowCmd struct {
	RunID string `arg:"" name:"run"`
}

func (c *AgentRunShowCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	run, err := s.Run(spec.UUIDv7(c.RunID))
	if err != nil {
		return err
	}
	events, err := s.Events(run.ID)
	if err != nil {
		return err
	}
	state := "created"
	if len(events) > 0 {
		state = events[len(events)-1].Type
	}
	return writeJSON(map[string]any{"run": run, "state": state, "events": events})
}

type AgentRunAbortCmd struct {
	RunID string `arg:"" name:"run"`
}

func (c *AgentRunAbortCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	run, err := s.Run(spec.UUIDv7(c.RunID))
	if err != nil {
		return err
	}
	events, err := s.Events(run.ID)
	if err != nil {
		return err
	}
	if len(events) > 0 {
		switch events[len(events)-1].Type {
		case "completed", "aborted", "failed":
			return fmt.Errorf("run %s is already %s", run.ID, events[len(events)-1].Type)
		}
	}
	control := spec.AgentAbortControl{RunID: run.ID, RequestID: agentkit.NewID(), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := validateGenerated("#AgentAbortControl", control); err != nil {
		return err
	}
	if err := s.RequestAbort(control); err != nil {
		return err
	}
	return writeJSON(control)
}

func (c *AgentRunEventsCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.Events(spec.UUIDv7(c.RunID))
	if err != nil {
		return err
	}
	return writeJSON(v)
}

type AgentFollowupCmd struct {
	Session        string `arg:""`
	Prompt         string `arg:""`
	IdempotencyKey string `long:"idempotency-key"`
}

func (c *AgentFollowupCmd) Run() error {
	return executeAgentRun(c.Session, c.Prompt, c.IdempotencyKey, true, map[string]any{"control": "followup"})
}

type AgentSteerCmd struct {
	Session        string `arg:""`
	Guidance       string `arg:""`
	IdempotencyKey string `long:"idempotency-key"`
}

func (c *AgentSteerCmd) Run() error {
	return executeAgentRun(c.Session, c.Guidance, c.IdempotencyKey, true, map[string]any{"control": "steer"})
}

type AgentDelegateCmd struct {
	Team           string `arg:"" help:"Durable team dispatch UUIDv7"`
	From           string `arg:"" help:"Delegating member name"`
	To             string `arg:"" help:"Receiving member name"`
	Prompt         string `arg:"" help:"Delegated task"`
	IdempotencyKey string `long:"idempotency-key"`
}

type AgentTeamCmd struct {
	List AgentTeamListCmd `cmd:""`
	Show AgentTeamShowCmd `cmd:""`
}
type AgentTeamListCmd struct{}
type AgentTeamShowCmd struct {
	ID string `arg:""`
}

func (*AgentTeamListCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.Teams()
	if err != nil {
		return err
	}
	return writeJSON(v)
}

func (c *AgentTeamShowCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.Team(spec.UUIDv7(c.ID))
	if err != nil {
		return err
	}
	return writeJSON(v)
}

type AgentFederationCmd struct {
	Run  AgentFederationRunCmd  `cmd:"" help:"Create and run a session on a responsible Charly node"`
	List AgentFederationListCmd `cmd:"" help:"List replicated correlation metadata"`
}
type AgentFederationRunCmd struct {
	Node           string   `arg:"" help:"SSH host or configured Charly host alias"`
	Runtime        string   `arg:""`
	Prompt         string   `arg:""`
	Target         string   `long:"target" default:"{}"`
	Profile        string   `long:"profile" help:"Terminal profile name, JSON, or @file for terminal-backed runtimes"`
	Owner          string   `long:"owner" default:"local"`
	IdempotencyKey string   `long:"idempotency-key"`
	IdentityFile   string   `long:"identity-file" help:"SSH identity file for the responsible Charly node" type:"path"`
	SSHOption      []string `long:"ssh-option" help:"OpenSSH option for the responsible Charly node (repeatable, KEY=VALUE)"`
}

func (c *AgentFederationRunCmd) Run() error {
	store, err := agentStore()
	if err != nil {
		return err
	}
	record := spec.AgentFederationRecord{ID: agentkit.NewID(), Node: c.Node, Owner: c.Owner, State: "delegated", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := validateGenerated("#AgentFederationRecord", record); err != nil {
		return err
	}
	if err := store.PutFederation(record); err != nil {
		return err
	}
	bin, err := charlyBinary()
	if err != nil {
		return err
	}
	invoke := func(args ...string) ([]byte, error) {
		argv := []string{"--host", c.Node}
		if c.IdentityFile != "" {
			argv = append(argv, "--host-identity-file", c.IdentityFile)
		}
		for _, option := range c.SSHOption {
			argv = append(argv, "--host-option", option)
		}
		argv = append(argv, "agent")
		argv = append(argv, args...)
		cmd := exec.Command(bin, argv...)
		var stderr strings.Builder
		cmd.Stderr = io.MultiWriter(os.Stderr, &stderr)
		out, runErr := cmd.Output()
		if runErr != nil {
			return nil, fmt.Errorf("federated charly %s: %w (stderr: %s)", strings.Join(args, " "), runErr, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	sessionArgs := []string{"session", "create", c.Runtime, "--target", c.Target}
	if c.Profile != "" {
		sessionArgs = append(sessionArgs, "--profile", c.Profile)
	}
	out, err := invoke(sessionArgs...)
	if err != nil {
		return recordFederationFailure(store, record, err)
	}
	var session spec.AgentSession
	if err := json.Unmarshal(out, &session); err != nil {
		return fmt.Errorf("federated session response: %w", err)
	}
	record.SessionID = session.ID
	record.State = "active"
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.PutFederation(record); err != nil {
		return err
	}
	key := c.IdempotencyKey
	if key == "" {
		key = string(agentkit.NewID())
	}
	out, err = invoke("run", "start", string(session.ID), c.Prompt, "--idempotency-key", key)
	if err != nil {
		return recordFederationFailure(store, record, err)
	}
	var run spec.AgentRunRequest
	if err := json.Unmarshal(out, &run); err != nil {
		return fmt.Errorf("federated run response: %w", err)
	}
	record.RunID = run.ID
	record.State = "settled"
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.PutFederation(record); err != nil {
		return err
	}
	return writeJSON(record)
}

func recordFederationFailure(store *agentkit.Store, record spec.AgentFederationRecord, cause error) error {
	record.State = "failed"
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	incident := spec.Incident{ID: agentkit.NewID(), State: "needs_rca", Summary: cause.Error(), CreatedAt: record.UpdatedAt, EvidenceRefs: []string{"federation/" + string(record.ID) + ".json"}}
	if err := validateGenerated("#Incident", incident); err != nil {
		return errors.Join(cause, err)
	}
	if err := errors.Join(store.PutFederation(record), store.PutIncident(incident)); err != nil {
		return fmt.Errorf("federation failed and durable failure evidence could not be completed: %w", errors.Join(cause, err))
	}
	return fmt.Errorf("federation failed; incident %s recorded and no recovery attempted: %w", incident.ID, cause)
}

type AgentFederationListCmd struct{}

func (*AgentFederationListCmd) Run() error {
	s, e := agentStore()
	if e != nil {
		return e
	}
	v, e := s.Federation()
	if e != nil {
		return e
	}
	return writeJSON(v)
}

func (c *AgentDelegateCmd) Run() error {
	store, err := agentStore()
	if err != nil {
		return err
	}
	record, err := store.Team(spec.UUIDv7(c.Team))
	if err != nil {
		return err
	}
	if !agentkit.DelegationAllowed(record.Team, c.From, c.To, "run") {
		return fmt.Errorf("agent team %s does not authorize %q to delegate run to %q", record.ID, c.From, c.To)
	}
	session, ok := record.Sessions[c.To]
	if !ok {
		return fmt.Errorf("agent team %s has no session for member %q", record.ID, c.To)
	}
	return executeAgentRun(string(session), c.Prompt, c.IdempotencyKey, true, map[string]any{"control": "delegate", "team_id": record.ID, "from": c.From, "to": c.To})
}

func executeAgentRun(sessionID, prompt, key string, resume bool, params map[string]any) (returnErr error) {
	store, err := agentStore()
	if err != nil {
		return err
	}
	session, err := store.Session(spec.UUIDv7(sessionID))
	if err != nil {
		return err
	}
	if session.State == "closed" {
		return errors.New("agent session is closed")
	}
	if key == "" {
		key = string(agentkit.NewID())
	}
	run := spec.AgentRunRequest{ID: agentkit.NewID(), SessionID: session.ID, RequestID: agentkit.NewID(), IdempotencyKey: key, Prompt: prompt, Params: params, Resume: resume}
	if run.Params == nil {
		run.Params = map[string]any{}
	}
	if session.StorageRef != "" {
		run.Params["session_file"] = session.StorageRef
	}
	if session.TerminalProfile != nil {
		run.Params["terminal_profile"] = session.TerminalProfile
	}
	if err := validateGenerated("#AgentRunRequest", run); err != nil {
		return err
	}
	reserved, created, err := store.CreateRunOnce(run)
	if err != nil {
		return err
	}
	if !created {
		return writeJSON(reserved)
	}
	if reserved.ID != run.ID {
		return errors.New("agent run reservation returned inconsistent id")
	}
	session.State = "active"
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.PutSession(session); err != nil {
		return err
	}
	sequence := int64(0)
	record := func(kind string, payload map[string]any) error {
		sequence++
		return store.AppendEvent(spec.AgentEvent{RunID: run.ID, Sequence: sequence, Type: kind, Time: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload})
	}
	if err := record("started", map[string]any{"runtime": session.Runtime}); err != nil {
		return err
	}
	payload, err := marshalGenerated("#AgentRunRequest", run)
	if err != nil {
		return err
	}
	target, err := marshalGenerated("#TargetSpec", session.Target)
	if err != nil {
		return err
	}
	// The public run ID is the stable terminal correlation key. RequestID still
	// identifies the idempotent upstream request inside #AgentRunRequest, but a
	// terminal controller must be able to snapshot/reattach/close from run show.
	open := &pb.ChannelFrame{Kind: sdk.ChannelOpen, RequestId: string(run.ID), Class: classAgentRuntime, Reserved: session.Runtime, Op: "run", PayloadJson: payload, TargetJson: target}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inputs := make(chan *pb.ChannelFrame, 4096)
	var aborted atomic.Bool
	var abortCheckErr atomic.Value
	runtimeState := &agentRuntimeChannelState{
		gate: sdk.NewSequenceGate(1), inputs: inputs, record: record,
		store: store, runID: run.ID, persistTerminal: session.TerminalProfile != nil,
	}
	defer func() {
		if err := store.ClearAbort(run.ID); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("clear abort control for run %s: %w", run.ID, err))
		}
	}()
	go func() {
		control, checkErr := store.WaitAbort(ctx, run.ID)
		if checkErr != nil {
			if !errors.Is(checkErr, context.Canceled) {
				abortCheckErr.Store(checkErr)
				cancel()
			}
			return
		}
		if control == nil {
			return
		}
		aborted.Store(true)
		select {
		case inputs <- &pb.ChannelFrame{Kind: sdk.ChannelCancel, RequestId: string(control.RequestID)}:
		case <-ctx.Done():
		}
	}()
	channel := newControllerChannel(ctx, inputs, runtimeState.handle)
	err = runControlChannel(ctx, open, session.Target, channel)
	err = runtimeState.result(&session, err)
	if value := abortCheckErr.Load(); value != nil {
		checkErr, ok := value.(error)
		if !ok {
			return errors.New("observe abort control: invalid asynchronous error value")
		}
		err = fmt.Errorf("observe abort control: %w", checkErr)
	}
	return finalizeAgentRun(store, session, run, aborted.Load(), err, record)
}

type agentRuntimeChannelState struct {
	gate            *sdk.SequenceGate
	inputs          chan<- *pb.ChannelFrame
	record          func(string, map[string]any) error
	store           *agentkit.Store
	runID           spec.UUIDv7
	persistTerminal bool
	mu              sync.Mutex
	ref             string
	err             error
}

func (s *agentRuntimeChannelState) handle(frame *pb.ChannelFrame) error {
	accepted, err := acceptChannelFrame(s.gate, frame, s.inputs)
	if err != nil || !accepted {
		return err
	}
	if s.persistTerminal {
		if err := persistTerminalFrame(s.store, s.runID, frame); err != nil {
			return err
		}
	}
	if frame.GetSequence() != 0 && frame.GetKind() != sdk.ChannelAck {
		select {
		case s.inputs <- &pb.ChannelFrame{Kind: sdk.ChannelAck, AckSequence: frame.GetSequence()}:
		default:
			return errors.New("agent channel acknowledgement queue exhausted")
		}
	}
	kind := s.eventKind(frame)
	data := map[string]any{"kind": frame.GetKind(), "name": frame.GetName(), "sequence": frame.GetSequence()}
	if err := s.projectPayload(frame, data); err != nil {
		return err
	}
	if len(frame.GetData()) > 0 {
		data["data"] = string(frame.GetData())
	}
	if frame.GetError() != "" {
		data["error"] = frame.GetError()
	}
	return s.record(kind, data)
}

func (s *agentRuntimeChannelState) eventKind(frame *pb.ChannelFrame) string {
	switch {
	case frame.GetKind() == sdk.ChannelError, frame.GetKind() == sdk.ChannelResync && frame.GetError() != "":
		s.setFailure(errors.New(frame.GetError()))
		return "failed"
	case frame.GetKind() == sdk.ChannelExit && frame.GetExitCode() != 0:
		s.setFailure(fmt.Errorf("runtime exited with status %d", frame.GetExitCode()))
		return "failed"
	case frame.GetKind() == sdk.ChannelExit:
		return "completed"
	case frame.GetName() == "settled":
		return "settled"
	case frame.GetKind() == sdk.ChannelStdout || frame.GetKind() == sdk.ChannelTerminal:
		return "message"
	case frame.GetKind() == sdk.ChannelStatus && frame.GetName() == "tool":
		return "tool"
	default:
		return "status"
	}
}

func (s *agentRuntimeChannelState) projectPayload(frame *pb.ChannelFrame, data map[string]any) error {
	if len(frame.GetPayloadJson()) == 0 {
		return nil
	}
	if frame.GetKind() == sdk.ChannelStatus && frame.GetName() == "session" {
		var binding spec.AgentSessionBinding
		if err := json.Unmarshal(frame.GetPayloadJson(), &binding); err != nil {
			return fmt.Errorf("decode runtime session binding: %w", err)
		}
		if err := validateGenerated("#AgentSessionBinding", binding); err != nil {
			return err
		}
		s.mu.Lock()
		s.ref = binding.StorageRef
		s.mu.Unlock()
	}
	var value any
	if err := json.Unmarshal(frame.GetPayloadJson(), &value); err != nil {
		return fmt.Errorf("decode runtime event payload: %w", err)
	}
	data["payload"] = value
	return nil
}

func (s *agentRuntimeChannelState) setFailure(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func (s *agentRuntimeChannelState) result(session *spec.AgentSession, channelErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ref != "" {
		session.StorageRef = s.ref
	}
	if channelErr == nil {
		return s.err
	}
	return channelErr
}

func finalizeAgentRun(store *agentkit.Store, session spec.AgentSession, run spec.AgentRunRequest, aborted bool, runErr error, record func(string, map[string]any) error) error {
	if aborted {
		if err := record("aborted", map[string]any{"requested": true}); err != nil {
			return err
		}
		session.State = "detached"
		session.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := store.PutSession(session); err != nil {
			return err
		}
		return writeJSON(run)
	}
	if runErr != nil {
		cause := runErr
		if eventErr := record("failed", map[string]any{"error": cause.Error()}); eventErr != nil {
			runErr = errors.Join(runErr, fmt.Errorf("persist failed run event: %w", eventErr))
		}
		incident := spec.Incident{ID: agentkit.NewID(), RunID: run.ID, State: "needs_rca", Summary: runErr.Error(), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), EvidenceRefs: []string{"events/" + string(run.ID) + ".jsonl"}}
		if incidentErr := store.PutIncident(incident); incidentErr != nil {
			return errors.Join(runErr, fmt.Errorf("persist incident for failed run %s: %w", run.ID, incidentErr))
		}
		session.State = "failed"
	} else {
		session.State = "detached"
	}
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if sessionErr := store.PutSession(session); sessionErr != nil {
		return errors.Join(runErr, fmt.Errorf("persist final session state: %w", sessionErr))
	}
	if runErr != nil {
		return fmt.Errorf("agent run %s failed; incident recorded and no recovery attempted: %w", run.ID, runErr)
	}
	return writeJSON(run)
}

type AgentDispatchCmd struct {
	Team   string `arg:"" help:"#AgentTeam JSON or @file"`
	Prompt string `long:"prompt"`
}

func (c *AgentDispatchCmd) Run() error {
	var team spec.AgentTeam
	if err := decodeGenerated(c.Team, "#AgentTeam", &team); err != nil {
		return err
	}
	if err := agentkit.ValidateTeam(team); err != nil {
		return err
	}
	// Dispatch is intentionally bounded by the generated delegation graph. Each
	// member gets a durable session; no implicit peer authority is invented.
	var sessions []spec.AgentSession
	sessionIDs := map[string]spec.UUIDv7{}
	store, err := agentStore()
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, member := range team.Agents {
		s := spec.AgentSession{ID: agentkit.NewID(), Runtime: member.Runtime, Target: member.Target, State: "new", CreatedAt: now, UpdatedAt: now, Metadata: spec.StrMap{"team_member": member.Name, "role": member.Role}, TerminalProfile: member.TerminalProfile}
		if err := validateGenerated("#AgentSession", s); err != nil {
			return err
		}
		if err := store.PutSession(s); err != nil {
			return err
		}
		sessions = append(sessions, s)
		sessionIDs[member.Name] = s.ID
	}
	record := spec.AgentTeamRecord{ID: agentkit.NewID(), Team: team, Sessions: sessionIDs, CreatedAt: now}
	if err := validateGenerated("#AgentTeamRecord", record); err != nil {
		return err
	}
	if err := store.PutTeam(record); err != nil {
		return err
	}
	return writeJSON(map[string]any{"dispatch": record, "sessions": sessions, "prompt": c.Prompt})
}

type AgentTerminalCmd struct {
	Launch     AgentTerminalLaunchCmd     `cmd:"" name:"launch" help:"Launch and detach a typed terminal profile"`
	Run        AgentTerminalRunCmd        `cmd:"" help:"Launch a typed terminal profile"`
	Attach     AgentTerminalAttachCmd     `cmd:"" help:"Attach using a real TTY"`
	Snapshot   AgentTerminalSnapshotCmd   `cmd:"" help:"Capture a structured virtual-screen snapshot"`
	Input      AgentTerminalInputCmd      `cmd:"" help:"Send literal text or an explicit paste"`
	Key        AgentTerminalKeyCmd        `cmd:"" help:"Send one allowlisted canonical key"`
	Resize     AgentTerminalResizeCmd     `cmd:"" help:"Resize a detached terminal"`
	Signal     AgentTerminalSignalCmd     `cmd:"" help:"Send one profile-allowlisted signal"`
	Close      AgentTerminalCloseCmd      `cmd:"" help:"Close and clean up a terminal run"`
	Transcript AgentTerminalTranscriptCmd `cmd:"" help:"Read durable ordered terminal evidence"`
}
type AgentTerminalRunCmd struct {
	Profile  string `arg:"" help:"#TerminalProfile JSON or @file"`
	Target   string `long:"target" default:"{}"`
	Provider string `long:"provider" default:"tmux"`
	RunID    string `long:"run-id" help:"Stable UUIDv7 used for detach/reattach"`
}

type AgentTerminalLaunchCmd AgentTerminalRunCmd

func (c *AgentTerminalRunCmd) Run() error {
	return executeTerminal(c.Profile, c.Target, c.Provider, c.RunID, terminalWait)
}

func (c *AgentTerminalLaunchCmd) Run() error {
	return executeTerminal(c.Profile, c.Target, c.Provider, c.RunID, terminalDetach)
}

type AgentTerminalAttachCmd struct {
	Profile  string `arg:""`
	Target   string `long:"target" default:"{}"`
	Provider string `long:"provider" default:"tmux"`
	RunID    string `long:"run-id" required:"" help:"UUIDv7 of the detached terminal run"`
}

type AgentTerminalSnapshotCmd struct {
	Profile  string `arg:""`
	Target   string `long:"target" default:"{}"`
	Provider string `long:"provider" default:"tmux"`
	RunID    string `long:"run-id" required:""`
}
type AgentTerminalInputCmd struct {
	Profile  string `arg:""`
	Text     string `arg:""`
	Paste    bool   `long:"paste" help:"Send as a bracket-safe paste rather than literal keystrokes"`
	Target   string `long:"target" default:"{}"`
	Provider string `long:"provider" default:"tmux"`
	RunID    string `long:"run-id" required:""`
}
type AgentTerminalKeyCmd struct {
	Profile  string `arg:""`
	Key      string `arg:""`
	Target   string `long:"target" default:"{}"`
	Provider string `long:"provider" default:"tmux"`
	RunID    string `long:"run-id" required:""`
}
type AgentTerminalResizeCmd struct {
	Profile  string `arg:""`
	Cols     int64  `arg:""`
	Rows     int64  `arg:""`
	Target   string `long:"target" default:"{}"`
	Provider string `long:"provider" default:"tmux"`
	RunID    string `long:"run-id" required:""`
}
type AgentTerminalSignalCmd struct {
	Profile  string `arg:""`
	Signal   string `arg:""`
	Target   string `long:"target" default:"{}"`
	Provider string `long:"provider" default:"tmux"`
	RunID    string `long:"run-id" required:""`
}
type AgentTerminalCloseCmd struct {
	Profile  string `arg:""`
	Target   string `long:"target" default:"{}"`
	Provider string `long:"provider" default:"tmux"`
	RunID    string `long:"run-id" required:""`
}
type AgentTerminalTranscriptCmd struct {
	RunID string `arg:"" name:"run" help:"Terminal run UUIDv7"`
}

func (c *AgentTerminalAttachCmd) Run() error {
	st, err := os.Stdin.Stat()
	if err != nil {
		return fmt.Errorf("inspect terminal input: %w", err)
	}
	if st.Mode()&os.ModeCharDevice == 0 {
		return errors.New("agent terminal attach requires a TTY")
	}
	return executeTerminal(c.Profile, c.Target, c.Provider, c.RunID, terminalAttach)
}

func (c *AgentTerminalSnapshotCmd) Run() error {
	return executeTerminalSnapshot(c.Profile, c.Target, c.Provider, c.RunID)
}
func (c *AgentTerminalInputCmd) Run() error {
	kind := "text"
	if c.Paste {
		kind = "paste"
	}
	input := spec.TerminalInput{Kind: kind, Text: c.Text}
	if err := validateGenerated("#TerminalInput", input); err != nil {
		return err
	}
	return executeTerminalControl(c.Profile, c.Target, c.Provider, c.RunID, input)
}
func (c *AgentTerminalKeyCmd) Run() error {
	input := spec.TerminalInput{Kind: "key", Key: c.Key}
	if err := validateGenerated("#TerminalInput", input); err != nil {
		return err
	}
	return executeTerminalControl(c.Profile, c.Target, c.Provider, c.RunID, input)
}
func (c *AgentTerminalResizeCmd) Run() error {
	input := spec.TerminalInput{Kind: "resize", Cols: c.Cols, Rows: c.Rows}
	if err := validateGenerated("#TerminalInput", input); err != nil {
		return err
	}
	return executeTerminalControl(c.Profile, c.Target, c.Provider, c.RunID, input)
}
func (c *AgentTerminalSignalCmd) Run() error {
	input := spec.TerminalInput{Kind: "signal", Signal: c.Signal}
	if err := validateGenerated("#TerminalInput", input); err != nil {
		return err
	}
	return executeTerminalControl(c.Profile, c.Target, c.Provider, c.RunID, input)
}
func (c *AgentTerminalCloseCmd) Run() error {
	input := spec.TerminalInput{Kind: "close"}
	if err := validateGenerated("#TerminalInput", input); err != nil {
		return err
	}
	return executeTerminalControl(c.Profile, c.Target, c.Provider, c.RunID, input)
}

func (c *AgentTerminalTranscriptCmd) Run() error {
	runID := spec.UUIDv7(c.RunID)
	if err := validateGenerated("#UUIDv7", runID); err != nil {
		return err
	}
	store, err := agentStore()
	if err != nil {
		return err
	}
	frames, err := store.TerminalFrames(runID)
	if err != nil {
		return err
	}
	return writeJSON(frames)
}

type terminalExecutionMode uint8

const (
	terminalWait terminalExecutionMode = iota
	terminalDetach
	terminalAttach
)

func executeTerminal(profileArg, targetArg, provider, requestedRunID string, mode terminalExecutionMode) (returnErr error) {
	target, err := decodeTarget(targetArg)
	if err != nil {
		return err
	}
	profile, err := resolveTerminalProfile(profileArg, target)
	if err != nil {
		return err
	}
	runID := spec.UUIDv7(requestedRunID)
	if runID == "" {
		runID = agentkit.NewID()
	}
	if err := validateGenerated("#UUIDv7", runID); err != nil {
		return err
	}
	evidenceStore, err := agentStore()
	if err != nil {
		return err
	}
	cursor, err := terminalCursor(evidenceStore, runID)
	if err != nil {
		return err
	}
	payload, err := marshalGenerated("#TerminalProfile", profile)
	if err != nil {
		return err
	}
	targetJSON, err := marshalGenerated("#TargetSpec", target)
	if err != nil {
		return err
	}
	open := &pb.ChannelFrame{Kind: sdk.ChannelOpen, RequestId: string(runID), Class: classTerminal, Reserved: provider, Op: "run", PayloadJson: payload, TargetJson: targetJSON, AckSequence: cursor}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inputs := make(chan *pb.ChannelFrame, 256)
	var inputFailure <-chan error
	if mode == terminalAttach {
		cleanup, failures, err := startTerminalInput(ctx, cancel, inputs)
		if err != nil {
			return err
		}
		inputFailure = failures
		defer func() {
			returnErr = errors.Join(returnErr, cleanup())
		}()
	}
	state := &terminalChannelState{gate: sdk.NewSequenceGate(cursor + 1), inputs: inputs, store: evidenceStore, runID: runID, mode: mode, cancel: cancel}
	channel := newControllerChannel(ctx, inputs, state.handle)
	runErr := runControlChannel(ctx, open, target, channel)
	select {
	case err := <-inputFailure:
		return recordTerminalIncident(runID, err)
	default:
	}
	if mode == terminalDetach && state.launched.Load() {
		return writeJSON(map[string]any{"run_id": runID, "state": "detached"})
	}
	if failure := state.failure(); failure != nil {
		return recordTerminalIncident(runID, failure)
	}
	if runErr != nil {
		return recordTerminalIncident(runID, runErr)
	}
	return nil
}

func startTerminalInput(ctx context.Context, cancel context.CancelFunc, inputs chan<- *pb.ChannelFrame) (func() error, <-chan error, error) {
	var sequence atomic.Uint64
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, nil, fmt.Errorf("terminal raw mode: %w", err)
	}
	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	failures := make(chan error, 1)
	report := func(err error) {
		select {
		case failures <- err:
		default:
		}
		cancel()
	}
	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		signal.Stop(resize)
		return nil, nil, errors.Join(fmt.Errorf("read terminal size: %w", err), term.Restore(int(os.Stdin.Fd()), oldState))
	}
	frame, err := terminalInputFrame(spec.TerminalInput{Kind: "resize", Cols: int64(cols), Rows: int64(rows)}, sequence.Add(1))
	if err != nil {
		signal.Stop(resize)
		return nil, nil, errors.Join(err, term.Restore(int(os.Stdin.Fd()), oldState))
	}
	inputs <- frame
	go pumpTerminalResize(ctx, resize, inputs, &sequence, report)
	go pumpTerminalStdin(ctx, inputs, &sequence, report)
	cleanup := func() error {
		signal.Stop(resize)
		return term.Restore(int(os.Stdin.Fd()), oldState)
	}
	return cleanup, failures, nil
}

func pumpTerminalResize(ctx context.Context, resize <-chan os.Signal, inputs chan<- *pb.ChannelFrame, sequence *atomic.Uint64, report func(error)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-resize:
			cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
			if err != nil {
				report(fmt.Errorf("read resized terminal dimensions: %w", err))
				return
			}
			frame, err := terminalInputFrame(spec.TerminalInput{Kind: "resize", Cols: int64(cols), Rows: int64(rows)}, sequence.Add(1))
			if err != nil {
				report(err)
				return
			}
			select {
			case inputs <- frame:
			case <-ctx.Done():
				return
			}
		}
	}
}

func pumpTerminalStdin(ctx context.Context, inputs chan<- *pb.ChannelFrame, sequence *atomic.Uint64, report func(error)) {
	buffer := make([]byte, 4096)
	for {
		count, readErr := os.Stdin.Read(buffer)
		for _, input := range decodeTTYInputs(buffer[:count]) {
			frame, err := terminalInputFrame(input, sequence.Add(1))
			if err != nil {
				report(err)
				return
			}
			select {
			case inputs <- frame:
			case <-ctx.Done():
				return
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				report(fmt.Errorf("read terminal input: %w", readErr))
			}
			return
		}
	}
}

type terminalChannelState struct {
	gate     *sdk.SequenceGate
	inputs   chan<- *pb.ChannelFrame
	store    *agentkit.Store
	runID    spec.UUIDv7
	mode     terminalExecutionMode
	cancel   context.CancelFunc
	launched atomic.Bool
	mu       sync.Mutex
	err      error
}

func (s *terminalChannelState) handle(frame *pb.ChannelFrame) error {
	accepted, err := acceptChannelFrame(s.gate, frame, s.inputs)
	if err != nil || !accepted {
		return err
	}
	if err := persistTerminalFrame(s.store, s.runID, frame); err != nil {
		return err
	}
	if frame.GetSequence() != 0 && frame.GetKind() != sdk.ChannelAck {
		select {
		case s.inputs <- &pb.ChannelFrame{Kind: sdk.ChannelAck, AckSequence: frame.GetSequence()}:
		default:
			return errors.New("terminal acknowledgement queue exhausted")
		}
	}
	s.captureFailure(frame)
	// launch is a single-document command: it persists and acknowledges all
	// early terminal evidence, but only emits the final {run_id,state} result.
	// Fast processes can produce frames immediately after the provider attaches;
	// leaking those frames would make otherwise successful launch output invalid
	// JSON and force callers to depend on process timing.
	if s.mode == terminalDetach {
		if frame.GetKind() == sdk.ChannelStatus && (frame.GetName() == "running" || frame.GetName() == "reattached") {
			s.launched.Store(true)
			s.cancel()
		}
		return nil
	}
	if s.mode == terminalAttach && (frame.GetKind() == sdk.ChannelTerminal || frame.GetKind() == sdk.ChannelStdout) {
		_, err := os.Stdout.Write(frame.GetData())
		return err
	}
	return writeJSON(frame)
}

func (s *terminalChannelState) captureFailure(frame *pb.ChannelFrame) {
	if frame.GetKind() != sdk.ChannelError && (frame.GetKind() != sdk.ChannelResync || frame.GetError() == "") && (frame.GetKind() != sdk.ChannelExit || frame.GetExitCode() == 0) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if frame.GetError() != "" {
		s.err = errors.New(frame.GetError())
	} else {
		s.err = fmt.Errorf("terminal exited with status %d", frame.GetExitCode())
	}
}

func (s *terminalChannelState) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func recordTerminalIncident(runID spec.UUIDv7, cause error) error {
	store, err := agentStore()
	if err != nil {
		return errors.Join(cause, err)
	}
	incident := spec.Incident{
		ID: agentkit.NewID(), RunID: runID, State: "needs_rca", Summary: cause.Error(),
		CreatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		EvidenceRefs: []string{"terminal/" + string(runID)},
	}
	if err := validateGenerated("#Incident", incident); err != nil {
		return errors.Join(cause, err)
	}
	if err := store.PutIncident(incident); err != nil {
		return errors.Join(cause, err)
	}
	return fmt.Errorf("terminal run %s failed; incident %s recorded and no recovery attempted: %w", runID, incident.ID, cause)
}

func executeTerminalControl(profileArg, targetArg, provider, requestedRunID string, input spec.TerminalInput) error {
	if err := validateGenerated("#TerminalInput", input); err != nil {
		return err
	}
	_, target, runID, open, err := terminalOpen(profileArg, targetArg, provider, requestedRunID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inputs := make(chan *pb.ChannelFrame, 128)
	frame, err := terminalInputFrame(input, 1)
	if err != nil {
		return err
	}
	inputs <- frame
	var acknowledged atomic.Bool
	var providerErr error
	var providerErrMu sync.Mutex
	sequenceGate := sdk.NewSequenceGate(open.GetAckSequence() + 1)
	evidenceStore, err := agentStore()
	if err != nil {
		return err
	}
	channel := newControllerChannel(ctx, inputs, func(frame *pb.ChannelFrame) error {
		accepted, err := acceptChannelFrame(sequenceGate, frame, inputs)
		if err != nil {
			return err
		}
		if !accepted {
			return nil
		}
		if err := persistTerminalFrame(evidenceStore, runID, frame); err != nil {
			return err
		}
		if frame.GetKind() == sdk.ChannelError {
			providerErrMu.Lock()
			providerErr = errors.New(frame.GetError())
			providerErrMu.Unlock()
			cancel()
			return nil
		}
		if frame.GetSequence() != 0 && frame.GetKind() != sdk.ChannelAck {
			select {
			case inputs <- &pb.ChannelFrame{Kind: sdk.ChannelAck, AckSequence: frame.GetSequence()}:
			case <-ctx.Done():
			}
		}
		if frame.GetKind() == sdk.ChannelAck && frame.GetAckSequence() == 1 {
			acknowledged.Store(true)
			cancel()
		}
		return nil
	})
	runErr := runControlChannel(ctx, open, target, channel)
	providerErrMu.Lock()
	channelErr := providerErr
	providerErrMu.Unlock()
	if channelErr != nil {
		return recordTerminalIncident(runID, channelErr)
	}
	if !acknowledged.Load() {
		cause := errors.New("terminal control was not acknowledged")
		if runErr != nil {
			cause = errors.Join(cause, runErr)
		}
		return recordTerminalIncident(runID, cause)
	}
	return writeJSON(input)
}

func executeTerminalSnapshot(profileArg, targetArg, provider, requestedRunID string) error {
	profile, target, runID, open, err := terminalOpen(profileArg, targetArg, provider, requestedRunID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inputs := make(chan *pb.ChannelFrame, 128)
	var snapshot *spec.TerminalSnapshot
	var providerErr error
	var providerErrMu sync.Mutex
	sequenceGate := sdk.NewSequenceGate(open.GetAckSequence() + 1)
	evidenceStore, err := agentStore()
	if err != nil {
		return err
	}
	channel := newControllerChannel(ctx, inputs, func(frame *pb.ChannelFrame) error {
		accepted, err := acceptChannelFrame(sequenceGate, frame, inputs)
		if err != nil {
			return err
		}
		if !accepted {
			return nil
		}
		if err := persistTerminalFrame(evidenceStore, runID, frame); err != nil {
			return err
		}
		if frame.GetSequence() != 0 && frame.GetKind() != sdk.ChannelAck {
			select {
			case inputs <- &pb.ChannelFrame{Kind: sdk.ChannelAck, AckSequence: frame.GetSequence()}:
			case <-ctx.Done():
			}
		}
		if frame.GetKind() == sdk.ChannelError {
			providerErrMu.Lock()
			providerErr = errors.New(frame.GetError())
			providerErrMu.Unlock()
			cancel()
			return nil
		}
		if frame.GetKind() == sdk.ChannelResync || (frame.GetKind() == sdk.ChannelStatus && frame.GetName() == "screen") {
			cols, rows := int64(frame.GetCols()), int64(frame.GetRows())
			if cols == 0 {
				cols = profile.Cols
			}
			if rows == 0 {
				rows = profile.Rows
			}
			value := spec.TerminalSnapshot{RunID: runID, Sequence: int64(frame.GetSequence()), Cols: cols, Rows: rows, Screen: string(frame.GetData())}
			if validateErr := validateGenerated("#TerminalSnapshot", value); validateErr != nil {
				providerErrMu.Lock()
				providerErr = validateErr
				providerErrMu.Unlock()
			} else {
				snapshot = &value
			}
			cancel()
		}
		return nil
	})
	runErr := runControlChannel(ctx, open, target, channel)
	providerErrMu.Lock()
	channelErr := providerErr
	providerErrMu.Unlock()
	if channelErr != nil {
		return recordTerminalIncident(runID, channelErr)
	}
	if snapshot == nil {
		cause := errors.New("terminal provider produced no screen snapshot")
		if runErr != nil {
			cause = errors.Join(cause, runErr)
		}
		return recordTerminalIncident(runID, cause)
	}
	return writeJSON(snapshot)
}

func terminalOpen(profileArg, targetArg, provider, requestedRunID string) (spec.TerminalProfile, spec.TargetSpec, spec.UUIDv7, *pb.ChannelFrame, error) {
	target, err := decodeTarget(targetArg)
	if err != nil {
		return spec.TerminalProfile{}, target, "", nil, err
	}
	profile, err := resolveTerminalProfile(profileArg, target)
	if err != nil {
		return profile, target, "", nil, err
	}
	runID := spec.UUIDv7(requestedRunID)
	if err := validateGenerated("#UUIDv7", runID); err != nil {
		return profile, target, runID, nil, err
	}
	store, err := agentStore()
	if err != nil {
		return profile, target, runID, nil, err
	}
	cursor, err := terminalCursor(store, runID)
	if err != nil {
		return profile, target, runID, nil, err
	}
	payload, err := marshalGenerated("#TerminalProfile", profile)
	if err != nil {
		return profile, target, runID, nil, err
	}
	targetJSON, err := marshalGenerated("#TargetSpec", target)
	if err != nil {
		return profile, target, runID, nil, err
	}
	open := &pb.ChannelFrame{Kind: sdk.ChannelOpen, RequestId: string(runID), Class: classTerminal, Reserved: provider, Op: "run", PayloadJson: payload, TargetJson: targetJSON, AckSequence: cursor}
	return profile, target, runID, open, nil
}

func terminalCursor(store *agentkit.Store, runID spec.UUIDv7) (uint64, error) {
	frames, err := store.TerminalFrames(runID)
	if err != nil {
		return 0, err
	}
	if len(frames) == 0 {
		return 0, nil
	}
	last := frames[len(frames)-1].Sequence
	if last < 1 {
		return 0, fmt.Errorf("terminal transcript %s has invalid final sequence %d", runID, last)
	}
	return uint64(last), nil
}

func terminalInputFrame(input spec.TerminalInput, sequence uint64) (*pb.ChannelFrame, error) {
	payload, err := marshalGenerated("#TerminalInput", input)
	if err != nil {
		return nil, err
	}
	frame := &pb.ChannelFrame{Sequence: sequence, PayloadJson: payload}
	switch input.Kind {
	case "text", "paste", "command":
		frame.Kind, frame.Name, frame.Data = sdk.ChannelStdin, input.Kind, []byte(input.Text)
	case "key":
		frame.Kind, frame.Name = sdk.ChannelTerminal, input.Key
	case "resize":
		frame.Kind, frame.Cols, frame.Rows = sdk.ChannelResize, uint32(input.Cols), uint32(input.Rows)
	case "signal":
		frame.Kind, frame.Name = sdk.ChannelSignal, input.Signal
	case "close":
		frame.Kind = sdk.ChannelExit
	}
	return frame, nil
}

func decodeTTYInputs(data []byte) []spec.TerminalInput {
	keys := map[string]string{
		"\x1b[A": "up", "\x1b[B": "down", "\x1b[C": "right", "\x1b[D": "left",
		"\x1b[H": "home", "\x1b[F": "end", "\x1b[2~": "insert", "\x1b[3~": "delete",
		"\x1b[5~": "page-up", "\x1b[6~": "page-down",
	}
	controls := map[byte]string{
		'\r': "enter", '\n': "enter", '\t': "tab", 0x7f: "backspace", 0x1b: "escape",
		0x03: "ctrl-c", 0x04: "ctrl-d", 0x1a: "ctrl-z", 0x0c: "ctrl-l", 0x01: "ctrl-a", 0x05: "ctrl-e",
	}
	var out []spec.TerminalInput
	for len(data) > 0 {
		matched := false
		for sequence, key := range keys {
			if strings.HasPrefix(string(data), sequence) {
				out = append(out, spec.TerminalInput{Kind: "key", Key: key})
				data = data[len(sequence):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if key, ok := controls[data[0]]; ok {
			out = append(out, spec.TerminalInput{Kind: "key", Key: key})
			data = data[1:]
			continue
		}
		end := 0
		for end < len(data) {
			if _, ok := controls[data[end]]; ok {
				break
			}
			end++
		}
		if end == 0 {
			end = 1
		}
		out = append(out, spec.TerminalInput{Kind: "text", Text: string(data[:end])})
		data = data[end:]
	}
	return out
}

func acceptChannelFrame(gate *sdk.SequenceGate, frame *pb.ChannelFrame, inputs chan<- *pb.ChannelFrame) (bool, error) {
	if frame.GetKind() == sdk.ChannelAck || frame.GetSequence() == 0 {
		return true, nil
	}
	if err := gate.Accept(frame); err != nil {
		request := &pb.ChannelFrame{Kind: sdk.ChannelResync, ReplayFrom: gate.Expected()}
		select {
		case inputs <- request:
			return false, nil
		default:
			return false, errors.Join(err, errors.New("channel replay request queue exhausted"))
		}
	}
	return true, nil
}

func persistTerminalFrame(store *agentkit.Store, runID spec.UUIDv7, frame *pb.ChannelFrame) error {
	if frame.GetKind() == sdk.ChannelAck || frame.GetSequence() == 0 {
		return nil
	}
	evidence := spec.TerminalFrame{RunID: runID, Sequence: int64(frame.GetSequence()), Status: frame.GetName()}
	switch frame.GetKind() {
	case sdk.ChannelTerminal, sdk.ChannelStdout, sdk.ChannelStderr:
		evidence.Kind = "raw"
		evidence.Stream = frame.GetKind()
		evidence.Data = append([]byte(nil), frame.GetData()...)
	case sdk.ChannelResync:
		evidence.Kind = "resync"
		evidence.Snapshot = string(frame.GetData())
	case sdk.ChannelStatus:
		if frame.GetName() == "screen" {
			evidence.Kind = "screen"
			evidence.Snapshot = string(frame.GetData())
		} else {
			evidence.Kind = "status"
		}
	case sdk.ChannelExit:
		evidence.Kind = "exit"
		code := int(frame.GetExitCode())
		evidence.ExitCode = &code
	case sdk.ChannelError:
		evidence.Kind = "error"
		evidence.Error = frame.GetError()
	default:
		evidence.Kind = "status"
		evidence.Status = frame.GetKind()
	}
	if err := validateGenerated("#TerminalFrame", evidence); err != nil {
		return err
	}
	stored, err := store.AppendTerminalFrame(evidence)
	if err != nil {
		return err
	}
	if stored.Sequence != evidence.Sequence {
		return fmt.Errorf("terminal transcript sequence %d, provider sent %d", stored.Sequence, evidence.Sequence)
	}
	return nil
}

func resolveTerminalProfile(arg string, target spec.TargetSpec) (spec.TerminalProfile, error) {
	var profile spec.TerminalProfile
	if strings.HasPrefix(strings.TrimSpace(arg), "{") || strings.HasPrefix(arg, "@") {
		return profile, decodeGenerated(arg, "#TerminalProfile", &profile)
	}
	if target.Deployment == "" {
		return profile, fmt.Errorf("terminal profile %q needs target.deployment so it can be resolved from OCI capability labels; pass #TerminalProfile JSON for an unbaked local process", arg)
	}
	value, err := terminalProfileFromDeployment(arg, target)
	if err != nil {
		return profile, err
	}
	if err := validateGenerated("#TerminalProfile", value); err != nil {
		return profile, err
	}
	return value, nil
}

func terminalProfileFromDeployment(name string, target spec.TargetSpec) (spec.TerminalProfile, error) {
	bin, err := charlyBinary()
	if err != nil {
		return spec.TerminalProfile{}, err
	}
	command := exec.Command(bin, "box", "labels", target.Deployment, "--format", "terminal_profiles")
	command.Stderr = os.Stderr
	payload, err := command.Output()
	if err != nil {
		return spec.TerminalProfile{}, fmt.Errorf("resolve terminal profiles for deployment %q: %w", target.Deployment, err)
	}
	var profiles map[string]spec.TerminalProfile
	if err := json.Unmarshal(payload, &profiles); err != nil {
		return spec.TerminalProfile{}, fmt.Errorf("decode %s OCI label for deployment %q: %w", spec.LabelTerminalProfiles, target.Deployment, err)
	}
	profile, ok := profiles[name]
	if !ok {
		return spec.TerminalProfile{}, fmt.Errorf("terminal profile %q is not declared by deployment %q", name, target.Deployment)
	}
	return profile, nil
}

type AgentIncidentCmd struct {
	Create AgentIncidentCreateCmd `cmd:""`
	List   AgentIncidentListCmd   `cmd:""`
	Show   AgentIncidentShowCmd   `cmd:""`
}
type AgentIncidentCreateCmd struct {
	Summary string `arg:""`
	RunID   string `long:"run"`
}

func (c *AgentIncidentCreateCmd) Run() error {
	s, e := agentStore()
	if e != nil {
		return e
	}
	v := spec.Incident{ID: agentkit.NewID(), RunID: spec.UUIDv7(c.RunID), State: "needs_rca", Summary: c.Summary, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if e = validateGenerated("#Incident", v); e != nil {
		return e
	}
	if e = s.PutIncident(v); e != nil {
		return e
	}
	return writeJSON(v)
}

type AgentIncidentListCmd struct{}
type AgentIncidentShowCmd struct {
	ID string `arg:""`
}

func (*AgentIncidentListCmd) Run() error {
	s, e := agentStore()
	if e != nil {
		return e
	}
	v, e := s.Incidents()
	if e != nil {
		return e
	}
	return writeJSON(v)
}

func (c *AgentIncidentShowCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.Incident(spec.UUIDv7(c.ID))
	if err != nil {
		return err
	}
	return writeJSON(v)
}

type AgentRCACmd struct {
	Start    AgentRCAStartCmd    `cmd:""`
	Show     AgentRCAShowCmd     `cmd:""`
	List     AgentRCAListCmd     `cmd:""`
	Complete AgentRCACompleteCmd `cmd:""`
}
type AgentRCAStartCmd struct {
	Incident string `arg:""`
}
type AgentRCAShowCmd struct {
	ID string `arg:""`
}
type AgentRCAListCmd struct{}

func (c *AgentRCAShowCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.RCA(spec.UUIDv7(c.ID))
	if err != nil {
		return err
	}
	return writeJSON(v)
}

func (*AgentRCAListCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.RCAs()
	if err != nil {
		return err
	}
	return writeJSON(v)
}

func (c *AgentRCAStartCmd) Run() error {
	s, e := agentStore()
	if e != nil {
		return e
	}
	i, e := s.Incident(spec.UUIDv7(c.Incident))
	if e != nil {
		return e
	}
	if i.State != "needs_rca" {
		return fmt.Errorf("incident state is %s", i.State)
	}
	r := spec.RCARecord{ID: agentkit.NewID(), IncidentID: i.ID, State: "active"}
	i.State = "rca_active"
	if e = s.PutRCA(r); e != nil {
		return e
	}
	if e = s.PutIncident(i); e != nil {
		return e
	}
	return writeJSON(r)
}

type AgentRCACompleteCmd struct {
	RCA       string   `arg:""`
	RootCause string   `long:"root-cause" required:""`
	Finding   []string `long:"finding"`
}

func (c *AgentRCACompleteCmd) Run() error {
	s, e := agentStore()
	if e != nil {
		return e
	}
	r, e := s.RCA(spec.UUIDv7(c.RCA))
	if e != nil {
		return e
	}
	if r.State != "active" {
		return errors.New("RCA is not active")
	}
	r.State = "complete"
	r.RootCause = c.RootCause
	r.Findings = c.Finding
	r.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	i, e := s.Incident(r.IncidentID)
	if e != nil {
		return e
	}
	i.State = "awaiting_recovery"
	if e = s.PutRCA(r); e != nil {
		return e
	}
	if e = s.PutIncident(i); e != nil {
		return e
	}
	return writeJSON(r)
}

type AgentRecoverCmd struct {
	Plan   AgentRecoverPlanCmd   `cmd:"" help:"Build a CUE-validated recovery decision without applying it"`
	Apply  AgentRecoverApplyCmd  `cmd:"" help:"Apply an explicit CUE-validated recovery decision"`
	Decide AgentRecoverDecideCmd `cmd:"" help:"Plan and apply a recovery decision in one invocation"`
	List   AgentRecoverListCmd   `cmd:""`
	Show   AgentRecoverShowCmd   `cmd:""`
}
type AgentRecoverPlanCmd struct {
	Incident       string `arg:""`
	RCA            string `long:"rca"`
	Action         string `long:"action" required:"" enum:"reattach,resume,restart,rebuild-target,change-runtime,reassign,abort,operator"`
	EmergencyAbort bool   `long:"emergency-abort"`
	Params         string `long:"params" default:"{}" help:"Recovery params JSON or @file"`
}
type AgentRecoverApplyCmd struct {
	Decision string `arg:"" help:"#RecoveryDecision JSON or @file"`
}
type AgentRecoverListCmd struct{}
type AgentRecoverShowCmd struct {
	ID string `arg:""`
}
type AgentRecoverDecideCmd struct {
	Incident       string `arg:""`
	RCA            string `long:"rca"`
	Action         string `long:"action" required:"" enum:"reattach,resume,restart,rebuild-target,change-runtime,reassign,abort,operator"`
	EmergencyAbort bool   `long:"emergency-abort"`
	Params         string `long:"params" default:"{}" help:"CUE #RecoveryParams JSON or @file"`
}

func (c *AgentRecoverDecideCmd) Run() error {
	params, err := decodeRecoveryParams(c.Params)
	if err != nil {
		return err
	}
	v, s, err := prepareRecovery(c.Incident, c.RCA, c.Action, c.EmergencyAbort, params)
	if err != nil {
		return err
	}
	v, err = applyRecovery(s, v)
	if err != nil {
		return err
	}
	return writeJSON(v)
}

func (c *AgentRecoverPlanCmd) Run() error {
	params, err := decodeRecoveryParams(c.Params)
	if err != nil {
		return err
	}
	v, _, err := prepareRecovery(c.Incident, c.RCA, c.Action, c.EmergencyAbort, params)
	if err != nil {
		return err
	}
	return writeJSON(v)
}

func (c *AgentRecoverApplyCmd) Run() error {
	var v spec.RecoveryDecision
	if err := decodeGenerated(c.Decision, "#RecoveryDecision", &v); err != nil {
		return err
	}
	s, err := agentStore()
	if err != nil {
		return err
	}
	if err := authorizeRecovery(s, v); err != nil {
		return err
	}
	if err := validateRecoveryActionParams(v); err != nil {
		return err
	}
	v, err = applyRecovery(s, v)
	if err != nil {
		return err
	}
	return writeJSON(v)
}

func (*AgentRecoverListCmd) Run() error {
	s, e := agentStore()
	if e != nil {
		return e
	}
	v, e := s.Recoveries()
	if e != nil {
		return e
	}
	return writeJSON(v)
}

func (c *AgentRecoverShowCmd) Run() error {
	s, err := agentStore()
	if err != nil {
		return err
	}
	v, err := s.Recovery(spec.UUIDv7(c.ID))
	if err != nil {
		return err
	}
	return writeJSON(v)
}

func decodeRecoveryParams(arg string) (*spec.RecoveryParams, error) {
	data, err := readArg(arg)
	if err != nil {
		return nil, err
	}
	var params spec.RecoveryParams
	if err := json.Unmarshal(data, &params); err != nil {
		return nil, fmt.Errorf("recovery params: %w", err)
	}
	if err := validateGenerated("#RecoveryParams", params); err != nil {
		return nil, err
	}
	return &params, nil
}

func prepareRecovery(incident, rca, action string, emergency bool, params *spec.RecoveryParams) (spec.RecoveryDecision, *agentkit.Store, error) {
	s, err := agentStore()
	if err != nil {
		return spec.RecoveryDecision{}, nil, err
	}
	v := spec.RecoveryDecision{
		ID: agentkit.NewID(), IncidentID: spec.UUIDv7(incident), RCAID: spec.UUIDv7(rca),
		Action: action, AuthorizedEmergencyAbort: emergency, Params: params,
		State: "planned", DecidedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := validateGenerated("#RecoveryDecision", v); err != nil {
		return v, nil, err
	}
	if err := validateRecoveryActionParams(v); err != nil {
		return v, nil, err
	}
	if err := authorizeRecovery(s, v); err != nil {
		return v, nil, err
	}
	return v, s, nil
}

func authorizeRecovery(s *agentkit.Store, v spec.RecoveryDecision) error {
	i, err := s.Incident(v.IncidentID)
	if err != nil {
		return err
	}
	completed := false
	if v.RCAID != "" {
		r, err := s.RCA(v.RCAID)
		if err != nil {
			return err
		}
		completed = r.State == "complete" && r.IncidentID == i.ID
	}
	if !completed && (v.Action != "abort" || !v.AuthorizedEmergencyAbort) {
		return errors.New("recovery requires a completed RCA; only an explicit emergency abort may bypass it")
	}
	if i.State == "resolved" {
		return fmt.Errorf("incident %s is already resolved", i.ID)
	}
	return nil
}

func applyRecovery(s *agentkit.Store, v spec.RecoveryDecision) (spec.RecoveryDecision, error) {
	if v.State != "planned" {
		return v, fmt.Errorf("recovery decision %s is %q, not planned", v.ID, v.State)
	}
	i, err := s.Incident(v.IncidentID)
	if err != nil {
		return v, err
	}
	if err := s.PutRecovery(v); err != nil {
		return v, err
	}
	if err := executeRecoveryAction(s, v); err != nil {
		v.State = "failed"
		v.Error = err.Error()
		if validateErr := validateGenerated("#RecoveryDecision", v); validateErr != nil {
			return v, errors.Join(err, validateErr)
		}
		if storeErr := s.PutRecovery(v); storeErr != nil {
			return v, errors.Join(err, storeErr)
		}
		return v, err
	}
	v.State = "applied"
	v.AppliedAt = time.Now().UTC().Format(time.RFC3339Nano)
	i.State = "resolved"
	if err := validateGenerated("#RecoveryDecision", v); err != nil {
		return v, err
	}
	if err := s.PutRecovery(v); err != nil {
		return v, err
	}
	if err := s.PutIncident(i); err != nil {
		return v, err
	}
	return v, nil
}

func validateRecoveryActionParams(v spec.RecoveryDecision) error {
	if v.Params == nil {
		return fmt.Errorf("recovery action %q requires params", v.Action)
	}
	p := v.Params
	requireRun := func() error {
		if p.RunID == "" {
			return fmt.Errorf("recovery action %q requires params.run_id", v.Action)
		}
		return nil
	}
	requireSession := func() error {
		if p.SessionID == "" {
			return fmt.Errorf("recovery action %q requires params.session_id", v.Action)
		}
		return nil
	}
	switch v.Action {
	case "reattach":
		if err := requireRun(); err != nil {
			return err
		}
		if p.TerminalProfile == nil {
			return errors.New("recovery action reattach requires params.terminal_profile")
		}
	case "resume", "restart":
		return requireSession()
	case "rebuild-target":
		if p.Deployment == "" {
			return errors.New("recovery action rebuild-target requires params.deployment")
		}
	case "change-runtime":
		if err := requireSession(); err != nil {
			return err
		}
		if p.Runtime == "" {
			return errors.New("recovery action change-runtime requires params.runtime")
		}
	case "reassign":
		if err := requireSession(); err != nil {
			return err
		}
		if p.Target == nil {
			return errors.New("recovery action reassign requires params.target")
		}
	case "abort":
		return requireRun()
	case "operator":
		if p.Note == "" {
			return errors.New("recovery action operator requires params.note")
		}
	default:
		return fmt.Errorf("unsupported recovery action %q", v.Action)
	}
	return nil
}

func executeRecoveryAction(store *agentkit.Store, v spec.RecoveryDecision) error {
	p := v.Params
	stateArgs := []string{"agent"}
	if agentStateDir != "" {
		stateArgs = append(stateArgs, "--state-dir", agentStateDir)
	}
	run := func(args ...string) error {
		binary, err := charlyBinary()
		if err != nil {
			return err
		}
		command := exec.Command(binary, args...)
		var stderr strings.Builder
		command.Stderr = &stderr
		if _, err := command.Output(); err != nil {
			return fmt.Errorf("recovery action %s: %w (stderr: %s)", v.Action, err, strings.TrimSpace(stderr.String()))
		}
		return nil
	}
	runInteractive := func(args ...string) error {
		binary, err := charlyBinary()
		if err != nil {
			return err
		}
		command := exec.Command(binary, args...)
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		return command.Run()
	}
	switch v.Action {
	case "reattach":
		profile, err := json.Marshal(p.TerminalProfile)
		if err != nil {
			return fmt.Errorf("encode recovery terminal profile: %w", err)
		}
		target := spec.TargetSpec{}
		if p.Target != nil {
			target = *p.Target
		}
		targetJSON, err := json.Marshal(target)
		if err != nil {
			return fmt.Errorf("encode recovery target: %w", err)
		}
		provider := p.Provider
		if provider == "" {
			provider = "tmux"
		}
		return runInteractive(append(stateArgs, "terminal", "attach", string(profile), "--target", string(targetJSON), "--provider", provider, "--run-id", string(p.RunID))...)
	case "resume", "restart":
		args := append([]string{}, stateArgs...)
		args = append(args, "run", "start", string(p.SessionID), p.Prompt, "--idempotency-key", string(v.ID))
		if v.Action == "resume" {
			args = append(args, "--resume")
		}
		return run(args...)
	case "rebuild-target":
		return run("update", p.Deployment, "--build")
	case "change-runtime":
		session, err := store.Session(p.SessionID)
		if err != nil {
			return err
		}
		session.Runtime = p.Runtime
		session.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := validateGenerated("#AgentSession", session); err != nil {
			return err
		}
		return store.PutSession(session)
	case "reassign":
		session, err := store.Session(p.SessionID)
		if err != nil {
			return err
		}
		session.Target = *p.Target
		session.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		if err := validateGenerated("#AgentSession", session); err != nil {
			return err
		}
		return store.PutSession(session)
	case "abort":
		if p.TerminalProfile != nil {
			profile, err := json.Marshal(p.TerminalProfile)
			if err != nil {
				return fmt.Errorf("encode recovery terminal profile: %w", err)
			}
			target := spec.TargetSpec{}
			if p.Target != nil {
				target = *p.Target
			}
			targetJSON, err := json.Marshal(target)
			if err != nil {
				return fmt.Errorf("encode recovery target: %w", err)
			}
			provider := p.Provider
			if provider == "" {
				provider = "tmux"
			}
			return run(append(stateArgs, "terminal", "close", string(profile), "--target", string(targetJSON), "--provider", provider, "--run-id", string(p.RunID))...)
		}
		return run(append(stateArgs, "run", "abort", string(p.RunID))...)
	case "operator":
		return nil
	default:
		return fmt.Errorf("unsupported recovery action %q", v.Action)
	}
}

type controllerChannel struct {
	ctx   context.Context
	input <-chan *pb.ChannelFrame
	send  func(*pb.ChannelFrame) error
	mu    sync.Mutex
}

func newControllerChannel(ctx context.Context, input <-chan *pb.ChannelFrame, send func(*pb.ChannelFrame) error) *controllerChannel {
	return &controllerChannel{ctx: ctx, input: input, send: send}
}
func (c *controllerChannel) Context() context.Context { return c.ctx }
func (c *controllerChannel) Send(f *pb.ChannelFrame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.send(f)
}
func (c *controllerChannel) Recv() (*pb.ChannelFrame, error) {
	if c.input == nil {
		<-c.ctx.Done()
		return nil, c.ctx.Err()
	}
	select {
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	case f, ok := <-c.input:
		if !ok {
			<-c.ctx.Done()
			return nil, c.ctx.Err()
		}
		return f, nil
	}
}

func runControlChannel(ctx context.Context, open *pb.ChannelFrame, target spec.TargetSpec, ch sdk.ProviderChannel) (returnErr error) {
	targetSummary := terminalTargetSummary(target)
	fmt.Fprintf(os.Stderr, "agent channel: OPEN %s:%s operation=%s request=%s target=%s\n", open.GetClass(), open.GetReserved(), open.GetOp(), open.GetRequestId(), targetSummary)
	conn, client, err := dialTargetProvider(ctx, target)
	if err != nil {
		return fmt.Errorf("agent channel: dial controller for %s:%s target=%s: %w", open.GetClass(), open.GetReserved(), targetSummary, err)
	}
	defer func() { returnErr = errors.Join(returnErr, conn.Close()) }()
	downstream, err := sdk.OpenProviderChannel(ctx, client, open)
	if err != nil {
		return fmt.Errorf("agent channel: open %s:%s target=%s: %w", open.GetClass(), open.GetReserved(), targetSummary, err)
	}
	err = sdk.RelayChannel(ch, downstream)
	if ctx.Err() != nil {
		fmt.Fprintf(os.Stderr, "agent channel: CLOSE %s:%s request=%s target=%s (controller context closed)\n", open.GetClass(), open.GetReserved(), open.GetRequestId(), targetSummary)
		return nil
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent channel: FAIL %s:%s request=%s target=%s: %v\n", open.GetClass(), open.GetReserved(), open.GetRequestId(), targetSummary, err)
		return fmt.Errorf("agent channel: relay %s:%s target=%s: %w", open.GetClass(), open.GetReserved(), targetSummary, err)
	}
	fmt.Fprintf(os.Stderr, "agent channel: CLOSE %s:%s request=%s target=%s\n", open.GetClass(), open.GetReserved(), open.GetRequestId(), targetSummary)
	return err
}

func dialTargetProvider(ctx context.Context, _ spec.TargetSpec) (*grpc.ClientConn, pb.ProviderClient, error) {
	// Every command enters through the active LOCAL Charly endpoint. The open
	// frame retains the complete CUE TargetSpec; relayNestedTarget is the one
	// generic consumer for deployment/exec/SSH process+gRPC pairs and performs
	// endpoint replication at each remote boundary. Directly dialing an SSH-first
	// target here would bypass bootstrap and make the remote node see an
	// unconsumed outer hop.
	dialTarget := spec.TargetSpec{Hops: []spec.TargetHop{{Transport: "exec"}, {Transport: "grpc"}}}
	bin, err := charlyBinary()
	if err != nil {
		return nil, nil, err
	}
	return targetkit.DialProvider(ctx, dialTarget, targetkit.DialOptions{CharlyBinary: bin, Stderr: os.Stderr})
}

func terminalTargetSummary(target spec.TargetSpec) string {
	parts := make([]string, 0, len(target.Hops)+1)
	if target.Deployment != "" {
		deployment := target.Deployment
		if target.Instance != "" {
			deployment += ":" + target.Instance
		}
		parts = append(parts, "deployment="+deployment)
	}
	for _, hop := range target.Hops {
		parts = append(parts, hop.Transport)
	}
	if len(parts) == 0 {
		return "local"
	}
	return strings.Join(parts, "->")
}

func localCapabilities(ctx context.Context) (capabilities *pb.Capabilities, returnErr error) {
	conn, _, err := dialTargetProvider(ctx, spec.TargetSpec{})
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, conn.Close()) }()
	return pb.NewPluginMetaClient(conn).Describe(ctx, &pb.Empty{})
}

func charlyBinary() (string, error) {
	if binary := os.Getenv("CHARLY_BIN"); binary != "" {
		return binary, nil
	}
	return os.Executable()
}
func decodeTarget(arg string) (spec.TargetSpec, error) {
	var v spec.TargetSpec
	trimmed := strings.TrimSpace(arg)
	if trimmed != "" && !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "@") {
		separator := strings.LastIndex(trimmed, "::")
		if separator < 0 {
			v.Deployment = trimmed
			return v, validateGenerated("#TargetSpec", v)
		}
		host, deployment := trimmed[:separator], trimmed[separator+2:]
		if host == "" || deployment == "" || strings.Contains(deployment, "::") {
			return v, fmt.Errorf("target %q must be <deployment> or <host>::<deployment>", arg)
		}
		hop, err := parseTargetSSHHost(host)
		if err != nil {
			return v, err
		}
		v.Deployment = deployment
		v.Hops = []spec.TargetHop{hop, {Transport: "grpc"}}
		return v, validateGenerated("#TargetSpec", v)
	}
	return v, decodeGenerated(arg, "#TargetSpec", &v)
}

func parseTargetSSHHost(value string) (spec.TargetHop, error) {
	hop := spec.TargetHop{Transport: "ssh"}
	endpoint := value
	if at := strings.LastIndex(endpoint, "@"); at >= 0 {
		hop.User, endpoint = endpoint[:at], endpoint[at+1:]
		if hop.User == "" {
			return hop, fmt.Errorf("target host %q has an empty SSH user", value)
		}
	}
	if endpoint == "" {
		return hop, fmt.Errorf("target host %q has an empty SSH address", value)
	}
	host := endpoint
	port := ""
	if strings.HasPrefix(endpoint, "[") {
		if strings.HasSuffix(endpoint, "]") {
			host = endpoint
		} else {
			parsedHost, parsedPort, err := net.SplitHostPort(endpoint)
			if err != nil {
				return hop, fmt.Errorf("target host %q: %w", value, err)
			}
			host, port = "["+parsedHost+"]", parsedPort
		}
	} else if strings.Count(endpoint, ":") == 1 {
		parsedHost, parsedPort, err := net.SplitHostPort(endpoint)
		if err != nil {
			return hop, fmt.Errorf("target host %q: %w", value, err)
		}
		host, port = parsedHost, parsedPort
	} else if strings.Count(endpoint, ":") > 1 {
		host = "[" + endpoint + "]"
	}
	if port != "" {
		parsed, err := strconv.ParseInt(port, 10, 64)
		if err != nil || parsed < 1 || parsed > 65535 {
			return hop, fmt.Errorf("target host %q has invalid SSH port %q", value, port)
		}
		hop.Port = parsed
	}
	hop.Address = host
	return hop, nil
}
func decodeGenerated(arg, def string, dst any) error {
	b, err := readArg(arg)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, dst); err != nil {
		return err
	}
	return validateGenerated(def, dst)
}
func validateGenerated(def string, value any) error {
	return sdk.ValidateGenerated(def, value)
}

func marshalGenerated(def string, value any) ([]byte, error) {
	if err := validateGenerated(def, value); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", def, err)
	}
	return payload, nil
}
func readArg(arg string) ([]byte, error) {
	if strings.HasPrefix(arg, "@") {
		return os.ReadFile(strings.TrimPrefix(arg, "@"))
	}
	return []byte(arg), nil
}
func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

var _ io.Reader
