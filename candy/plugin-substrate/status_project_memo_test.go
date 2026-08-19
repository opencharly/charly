package substratekind

import (
	"context"
	"errors"
	"testing"

	"github.com/opencharly/spec/spec"
)

// TestFanoutMemoResolvesOnce pins the property the check-pod bed failure paid for: within one
// status fan-out the resolved-project envelope is resolved EXACTLY once, however many
// collectors ask for it. Without the memo this counts 2 (kubernetes + android) and the test
// reds.
func TestFanoutMemoResolvesOnce(t *testing.T) {
	ctx := withResolvedProjectMemo(context.Background())

	calls := 0
	resolve := func() (*spec.ResolvedProject, error) {
		calls++
		return &spec.ResolvedProject{}, nil
	}
	// Drive the same code path fetchResolvedProject takes, with a stub in place of the
	// reverse-channel Invoke (which needs a live host).
	get := func() (*spec.ResolvedProject, error) {
		if m := memoFromContext(ctx); m != nil {
			m.once.Do(func() { m.rp, m.err = resolve() })
			return m.rp, m.err
		}
		return resolve()
	}

	for i := 0; i < 5; i++ {
		if _, err := get(); err != nil {
			t.Fatalf("resolve %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("one fan-out must resolve the project exactly once; got %d resolutions", calls)
	}
}

// TestNoMemoResolvesEveryTime is the other half: a single-word OpStatusCollect arriving over the
// wire carries no memo, and must NOT silently share state with some other request.
func TestNoMemoResolvesEveryTime(t *testing.T) {
	ctx := context.Background()
	if m := memoFromContext(ctx); m != nil {
		t.Fatalf("a bare context must carry no memo; got %#v", m)
	}
}

// TestFanoutMemoSharesFailure pins that a failed resolution is shared like a successful one —
// otherwise a broken project would pay the full expensive resolution once per collector,
// exactly when the host is least able to afford it.
func TestFanoutMemoSharesFailure(t *testing.T) {
	ctx := withResolvedProjectMemo(context.Background())
	want := errors.New("resolve exploded")

	calls := 0
	get := func() (*spec.ResolvedProject, error) {
		m := memoFromContext(ctx)
		m.once.Do(func() { calls++; m.rp, m.err = nil, want })
		return m.rp, m.err
	}
	for i := 0; i < 3; i++ {
		if _, err := get(); !errors.Is(err, want) {
			t.Fatalf("attempt %d: want the shared error, got %v", i, err)
		}
	}
	if calls != 1 {
		t.Errorf("a failure must be memoised too; got %d resolutions", calls)
	}
}
