package main

import "testing"

// loader_threaded_declared_fields_test.go — the parse-guard FEED contract (Cutover C task 0,
// finding #1): loaderThreaded() must populate spec.Threaded.StructuralDeclaredFields from each
// structural kind's REGISTERED input schema (the process-wide compiled plugin schema set), never
// a hand-maintained word list. The sdk/loaderkit in-body member scan consults it to keep a
// structural body's DECLARED fields as data even when a field name collides with a kind word —
// the group bed's `iterate:` carrying `agent:` (box/arch corpus regression, RCA'd live).
// A kind word absent from the map falls back to every kind-word key being a member — the
// documented no-declared-schema fallback — so an UNPOPULATED map silently re-opens the
// collision class: this test pins the feed, not just the fallback.
func TestLoaderThreaded_StructuralDeclaredFields(t *testing.T) {
	threaded := loaderThreaded()
	fields, ok := threaded.StructuralDeclaredFields["group"]
	if !ok {
		t.Fatal("group declares the #GroupInput input schema but loaderThreaded threaded no declared fields — the parse guard's parent-disc channel is inert for group")
	}
	// The declared fields the corpus collision needs: `iterate` (the AI-benchmark harness
	// block whose body carries the kind word `agent`) plus the identity/lifecycle fields.
	for _, want := range []string{"description", "disposable", "lifecycle", "iterate"} {
		if !fields[want] {
			t.Errorf("group declared fields missing %q", want)
		}
	}
	if fields["web"] || fields["cache"] {
		t.Error("group declared fields must be SCHEMA fields only — authored member names must never leak into the declared set")
	}
}
