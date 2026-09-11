package main

// plugin_connect_deadline_test.go — the charly#588 gates.
//
// The defect was an UNBOUNDED wait, so the tests are written to fail on the wait, not merely on an
// error string: a plugin that never becomes ready must return a NAMED error within the bound, and
// the end-to-end fixture is the EXACT shape the issue reproduced — a plugin child that completes
// the go-plugin handshake and then never answers Describe, where the pre-fix call was measured
// still parked after 60s (TestConnectPluginReady_BoundsAStuckDescribe).

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/transport"
)

// stuckPluginChildEnv makes the test binary re-exec itself as the stuck plugin child.
const stuckPluginChildEnv = "CHARLY_TEST_STUCK_PLUGIN_CHILD"

// failingTransport is a plugin connect that fails before any wire work.
type failingTransport struct {
	closer *reapTrackingCloser
	err    error
}

func (f *failingTransport) Connect(context.Context) (*PluginUnit, io.Closer, error) {
	if f.closer == nil {
		return nil, nil, f.err // untyped nil, exactly what a failing transport returns — never a typed nil
	}
	return nil, f.closer, f.err
}

// immediateTransport is a plugin that is ready at once.
type immediateTransport struct {
	unit   *PluginUnit
	closer io.Closer
}

func (i *immediateTransport) Connect(context.Context) (*PluginUnit, io.Closer, error) {
	return i.unit, i.closer, nil
}

// TestLocalTransport_ReadyTimeoutDefaultsToTheReadyBound guards the default: a transport built by
// the loader (no explicit ReadyTimeout) must actually carry the charly#588 readiness bound, not
// zero — a zero would hand the readiness read an unbounded context and restore the hang.
func TestLocalTransport_ReadyTimeoutDefaultsToTheReadyBound(t *testing.T) {
	if pluginReadyTimeout <= 0 {
		t.Fatal("pluginReadyTimeout is not positive — the readiness read would be unbounded")
	}
	if got := (&LocalTransport{BinPath: "/bin/true"}).readyTimeout(); got != pluginReadyTimeout {
		t.Errorf("default readyTimeout = %s, want the package bound %s", got, pluginReadyTimeout)
	}
	override := 3 * time.Second
	if got := (&LocalTransport{BinPath: "/bin/true", ReadyTimeout: override}).readyTimeout(); got != override {
		t.Errorf("explicit readyTimeout = %s, want %s (the regression tests drive the real path with it)", got, override)
	}
}

// TestConnectPluginReady_BoundsAStuckDescribe is THE charly#588 regression. The fixture child
// completes the go-plugin handshake — so go-plugin's own StartTimeout is satisfied — and then never
// answers Describe, which is precisely the state the issue found: host parked, child alive and idle,
// no error, no summary.yml. Before the fix this call was still parked after 60s; now it fails FAST,
// naming the plugin, its source, the phase and the bound.
func TestConnectPluginReady_BoundsAStuckDescribe(t *testing.T) {
	t.Setenv(stuckPluginChildEnv, "stuck-describe")
	const bound = 5 * time.Second
	start := time.Now()
	unit, closer, err := connectPluginReady(
		&LocalTransport{
			BinPath:      os.Args[0],
			Args:         []string{"-test.run=TestConnectDeadlineHelperStuckPluginChild"},
			ReadyTimeout: bound,
		},
		"plugin-adb", "github.com/opencharly/plugin-adb")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("connect of a plugin that never answers Describe returned (unit=%v closer=%v) — charly#588", unit != nil, closer != nil)
	}
	if unit != nil || closer != nil {
		t.Errorf("failed connect returned unit=%v closer=%v; want nils so nothing leaks into the registry", unit != nil, closer != nil)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error %v is not a deadline error — the caller must be able to tell a bound from a refusal", err)
	}
	for _, want := range []string{
		"plugin-adb",
		"did not become ready within 5s", // the bound that ACTUALLY applied, not the package default
		"describe phase",
		"github.com/opencharly/plugin-adb",
		"readiness read timed out after 5s",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q — the next occurrence would not be diagnosable from the log alone", err, want)
		}
	}
	if elapsed > 60*time.Second {
		t.Errorf("the bound took %s to return; want ~5s (the wait it replaces is unbounded)", elapsed)
	}
}

