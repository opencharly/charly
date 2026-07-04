package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// provider.go is the out-of-process provider for BOTH capabilities the plugin serves
// (F1). Invoke branches on the request class: a "deploy" op drives the `deploy:android`
// SUBSTRATE lifecycle (deploy.go — gate on boot, install the host-preresolved apk specs);
// every other op is the `adb:` check VERB. For the verb, charly's host dispatches an
// `adb:` check step through the registry (ResolveVerb("adb") → this grpcProvider →
// Provider.Invoke) with the FULL #Op marshaled as params_json and a CheckEnv snapshot as
// env. Because the out-of-process verb path does NOT run a host-side matcher pipeline,
// invokeVerb OWNS the whole verdict: dispatch the method, then evaluate the
// stdout/stderr/exit_status matchers + artifact validators itself (via the shared sdk
// implementation — R3), and return the wire {status,message} the host decodes.

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

// Invoke runs one operation for the plugin's capabilities. The plugin serves BOTH
// the `adb:` check verb AND the `deploy:android` SUBSTRATE (F1), distinguished by the
// request's class: a "deploy" op drives the substrate install lifecycle (deploy.go);
// every other op is the adb verb.
func (p provider) Invoke(ctx context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	if req.GetClass() == "deploy" {
		return invokeDeployAndroid(req)
	}
	return p.invokeVerb(ctx, req)
}

// invokeVerb runs one `adb:` verb operation. It decodes the full #Op + the env, skips
// in box mode (these probes need a running container with a host-mapped adb port),
// dispatches the method, and self-evaluates the matchers + artifact validators.
func (provider) invokeVerb(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return resultJSON("fail", "adb: decode op: "+err.Error())
		}
	}
	var env adbEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	method := string(op.Adb)

	// Live-container verb: skip under `charly check box` (no host-mapped adb port
	// on a disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return resultJSON("skip", fmt.Sprintf("adb: %s requires a running container (skip under charly check box)", method))
	}
	// No device context at all (no resolved adb addr, no container) → skip, the
	// check-verb analogue of the host's empty-box skip. The deploy/status
	// seams always set AdbAddr, so they never hit this.
	if env.AdbAddr == "" && env.inPodContainer() == "" {
		return resultJSON("skip", fmt.Sprintf("adb: %s has no device context (box=%q)", method, env.Box))
	}

	out, runErr := dispatch(&env, &op)

	// Exit semantics: the in-tree CLI mapped a Run() error → exit 1, success →
	// exit 0; the host compared that against the authored exit_status (default 0).
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
		return resultJSON("fail", fmt.Sprintf("adb: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, preview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return resultJSON("fail", fmt.Sprintf("adb: %s: stdout: %v (got: %s)", method, err, preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return resultJSON("fail", fmt.Sprintf("adb: %s: stderr: %v (got: %s)", method, err, preview(stderr)))
	}

	// Artifact validators run for the one artifact-producing method (screencap).
	if method == "screencap" {
		if err := sdk.RunArtifactValidators(&op); err != nil {
			return resultJSON("fail", fmt.Sprintf("adb: %s: %v", method, err))
		}
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("adb %s: exit=%d", method, exit)
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
