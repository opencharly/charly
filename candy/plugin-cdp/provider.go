package cdp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/charly/candy/plugin-cdp/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// provider.go is the out-of-process cdp verb provider — charly's host dispatches a
// `cdp:` check step to it through the registry (ResolveVerb("cdp") → this grpcProvider →
// Provider.Invoke) with the FULL #Op marshaled as params_json and a CheckEnv snapshot as
// env. Because the out-of-process path does NOT run a host-side matcher
// pipeline, this Invoke OWNS the whole verdict: read the host-pre-resolved DevTools URL
// (the host owns the podman / venue / port-mapping resolution), dispatch the method (the
// /json HTTP surface for status/open/list/close; the per-tab CDP WebSocket for the rest),
// then evaluate the stdout/stderr/exit_status matchers + the screenshot artifact validators
// itself (via the shared sdk implementation — R3), and return the wire {status,message}
// the host decodes.

// cdpEndpoint is the plugin-side decode of the host-resolved CdpEnv
// (charly/cdp_preresolve.go). URL is the host-reachable DevTools base URL the plugin
// dials. The plugin needs no podman / venue resolution — it just reads this.
type cdpEndpoint struct {
	URL string `json:"url"`
}

// cdpEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for a
// `cdp:` check step (provider_checkenv.go). Box/Mode mirror the shared CheckEnv; Cdp
// carries the host-resolved DevTools endpoint (nil when the host could not resolve one —
// e.g. no cdp op, no live deployment).
type cdpEnv struct {
	Box       string          `json:"box"`
	Mode      string          `json:"mode"` // "live" | "box"
	Substrate json.RawMessage `json:"substrate"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `cdp:` operation. It decodes the full #Op, the typed plugin
// input (params.CdpInput — the per-verb fields live in the desugared
// plugin_input since the schema-compaction cutover), and the env, skips in box
// mode (no live Chrome DevTools endpoint on a disposable `charly check box`),
// skips a nil endpoint, dispatches the method, and self-evaluates the matchers +
// screenshot artifact validators.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "cdp: decode op: "+err.Error())
		}
	}
	var in params.CdpInput
	kit.DecodeInput(op.PluginInput, &in)
	var env cdpEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	// The host's verb preresolver ships the dialable DevTools endpoint in the opaque
	// CheckEnv.Substrate (the generic per-verb channel that replaced the typed
	// CheckEnv.Cdp field); decode it into the plugin's own endpoint type.
	var ep *cdpEndpoint
	if len(env.Substrate) > 0 {
		var e cdpEndpoint
		if err := json.Unmarshal(env.Substrate, &e); err == nil {
			ep = &e
		}
	}
	method := in.Method

	// Live-deployment verb: skip under `charly check box` (no running Chrome DevTools
	// endpoint on a disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("cdp: %s requires a running deployment (skip under charly check box)", method))
	}
	// No endpoint resolved → skip. The host already FAILs the resolution-error case before
	// dispatch, so a nil endpoint here means no live deployment context at all (the
	// analogue of the host's empty-box skip).
	if ep == nil {
		return sdk.ResultJSON("skip", fmt.Sprintf("cdp: %s has no resolved DevTools endpoint (box=%q)", method, env.Box))
	}

	out, runErr := dispatch(ep, &op, &in)

	// The shared exit/stdout/stderr + screenshot-artifact verdict pipeline (R3). screenshot is
	// cdp's one artifact-producing method.
	return sdk.VerbVerdict("cdp", method, out, runErr, &op, method == "screenshot")
}