// TestConnectPluginReady_HonorsACallerCancellation proves the transport bound COMPOSES with the
// caller's context instead of replacing it: a caller that cancels must abort the readiness read
// immediately, and the bound must not then claim the timeout as its own.
func TestConnectPluginReady_HonorsACallerCancellation(t *testing.T) {
	t.Setenv(stuckPluginChildEnv, "stuck-describe")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	defer cancel()
	start := time.Now()
	_, _, err := (&LocalTransport{
		BinPath:      os.Args[0],
		Args:         []string{"-test.run=TestConnectDeadlineHelperStuckPluginChild"},
		ReadyTimeout: 30 * time.Second, // far longer than the cancellation, so only the cancel can end it
	}).Connect(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("a cancelled connect returned no error")
	}
	if elapsed > 10*time.Second {
		t.Errorf("cancellation took %s to abort the readiness read; the caller's context must propagate", elapsed)
	}
	if strings.Contains(err.Error(), "did not become ready within") {
		t.Errorf("error %q claims the readiness bound although the CALLER cancelled the read", err)
	}
}

// TestConnectPluginReady_NamesThePluginAndSourceOnAnyFailure proves the diagnosability half applies
// to EVERY connect failure, not only the deadline one: the phase that failed comes through with the
// plugin and its source.
func TestConnectPluginReady_NamesThePluginAndSourceOnAnyFailure(t *testing.T) {
	_, _, err := connectPluginReady(&failingTransport{err: errors.New("handshake refused")}, "plugin-adb", "github.com/opencharly/plugin-adb")
	if err == nil {
		t.Fatal("a failing transport produced no error")
	}
	for _, want := range []string{"plugin-adb", "github.com/opencharly/plugin-adb", "phase", "handshake refused"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("error %q claims a bound it never hit", err)
	}
}

// TestConnectPluginReady_ClosesTheCloserOfAFailedConnect guards the leak: a transport that fails
// while already holding a closer must have it closed by the seam (otherwise the child outlives the
// failure with nothing pointing at it).
func TestConnectPluginReady_ClosesTheCloserOfAFailedConnect(t *testing.T) {
	closer := &reapTrackingCloser{}
	if _, _, err := connectPluginReady(&failingTransport{closer: closer, err: errors.New("boom")}, "plugin-adb", "src"); err == nil {
		t.Fatal("a failing transport produced no error")
	}
	if closer.closes != 1 {
		t.Errorf("closer closed %d times; want exactly 1 — a failed connect must not leak its child", closer.closes)
	}
}

// TestConnectPluginReady_ReadyReturnsPromptlyWithItsCloser guards the happy path: a healthy connect
// must not be delayed by the bound and must hand back the closer that reaps the child.
func TestConnectPluginReady_ReadyReturnsPromptlyWithItsCloser(t *testing.T) {
	closer := &reapTrackingCloser{}
	start := time.Now()
	unit, gotCloser, err := connectPluginReady(&immediateTransport{unit: &PluginUnit{}, closer: closer}, "plugin-adb", "src")
	if err != nil {
		t.Fatalf("healthy connect error = %v", err)
	}
	if unit == nil || gotCloser != closer {
		t.Fatalf("healthy connect returned unit=%v closer=%v; want the transport's own", unit != nil, gotCloser)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("a ready plugin took %s — the bound must not delay a connect that answers", elapsed)
	}
	if closer.closes != 0 {
		t.Errorf("the successful connect closed its closer %d times; the registry owns it now", closer.closes)
	}
}

