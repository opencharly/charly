package substratekind

// status_project_memo.go — ONE resolved-project resolution per status fan-out.
//
// collectFlat reaches five substrate words, and TWO of them (kubernetes, android) need the
// resolved-project envelope to enumerate their deploy nodes. Each was calling
// fetchResolvedProject independently, so a single `charly status` paid for the full
// LoadUnified -> Walk -> Materialize pass TWICE — measured at ~3.4s per resolution on this
// repo's own project, against a probe whose never-hang ceiling is 2 minutes. Under a
// concurrent bed roster that margin is what fails: the check-pod bed's `charly status --json`
// probe was killed at exactly 2m0.003s (run 2026.231.0020) while the same probe passed at 88s
// on an idle host.
//
// The memo is scoped to ONE fan-out, deliberately, and NOT to the process. A process-lifetime
// cache would be wrong for any long-lived host (`charly mcp serve` and the plugin servers keep
// a provider alive across requests) — it would serve a project resolved before the user's last
// edit. A context-scoped memo cannot: the context dies with the fan-out that made it.
//
// A single-word OpStatusCollect arriving over the wire carries no memo and resolves directly,
// which is the correct behaviour rather than a fallback — there is no second consumer to share
// with, so there is nothing to amortise.
//
// This does NOT close #208. Two of the three resolutions per `charly status` are removed here;
// the third lives in candy/plugin-status and cannot be shared from this package without a home
// in the sdk, because the kernel/plugin boundary law forbids one plugin importing another.

import (
	"context"
	"sync"

	"github.com/opencharly/spec/spec"
)

// resolvedProjectMemoKey is the unexported context key for one fan-out's memo.
type resolvedProjectMemoKey struct{}

// resolvedProjectMemo holds one lazily-resolved envelope plus the error it failed with, so a
// failure is shared exactly like a success and the expensive call is never retried within one
// fan-out.
type resolvedProjectMemo struct {
	once sync.Once
	rp   *spec.ResolvedProject
	err  error
}

// withResolvedProjectMemo scopes a resolved-project memo to ctx. Call it ONCE per status
// fan-out; every fetchResolvedProject under that ctx then shares a single resolution.
func withResolvedProjectMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, resolvedProjectMemoKey{}, &resolvedProjectMemo{})
}

// memoFromContext returns the fan-out's memo, or nil when ctx carries none.
func memoFromContext(ctx context.Context) *resolvedProjectMemo {
	m, _ := ctx.Value(resolvedProjectMemoKey{}).(*resolvedProjectMemo)
	return m
}
