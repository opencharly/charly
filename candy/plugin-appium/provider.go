package appium

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// provider.go is the out-of-process appium verb provider — charly's host dispatches an
// `appium:` check step to it through the registry (ResolveVerb("appium") → this
// grpcProvider → Provider.Invoke) with the FULL #Op marshaled as params_json and a
// CheckEnv snapshot as env. Because the out-of-process path does NOT run a host-side
// matcher pipeline, this Invoke OWNS the whole verdict: dispatch the
// method, then evaluate the stdout/stderr/exit_status matchers + artifact validators
// itself (via the shared sdk matcher implementation — R3), and return the wire
// {status,message} the host's invokeVerbProvider decodes.

// checkEnv is the plugin-side decode of charly's CheckEnv (provider_checkenv.go) — the
// serializable invocation context the host ships as Operation.Env. ContainerName is the
// host-authoritative container name (charly-<box>[_<instance>], registry-ref-stripped)
// the plugin uses to reach the running Appium server.
type checkEnv struct {
	Box           string `json:"box"`
	Instance      string `json:"instance"`
	Mode          string `json:"mode"` // "live" | "box"
	ContainerName string `json:"container_name"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `appium:` check step. It decodes the full #Op + the check env, skips
// in box mode (these probes need a running container with port mappings), dispatches the
// method, and self-evaluates the matchers + artifact validators.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return sdk.ResultJSON("fail", "appium: decode op: "+err.Error())
		}
	}
	var env checkEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := string(op.Appium)

	// Live-container verb: skip under `charly check box` (no port mappings on a
	// disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return sdk.ResultJSON("skip", fmt.Sprintf("appium: %s requires a running container (skip under charly check box)", method))
	}
	if env.Box == "" {
		return sdk.ResultJSON("skip", fmt.Sprintf("appium: %s has no image context", method))
	}

	out, runErr := dispatch(&env, &op)

	// The shared exit/stdout/stderr + screenshot-artifact verdict pipeline (R3). screenshot is
	// appium's one artifact-producing method.
	return sdk.VerbVerdict("appium", method, out, runErr, &op, method == "screenshot")
}
