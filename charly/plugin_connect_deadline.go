// plugin_connect_deadline.go — THE bound on the plugin readiness READ, plus the bounded teardown
// that lets a failed connect actually report.
//
// charly#588 (RCA): `charly box build` (image-build step) and `charly check live` (plugin
// resolution) were each observed to block INDEFINITELY — the process parked (state S / futex_wait),
// its `__plugin serve` children alive and idle at 0.0% CPU, no step output, no error, and no
// summary.yml for 25+ minutes. The unbounded wait is the readiness READ that follows the handshake:
// describe() → conn.Meta.Describe(ctx, …) (plugin_grpc.go), a plain gRPC unary call issued on the
// loader's context, which is context.Background() at every call site — so a plugin child that
// completes the go-plugin handshake and then never answers leaves that call parked FOREVER.
//
// The other legs are already bounded, which is why the bound belongs HERE and not around the whole
// connect: go-plugin bounds the spawn→handshake leg with StartTimeout (client.go:
// `timeout := time.After(c.config.StartTimeout)`, set from the project's resolved readiness
// per_attempt — plugin_transport.go's localPluginClientConfig), and the build BEFORE the connect is
// legitimately slow (a cold `go build` of a candy plus its module graph takes minutes), so it must
// not be bounded by a readiness deadline at all. Reproduced before the fix, and kept as a
// regression test (plugin_connect_deadline_test.go): LocalTransport.Connect against a child whose
// Describe never returns was still parked after 60s, while the identical connect under this bound
// returned in ~the bound with "plugin describe: rpc error: code = DeadlineExceeded".
//
// WHY A FIXED CONSTANT AND NOT the resolved readiness per_attempt: resolving readiness from inside
// the connect path is a DEADLOCK, measured while building this fix. loadedReadiness() runs
// LoadUnified, which materialises the project through the plugin-loader candy, which calls
// loadBuiltinPluginUnits → builtinGateOnce.Do — and the builtin gate is ITSELF a caller of the
// connect seam, so a seam that resolved readiness would re-enter that non-reentrant sync.Once from
// inside its own callback and park forever (SIGQUIT dump: goroutine 1 in
// loadBuiltinPluginUnits → sync.Once.doSlow → loadBuiltinPluginUnits.func1 → loadedReadiness →
// sync.Once.doSlow → loadBuiltinPluginUnits). The bound therefore stays a plain constant, and it
// costs nothing: the per_attempt knob keeps governing the SPAWN leg (go-plugin's StartTimeout)
// while this constant governs only the post-handshake READ, which is a sub-second operation for a
// healthy plugin (its schema is a CUE string already in the child's memory) — the two never
// interact, so raising per_attempt for a heavy roster still moves what it always moved.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	plugin "github.com/hashicorp/go-plugin"
)

const (
	// pluginReadyTimeout bounds the readiness READ a connect performs after the go-plugin
	// handshake (describe → the plugin's capability manifest + CUE schema). It exists because
	// that leg had NO deadline while every other leg did (see the file header): without it a
	// plugin that accepts the handshake and then never answers parks the host forever. Two
	// minutes is ~three orders of magnitude above the healthy cost and is the same order as the
	// project's default readiness per_attempt, so a healthy plugin cannot fail here — the bound
	// only ever fires on a plugin that is genuinely not answering.
	pluginReadyTimeout = 2 * time.Minute

	// pluginTeardownGrace bounds EACH half of a failed connect's teardown: the graceful
	// Client.Kill() attempt and, for a peer that never answered it, the force reap.
	pluginTeardownGrace = 5 * time.Second
)

// pluginConnectPhaseError records WHICH phase of a connect a failure came from, so a bounded
// connect can name the wait that never finished (charly#588 ask #2: identify the culprit from the
// log alone). The phases are the legs of connectAndDescribe: "start/handshake" (go-plugin spawn +
// handshake), "dispense", "describe" (the readiness READ this file bounds), and "protocol-gate"
// (buildUnit's version check).
type pluginConnectPhaseError struct {
	phase string
	err   error
}

func (e *pluginConnectPhaseError) Error() string { return e.err.Error() }

func (e *pluginConnectPhaseError) Unwrap() error { return e.err }

