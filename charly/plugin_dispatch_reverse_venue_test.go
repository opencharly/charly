package main

// plugin_dispatch_reverse_venue_test.go — the S1 (venue-scoped-executor-session seam) + S2
// (InvokeProvider lazy-connect fallback) production regression suite for Unit 6 of the
// core-minimization program. S1 is spike-proven (see the archived
// scratchpad/venue-spike-harness.go.txt); these tests are its adaptation onto the REAL wire
// field (pb.InvokeProviderRequest.VenueDescriptorJson / sdk.InvokeProviderOpts) rather than the
// spike's throwaway env-JSON convention — same fixtures, same coverage, production shape.

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/opencharly/spec/ops"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencharly/spec/exec"
	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// venuePeerResult mirrors exampledispatchpeer's reply shape (candy/plugin-example-dispatch).
type venuePeerResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Stdout  string `json:"stdout"`
	Stderr  string `json:"stderr"`
	Exit    int    `json:"exit"`
}

// buildAndConnectExampleDispatchPlugin builds + connects candy/plugin-example-dispatch OOP and
// registers both its providers (exampledispatch + exampledispatchpeer) — mirroring
// TestPluginDispatch_InvokeProviderAndHostBuild's own harness (R3: same build+connect shape,
// factored here so every S1/S2 fixture below shares it).
func buildAndConnectExampleDispatchPlugin(t *testing.T) *grpcProvider {
	t.Helper()
	ctx := context.Background()
	srcA := pluginModuleDir(t, "example-dispatch")
	if _, err := os.Stat(filepath.Join(srcA, "go.mod")); err != nil {
		t.Fatalf("dispatch plugin module not found at %s: %v", srcA, err)
	}
	bin, err := buildPluginBinary(ctx, srcA, "plugin-example-dispatch-venue-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "venue-test", nil); err != nil {
		t.Fatalf("RegisterPluginProviders: %v", err)
	}
	for _, p := range unit.Providers {
		if p.Reserved() == "exampledispatch" {
			return p.(*grpcProvider)
		}
	}
	t.Fatalf("exampledispatch provider not found in %+v", unit.Providers)
	return nil
}

// requireLoopbackSSH returns the local user + loopback host for an SSH descriptor fixture,
// skipping (an environment gap, not a mechanism failure) when localhost SSH isn't reachable.
func requireLoopbackSSH(t *testing.T) (sshUser, sshHost string) {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	exec := exec.SSHExecutor{User: u.Username, Host: "127.0.0.1", ConnectTimeout: 5}
	stdout, stderr, exit, err := exec.RunCapture(context.Background(), "echo ssh-preflight-ok")
	if err != nil || exit != 0 || !strings.Contains(stdout, "ssh-preflight-ok") {
		t.Skipf("loopback SSH not reachable (err=%v exit=%d stderr=%q) — skipping SSH fixture", err, exit, stderr)
	}
	return u.Username, "127.0.0.1"
}

// marshalVenueDescriptor marshals d for req.VenueDescriptorJson (empty JSON, i.e. absent, when d
// is nil).
func marshalVenueDescriptor(t *testing.T, d *spec.VenueDescriptor) []byte {
	t.Helper()
	if d == nil {
		return nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal venue descriptor: %v", err)
	}
	return b
}

// venueInvokeReq builds an InvokeProviderRequest at exampledispatchpeer (the only target every
// S1 fixture in this file dispatches to).
func venueInvokeReq(peerCmd string, vd []byte) *pb.InvokeProviderRequest {
	params, _ := marshalJSON(peerInputForTest{PeerCmd: peerCmd})
	return &pb.InvokeProviderRequest{Class: "verb", Reserved: "exampledispatchpeer", Op: ops.OpRun, ParamsJson: params, VenueDescriptorJson: vd}
}

// peerInputForTest mirrors candy/plugin-example-dispatch's own peerInput{PeerCmd} shape (the raw,
// unwrapped params exampledispatchpeer decodes directly — no plugin_input envelope, matching how
// InvokeWithExecutor/InvokeProvider are driven directly rather than through the check-step sugar).
type peerInputForTest struct {
	PeerCmd string `json:"peer_cmd,omitempty"`
}

