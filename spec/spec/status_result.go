package spec

// status_result.go — the check-engine's pass/fail/skip verdict enum (FLOOR-SLIM Unit 4,
// moved from sdk/kit as part of the CheckResult wire-envelope split). HAND-WRITTEN — a
// distinct named int type with the iota consts + String() method, NOT emitted by
// `task cue:gen` (gengotypes has no construct for an iota-based enum + a Stringer method;
// CUE owns the wire VALUE SET via #CheckResult.status's plain `int` shape, referenced here
// via @go(Status,type=Status) — this file supplies the named Go type that reference
// points at). Mirrors the #SubstrateKind split in status_types.go (a string-backed enum
// suppressed via @go(-) with its Go type hand-written here-style) — Status is int-backed,
// so there is no separate disjunction def to suppress; the CUE field is a plain `int`.
type Status int

const (
	StatusPass Status = iota
	StatusFail
	StatusSkip
)

// String renders the lowercase verdict word. Reporters uppercase it for display
// (strings.ToUpper → "PASS"/"FAIL"/"SKIP").
func (s Status) String() string {
	switch s {
	case StatusPass:
		return "pass"
	case StatusFail:
		return "fail"
	case StatusSkip:
		return "skip"
	}
	return "unknown"
}
