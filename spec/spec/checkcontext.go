package spec

// checkcontext.go — the check-engine CONTRACT cluster (#55 CHECK-ENGINE cone, Unit 1a).
//
// These are the host↔candy check-verb contract types: the CheckContext live-engine
// surface a host-coupled verb's RunVerb consumes, the CheckVerbProvider a candy
// implements, the narrow venue CheckExecutor, and the in-process value structs the
// contract carries (CheckRunMode, CheckVerbResult, CheckHTTPRequest/Response,
// CheckGraphicsEndpoint). They live in the spec contract module — exactly like
// DeployExecutor (deploy_executor.go) and CheckResult/CheckEnv — because charly core's
// reverse-channel verb dispatch references the contract while importing ONLY spec, and
// sdk/kit + every host-coupled check-verb candy implement it. sdk/kit re-exports each as
// a type alias (kit.go) so the candy call sites compile UNCHANGED.
//
// These types are IN-PROCESS: none is the marshaled gRPC payload. The wire forms are the
// separate proto messages (pb.HTTPDoRequest/Reply, pb.ResolveGraphicsEndpointReply, …,
// hand-mapped field-by-field in charly's checkContextReverseServer) plus the CUE-sourced
// spec.CheckEnv snapshot (the run mode crosses as its "box"/"live" string) — so the
// contract needs no CUE source, matching the hand-Go DeployExecutor precedent.

import (
	"context"
	"time"
)

// CheckRunMode is the mode a check runs under (charly's RunMode). It never crosses the
// wire — spec.CheckEnv carries the "box"/"live" string form — so it is a plain Go int.
type CheckRunMode int

const (
	// CheckModeLive — `charly check live`, against a running container/VM (in-container probes).
	CheckModeLive CheckRunMode = iota
	// CheckModeBox — `charly check box`, against a disposable build container.
	CheckModeBox
)

// String renders the mode as "box" / "live" (mirrors charly's runModeName).
func (m CheckRunMode) String() string {
	if m == CheckModeBox {
		return "box"
	}
	return "live"
}

// CheckExecutor is the subset of DeployExecutor a check verb needs: run one
// command/script on the venue and capture stdout/stderr/exit separately. DeployExecutor
// satisfies this structurally (RunCapture + Kind have identical signatures), so
// *Runner.Exec is passed straight through.
type CheckExecutor interface {
	// RunCapture runs a shell command/script on the venue, returning stdout,
	// stderr, the exit code, and any execution error (NOT a non-zero exit — that
	// is reported via the exit code). No root escalation; callers add sudo.
	RunCapture(ctx context.Context, script string) (stdout, stderr string, exit int, err error)
	// Kind classifies the venue: "host" | "container" | "image" | "vm".
	Kind() string
}

// CheckGraphicsEndpoint is the resolved, dialable VM graphics endpoint a vnc/spice verb gets
// from CheckContext.ResolveGraphicsEndpoint. Exactly one of Addr / Socket is set (the host
// bridges a UNIX socket to TCP for a TCP-only client, or forwards a remote listener, before
// returning). Password is the resolved ticket ("" = no auth). Skip=true (with SkipMessage)
// means the deployment declares no graphics device of that kind — an N/A skip, not a failure.
type CheckGraphicsEndpoint struct {
	Addr        string
	Socket      string
	Password    string
	Skip        bool
	SkipMessage string
}

