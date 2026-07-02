package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/opencharly/charly/charly/plugin/proto"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
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

// pluginResult is the wire form a verb provider returns (the host's pluginCheckResult).
type pluginResult struct {
	Status  string `json:"status"` // "pass" | "fail" | "skip"
	Message string `json:"message"`
}

func resultJSON(status, msg string) (*pb.InvokeReply, error) {
	j, err := json.Marshal(pluginResult{Status: status, Message: msg})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `appium:` check step. It decodes the full #Op + the check env, skips
// in box mode (these probes need a running container with port mappings), dispatches the
// method, and self-evaluates the matchers + artifact validators.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return resultJSON("fail", "appium: decode op: "+err.Error())
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
		return resultJSON("skip", fmt.Sprintf("appium: %s requires a running container (skip under charly check box)", method))
	}
	if env.Box == "" {
		return resultJSON("skip", fmt.Sprintf("appium: %s has no image context", method))
	}

	out, runErr := dispatch(&env, &op)

	// Exit semantics: the in-tree CLI mapped a Run() error → exit 1, success → exit 0;
	// the host compared that against the authored exit_status (default 0). Preserve it.
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
		return resultJSON("fail", fmt.Sprintf("appium: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, preview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return resultJSON("fail", fmt.Sprintf("appium: %s: stdout: %v (got: %s)", method, err, preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return resultJSON("fail", fmt.Sprintf("appium: %s: stderr: %v (got: %s)", method, err, preview(stderr)))
	}

	// Artifact validators run for the one artifact-producing method (screenshot) —
	// mirrors the host's spec.artifact branch. The validators are the SHARED
	// SDK implementation (sdk.RunArtifactValidators), the ONE copy every artifact-
	// producing verb plugin reuses (R3).
	if method == "screenshot" {
		if err := sdk.RunArtifactValidators(&op); err != nil {
			return resultJSON("fail", fmt.Sprintf("appium: %s: %v", method, err))
		}
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("appium %s: exit=%d", method, exit)
	}
	return resultJSON("pass", body)
}

// preview trims long output for an error message (mirrors charly's trimPreview).
func preview(s string) string {
	s = strings.TrimSpace(s)
	const max = 400
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
