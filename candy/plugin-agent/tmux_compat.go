package agentkind

// This file preserves the operator-facing `charly tmux` grammar while routing
// every operation through the generic terminal:tmux Provider.Channel contract.
// It intentionally contains no tmux command execution or transport logic.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opencharly/sdk/agentkit"
	"github.com/opencharly/sdk/spec"
)

// Compat-session CREATION geometry. A detached compat session is created with
// no attached TTY to inherit a size from, so the facade uses one fixed
// default — the same 120x40 the candy-authored agent terminal profiles pin
// (candy/claude-code, candy/codex, candy/gemini). It is only the creation
// size: a later attach reconciles the profile to the live window
// (TestTmuxReattachUsesLiveWindowSize) and runtime resize rides typed resize
// frames.
const (
	tmuxCompatCols = 120
	tmuxCompatRows = 40
)

type TmuxCompatCmd struct {
	Attach  TmuxCompatAttachCmd  `cmd:"" help:"Attach to a typed tmux terminal (interactive)"`
	Capture TmuxCompatCaptureCmd `cmd:"" help:"Read the structured terminal snapshot or transcript"`
	Cmd     TmuxCompatCommandCmd `cmd:"" help:"Send a literal command and Enter"`
	Kill    TmuxCompatKillCmd    `cmd:"" help:"Close a run-owned tmux terminal"`
	List    TmuxCompatListCmd    `cmd:"" help:"List compatibility tmux sessions"`
	Run     TmuxCompatRunCmd     `cmd:"" help:"Start a command in a detached typed terminal"`
	Send    TmuxCompatSendCmd    `cmd:"" help:"Send literal text or canonical keys"`
	Shell   TmuxCompatShellCmd   `cmd:"" help:"Create or reattach a persistent typed shell"`
}

type tmuxCompatTarget struct {
	Box      string `arg:"" help:"Deployment or host::deployment target"`
	Session  string `short:"s" long:"session" required:"" help:"Compatibility session name"`
	Instance string `short:"i" long:"instance" help:"Deployment instance"`
}

type TmuxCompatRunCmd struct {
	Box      string `arg:"" help:"Deployment or host::deployment target"`
	Command  string `arg:"" help:"Shell command to run"`
	Session  string `short:"s" long:"session" required:"" help:"Compatibility session name"`
	Instance string `short:"i" long:"instance" help:"Deployment instance"`
}

func (c *TmuxCompatRunCmd) Run() error {
	target, err := compatTarget(c.Box, c.Instance)
	if err != nil {
		return err
	}
	if _, found, err := findCompatSession(c.Session, target); err != nil {
		return err
	} else if found {
		return fmt.Errorf("tmux compatibility session %q already exists", c.Session)
	}
	profile := spec.TerminalProfile{
		Name: "tmux-compat-" + c.Session, Entrypoint: []string{"sh", "-lc", c.Command},
		Cols: tmuxCompatCols, Rows: tmuxCompatRows, Persistence: "detach", Transcript: "both",
	}
	session, err := createCompatSession(c.Session, target, profile)
	if err != nil {
		return err
	}
	return runCompatTerminal("launch", session, "", "")
}

type TmuxCompatShellCmd tmuxCompatTarget

func (c *TmuxCompatShellCmd) Run() error {
	target, err := compatTarget(c.Box, c.Instance)
	if err != nil {
		return err
	}
	session, found, err := findCompatSession(c.Session, target)
	if err != nil {
		return err
	}
	if !found {
		profile := spec.TerminalProfile{Name: "tmux-compat-" + c.Session, Entrypoint: []string{"sh"}, Cols: tmuxCompatCols, Rows: tmuxCompatRows, Persistence: "detach", Transcript: "both"}
		session, err = createCompatSession(c.Session, target, profile)
		if err != nil {
			return err
		}
	}
	return runCompatTerminal("attach", session, "", "")
}

type TmuxCompatAttachCmd tmuxCompatTarget

func (c *TmuxCompatAttachCmd) Run() error {
	session, err := requireCompatSession(c.Session, c.Box, c.Instance)
	if err != nil {
		return err
	}
	return runCompatTerminal("attach", session, "", "")
}

type TmuxCompatCaptureCmd struct {
	tmuxCompatTarget
	Lines int `short:"n" long:"lines" default:"0" help:"When positive, return the durable ordered transcript"`
}

func (c *TmuxCompatCaptureCmd) Run() error {
	session, err := requireCompatSession(c.Session, c.Box, c.Instance)
	if err != nil {
		return err
	}
	if c.Lines > 0 {
		return (&AgentTerminalTranscriptCmd{RunID: string(session.ID)}).Run()
	}
	return runCompatTerminal("snapshot", session, "", "")
}

type TmuxCompatCommandCmd struct {
	tmuxCompatTarget
	Command string `arg:"" help:"Literal command text"`
	// Notify is parsed for grammar compatibility with the former facade and is
	// intentionally INERT either way: the typed terminal channel reports
	// command progress as channel events (status/snapshot/settled), so there
	// is no notify side-channel left to drive. The flag must stay — removing
	// it would break existing invocations at kong parse time.
	Notify bool `long:"notify" negatable:"" default:"true" help:"Accepted for compatibility; no effect (completion arrives as channel events)"`
}

func (c *TmuxCompatCommandCmd) Run() error {
	session, err := requireCompatSession(c.Session, c.Box, c.Instance)
	if err != nil {
		return err
	}
	return runCompatTerminal("command", session, c.Command, "")
}

