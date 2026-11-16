package main

// audit_event.go — the ONE append path for §20.1's audit record (D-AUDIT-1..5), and the
// wiring side of P06 leg 3a: charly's own mutations append exactly one record each.
//
// THE SHAPE IS THE CONTRACT'S, NOT THIS FILE'S. Every field below mirrors DESIGN.cue's
// #AuditEvent / #AuditDetail (DESIGN.cue:1146-1172) one-for-one, including the two closed
// word lists (`_auditEvent`: mutate|authorize|admit|activate|teardown, `_auditOutcome`:
// ok|refused|failed, `_detailCode`: reason|path|field|ref|count|duration) and the three
// scalar patterns (#Ref for identity/target, #Timestamp for at, #Redacted for a value
// that must be named but never held).
//
// RECONCILIATION (stated here because it is a landing prerequisite, not a detail): the
// contract publishes NO Go type for #AuditEvent at the pinned spec tag, so this file
// carries a hand-written mirror. When opencharly/sdk's writer (P06 leg 3b) lands, the
// mirror is deleted in favour of the generated spec type, `AuditAppender` remains the
// single seam core depends on, and `stateAuditAppender` is replaced by the sdk
// implementation — no call site below changes, because each one reaches the seam through
// `appendAuditEvent` only.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/opencharly/spec/spec"
	"github.com/opencharly/spec/storeroot"
)

// The closed vocabularies, from DESIGN.cue's _auditEvent/_auditOutcome.
const (
	auditEventMutate    = "mutate"
	auditEventAuthorize = "authorize"
	auditEventAdmit     = "admit"
	auditEventActivate  = "activate"
	auditEventTeardown  = "teardown"

	auditOutcomeOK      = "ok"
	auditOutcomeRefused = "refused"
	auditOutcomeFailed  = "failed"

	auditDetailReason = "reason"
)

// AuditEvent mirrors spec #AuditEvent (DESIGN.cue:1153).
type AuditEvent struct {
	At        string        `json:"at"`               // #Timestamp
	Event     string        `json:"event"`            // or(_auditEventWord)
	Outcome   string        `json:"outcome"`          // or(_auditOutcomeWord)
	Identity  string        `json:"identity"`         // #Ref — who acted (D-AUDIT-2)
	Target    string        `json:"target,omitempty"` // #Ref — what was acted on
	Action    string        `json:"action,omitempty"` // #Word — a charly verb (D-AUDIT-5)
	Namespace string        `json:"namespace,omitempty"`
	Revision  string        `json:"revision,omitempty"`
	Detail    []AuditDetail `json:"detail,omitempty"` // ≤32 (list.MaxItems(32))
}

// AuditDetail mirrors #AuditDetail (DESIGN.cue:1164).
type AuditDetail struct {
	Code  string         `json:"code"` // or(_detailCodeWord)
	Text  string         `json:"text"` // #Text (non-empty)
	Value *AuditRedacted `json:"value,omitempty"`
}

// AuditRedacted is #Redacted: a record says a value was there without holding it
// (D-AUDIT-3 — an audit that holds a secret is a leak with a timestamp).
type AuditRedacted struct {
	Redacted bool `json:"redacted"`
}

// AuditAppender appends exactly one record and never rewrites or deletes one (D-AUDIT-4).
//
// This is the NARROW INTERFACE core codes against: P06 leg 3b (opencharly/sdk) owns the
// implementation, core owns the call sites. `Append` must be safe to call from concurrent
// processes (D-CONC-1) and must never write a record twice.
type AuditAppender interface {
	Append(AuditEvent) error
}

// stateAuditAppender is the core-side implementation the wiring needs to be testable and
// real before 3b lands: ONE append-only JSON-Lines file under the state root
// (storeroot.RootState — D-DEP-5's durable tree, which `cache clear` must not touch).
//
// It carries NO state between calls (the path resolves per call), so it is not the
// package-level singleton D-CORE-3 forbids.
type stateAuditAppender struct{}

// auditRecordFile is the record's ONE name under the state root.
const auditRecordFile = "audit.jsonl"

// Append writes one record as one line.
//
// Concurrency (D-CONC-1): the file is opened O_APPEND and each record is written by a
// SINGLE Write call, so the kernel serializes the offset update on the inode — two
// processes appending concurrently cannot interleave within a record. A partially
// written last line (a crash) is visible and is the next reader's business, not a
// mutation of an existing record (D-AUDIT-4).
func (stateAuditAppender) Append(ev AuditEvent) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("audit: encode record: %w", err)
	}
	path, err := auditRecordPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("audit: create state root: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("audit: open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("audit: append %s: %w", path, err)
	}
	return nil
}

