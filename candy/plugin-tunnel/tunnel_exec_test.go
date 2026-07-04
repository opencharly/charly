package tunnelverb

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/opencharly/charly/candy/plugin-tunnel/params"
	pb "github.com/opencharly/sdk/proto"
)

// TestInvoke_PlanWireRoundTrip proves the FULL wire path both callers use: the
// `plugin_input` envelope (the core adapter's {method,config} AND a `plugin: tunnel`
// check step's marshaled Op both nest under plugin_input) decodes into params.TunnelInput,
// dispatches the plan method, and returns a pluginCheckResult the check runner decodes.
func TestInvoke_PlanWireRoundTrip(t *testing.T) {
	in := params.TunnelInput{
		Method: "plan",
		Config: params.TunnelConfig{
			Provider: "tailscale",
			Ports:    []params.TunnelPort{{Port: 8888, BackendPort: 8888, Protocol: "http", Public: false}},
		},
		Expect: []string{"tailscale serve --bg --https=8888 http://127.0.0.1:8888"},
	}
	paramsJSON, err := json.Marshal(map[string]any{"plugin_input": in})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	reply, err := provider{}.Invoke(context.Background(), &pb.InvokeRequest{ParamsJson: paramsJSON})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var r pluginCheckResult
	if err := json.Unmarshal(reply.GetResultJson(), &r); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if r.Status != "pass" {
		t.Errorf("plan wire round-trip: status=%q message=%q, want pass", r.Status, r.Message)
	}
}

// TestInvoke_UnknownMethod proves an unrecognized method returns an error reply (never panics).
func TestInvoke_UnknownMethod(t *testing.T) {
	paramsJSON, _ := json.Marshal(map[string]any{"plugin_input": params.TunnelInput{Method: "bogus"}})
	reply, err := provider{}.Invoke(context.Background(), &pb.InvokeRequest{ParamsJson: paramsJSON})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var r tunnelReply
	if err := json.Unmarshal(reply.GetResultJson(), &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Error == "" {
		t.Errorf("unknown method: want a non-empty error reply")
	}
}

// TestPlanArgvLines_Tailscale proves the plan dry-run rebuilds the EXACT tailscale
// serve/funnel argv the start path would run — the same strings box/fedora's
// check-tunnel-pod bed (tunnel-plan-tailscale) and the candy's tunnel-plan-argv ADE
// step assert. Guards the moved command-building logic + the backend/flag/scheme helpers.
func TestPlanArgvLines_Tailscale(t *testing.T) {
	cfg := params.TunnelConfig{
		Provider: "tailscale",
		BoxName:  "check-tunnel-pod",
		Ports: []params.TunnelPort{
			{Port: 8888, BackendPort: 8888, Protocol: "http", Public: false},
			{Port: 443, BackendPort: 443, Protocol: "https+insecure", Public: true},
		},
	}
	got, err := planArgvLines(cfg)
	if err != nil {
		t.Fatalf("planArgvLines: %v", err)
	}
	want := []string{
		"tailscale serve --bg --https=8888 http://127.0.0.1:8888",
		"tailscale funnel --bg --https=443 https+insecure://127.0.0.1:443",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tailscale plan argv:\n got: %v\nwant: %v", got, want)
	}
}

// TestPlanArgvLines_Cloudflare proves the cloudflare plan renders the deterministic,
// host-independent ingress + run representation the check-tunnel-pod bed
// (tunnel-plan-cloudflare) asserts.
func TestPlanArgvLines_Cloudflare(t *testing.T) {
	cfg := params.TunnelConfig{
		Provider:   "cloudflare",
		TunnelName: "charly-check-tunnel",
		Hostname:   "tunnel.example.com",
		BoxName:    "check-tunnel-pod",
		Ports: []params.TunnelPort{
			{Port: 8080, BackendPort: 8080, Protocol: "https", Public: true},
		},
	}
	got, err := planArgvLines(cfg)
	if err != nil {
		t.Fatalf("planArgvLines: %v", err)
	}
	want := []string{
		"ingress tunnel.example.com -> https://localhost:8080",
		"cloudflared tunnel run charly-check-tunnel",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cloudflare plan argv:\n got: %v\nwant: %v", got, want)
	}
}

// TestPlanArgvLines_TCPAndBackend proves the tcp scheme + a distinct backend_port flow
// through tailscaleFlag/schemeTarget/backend correctly.
func TestPlanArgvLines_TCPAndBackend(t *testing.T) {
	cfg := params.TunnelConfig{
		Provider: "tailscale",
		Ports: []params.TunnelPort{
			{Port: 10000, BackendPort: 5432, Protocol: "tcp", Public: false},
			{Port: 47998, Protocol: "udp", Public: false}, // udp is never tunneled → skipped
		},
	}
	got, err := planArgvLines(cfg)
	if err != nil {
		t.Fatalf("planArgvLines: %v", err)
	}
	want := []string{"tailscale serve --bg --tcp=10000 tcp://127.0.0.1:5432"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tcp/backend plan argv:\n got: %v\nwant: %v", got, want)
	}
}

// TestTunnelPlan_ExpectMatch proves the self-asserting plan verb: matching expect → pass,
// mismatched expect → fail, unknown provider → fail.
func TestTunnelPlan_ExpectMatch(t *testing.T) {
	cfg := params.TunnelConfig{
		Provider: "tailscale",
		Ports:    []params.TunnelPort{{Port: 8888, Protocol: "http"}},
	}
	if r := tunnelPlan(cfg, []string{"tailscale serve --bg --https=8888 http://127.0.0.1:8888"}); r.Status != "pass" {
		t.Errorf("matching expect: status=%q message=%q, want pass", r.Status, r.Message)
	}
	if r := tunnelPlan(cfg, []string{"wrong argv"}); r.Status != "fail" {
		t.Errorf("mismatched expect: status=%q, want fail", r.Status)
	}
	if r := tunnelPlan(params.TunnelConfig{Provider: "bogus"}, nil); r.Status != "fail" {
		t.Errorf("unknown provider: status=%q, want fail", r.Status)
	}
}