// --- S1 Fixture 1: baseline gap — venue-less caller, NO descriptor. The peer gets NO executor at
// all (byte-identical pre-S1 behavior when the field is absent). ---
func TestInvokeProvider_VenueDescriptor_Baseline_NoDescriptor_PeerGetsNoExecutor(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds a plugin binary OOP (slow)")
	}
	_ = buildAndConnectExampleDispatchPlugin(t)

	srv := &executorReverseServer{} // exec == nil — the venue-less caller (e.g. dispatchInProcCommand's shape)
	res, err := srv.InvokeProvider(context.Background(), venueInvokeReq("echo should-not-run", nil))
	if err != nil {
		t.Fatalf("InvokeProvider (venue-less, no descriptor) unexpectedly errored at the outer hop: %v", err)
	}
	var peer venuePeerResult
	if err := json.Unmarshal(res.GetResultJson(), &peer); err != nil {
		t.Fatalf("decode peer reply: %v raw=%s", err, res.GetResultJson())
	}
	t.Logf("baseline (no descriptor) peer result: %+v", peer)
	if peer.Status != "fail" || !strings.Contains(peer.Message, "no-executor") {
		t.Fatalf("expected the pre-S1 gap (peer reports no executor), got: %+v", peer)
	}
}

// --- S1 Fixture 2: venue-less caller + shell descriptor, direct to the OOP peer. ---
func TestInvokeProvider_VenueDescriptor_Venueless_ShellDescriptor(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds a plugin binary OOP (slow)")
	}
	_ = buildAndConnectExampleDispatchPlugin(t)

	srv := &executorReverseServer{} // exec == nil — the venue-less caller
	vd := marshalVenueDescriptor(t, &spec.VenueDescriptor{Kind: "shell"})
	res, err := srv.InvokeProvider(context.Background(), venueInvokeReq("hostname", vd))
	if err != nil {
		t.Fatalf("InvokeProvider (venue-less, shell descriptor): %v", err)
	}
	var peer venuePeerResult
	if err := json.Unmarshal(res.GetResultJson(), &peer); err != nil {
		t.Fatalf("decode peer reply: %v raw=%s", err, res.GetResultJson())
	}
	t.Logf("venue-less + shell descriptor peer result: %+v", peer)
	if peer.Status != "pass" {
		t.Fatalf("expected pass, got: %+v", peer)
	}
	wantHost, _ := os.Hostname()
	if strings.TrimSpace(peer.Stdout) != wantHost {
		t.Fatalf("peer executed on the WRONG host: got %q want %q (host-local materialization failed)", peer.Stdout, wantHost)
	}
}

// --- S1 Fixture 3: venue-less caller + SSH descriptor (loopback), direct to the OOP peer. ---
func TestInvokeProvider_VenueDescriptor_Venueless_SSHDescriptor(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds a plugin binary OOP (slow)")
	}
	sshUser, sshHost := requireLoopbackSSH(t)
	_ = buildAndConnectExampleDispatchPlugin(t)

	srv := &executorReverseServer{} // exec == nil — the venue-less caller
	vd := marshalVenueDescriptor(t, &spec.VenueDescriptor{Kind: "ssh", User: sshUser, Host: sshHost, ConnectTimeout: 5})
	res, err := srv.InvokeProvider(context.Background(), venueInvokeReq("whoami", vd))
	if err != nil {
		t.Fatalf("InvokeProvider (venue-less, ssh descriptor): %v", err)
	}
	var peer venuePeerResult
	if err := json.Unmarshal(res.GetResultJson(), &peer); err != nil {
		t.Fatalf("decode peer reply: %v raw=%s", err, res.GetResultJson())
	}
	t.Logf("venue-less + ssh descriptor peer result: %+v", peer)
	if peer.Status != "pass" {
		t.Fatalf("expected pass, got: %+v", peer)
	}
	if strings.TrimSpace(peer.Stdout) != sshUser {
		t.Fatalf("peer executed as the WRONG identity over SSH: got %q want %q", peer.Stdout, sshUser)
	}
}