type TmuxCompatSendCmd struct {
	tmuxCompatTarget
	Keys    []string `arg:"" help:"Literal fragments or canonical key names"`
	Literal bool     `short:"l" long:"literal" help:"Send arguments as literal text"`
	Enter   bool     `long:"enter" help:"Append the canonical Enter key"`
}

func (c *TmuxCompatSendCmd) Run() error {
	session, err := requireCompatSession(c.Session, c.Box, c.Instance)
	if err != nil {
		return err
	}
	if c.Literal {
		if err := runCompatTerminal("input", session, strings.Join(c.Keys, ""), ""); err != nil {
			return err
		}
	} else {
		for _, key := range c.Keys {
			if err := runCompatTerminal("key", session, "", key); err != nil {
				return err
			}
		}
	}
	if c.Enter {
		return runCompatTerminal("key", session, "", "enter")
	}
	return nil
}

type TmuxCompatKillCmd tmuxCompatTarget

func (c *TmuxCompatKillCmd) Run() error {
	session, err := requireCompatSession(c.Session, c.Box, c.Instance)
	if err != nil {
		return err
	}
	if err := runCompatTerminal("close", session, "", ""); err != nil {
		return err
	}
	store, err := agentStore()
	if err != nil {
		return err
	}
	session.State = "closed"
	session.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return store.PutSession(session)
}

type TmuxCompatListCmd struct {
	Box      string `arg:"" help:"Deployment or host::deployment target"`
	Instance string `short:"i" long:"instance" help:"Deployment instance"`
}

func (c *TmuxCompatListCmd) Run() error {
	target, err := compatTarget(c.Box, c.Instance)
	if err != nil {
		return err
	}
	store, err := agentStore()
	if err != nil {
		return err
	}
	sessions, err := store.Sessions()
	if err != nil {
		return err
	}
	out := make([]spec.AgentSession, 0)
	for _, session := range sessions {
		if isCompatSession(session) && session.State != "closed" && sameCompatTarget(session.Target, target) {
			out = append(out, session)
		}
	}
	return writeJSON(out)
}

func compatTarget(box, instance string) (spec.TargetSpec, error) {
	target, err := decodeTarget(box)
	if err != nil {
		return target, err
	}
	target.Instance = instance
	return target, validateGenerated("#TargetSpec", target)
}

func createCompatSession(name string, target spec.TargetSpec, profile spec.TerminalProfile) (spec.AgentSession, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	session := spec.AgentSession{
		ID: agentkit.NewID(), Runtime: "tmux", Target: target, State: "new",
		CreatedAt: now, UpdatedAt: now, TerminalProfile: &profile,
		Metadata: spec.StrMap{"tmux_compat_session": name},
	}
	if err := validateGenerated("#AgentSession", session); err != nil {
		return session, err
	}
	store, err := agentStore()
	if err != nil {
		return session, err
	}
	return session, store.PutSession(session)
}

func findCompatSession(name string, target spec.TargetSpec) (spec.AgentSession, bool, error) {
	store, err := agentStore()
	if err != nil {
		return spec.AgentSession{}, false, err
	}
	sessions, err := store.Sessions()
	if err != nil {
		return spec.AgentSession{}, false, err
	}
	for i := len(sessions) - 1; i >= 0; i-- {
		session := sessions[i]
		if session.Metadata["tmux_compat_session"] == name && session.State != "closed" && sameCompatTarget(session.Target, target) {
			return session, true, nil
		}
	}
	return spec.AgentSession{}, false, nil
}

func requireCompatSession(name, box, instance string) (spec.AgentSession, error) {
	target, err := compatTarget(box, instance)
	if err != nil {
		return spec.AgentSession{}, err
	}
	session, found, err := findCompatSession(name, target)
	if err != nil {
		return session, err
	}
	if !found {
		return session, fmt.Errorf("tmux compatibility session %q not found for %s", name, box)
	}
	return session, nil
}

func isCompatSession(session spec.AgentSession) bool {
	return session.Runtime == "tmux" && session.Metadata["tmux_compat_session"] != "" && session.TerminalProfile != nil
}

func sameCompatTarget(a, b spec.TargetSpec) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func runCompatTerminal(operation string, session spec.AgentSession, text, key string) error {
	if session.TerminalProfile == nil {
		return errors.New("tmux compatibility session has no terminal profile")
	}
	profile, err := json.Marshal(session.TerminalProfile)
	if err != nil {
		return err
	}
	target, err := json.Marshal(session.Target)
	if err != nil {
		return err
	}
	runID := string(session.ID)
	switch operation {
	case "launch":
		return executeTerminal(string(profile), string(target), "tmux", runID, terminalDetach)
	case "attach":
		return executeTerminal(string(profile), string(target), "tmux", runID, terminalAttach)
	case "snapshot":
		return executeTerminalSnapshot(string(profile), string(target), "tmux", runID)
	case "input":
		return executeTerminalControl(string(profile), string(target), "tmux", runID, spec.TerminalInput{Kind: "text", Text: text})
	case "command":
		return executeTerminalControl(string(profile), string(target), "tmux", runID, spec.TerminalInput{Kind: "command", Text: text})
	case "key":
		return executeTerminalControl(string(profile), string(target), "tmux", runID, spec.TerminalInput{Kind: "key", Key: key})
	case "close":
		return executeTerminalControl(string(profile), string(target), "tmux", runID, spec.TerminalInput{Kind: "close"})
	default:
		return fmt.Errorf("unsupported tmux compatibility operation %q", operation)
	}
}