// auditRecordPath resolves the record file through the ONE roots resolver — never a
// hand-built path (the storeroot package's own rule).
func auditRecordPath() (string, error) {
	dir, err := storeroot.Dir(storeroot.RootState)
	if err != nil {
		return "", fmt.Errorf("audit: resolve state root: %w", err)
	}
	return filepath.Join(dir, auditRecordFile), nil
}

// refPattern is #Ref's pattern (DESIGN.cue:51). A record's identity and target are
// normalized to it before they are written, because a record that the contract's own
// schema would reject is not a record.
var refPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*(\.[a-z][a-z0-9]*(-[a-z0-9]+)*)*$`)

// auditRef normalizes an arbitrary identifier into a #Ref: lowercase, every run of
// characters outside [a-z0-9-] collapsed to a single hyphen, and a SEGMENT SEPARATOR
// ('/', ':' or '.') kept as the '.' a namespaced #Ref uses — which is how a deploy key's
// `box/instance` (spec.DeployKey) becomes `box.instance`. A leading non-letter is dropped,
// a trailing separator trimmed, and an empty result becomes "unknown" so #Ref stays
// satisfiable and the record still says something true.
func auditRef(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastSep := ""
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastSep = ""
		case r == '/' || r == ':' || r == '.':
			if b.Len() > 0 && lastSep != "." {
				b.WriteRune('.')
				lastSep = "."
			}
		default:
			if b.Len() > 0 && lastSep != "-" && lastSep != "." {
				b.WriteRune('-')
				lastSep = "-"
			}
		}
	}
	out := strings.Trim(b.String(), "-.")
	out = strings.TrimLeft(out, "0123456789-.")
	if out == "" || !refPattern.MatchString(out) {
		return "unknown"
	}
	return out
}

// auditIdentity answers D-AUDIT-2's "who acted" for an act core itself performs: the OS
// account that ran this process, qualified by the host, as a #Ref.
func auditIdentity() string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	if name == "" {
		name = os.Getenv("USER")
	}
	if name == "" {
		name = "unknown"
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return auditRef(name)
	}
	return auditRef(name + "." + host)
}

// appendAuditEvent is the ONE way a mutation records itself: it fills the record's
// derived fields (at, identity) and hands it to the appender.
//
// Failure policy (D-LAW-5: fail loud, no silent loss): the caller decides how to report
// a lost record, but it is never swallowed here — the error is returned to be surfaced.
func appendAuditEvent(ev AuditEvent) error {
	if ev.At == "" {
		ev.At = time.Now().UTC().Format(time.RFC3339)
	}
	if ev.Identity == "" {
		ev.Identity = auditIdentity()
	}
	if ev.Event == "" || ev.Outcome == "" {
		return fmt.Errorf("audit: record needs an event and an outcome (event=%q outcome=%q)", ev.Event, ev.Outcome)
	}
	return (stateAuditAppender{}).Append(ev)
}

// auditOutcome maps an act's error to the closed outcome word.
func auditOutcome(err error) string {
	if err != nil {
		return auditOutcomeFailed
	}
	return auditOutcomeOK
}

// lifecycleAuditEvent maps a lifecycle op to its record shape, and says whether the op is
// a MUTATION that must be recorded at all.
//
// The three state transitions are recorded; `shell`, `logs`, `service` and `cmd` are
// interactions with a running venue whose payload is an opaque argv, so core cannot
// classify them as a mutation and does not invent one (see the P06 leg 3a report for the
// named omissions; `remove` never reaches this seam — candy/plugin-pod owns that
// orchestration and appends its own record).
func lifecycleAuditEvent(op string) (event string, mutation bool) {
	switch op {
	case "start":
		return auditEventActivate, true
	case "stop", "update":
		return auditEventMutate, true
	}
	return "", false
}

// recordLifecycleAct appends the ONE record for a lifecycle act, when the op is a mutation.
//
// The record names the failure, never the payload: an error message from a deploy can echo
// an environment value, so no free-form error text reaches the record (D-AUDIT-3 — an audit
// that holds a secret is a leak with a timestamp). The operator reads the message from the
// command's own error output.
func recordLifecycleAct(op, box, instance string, actErr error) error {
	event, mutation := lifecycleAuditEvent(op)
	if !mutation {
		return nil
	}
	return appendAuditEvent(AuditEvent{
		Event:   event,
		Outcome: auditOutcome(actErr),
		Target:  auditRef(spec.DeployKey(box, instance)),
		Action:  op,
	})
}

// recordDeployDelAct appends the ONE teardown record for a `deploy del` act (D-AUDIT-1).
func recordDeployDelAct(req spec.DeployNodeDelDispatchRequest, actErr error) error {
	return appendAuditEvent(AuditEvent{
		Event:   auditEventTeardown,
		Outcome: auditOutcome(actErr),
		Target:  auditRef(req.Name),
		Action:  "del",
	})
}
