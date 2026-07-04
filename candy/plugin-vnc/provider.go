package vnc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/sdk"
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

// vncEndpoint is the plugin-side decode of the host-resolved VncEnv (charly/
// vnc_preresolve.go). Addr is the host-reachable "host:port" the plugin dials over TCP
// (a container's published 5900, or a VM's bridged/forwarded RFB address); Password is
// the resolved VNC ticket ("" = no auth / VeNCrypt-None). The plugin needs no podman /
// venue / libvirt resolution — it just dials this.
type vncEndpoint struct {
	Addr     string `json:"addr"`
	Password string `json:"password"`
}

// vncEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env for a
// `vnc:` check step (provider_checkenv.go). Box/Mode mirror the shared CheckEnv; Vnc
// carries the host-resolved endpoint (nil when the host could not resolve one — e.g. no
// vnc op, no live deployment, a VM with no VNC display device).
type vncEnv struct {
	Box       string          `json:"box"`
	Mode      string          `json:"mode"` // "live" | "box"
	Substrate json.RawMessage `json:"substrate"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `vnc:` operation. It decodes the full #Op + the env, skips in box mode
// (no live VNC endpoint on a disposable `charly check box`), skips a nil endpoint, dials
// the pre-resolved RFB endpoint, dispatches the method, and self-evaluates the matchers +
// screenshot artifact validators.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return resultJSON("fail", "vnc: decode op: "+err.Error())
		}
	}
	var env vncEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	// The host's verb preresolver ships the dialable RFB endpoint in the opaque
	// CheckEnv.Substrate (the generic per-verb channel that replaced the typed
	// CheckEnv.Vnc field); decode it into the plugin's own endpoint type.
	var ep *vncEndpoint
	if len(env.Substrate) > 0 {
		var e vncEndpoint
		if err := json.Unmarshal(env.Substrate, &e); err == nil {
			ep = &e
		}
	}
	method := string(op.Vnc)

	// Live-deployment verb: skip under `charly check box` (no running VNC server on a
	// disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return resultJSON("skip", fmt.Sprintf("vnc: %s requires a running deployment (skip under charly check box)", method))
	}
	// No endpoint resolved → skip. The host already FAILs the resolution-error case (and
	// SKIPs the "VM declares no VNC display device" case) before dispatch, so a nil
	// endpoint here means no live deployment context at all (the analogue of
	// the host's empty-box skip).
	if ep == nil {
		return resultJSON("skip", fmt.Sprintf("vnc: %s has no resolved VNC endpoint (box=%q)", method, env.Box))
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
		return resultJSON("fail", fmt.Sprintf("vnc: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, preview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return resultJSON("fail", fmt.Sprintf("vnc: %s: stdout: %v (got: %s)", method, err, preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return resultJSON("fail", fmt.Sprintf("vnc: %s: stderr: %v (got: %s)", method, err, preview(stderr)))
	}

	// Artifact validators run for the artifact-producing screenshot method.
	if method == "screenshot" {
		if err := sdk.RunArtifactValidators(&op); err != nil {
			return resultJSON("fail", fmt.Sprintf("vnc: %s: %v", method, err))
		}
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("vnc %s: exit=%d", method, exit)
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
