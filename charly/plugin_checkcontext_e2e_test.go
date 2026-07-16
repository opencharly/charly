package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// TestKitVerbOutOfProcess_HTTPDoEndToEnd proves the FULL F2 CheckContextService reverse
// channel END-TO-END on real code: the http kit verb (candy/plugin-http) is host-built +
// served OUT-OF-PROCESS over go-plugin gRPC (LocalTransport, which carries the GRPCBroker),
// and its live-mode RunVerb dials cc.HTTPDo — which crosses the CheckContextService reverse
// channel to the host's checkContextReverseServer → doHTTPRequest against a real httptest
// server. This is the rigorous proof that the HTTP-over-RPC leg (F2's highest-risk unknown:
// host-vantage HTTP semantics out-of-process) round-trips byte-correctly: status + body
// matched on the success path, status-mismatch FAILED on the negative path — both through the
// reverse channel, not in-proc. Builds + execs a real binary, gated behind -short exactly like
// TestExternalDeployPlugin_ReverseChannelEndToEnd.
func TestKitVerbOutOfProcess_HTTPDoEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + execs the external plugin binary (slow)")
	}
	ctx := context.Background()

	srcDir, err := filepath.Abs("../candy/plugin-http")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(srcDir, "go.mod")); err != nil {
		t.Fatalf("http kit plugin module not found at %s: %v", srcDir, err)
	}

	// 1. Host-build the kit verb's OUT-OF-PROCESS serve binary (./cmd/serve, via sdk.ServeCheckVerb).
	bin, err := buildPluginBinary(ctx, srcDir, "plugin-http-test")
	if err != nil {
		t.Fatalf("buildPluginBinary: %v", err)
	}
	// 2. Connect OUT-OF-PROCESS via LocalTransport — the connection carries the broker.
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		t.Fatalf("LocalTransport.Connect: %v", err)
	}
	defer func() { _ = closer.Close() }()

	if len(unit.Providers) != 1 || unit.Providers[0].Reserved() != "http" {
		t.Fatalf("providers = %+v, want exactly one verb:http", unit.Providers)
	}
	gp, ok := unit.Providers[0].(*grpcProvider)
	if !ok {
		t.Fatalf("provider is %T, want *grpcProvider (the broker-carrying out-of-proc peer)", unit.Providers[0])
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("service is ready"))
	}))
	defer srv.Close()

	// The host serves BOTH reverse services on the broker: ExecutorService (the venue) +
	// CheckContextService (HTTPDo) — the SAME wiring invokeVerbProvider uses for a live check.
	cc := &checkContextReverseServer{httpBase: &http.Client{Timeout: 10 * time.Second}}
	envJSON, err := marshalJSON(&CheckEnv{Mode: "live", VenueKind: "host"})
	if err != nil {
		t.Fatal(err)
	}

	dispatch := func(op *spec.Op) pluginCheckResult {
		t.Helper()
		params, mErr := marshalJSON(op)
		if mErr != nil {
			t.Fatalf("marshal op: %v", mErr)
		}
		out, iErr := gp.InvokeWithExecutor(ctx,
			&Operation{Reserved: "http", Op: OpRun, Params: params, Env: envJSON},
			kit.ShellExecutor{}, buildEngineContext{}, false, cc)
		if iErr != nil {
			t.Fatalf("InvokeWithExecutor: %v", iErr)
		}
		var res pluginCheckResult
		if uErr := json.Unmarshal(out.JSON, &res); uErr != nil {
			t.Fatalf("decode result: %v", uErr)
		}
		return res
	}

	// Success: status 200 + body contains "ready" → pass, end-to-end over the reverse channel.
	pass := dispatch(&spec.Op{Plugin: "http", PluginInput: map[string]any{
		"http": srv.URL, "status": 200, "body": []any{map[string]any{"contains": "ready"}},
	}})
	if pass.Status != "pass" {
		t.Fatalf("out-of-process http verb: status=%q msg=%q, want pass", pass.Status, pass.Message)
	}

	// Negative: status 500 expected vs 200 actual → fail (the verdict also crosses the channel).
	fail := dispatch(&spec.Op{Plugin: "http", PluginInput: map[string]any{"http": srv.URL, "status": 500}})
	if fail.Status != "fail" {
		t.Fatalf("status mismatch over reverse channel: status=%q, want fail", fail.Status)
	}
}
