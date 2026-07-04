package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// provider.go is the out-of-process wl verb provider — charly's host dispatches a `wl:`
// check step to it through the registry (ResolveVerb("wl") → this grpcProvider →
// invokeVerbProvider) with the FULL #Op marshaled as params_json, a CheckEnv snapshot as
// env, AND — because wl is EXEC-based — the host's live DeployExecutor attached over the
// E3b reverse channel (the executorInvoker branch in invokeVerbProvider). Because the
// out-of-process path does NOT run a host-side matcher pipeline, this Invoke
// OWNS the whole verdict: get the venue executor (sdk.ExecutorFromInvoke), dispatch the
// method (RunCapture-driven; `screenshot` also GetFile-pulls the PNG to op.Artifact), then
// evaluate the stdout/stderr/exit_status matchers + the artifact validators itself (via the
// shared sdk implementation — R3), and return the wire {status,message} the host decodes.

// wlEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for a
// `wl:` check step (provider_checkenv.go). The fields mirror the shared CheckEnv; wl reads
// Mode to skip box-context runs and carries Box/ContainerName/Venue/VenueKind for messages —
// the actual venue work travels over the executor reverse channel, not this snapshot (unlike
// the PORT-based mcp/spice/cdp/vnc verbs, which carry a pre-resolved endpoint).
type wlEnv struct {
	Box           string `json:"box"`
	Mode          string `json:"mode"` // "live" | "box"
	ContainerName string `json:"container_name"`
	Venue         string `json:"venue"`
	VenueKind     string `json:"venue_kind"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `wl:` operation. It decodes the full #Op + the env, skips in box mode
// (no running desktop to drive on a disposable `charly check box`), dials back the host's
// live executor over the reverse channel (a missing broker is a HARD FAIL — wl needs the
// venue), dispatches the method, and self-evaluates the matchers + the artifact validators
// (`screenshot`).
func (p provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "wl: decode op: "+err.Error())
		}
	}
	var env wlEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := string(op.Wl)

	// Live-deployment verb: skip under `charly check box` (no running desktop with a
	// compositor in a disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("wl: %s requires a running deployment (skip under charly check box)", method))
	}

	// wl is EXEC-based: it drives the venue's compositor (wlrctl/grim/wtype/swaymsg/… and
	// the screenshot pull) ONLY through the host's live executor over the E3b reverse
	// channel. A missing broker is therefore a HARD FAIL with a clear message, never a
	// silent skip — the verb cannot do its job without the venue.
	exec, err := sdk.ExecutorFromInvoke(req.GetExecutorBrokerId())
	if err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("wl: %s has no host executor attached — wl needs the live venue (%v)", method, err))
	}

	out, runErr := dispatch(ctx, exec, &op)

	// Exit semantics: the in-tree CLI mapped a Run() error → exit 1, success → exit 0; the
	// host compared that against the authored exit_status (default 0).
	exit := 0
	stderr := ""
	if runErr != nil {
		exit = 1
		stderr = runErr.Error()
	}
	wantExit := 0
	if op.ExitStatus != nil {
		wantExit = *op.ExitStatus
	}
	if exit != wantExit {
		return sdk.ResultJSON("fail", fmt.Sprintf("wl: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, kit.TrimPreview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("wl: %s: stdout: %v (got: %s)", method, err, kit.TrimPreview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return sdk.ResultJSON("fail", fmt.Sprintf("wl: %s: stderr: %v (got: %s)", method, err, kit.TrimPreview(stderr)))
	}

	// Artifact-producing method (`screenshot`): the PNG was already GetFile-pulled to
	// op.Artifact (the host path) inside dispatch, BEFORE this point, so the validators
	// (min_bytes / min_dimensions / not_uniform) read a real file. A no-op for every other
	// method (op.Artifact empty), mirroring the host's post-run pipeline.
	if op.Artifact != "" {
		if err := sdk.RunArtifactValidators(&op); err != nil {
			return sdk.ResultJSON("fail", fmt.Sprintf("wl: %s: artifact: %v", method, err))
		}
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("wl %s: exit=%d", method, exit)
	}
	return sdk.ResultJSON("pass", body)
}
