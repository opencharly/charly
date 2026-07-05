package dbus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// provider.go is the out-of-process dbus verb provider — charly's host dispatches a `dbus:`
// check step to it through the registry (ResolveVerb("dbus") → this grpcProvider →
// invokeVerbProvider) with the FULL #Op marshaled as params_json, a CheckEnv snapshot as
// env, AND — because dbus is EXEC-based — the host's live DeployExecutor attached over the
// E3b reverse channel (the executorInvoker branch in invokeVerbProvider). Because the
// out-of-process path does NOT run a host-side matcher pipeline, this Invoke
// OWNS the whole verdict: get the venue executor (sdk.ExecutorFromInvoke), dispatch the
// method (RunCapture-driven gdbus), then evaluate the stdout/stderr/exit_status matchers
// itself (via the shared sdk implementation — R3), and return the wire {status,message} the
// host decodes.

// dbusEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for a
// `dbus:` check step (provider_checkenv.go). The fields mirror the shared CheckEnv; dbus
// reads Mode to skip box-context runs and carries Box/ContainerName/Venue/VenueKind for
// messages — the actual venue work travels over the executor reverse channel, not this
// snapshot (unlike the PORT-based mcp/spice/cdp/vnc verbs, which carry a pre-resolved
// endpoint).
type dbusEnv struct {
	Box           string `json:"box"`
	Mode          string `json:"mode"` // "live" | "box"
	ContainerName string `json:"container_name"`
	Venue         string `json:"venue"`
	VenueKind     string `json:"venue_kind"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `dbus:` operation. It decodes the full #Op + the env, skips in box mode
// (no running deployment to probe on a disposable `charly check box`), dials back the host's
// live executor over the reverse channel (a missing broker is a HARD FAIL — dbus needs the
// venue), dispatches the method, and self-evaluates the matchers.
func (p provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "dbus: decode op: "+err.Error())
		}
	}
	var env dbusEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := string(op.Dbus)

	// Live-deployment verb: skip under `charly check box` (no running deployment with a
	// session bus in a disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("dbus: %s requires a running deployment (skip under charly check box)", method))
	}

	// dbus is EXEC-based: it drives the venue's session bus (gdbus list/call/introspect/notify)
	// ONLY through the host's live executor over the E3b reverse channel. A missing broker is
	// therefore a HARD FAIL with a clear message, never a silent skip — the verb cannot do its
	// job without the venue.
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("dbus: %s has no host executor attached — dbus needs the live venue (%v)", method, err))
	}

	out, runErr := dispatch(ctx, exec, &op)

	// The shared exit/stdout/stderr verdict pipeline (R3). dbus produces no artifact.
	return sdk.VerbVerdict("dbus", method, out, runErr, &op, false)
}