// --- S1 Fixture 4: OUT-OF-PROCESS caller (its OWN executor is a plain ShellExecutor) supplying a
// DIFFERENT (SSH) descriptor for the peer via the REAL sdk.InvokeProviderOpts/dispatchInput wiring
// — proves the descriptor OVERRIDES the caller's own s.exec, and exercises the TRUE nested-broker
// case (both caller and peer out-of-process, same connection, concurrent broker ids). ---
func TestInvokeProvider_VenueDescriptor_OutOfProcessCaller_SSHOverridesOwnShellExecutor(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds a plugin binary OOP (slow)")
	}
	sshUser, sshHost := requireLoopbackSSH(t)
	gpA := buildAndConnectExampleDispatchPlugin(t)

	params, err := marshalJSON(map[string]any{
		"target_word": "exampledispatchpeer",
		"peer_cmd":    "whoami",
		"venue_descriptor": map[string]any{
			"kind": "ssh", "user": sshUser, "host": sshHost, "connect_timeout": 5,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Drive A WITH its OWN reverse channel (exec.ShellExecutor{} — a real, host-local venue,
	// DIFFERENT from the SSH descriptor A asks the host to materialize for the peer).
	res, err := gpA.InvokeWithExecutor(context.Background(), &Operation{Reserved: "exampledispatch", Op: ops.OpRun, Params: params}, exec.ShellExecutor{}, buildEngineContext{}, false, nil)
	if err != nil {
		t.Fatalf("InvokeWithExecutor (OOP caller, ssh descriptor for peer): %v", err)
	}
	var out struct {
		ProviderResult json.RawMessage `json:"provider_result"`
	}
	if err := json.Unmarshal(res.JSON, &out); err != nil {
		t.Fatalf("decode dispatch reply: %v raw=%s", err, res.JSON)
	}
	var peer venuePeerResult
	if err := json.Unmarshal(out.ProviderResult, &peer); err != nil {
		t.Fatalf("decode peer reply: %v raw=%s", err, out.ProviderResult)
	}
	t.Logf("OOP caller (own ShellExecutor) + ssh descriptor for peer result: %+v", peer)
	if peer.Status != "pass" {
		t.Fatalf("expected pass, got: %+v", peer)
	}
	if strings.TrimSpace(peer.Stdout) != sshUser {
		t.Fatalf("peer did not execute over the DESCRIPTOR-materialized SSH venue (got the caller's own ShellExecutor identity instead?): got %q want %q", peer.Stdout, sshUser)
	}
}

// --- S1 Fixture 5: concurrency stress — many simultaneous venue-less InvokeProvider calls against
// the SAME connection's broker (NextId() racing, mixed shell/SSH descriptors), per the standing
// "concurrency always proven under high load" mandate: a shared-registry/broker mechanism is
// invisible to a serial run and must be proven under real simultaneity.
//
// n is capped BELOW sshd's default MaxStartups admission threshold (10 unauthenticated
// connections before probabilistic dropping kicks in) — the spike's own n=24 run hit that exact
// throttle (sshd's OWN connection-admission defense, NOT a broker deadlock or a mechanism race,
// confirmed clean under -race with zero hangs — see the archived spike report). Kept under
// threshold here so this fixture isolates the MECHANISM signal from that unrelated host-sshd
// artifact.
func TestInvokeProvider_VenueDescriptor_ConcurrentVenueless_NoDeadlockNoCrossTalk(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds a plugin binary OOP (slow)")
	}
	sshUser, sshHost := requireLoopbackSSH(t)
	_ = buildAndConnectExampleDispatchPlugin(t)

	const n = 8 // 4 concurrent fresh SSH dials + 4 shell — well under sshd's MaxStartups=10 soft floor
	type outcome struct {
		idx  int
		peer venuePeerResult
		err  error
	}
	results := make(chan outcome, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			srv := &executorReverseServer{} // exec == nil — a fresh venue-less caller per goroutine
			useSSH := i%2 == 0
			var vd *spec.VenueDescriptor
			var cmd, want string
			if useSSH {
				vd = &spec.VenueDescriptor{Kind: "ssh", User: sshUser, Host: sshHost, ConnectTimeout: 5}
				cmd, want = "whoami", sshUser
			} else {
				vd = &spec.VenueDescriptor{Kind: "shell"}
				cmd = "hostname"
				want, _ = os.Hostname()
			}
			res, err := srv.InvokeProvider(context.Background(), venueInvokeReq(cmd, marshalVenueDescriptor(t, vd)))
			if err != nil {
				results <- outcome{idx: i, err: err}
				return
			}
			var peer venuePeerResult
			if uerr := json.Unmarshal(res.GetResultJson(), &peer); uerr != nil {
				results <- outcome{idx: i, err: uerr}
				return
			}
			if strings.TrimSpace(peer.Stdout) != want {
				results <- outcome{idx: i, err: fmt.Errorf("cross-talk: got %q want %q (ssh=%v)", peer.Stdout, want, useSSH)}
				return
			}
			results <- outcome{idx: i, peer: peer}
		}(i)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("DEADLOCK: %d/%d goroutines never returned within 60s (broker-nesting hang)", n-len(results), n)
	}
	close(results)
	failures := 0
	for o := range results {
		if o.err != nil {
			failures++
			t.Errorf("goroutine %d: %v", o.idx, o.err)
		}
	}
	if failures > 0 {
		t.Fatalf("%d/%d concurrent InvokeProvider calls failed", failures, n)
	}
	t.Logf("%d concurrent venue-less InvokeProvider calls (mixed shell/ssh) — no deadlock, no cross-talk", n)
}

// --- S2 Fixture 1: lazy-connect fallback — the target word is NOT pre-registered; InvokeProvider
// resolves it via connectPluginByWordRef (the project's own candy closure at repo root discovers
// candy/plugin-example-dispatch). ---
func TestInvokeProvider_LazyConnectFallback(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds a plugin binary OOP + scans the project candy closure (slow)")
	}
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	// Chdir to a MINIMAL synthetic project (only plugin-example-dispatch + deps) so the
	// lazy-connect's pass-1 project-closure scan is tiny — a cwd at the repo root (50+ remote
	// candies) made the scan take minutes. The plugin binary build is content-stamp-cached
	// (pluginBuildCacheDir), so the second run reuses it. t.Chdir restores the ORIGINAL cwd
	// on cleanup (the framework-managed restore — a manual os.Chdir + t.Cleanup would leak the
	// repo-root cwd into later tests).
	dispatchDir := seedDispatchProject(t, repoRoot)
	t.Cleanup(func() { _ = os.RemoveAll(dispatchDir) })
	t.Chdir(dispatchDir)

	if _, ok := providerRegistry.resolve(ClassVerb, "exampledispatchpeer"); ok {
		t.Fatal("test invariant violated: exampledispatchpeer is already registered — the fallback path would not be exercised")
	}
	srv := &executorReverseServer{}
	res, err := srv.InvokeProvider(context.Background(), &pb.InvokeProviderRequest{Class: "verb", Reserved: "exampledispatchpeer", Op: ops.OpRun})
	if err != nil {
		t.Fatalf("InvokeProvider (lazy-connect fallback): %v", err)
	}
	var peer venuePeerResult
	if err := json.Unmarshal(res.GetResultJson(), &peer); err != nil {
		t.Fatalf("decode peer reply: %v raw=%s", err, res.GetResultJson())
	}
	if peer.Status != "pass" || peer.Message != "peer-reached" {
		t.Fatalf("unexpected peer reply after lazy-connect: %+v", peer)
	}
	if _, ok := providerRegistry.resolve(ClassVerb, "exampledispatchpeer"); !ok {
		t.Fatal("lazy-connect fallback did not leave the provider registered for subsequent callers")
	}
}

