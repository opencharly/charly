package agentkind

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/agentkit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

func TestAgentTeamKindOwnsSchemaAndDeepValidation(t *testing.T) {
	capabilities, err := NewMeta().Describe(context.Background(), &pb.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, capability := range capabilities.GetProvided() {
		if capability.GetClass() == "kind" && capability.GetWord() == "agent-team" {
			found = capability.GetInputDef() == "#AgentTeamInput"
		}
	}
	if !found || !strings.Contains(capabilities.GetSchemaCue(), "#AgentTeamInput") {
		t.Fatal("agent-team kind did not publish its plugin-owned CUE input contract")
	}
	team := spec.AgentTeam{Agents: []spec.AgentTeamMember{{Name: "worker", Runtime: "pi"}}, Coordinator: "worker"}
	payload, err := json.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (provider{}).Invoke(context.Background(), &pb.InvokeRequest{Class: "kind", Reserved: "agent-team", Op: sdk.OpLoad, ParamsJson: payload}); err != nil {
		t.Fatal(err)
	}
	team.Coordinator = "missing"
	payload, err = json.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (provider{}).Invoke(context.Background(), &pb.InvokeRequest{Class: "kind", Reserved: "agent-team", Op: sdk.OpLoad, ParamsJson: payload}); err == nil {
		t.Fatal("agent-team with an unknown coordinator passed deep validation")
	}
}

func TestAgentKindValidatesRawJSONWithoutDestroyingCUEDefaults(t *testing.T) {
	valid := []byte(`{"command":["claude"],"output_format":"stream-json"}`)
	reply, err := (provider{}).Invoke(context.Background(), &pb.InvokeRequest{Class: "kind", Reserved: "agent", Op: sdk.OpLoad, ParamsJson: valid})
	if err != nil {
		t.Fatal(err)
	}
	var loaded spec.Agent
	if err := json.Unmarshal(reply.GetResultJson(), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.PromptVia != "" {
		t.Fatalf("omitted prompt_via was rewritten to %q before resolve", loaded.PromptVia)
	}
	invalid := []byte(`{"command":["claude"],"prompt_via":""}`)
	if _, err := (provider{}).Invoke(context.Background(), &pb.InvokeRequest{Class: "kind", Reserved: "agent", Op: sdk.OpLoad, ParamsJson: invalid}); err == nil {
		t.Fatal("explicit empty prompt_via passed the plugin-owned CUE ingress gate")
	}
}

func TestTTYInputIsProjectedToTypedTextAndKeys(t *testing.T) {
	got := decodeTTYInputs([]byte("hello界\x1b[A\r\x03"))
	want := []spec.TerminalInput{
		{Kind: "text", Text: "hello界"},
		{Kind: "key", Key: "up"},
		{Kind: "key", Key: "enter"},
		{Kind: "key", Key: "ctrl-c"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typed TTY input = %#v, want %#v", got, want)
	}
}

func TestDecodeQualifiedTargetsIntoGenericRoutes(t *testing.T) {
	local, err := decodeTarget("workbench")
	if err != nil {
		t.Fatal(err)
	}
	if local.Deployment != "workbench" || len(local.Hops) != 0 {
		t.Fatalf("local target = %#v", local)
	}
	remote, err := decodeTarget("agent@[::1]:2222::workbench")
	if err != nil {
		t.Fatal(err)
	}
	wantHops := []spec.TargetHop{{Transport: "ssh", Address: "[::1]", User: "agent", Port: 2222}, {Transport: "grpc"}}
	if remote.Deployment != "workbench" || !reflect.DeepEqual(remote.Hops, wantHops) {
		t.Fatalf("remote target = %#v, want deployment workbench and hops %#v", remote, wantHops)
	}
	if _, err := decodeTarget("host::"); err == nil {
		t.Fatal("empty remote deployment accepted")
	}
}

func TestSequenceGapRequestsBoundedReplay(t *testing.T) {
	gate := sdk.NewSequenceGate(1)
	inputs := make(chan *pb.ChannelFrame, 1)
	accepted, err := acceptChannelFrame(gate, &pb.ChannelFrame{Kind: sdk.ChannelStatus, Sequence: 2}, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if accepted {
		t.Fatal("out-of-order frame was accepted before replay")
	}
	request := <-inputs
	if request.GetKind() != sdk.ChannelResync || request.GetReplayFrom() != 1 {
		t.Fatalf("replay request = %#v", request)
	}
	for _, sequence := range []uint64{1, 2} {
		accepted, err := acceptChannelFrame(gate, &pb.ChannelFrame{Kind: sdk.ChannelStatus, Sequence: sequence}, inputs)
		if err != nil || !accepted {
			t.Fatalf("replayed sequence %d: accepted=%v error=%v", sequence, accepted, err)
		}
	}
}

func TestTerminalBackedAgentRunPersistsTranscriptByPublicRunID(t *testing.T) {
	store, err := agentkit.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runID := spec.UUIDv7("019f7600-0000-7000-8000-000000000011")
	inputs := make(chan *pb.ChannelFrame, 1)
	state := &agentRuntimeChannelState{
		gate: sdk.NewSequenceGate(1), inputs: inputs, record: func(string, map[string]any) error { return nil },
		store: store, runID: runID, persistTerminal: true,
	}
	if err := state.handle(&pb.ChannelFrame{Kind: sdk.ChannelTerminal, Sequence: 1, Data: []byte("agent transcript")}); err != nil {
		t.Fatal(err)
	}
	if ack := <-inputs; ack.GetAckSequence() != 1 {
		t.Fatalf("ack sequence = %d, want 1", ack.GetAckSequence())
	}
	frames, err := store.TerminalFrames(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || string(frames[0].Data) != "agent transcript" || frames[0].RunID != runID {
		t.Fatalf("terminal evidence = %#v", frames)
	}
}

func TestTerminalTransportFailureCreatesNeedsRCAIncident(t *testing.T) {
	previous := agentStateDir
	agentStateDir = t.TempDir()
	t.Cleanup(func() { agentStateDir = previous })
	runID := spec.UUIDv7("019f75b2-dfb3-71be-813c-ddc14510f4ca")
	err := recordTerminalIncident(runID, errors.New("fixture transport failure"))
	if err == nil {
		t.Fatal("terminal failure returned success")
	}
	store, openErr := agentStore()
	if openErr != nil {
		t.Fatal(openErr)
	}
	incidents, listErr := store.Incidents()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(incidents) != 1 || incidents[0].RunID != runID || incidents[0].State != "needs_rca" || len(incidents[0].EvidenceRefs) != 1 {
		t.Fatalf("incident evidence = %#v", incidents)
	}
}

func TestTerminalReconnectStartsAfterDurableCursor(t *testing.T) {
	previous := agentStateDir
	agentStateDir = t.TempDir()
	t.Cleanup(func() { agentStateDir = previous })
	runID := spec.UUIDv7("019f7600-0000-7000-8000-000000000001")
	store, err := agentStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendTerminalFrame(spec.TerminalFrame{RunID: runID, Sequence: 1, Kind: "status", Status: "detached"}); err != nil {
		t.Fatal(err)
	}
	_, _, _, open, err := terminalOpen(`{"name":"shell","entrypoint":["sh"],"cols":80,"rows":24}`, `{}`, "tmux", string(runID))
	if err != nil {
		t.Fatal(err)
	}
	if open.GetAckSequence() != 1 {
		t.Fatalf("reconnect acknowledgement cursor = %d, want 1", open.GetAckSequence())
	}
}

func TestCompatibilityFacadeStoreHonorsAgentStateEnvironment(t *testing.T) {
	previous := agentStateDir
	agentStateDir = ""
	t.Cleanup(func() { agentStateDir = previous })
	want := t.TempDir()
	t.Setenv("CHARLY_AGENT_STATE_DIR", want)
	store, err := agentStore()
	if err != nil {
		t.Fatal(err)
	}
	if store.Dir != want {
		t.Fatalf("agent store dir = %q, want environment-isolated %q", store.Dir, want)
	}
}

func TestCommandModelsPublishAgentLeavesAndTUI(t *testing.T) {
	agent, tui, tmux := commandModels()
	paths := map[string]bool{}
	for _, model := range []*spec.CLIModel{agent, tui, tmux} {
		for _, leaf := range model.Leaves {
			paths[leaf.Path] = true
		}
	}
	for _, want := range []string{"agent.runtime.list", "agent.session.new", "agent.run.start", "agent.terminal.launch", "agent.rca.complete", "agent.recover.apply", "tui", "tmux.run", "tmux.attach", "tmux.capture"} {
		if !paths[want] {
			t.Errorf("published command models missing %q", want)
		}
	}
}

func TestRecoveryActionParamsAreTypedAndActionSpecific(t *testing.T) {
	runID := spec.UUIDv7("019f75b2-dfb3-71be-813c-ddc14510f4ca")
	sessionID := spec.UUIDv7("019f75b4-36de-768a-9905-e37ff0fbb151")
	profile := &spec.TerminalProfile{Name: "shell", Entrypoint: []string{"sh"}, Cols: 80, Rows: 24}
	target := &spec.TargetSpec{}
	tests := []struct {
		name   string
		action string
		params *spec.RecoveryParams
		ok     bool
	}{
		{name: "reattach", action: "reattach", params: &spec.RecoveryParams{RunID: runID, TerminalProfile: profile}, ok: true},
		{name: "reattach missing profile", action: "reattach", params: &spec.RecoveryParams{RunID: runID}},
		{name: "resume", action: "resume", params: &spec.RecoveryParams{SessionID: sessionID}, ok: true},
		{name: "restart missing session", action: "restart", params: &spec.RecoveryParams{}},
		{name: "rebuild", action: "rebuild-target", params: &spec.RecoveryParams{Deployment: "box"}, ok: true},
		{name: "runtime missing value", action: "change-runtime", params: &spec.RecoveryParams{SessionID: sessionID}},
		{name: "reassign", action: "reassign", params: &spec.RecoveryParams{SessionID: sessionID, Target: target}, ok: true},
		{name: "abort", action: "abort", params: &spec.RecoveryParams{RunID: runID}, ok: true},
		{name: "operator", action: "operator", params: &spec.RecoveryParams{Note: "manual repair verified"}, ok: true},
		{name: "operator missing note", action: "operator", params: &spec.RecoveryParams{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRecoveryActionParams(spec.RecoveryDecision{Action: tt.action, Params: tt.params})
			if (err == nil) != tt.ok {
				t.Fatalf("validateRecoveryActionParams() error = %v, want success %v", err, tt.ok)
			}
		})
	}
}

func TestAppliedRecoveryResolvesIncidentOnlyAfterAction(t *testing.T) {
	store, incident, rca := recoveryFixture(t)
	decision := spec.RecoveryDecision{
		ID: agentkit.NewID(), IncidentID: incident.ID, RCAID: rca.ID, Action: "operator",
		Params: &spec.RecoveryParams{Note: "operator repaired and verified target"}, State: "planned",
		DecidedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	applied, err := applyRecovery(store, decision)
	if err != nil {
		t.Fatal(err)
	}
	if applied.State != "applied" || applied.AppliedAt == "" {
		t.Fatalf("applied recovery = %#v", applied)
	}
	gotIncident, err := store.Incident(incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotIncident.State != "resolved" {
		t.Fatalf("incident state = %q, want resolved", gotIncident.State)
	}
}

func TestFailedRecoveryIsDurableAndLeavesIncidentUnresolved(t *testing.T) {
	store, incident, rca := recoveryFixture(t)
	missing := agentkit.NewID()
	decision := spec.RecoveryDecision{
		ID: agentkit.NewID(), IncidentID: incident.ID, RCAID: rca.ID, Action: "change-runtime",
		Params: &spec.RecoveryParams{SessionID: missing, Runtime: "pi"}, State: "planned",
		DecidedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	failed, err := applyRecovery(store, decision)
	if err == nil {
		t.Fatal("missing recovery session unexpectedly succeeded")
	}
	if failed.State != "failed" || !strings.Contains(failed.Error, string(missing)) {
		t.Fatalf("failed recovery = %#v", failed)
	}
	stored, err := store.Recovery(decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "failed" || stored.Error == "" {
		t.Fatalf("stored recovery = %#v", stored)
	}
	gotIncident, err := store.Incident(incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotIncident.State != "awaiting_recovery" {
		t.Fatalf("incident state = %q, want awaiting_recovery", gotIncident.State)
	}
}

func recoveryFixture(t *testing.T) (*agentkit.Store, spec.Incident, spec.RCARecord) {
	t.Helper()
	store, err := agentkit.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	incident := spec.Incident{
		ID: agentkit.NewID(), State: "awaiting_recovery", Summary: "fixture failure",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	rca := spec.RCARecord{
		ID: agentkit.NewID(), IncidentID: incident.ID, State: "complete", RootCause: "fixture root cause",
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := store.PutIncident(incident); err != nil {
		t.Fatal(err)
	}
	if err := store.PutRCA(rca); err != nil {
		t.Fatal(err)
	}
	return store, incident, rca
}
