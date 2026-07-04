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

// provider.go is the out-of-process spice verb provider — charly's host dispatches a
// `spice:` check step to it through the registry (ResolveVerb("spice") → this
// grpcProvider → Provider.Invoke) with the FULL #Op marshaled as params_json and a
// CheckEnv snapshot as env. Because the out-of-process path does NOT run a host-side
// matcher pipeline, this Invoke OWNS the whole verdict: DIAL the
// host-pre-resolved SPICE endpoint (the host owns the go-libvirt VM resolution +
// any qemu+ssh:// tunnel), dispatch the method, then evaluate the stdout/stderr/
// exit_status matchers + the artifact validators itself (via the shared sdk
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

// spiceEndpoint is the host-pre-resolved, DIALABLE SPICE endpoint. Exactly one of
// Socket / Address is set (the host prefers the UNIX socket; for a remote qemu+ssh://
// VM the host opens the side tunnel and hands back the forwarded LOCAL address). The
// plugin needs no libvirt / SSH machinery — it just dials this.
type spiceEndpoint struct {
	Address  string `json:"address"`  // "host:port" for a TCP listener (or forwarded-local TCP)
	Socket   string `json:"socket"`   // UNIX socket path (or forwarded-local socket)
	Password string `json:"password"` // SPICE ticket; empty = AUTH_NONE
}

// spiceEnv is the plugin-side decode of the CheckEnv the host ships as Operation.Env
// for a `spice:` check step (provider_checkenv.go). Box/Mode mirror the shared
// CheckEnv; Spice carries the host-resolved endpoint (nil when the host could not
// resolve one — e.g. no spice op, no VM context).
type spiceEnv struct {
	Box       string          `json:"box"`
	Mode      string          `json:"mode"` // "live" | "box"
	Substrate json.RawMessage `json:"substrate"`
}

type provider struct{ pb.UnimplementedProviderServer }

// Invoke runs one `spice:` operation. It decodes the full #Op + the env, skips in
// box mode (no live VM SPICE endpoint on a disposable `charly check box`), dials the
// pre-resolved endpoint, dispatches the method, and self-evaluates the matchers +
// artifact validators.
func (provider) Invoke(_ context.Context, req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	var op spec.Op
	if len(req.GetParamsJson()) > 0 {
		if err := json.Unmarshal(req.GetParamsJson(), &op); err != nil {
			return resultJSON("fail", "spice: decode op: "+err.Error())
		}
	}
	var env spiceEnv
	if len(req.GetEnvJson()) > 0 {
		_ = json.Unmarshal(req.GetEnvJson(), &env)
	}
	// The host's verb preresolver ships the dialable SPICE endpoint in the opaque
	// CheckEnv.Substrate (the generic per-verb channel that replaced the typed
	// CheckEnv.Spice field); decode it into the plugin's own endpoint type.
	var ep *spiceEndpoint
	if len(env.Substrate) > 0 {
		var e spiceEndpoint
		if err := json.Unmarshal(env.Substrate, &e); err == nil {
			ep = &e
		}
	}
	method := string(op.Spice)

	// Live-VM verb: skip under `charly check box` (no running VM SPICE endpoint on a
	// disposable `podman run --rm`) — mirrors the host's RunModeBox/box-mode skip.
	if env.Mode == "box" {
		return resultJSON("skip", fmt.Sprintf("spice: %s requires a running VM (skip under charly check box)", method))
	}
	// No endpoint resolved → skip. The host already decided a SKIP for the "VM
	// declares no SPICE device" case (returning the N/A result before dispatch), so a
	// nil endpoint here means no VM context at all (the analogue of the host's
	// empty-box skip).
	if ep == nil {
		return resultJSON("skip", fmt.Sprintf("spice: %s has no VM SPICE endpoint (box=%q)", method, env.Box))
	}

	s, dialErr := dialEndpoint(ep)
	if dialErr != nil {
		return resultJSON("fail", fmt.Sprintf("spice: %s: %v", method, dialErr))
	}

	out, runErr := dispatch(s, &op)

	// Exit semantics: the in-tree CLI mapped a Run() error → exit 1, success → exit 0;
	// the host compared that against the authored exit_status (default 0).
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
		return resultJSON("fail", fmt.Sprintf("spice: %s: exit=%d, want %d (stderr: %s)", method, exit, wantExit, preview(stderr)))
	}

	if err := sdk.MatchAll(out, op.Stdout); err != nil {
		return resultJSON("fail", fmt.Sprintf("spice: %s: stdout: %v (got: %s)", method, err, preview(out)))
	}
	if err := sdk.MatchAll(stderr, op.Stderr); err != nil {
		return resultJSON("fail", fmt.Sprintf("spice: %s: stderr: %v (got: %s)", method, err, preview(stderr)))
	}

	// Artifact validators run for the two artifact-producing methods.
	if method == "screenshot" || method == "cursor" {
		if err := sdk.RunArtifactValidators(&op); err != nil {
			return resultJSON("fail", fmt.Sprintf("spice: %s: %v", method, err))
		}
	}

	body := out
	if strings.TrimSpace(body) == "" {
		body = stderr
	}
	if strings.TrimSpace(body) == "" {
		body = fmt.Sprintf("spice %s: exit=%d", method, exit)
	}
	return resultJSON("pass", body)
}

// dialEndpoint opens a SPICE session against the host-pre-resolved endpoint —
// preferring the UNIX socket, falling back to the TCP address.
func dialEndpoint(ep *spiceEndpoint) (*SpiceSession, error) {
	if ep.Socket != "" {
		return DialSpiceUnix(ep.Socket, ep.Password)
	}
	if ep.Address == "" {
		return nil, fmt.Errorf("no SPICE address or socket in endpoint")
	}
	host, port, err := splitHostPort(ep.Address)
	if err != nil {
		return nil, err
	}
	return DialSpiceTCP(host, port, ep.Password)
}

// splitHostPort splits a "host:port" address into its parts. IPv6 is not a concern
// here — the host always resolves to 127.0.0.1 / a forwarded loopback address.
func splitHostPort(addr string) (string, int, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("address %q is not host:port", addr)
	}
	host := addr[:i]
	var port int
	if _, err := fmt.Sscanf(addr[i+1:], "%d", &port); err != nil || port <= 0 {
		return "", 0, fmt.Errorf("invalid port in address %q", addr)
	}
	return host, port, nil
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