// --- S2 Fixture 2: the Spike-2 concern — does the fallback deadlock (or corrupt
// inKindConnectPassFlag) when InvokeProvider is itself called WHILE the loader is already mid a
// connectDeclaredKindPlugins nested-load pass? connectDeclaredKindPlugins' OWN top-of-function
// guard (`if inKindConnectPass() { return }`) already makes its own re-entry a no-op; this proves
// that guard is sufficient for THIS caller too — no new guard was added in
// plugin_dispatch_reverse.go's InvokeProvider. ---
func TestInvokeProvider_LazyConnectFallback_DuringNestedKindConnectPass_NoDeadlock(t *testing.T) {
	t.Cleanup(snapshotProviderState())
	if testing.Short() {
		t.Skip("builds a plugin binary OOP + scans the project candy closure (slow)")
	}
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	// Chdir to a MINIMAL synthetic project (only the example-dispatch plugin + its deps) so the
	// lazy-connect's pass-1 project-closure scan is tiny — a cwd at the repo root (50+ remote
	// candies) made the scan take minutes and blew the deadlock test's 30s budget. t.Chdir
	// restores the ORIGINAL cwd on cleanup (framework-managed).
	dispatchDir := seedDispatchProject(t, repoRoot)
	t.Cleanup(func() { _ = os.RemoveAll(dispatchDir) })
	t.Chdir(dispatchDir)

	if _, ok := providerRegistry.resolve(ClassVerb, "exampledispatchpeer"); ok {
		t.Fatal("test invariant violated: exampledispatchpeer is already registered — the fallback path would not be exercised")
	}

	setKindConnectPass(true)
	defer setKindConnectPass(false)

	srv := &executorReverseServer{}
	type callResult struct {
		res *pb.InvokeReply
		err error
	}
	done := make(chan callResult, 1)
	go func() {
		res, err := srv.InvokeProvider(context.Background(), &pb.InvokeProviderRequest{Class: "verb", Reserved: "exampledispatchpeer", Op: ops.OpRun})
		done <- callResult{res: res, err: err}
	}()

	var cr callResult
	select {
	case cr = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("DEADLOCK: InvokeProvider's lazy-connect fallback hung while inKindConnectPass was already true")
	}
	if cr.err != nil {
		t.Fatalf("InvokeProvider during a nested kind-connect pass: %v", cr.err)
	}
	var peer venuePeerResult
	if err := json.Unmarshal(cr.res.GetResultJson(), &peer); err != nil {
		t.Fatalf("decode peer reply: %v raw=%s", err, cr.res.GetResultJson())
	}
	if peer.Status != "pass" {
		t.Fatalf("unexpected peer reply: %+v", peer)
	}
	if !inKindConnectPass() {
		t.Fatal("inKindConnectPass flag was clobbered by the nested fallback connect (expected it to stay true — only this test's own defer should clear it)")
	}
}

