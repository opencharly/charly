package record

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/charly/candy/plugin-record/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// provider.go is the out-of-process record verb provider — charly's host dispatches a
// `record:` check step to it through the registry (ResolveVerb("record") → this
// grpcProvider → invokeVerbProvider) with the FULL #Op marshaled as params_json, a
// CheckEnv snapshot as env, AND — because record is EXEC-based — the host's live
// DeployExecutor attached over the E3b reverse channel (the executorInvoker branch in
// invokeVerbProvider). Because the out-of-process path does NOT run a host-side
// matcher pipeline, this Invoke OWNS the whole verdict: get the venue
// executor (sdk.ExecutorFromInvoke), dispatch the method (RunCapture-driven; `stop` also
// GetFile-pulls the recording to op.Artifact), then evaluate the stdout/stderr/exit_status
// matchers + the artifact validators itself (via the shared sdk implementation — R3), and
// return the wire {status,message} the host decodes.

// recordEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for
// a `record:` check step (provider_checkenv.go). The fields mirror the shared CheckEnv;
// record reads Mode to skip box-context runs and carries Box/ContainerName/Venue/VenueKind
// for messages — the actual venue work travels over the executor reverse channel, not this
// snapshot (unlike the PORT-based mcp/spice verbs, which carry a pre-resolved endpoint).
type recordEnv struct {
	Box           string `json:"box"`
	Mode          string `json:"mode"` // "live" | "box"
	ContainerName string `json:"container_name"`
	Venue         string `json:"venue"`
	VenueKind     string `json:"venue_kind"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `record:` operation. It decodes the full #Op, the typed plugin input
// (params.RecordInput — the per-verb fields live in the desugared plugin_input since the
// schema-compaction cutover), and the env, skips in box mode (no running deployment to
// record on a disposable `charly check box`), dials back the host's live executor over
// the reverse channel (a missing broker is a HARD FAIL — record needs the venue),
// dispatches the method, and self-evaluates the matchers + the artifact validators
// (`stop`).
func (p provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "record: decode op: "+err.Error())
		}
	}
	var in params.RecordInput
	kit.DecodeInput(op.PluginInput, &in)
	var env recordEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := in.Method

	// Live-deployment verb: skip under `charly check box` (no running deployment to record
	// in a disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("record: %s requires a running deployment (skip under charly check box)", method))
	}

	// record is EXEC-based: it drives the venue (asciinema/wf-recorder via tmux, the
	// .cast/.mp4 pull) ONLY through the host's live executor over the E3b reverse channel.
	// A missing broker is therefore a HARD FAIL with a clear message, never a silent skip —
	// the verb cannot do its job without the venue.
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("record: %s has no host executor attached — record needs the live venue (%v)", method, err))
	}

	out, runErr := dispatch(ctx, exec, &op, &in)

	// The shared exit/stdout/stderr + artifact verdict pipeline (R3). The artifact-producing
	// method (`stop`) already GetFile-pulled the recording to the input's artifact path inside
	// dispatch, so a non-empty in.Artifact is the artifact gate (a no-op for list/start/cmd).
	return sdk.VerbVerdict("record", method, out, runErr, &op, in.Artifact != "")
}