// CheckContext is the live check-engine surface a host-coupled verb's RunVerb
// consumes. charly's *Runner implements it; a candy reaches the running deployment
// through it without importing charly's package main.
type CheckContext interface {
	// Exec runs commands on the venue (in-container under CheckModeLive, in a disposable
	// container under CheckModeBox, or host-side depending on the executor).
	Exec() CheckExecutor
	// Mode is the run mode (Live vs Box).
	Mode() CheckRunMode
	// HTTPDo issues an HTTP request from the CHARLY HOST's network namespace, applying
	// the per-request TLS / redirect / CA policy in req, and returns the status, body, and
	// response headers. It REPLACES the former HTTPClient() *http.Client leg: an
	// *http.Client cannot cross a process boundary, so out-of-process the REQUEST crosses
	// (CheckContextService.HTTPDo) and the host dials; in-process the host builds the client
	// and dials directly. The transport-level error is returned as err (a non-2xx is NOT an
	// error — the caller matches resp.Status).
	HTTPDo(ctx context.Context, req CheckHTTPRequest) (CheckHTTPResponse, error)
	// ResolveEndpoint resolves the check target's venue (container / VM / ssh / local) and
	// returns a host-reachable "host:port" address for an in-venue TCP port, opening (and
	// host-side tracking, for teardown after this verb's Invoke) any ssh -L forward a VM/ssh
	// venue needs. An endpoint verb (cdp/vnc/spice/…) declares its in-venue port and dials
	// the returned addr — REPLACING the per-verb host preresolvers: the host owns the
	// venue/podman/go-libvirt machinery the out-of-process plugin lacks. Empty addr with a
	// nil error means "no live venue" (box-mode / no-box) — the verb's own no-endpoint skip
	// then fires; a resolution failure is returned as err.
	ResolveEndpoint(ctx context.Context, port int) (addr string, err error)
	// ResolveGraphicsEndpoint resolves a VM's <graphics type='<kind>'> listener (kind =
	// "vnc" | "spice") to a dialable endpoint, opening (and host-side tracking, for teardown
	// after this verb's Invoke) any ssh -L forward + socket->TCP bridge the venue needs. A
	// graphics verb (vnc/spice) calls it instead of the removed per-verb host preresolver;
	// the host owns the go-libvirt resolution, tunnel, bridge, and credential-store password.
	// CheckGraphicsEndpoint.Skip=true means the deployment declares no graphics device of that
	// kind (an N/A skip). A zero CheckGraphicsEndpoint with a nil error means no live VM context.
	ResolveGraphicsEndpoint(ctx context.Context, kind string) (CheckGraphicsEndpoint, error)
	// ResolveImageLabel reads one OCI label value off the deployment-under-test's image — the
	// host owns the podman engine + container→image resolution the out-of-process plugin cannot
	// reach. A verb declares the label it needs (mcp reads ai.opencharly.mcp_provide) and parses
	// the returned value; an empty string means the label is absent on the image.
	ResolveImageLabel(ctx context.Context, label string) (value string, err error)
	// DialTimeout is the per-dial ceiling for host-side TCP reachability probes.
	DialTimeout() time.Duration
	// Box / Instance are the deployment's image + instance names (empty under CheckModeBox).
	Box() string
	Instance() string
	// Distros is the image's distro tag list (e.g. ["fedora:43","fedora"]) for
	// distro-specific package-name resolution.
	Distros() []string
	// AddBackground registers a host-side background process PID with the active plan run
	// so plan teardown reaps it (SIGTERM). A no-op when the engine has no scenario context
	// (a bare-Op run) or pid<=0. Used by a verb that fire-and-forgets a host process
	// (the `command` verb's background path).
	AddBackground(pid int)
}

// CheckHTTPRequest is the host-vantage HTTP request a check verb hands cc.HTTPDo. It carries
// the FULL request plus the per-request policy the host needs to build the client: Timeout
// is a Go duration string ("" = the engine's base timeout); CAPEM is the resolved CA PEM
// bytes (a candy reads its authored ca_file host-side and ships the bytes, so the host
// server needs no filesystem access). Both placements (in-proc + the CheckContextService
// RPC) consume the SAME struct.
type CheckHTTPRequest struct {
	Method            string
	URL               string
	Body              []byte
	Headers           map[string]string
	Timeout           string
	AllowInsecure     bool
	NoFollowRedirects bool
	CAPEM             []byte
}

// CheckHTTPResponse is the result of cc.HTTPDo: the status code, the response body, and the
// response headers as a pre-formatted "Key: value\n" blob (the host formats once — R3 —
// preserving multi-value headers the matcher pipeline consumes directly). A transport-level
// failure is returned as the HTTPDo error, not here.
type CheckHTTPResponse struct {
	Status     int
	Body       []byte
	HeaderBlob string
}

// CheckVerbResult is a host-coupled verb's verdict. charly converts it to its internal
// CheckResult (stamping the Op/Verb/timing) at the dispatch boundary.
type CheckVerbResult struct {
	Status        Status
	Message       string
	CapturedValue string // value stashed under `capture:` (recorded only on PASS)
}

// CheckVerbProvider is the typed in-process contract a host-coupled check-verb candy
// implements. Reserved() is the verb word; RunVerb runs the probe against the live
// CheckContext and returns a CheckVerbResult. The authored plugin_input rides op.PluginInput
// (decode it into the candy's CUE-generated params struct).
type CheckVerbProvider interface {
	Reserved() string
	RunVerb(ctx context.Context, cc CheckContext, op *Op) CheckVerbResult
}