// TestSubstrateFallbackRef — the S3b substrate-default regression test for the K-wave-2
// pod-config connect gap: a deploy-class word naming a known externalized substrate with an
// EMPTY caller extraRef defaults to the substrate's canonical plugin ref, so a candy-less
// box/<distro> project reaches deploy:pod for `charly config` (Pass-1 is empty there — no local
// candy/ dir — and without the default Pass-2 never fired). The caller's own ref wins when set;
// unknown words and non-deploy classes keep "" — byte-identical S2/S3b behavior.
func TestSubstrateFallbackRef(t *testing.T) {
	for _, tt := range []struct {
		name     string
		class    ProviderClass
		word     string
		extraRef string
		want     string
	}{
		{"pod substrate default", ClassDeployTarget, "pod", "", "@github.com/opencharly/plugin-deploy-pod/candy/plugin-deploy-pod"},
		{"kubernetes substrate default", ClassDeployTarget, "kubernetes", "", "@github.com/opencharly/plugin-kube/candy/plugin-kube"},
		{"vm substrate default", ClassDeployTarget, "vm", "", "@github.com/opencharly/plugin-deploy-vm/candy/plugin-deploy-vm"},
		{"caller ref wins", ClassDeployTarget, "pod", "@example.org/candy/custom", "@example.org/candy/custom"},
		{"unknown word no default", ClassDeployTarget, "builder-nope", "", ""},
		{"non-deploy class no default", ClassVerb, "pod", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := substrateFallbackRef(tt.class, tt.word, tt.extraRef); got != tt.want {
				t.Fatalf("substrateFallbackRef(%s, %q, %q) = %q, want %q", tt.class, tt.word, tt.extraRef, got, tt.want)
			}
		})
	}
}

// seedDispatchProject materializes a minimal synthetic project (<repoRoot>/.dispatch-test/) whose
// candy closure pins ONLY plugin-example-dispatch (the exampledispatchpeer word source) — the
// lazy-connect pass-1 scan stays tiny instead of walking the full repo's 50+ remote candies.
func seedDispatchProject(t *testing.T, repoRoot string) string {
	t.Helper()
	dir := filepath.Join(repoRoot, ".dispatch-test")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rootManifest := "version: 2026.240.1943\ndiscover:\n    - path: candy\n      recursive: true\n"
	if err := os.WriteFile(filepath.Join(dir, spec.UnifiedFileName), []byte(rootManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	seed := "fixture-seed:\n    candy:\n        version: 2026.001.0001\n        description: Dispatch fixture seed.\n        candy:\n            - '@github.com/opencharly/plugin-example-dispatch/candy/plugin-example-dispatch:v2026.237.1420'\n"
	seedDir := filepath.Join(dir, "candy", "fixture-seed")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seedDir, spec.UnifiedFileName), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
