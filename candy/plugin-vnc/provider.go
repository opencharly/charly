package vnc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/charly/candy/plugin-vnc/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// provider.go is the out-of-process vnc verb provider — charly's host dispatches a
// `vnc:` check step to it through the registry (ResolveVerb("vnc") → this grpcProvider →
// Provider.Invoke) with the FULL #Op marshaled as params_json and a CheckEnv snapshot as
// env. Because the out-of-process path does NOT run a host-side matcher
// pipeline, this Invoke OWNS the whole verdict: DIAL the host-pre-resolved RFB endpoint
// (the host owns the podman / venue / libvirt resolution + any bridge/SSH tunnel),
// dispatch the method, then evaluate the stdout/stderr/exit_status matchers + the
// screenshot artifact validators itself (via the shared sdk implementation — R3), and
// return the wire {status,message} the host decodes.

// vncEndpoint is the dialable RFB endpoint the plugin builds from the addr the generic
// VM-graphics reverse-leg (cc.ResolveGraphicsEndpoint) returns for the deployment's VNC
// display. Addr is the host-reachable "host:port" the plugin dials over TCP (a container's
// published 5900, or a VM's host-bridged/forwarded RFB address — the host bridges any UNIX
// socket, so the plugin always gets TCP); Password is the resolved VNC ticket ("" = no auth).
type vncEndpoint struct {
	Addr     string `json:"addr"`
	Password string `json:"password"`
}

// vncEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for a
// `vnc:` check step (provider_checkenv.go). Box/Mode mirror the shared CheckEnv; the endpoint
// is no longer pre-shipped — the plugin resolves it via cc.ResolveGraphicsEndpoint.
type vncEnv struct {
	Box  string `json:"box"`
	Mode string `json:"mode"` // "live" | "box"
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `vnc:` operation. It decodes the full #Op, the typed plugin
// input (params.VncInput — the per-verb fields live in the desugared
// plugin_input since the schema-compaction cutover), and the env, skips in box
// mode (no live VNC endpoint on a disposable `charly check box`), skips a nil
// endpoint, dials the pre-resolved RFB endpoint, dispatches the method, and
// self-evaluates the matchers + screenshot artifact validators.
func (provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "vnc: decode op: "+err.Error())
		}
	}
	var in params.VncInput
	kit.DecodeInput(op.PluginInput, &in)
	var env vncEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := in.Method

	// Live-deployment verb: skip under `charly check box` (no running VNC server on a
	// disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("vnc: %s requires a running deployment (skip under charly check box)", method))
	}
	// Resolve the dialable RFB endpoint via the GENERIC VM-graphics reverse-leg
	// (cc.ResolveGraphicsEndpoint) — the host owns the venue/podman/libvirt resolution + any
	// bridge/SSH tunnel + the credential-store password, venue-aware (container 5900 or VM
	// <graphics type='vnc'>). Replaces the former host-side vnc preresolver.
	cc, err := sdk.NewCheckContext(req.GetExecutorBrokerId(), req.GetEnvJson())
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("vnc: %s: %v", method, err))
	}
	ge, err := cc.ResolveGraphicsEndpoint(ctx, "vnc")
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("vnc: %s: %v", method, err))
	}
	// N/A: a VM that declares no VNC display device.
	if ge.Skip {
		return sdk.ResultJSON("skip", fmt.Sprintf("vnc %s — N/A: %s", method, ge.SkipMessage))
	}
	// No live deployment context (no-box) → skip, the analogue of the host's empty-box skip.
	// vnc always resolves to a TCP Addr (the host bridges any UNIX socket).
	if ge.Addr == "" {
		return sdk.ResultJSON("skip", fmt.Sprintf("vnc: %s has no resolved VNC endpoint (box=%q)", method, env.Box))
	}
	ep := &vncEndpoint{Addr: ge.Addr, Password: ge.Password}

	out, runErr := dispatch(ep, &op, &in)

	// The shared exit/stdout/stderr + screenshot-artifact verdict pipeline (R3). screenshot is
	// vnc's one artifact-producing method.
	return sdk.VerbVerdict("vnc", method, out, runErr, &op, method == "screenshot")
}