// TestBoundedTeardown_ForcesAfterGrace proves a teardown that never returns cannot itself hang the
// failure path: once the grace elapses the force reap runs and the helper returns.
func TestBoundedTeardown_ForcesAfterGrace(t *testing.T) {
	release := make(chan struct{})
	killEntered := make(chan struct{})
	forced := make(chan struct{})
	start := time.Now()
	boundedTeardown(func() { close(killEntered); <-release }, func() { close(forced) }, 100*time.Millisecond)
	elapsed := time.Since(start)
	<-killEntered
	select {
	case <-forced:
	default:
		t.Fatal("the force reap did not run although the graceful teardown never returned")
	}
	if elapsed > 5*time.Second {
		t.Errorf("boundedTeardown took %s for a 100ms grace", elapsed)
	}
	close(release)
}

// TestBoundedTeardown_GracefulTeardownDoesNotForce guards the other half: a teardown that returns
// inside the grace keeps its semantics — no child is SIGKILLed needlessly.
func TestBoundedTeardown_GracefulTeardownDoesNotForce(t *testing.T) {
	killed := make(chan struct{})
	var forced bool
	boundedTeardown(func() { close(killed) }, func() { forced = true }, 30*time.Second)
	<-killed
	if forced {
		t.Error("the force reap ran although the graceful teardown returned within its grace")
	}
}

// connectSeamFile is the ONE production file allowed to connect a transport on a background
// context — it is the seam whose transport carries the readiness bound.
const connectSeamFile = "plugin_connect_deadline.go"

// TestPluginConnectCallSitesCarryNoBackgroundContext is the mechanical half of the fix: the defect
// was not a missing helper but the LOADER handing context.Background() to a transport connect, and
// no deadline can bound a context that has none. A seam only helps while every call site uses it, so
// this gate reads the production sources and fails on the exact regression shape — a connect on a
// background context anywhere but in the seam itself (whose transport owns the bound).
func TestPluginConnectCallSitesCarryNoBackgroundContext(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	scanned, seamConnects := 0, 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		scanned++
		if !strings.Contains(string(src), ".Connect(context.Background())") {
			continue
		}
		if name != connectSeamFile {
			t.Errorf("%s hands context.Background() to a transport Connect — an unbounded plugin wait (charly#588); every loader call site goes through connectPluginReady", name)
			continue
		}
		seamConnects++
	}
	if scanned == 0 {
		t.Fatal("scanned no production sources — the gate would pass vacuously")
	}
	if seamConnects != 1 {
		t.Errorf("%s holds %d background-ctx connects; want exactly 1 — the seam the loader routes through", connectSeamFile, seamConnects)
	}
}

// TestConnectDeadlineHelperStuckPluginChild is the fixture child: it serves the real go-plugin wire
// (so the parent's handshake succeeds) and its Describe NEVER answers. It is skipped unless the
// parent re-execs this same test binary with stuckPluginChildEnv set — the standard helper-process
// pattern, which keeps the fixture hermetic (no external binary, no network).
func TestConnectDeadlineHelperStuckPluginChild(t *testing.T) {
	if os.Getenv(stuckPluginChildEnv) != "stuck-describe" {
		t.Skip("helper process for the charly#588 connect-deadline tests")
	}
	transport.Serve(&stuckDescribeProvider{}, &stuckDescribeMeta{})
}

type stuckDescribeMeta struct {
	pb.UnimplementedPluginMetaServer
}

// Describe never answers: the stuck-plugin shape charly#588 hung on (host parked, child idle, no
// error). Handling the request on a goroutine that never returns leaves the gRPC connection healthy
// and the RPC unanswered — the wait only a deadline can end.
func (*stuckDescribeMeta) Describe(context.Context, *pb.Empty) (*pb.Capabilities, error) { select {} }

type stuckDescribeProvider struct{ pb.UnimplementedProviderServer }
