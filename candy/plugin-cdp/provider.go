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

// Invoke runs one `cdp:` operation. It decodes the full #Op + the env, skips in box mode
// (no live Chrome DevTools endpoint on a disposable `charly check box`), skips a nil
// endpoint, dispatches the method, and self-evaluates the matchers + screenshot artifact
// validators.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return resultJSON("fail", "cdp: decode op: "+err.Error())
		}
	}
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
	method := string(op.Cdp)

	// Live-deployment verb: skip under `charly check box` (no running Chrome DevTools
	// endpoint on a disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return resultJSON("skip", fmt.Sprintf("cdp: %s requires a running deployment (skip under charly check box)", method))
	}
	// No endpoint resolved → skip. The host already FAILs the resolution-error case before
	// dispatch, so a nil endpoint here means no live deployment context at all (the
	// analogue of the host's empty-box skip).
	if ep == nil {
		return resultJSON("skip", fmt.Sprintf("cdp: %s has no resolved DevTools endpoint (box=%q)", method, env.Box))
	}

	out, runErr := dispatch(ep, &op)

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
		return resultJSON("fail", fmt.Sprintf("cdp: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, preview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return resultJSON("fail", fmt.Sprintf("cdp: %s: stdout: %v (got: %s)", method, err, preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return resultJSON("fail", fmt.Sprintf("cdp: %s: stderr: %v (got: %s)", method, err, preview(stderr)))
	}

	// Artifact validators run for the artifact-producing screenshot method.
	if method == "screenshot" {
		if err := sdk.RunArtifactValidators(&op); err != nil {
			return resultJSON("fail", fmt.Sprintf("cdp: %s: %v", method, err))
		}
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("cdp %s: exit=%d", method, exit)
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