// pluginPhase wraps err with the connect phase it came from (nil-safe, so a call site can wrap
// without a guard).
func pluginPhase(phase string, err error) error {
	if err == nil {
		return nil
	}
	return &pluginConnectPhaseError{phase: phase, err: err}
}

// pluginConnectPhase reports the phase a failed connect reached, defaulting to "connect" for an
// error that carries no phase marker.
func pluginConnectPhase(err error) string {
	var pe *pluginConnectPhaseError
	if errors.As(err, &pe) {
		return pe.phase
	}
	return "connect"
}

// connectPluginReady is THE plugin-connect entry point for the loader: it runs ONE transport
// connect and returns a NAMED error instead of an anonymous one — the plugin, its source, the phase
// that failed and the readiness bound that expired (charly#588 ask #2, so the next occurrence is
// diagnosable from the log alone). The bound itself lives in the transport (LocalTransport's
// readyTimeout → pluginReadyTimeout), so it covers the readiness read without capping the build
// that precedes it — this seam supplies the IDENTITY, the transport supplies the BOUND.
//
// The context here is deliberately context.Background(): the deadline is the transport's, applied
// to the readiness read itself (describeBounded) rather than to the whole connect, so a slow spawn
// and a slow host build keep their own bounds. This is the ONE place a connect may run on a
// deadline-carrying transport built by the loader.
func connectPluginReady(t PluginTransport, name, source string) (*PluginUnit, io.Closer, error) {
	unit, closer, err := t.Connect(context.Background())
	if err == nil {
		return unit, closer, nil
	}
	if closer != nil {
		_ = closer.Close() // defensive: a failing Connect returns no closer, but a leaked one is a child the host can never reap
	}
	return nil, nil, pluginConnectError(t, name, source, err)
}

// pluginConnectBound reports the readiness bound that governs this transport — its own when it
// carries one (LocalTransport.readyTimeout), else the package default. The error names the bound
// that ACTUALLY applied, so an overridden transport (the regression tests, or a future caller with
// a tighter bound) is reported truthfully instead of being described with the default.
func pluginConnectBound(t PluginTransport) time.Duration {
	if b, ok := t.(interface{ readyTimeout() time.Duration }); ok {
		return b.readyTimeout()
	}
	return pluginReadyTimeout
}

// pluginConnectError renders the diagnosable form of a failed plugin connect. A deadline error
// names the bound that expired; every other failure names the same identities without claiming a
// bound it did not hit.
func pluginConnectError(t PluginTransport, name, source string, err error) error {
	phase := pluginConnectPhase(err)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("plugin %s did not become ready within %s (%s phase, source %s): %w",
			name, pluginConnectBound(t), phase, source, err)
	}
	return fmt.Errorf("plugin %s (%s phase, source %s): %w", name, phase, source, err)
}

// killPluginClient tears down a plugin client AND its child process under pluginTeardownGrace.
//
// It exists because go-plugin's own Client.Kill() can itself park forever: Kill's graceful half
// asks the plugin to stop over an RPC sent on GRPCClient's doneCtx (grpc_client.go: Close →
// controller.Shutdown(c.doneCtx)), a Background-derived context with NO deadline — so a peer that
// completes the HTTP/2 connection and then never answers the Shutdown RPC would swallow the very
// error a bounded connect just produced (exactly the charly#588 shape: host parked, child alive
// and idle, nothing printed). The graceful attempt runs first and keeps its semantics; only when it
// has not returned within the grace do we SIGKILL the child, which unblocks the parked RPC and lets
// the caller report the failure.
func killPluginClient(client *plugin.Client, cmd *exec.Cmd) {
	boundedTeardown(client.Kill, func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}, pluginTeardownGrace)
}

// boundedTeardown runs kill and returns as soon as it completes; if it is still running after
// grace it runs force (the unconditional reap) and then waits one more grace. It never blocks
// longer than two graces, so a teardown can no longer be the reason a bounded operation still
// hangs. A nil kill is a no-op; a nil force means "wait out the second grace, then return anyway".
func boundedTeardown(kill, force func(), grace time.Duration) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if kill != nil {
			kill()
		}
	}()
	select {
	case <-done:
		return
	case <-time.After(grace):
	}
	if force != nil {
		force()
	}
	select {
	case <-done:
	case <-time.After(grace):
	}
}
