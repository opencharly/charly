package main

import "github.com/opencharly/spec/spec"

// checkResultFromVerb is the ONE conversion from a check verb's verdict to the result the
// plan run carries and the ledger records.
//
// A host-coupled verb returns spec.CheckVerbResult — its three-field verdict: Status,
// Message, and the optional CapturedValue the verb stashed while making its judgement.
// (kit.Result, which a compiled-in candy's RunVerb is written against, is a plain alias for
// this type: sdk/kit/kit.go.) charly's own spec.CheckResult carries the Op, the resolved Verb
// word and the timing fields on top of those three. This function is the single place that
// maps one to the other, so a field carried by the verdict cannot be silently dropped by one
// of the dispatch paths that build a CheckResult from a verb result. Before this function
// existed, four such paths each hand-built the literal and each dropped CapturedValue — the
// producer's value reached the ledger as the empty string on every one of them. (R3: one
// declaration per behaviour; R2: the siblings are fixed together, not one at a time.)
//
// CapturedValue is copied unconditionally, and this conversion states no policy of its own
// about when a capture is recorded: leaving it empty for a verdict that captured nothing is
// the PRODUCER's decision (a FAIL/SKIP arm in candy/plugin-command leaves it empty on purpose —
// a failed step's output was never vouched for). Whatever the verb stashed rides through, and
// the `omitempty` tag on spec.CheckResult keeps a captureless result byte-identical to the
// pre-extension wire.
//
// The caller passes `verb` rather than this function deriving it, because the word depends on
// the dispatch path and not on the verdict: an in-proc verb walks under its Reserved() word,
// while the generic `plugin:` fall-through reports "plugin" (provider_checkenv.go). Op and the
// timing fields (Elapsed/Attempts/TotalElapsed) are likewise the caller's to fill — the timing
// is measured around the verb call by the dispatch path, not inside the conversion.
func checkResultFromVerb(op *spec.Op, verb string, r spec.CheckVerbResult) spec.CheckResult {
	return spec.CheckResult{
		Op:            op,
		Verb:          verb,
		Status:        r.Status,
		Message:       r.Message,
		CapturedValue: r.CapturedValue,
	}
}
