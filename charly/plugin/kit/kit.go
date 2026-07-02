// Package kit is the importable contract a HOST-COUPLED plugin candy implements to
// run against charly's live check engine — the seam that lets a check verb whose
// logic needs the running deployment (exec-in-container, host TCP dial, host-vantage
// HTTP) live in its own candy module instead of charly's module.
//
// A host-coupled verb candy implements CheckVerbProvider; charly runs it in EITHER
// placement, invisibly above the registry: IN-PROCESS (compiled-in — charly passes
// the live *Runner as a CheckContext) OR OUT-OF-PROCESS (the CheckContext legs are
// served back to the candy over the host's reverse channel — ExecutorService for
// Exec + CheckContextService for HTTPDo/AddBackground, F2 — and the scalar legs ride
// the env_json snapshot). RunVerb is identical in both. This package imports only the
// stdlib + charly/spec (the generated param/Op types), so a candy module can import it
// without pulling charly's package main.
package kit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opencharly/charly/charly/spec"
)

// RunMode mirrors charly's RunMode: the mode a check runs under.
type RunMode int

const (
	// ModeLive — `charly check live`, against a running container/VM (in-container probes).
	ModeLive RunMode = iota
	// ModeBox — `charly check box`, against a disposable build container.
	ModeBox
)

// String renders the mode as "box" / "live" (mirrors charly's runModeName).
func (m RunMode) String() string {
	if m == ModeBox {
		return "box"
	}
	return "live"
}

// Executor is the subset of charly's DeployExecutor a check verb needs: run one
// command/script on the venue and capture stdout/stderr/exit separately. charly's
// DeployExecutor satisfies this structurally (RunCapture + Kind have identical
// signatures), so *Runner.Exec is passed straight through.
type Executor interface {
	// RunCapture runs a shell command/script on the venue, returning stdout,
	// stderr, the exit code, and any execution error (NOT a non-zero exit — that
	// is reported via the exit code). No root escalation; callers add sudo.
	RunCapture(ctx context.Context, script string) (stdout, stderr string, exit int, err error)
	// Kind classifies the venue: "host" | "container" | "image" | "vm".
	Kind() string
}

// CheckContext is the live check-engine surface a host-coupled verb's RunVerb
// consumes. charly's *Runner implements it; a candy reaches the running deployment
// through it without importing charly's package main.
type CheckContext interface {
	// Exec runs commands on the venue (in-container under ModeLive, in a disposable
	// container under ModeBox, or host-side depending on the executor).
	Exec() Executor
	// Mode is the run mode (Live vs Box).
	Mode() RunMode
	// HTTPDo issues an HTTP request from the CHARLY HOST's network namespace, applying
	// the per-request TLS / redirect / CA policy in req, and returns the status, body, and
	// response headers. It REPLACES the former HTTPClient() *http.Client leg: an
	// *http.Client cannot cross a process boundary, so out-of-process the REQUEST crosses
	// (CheckContextService.HTTPDo) and the host dials; in-process the host builds the client
	// and dials directly. The transport-level error is returned as err (a non-2xx is NOT an
	// error — the caller matches resp.Status).
	HTTPDo(ctx context.Context, req HTTPRequest) (HTTPResponse, error)
	// DialTimeout is the per-dial ceiling for host-side TCP reachability probes.
	DialTimeout() time.Duration
	// Box / Instance are the deployment's image + instance names (empty under ModeBox).
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

// HTTPRequest is the host-vantage HTTP request a check verb hands cc.HTTPDo. It carries
// the FULL request plus the per-request policy the host needs to build the client: Timeout
// is a Go duration string ("" = the engine's base timeout); CAPEM is the resolved CA PEM
// bytes (a candy reads its authored ca_file host-side and ships the bytes, so the host
// server needs no filesystem access). Both placements (in-proc + the CheckContextService
// RPC) consume the SAME struct.
type HTTPRequest struct {
	Method            string
	URL               string
	Body              []byte
	Headers           map[string]string
	Timeout           string
	AllowInsecure     bool
	NoFollowRedirects bool
	CAPEM             []byte
}

// HTTPResponse is the result of cc.HTTPDo: the status code, the response body, and the
// response headers as a pre-formatted "Key: value\n" blob (the host formats once — R3 —
// preserving multi-value headers the matcher pipeline consumes directly). A transport-level
// failure is returned as the HTTPDo error, not here.
type HTTPResponse struct {
	Status     int
	Body       []byte
	HeaderBlob string
}

// Status is a check verdict (mirrors charly's CheckStatus ordering).
type Status int

const (
	StatusPass Status = iota
	StatusFail
	StatusSkip
)

// Result is a host-coupled verb's verdict. charly converts it to its internal
// CheckResult (stamping the Op/Verb/timing) at the dispatch boundary.
type Result struct {
	Status        Status
	Message       string
	CapturedValue string // value stashed under `capture:` (recorded only on PASS)
}

// Pass / Fail / Skip are the verdict constructors a verb returns; the *f variants
// take a printf format (mirror charly's passf/failf/skipf).
func Pass(msg string) Result { return Result{Status: StatusPass, Message: msg} }
func Fail(msg string) Result { return Result{Status: StatusFail, Message: msg} }
func Skip(msg string) Result { return Result{Status: StatusSkip, Message: msg} }

func Passf(format string, a ...any) Result { return Pass(fmt.Sprintf(format, a...)) }
func Failf(format string, a ...any) Result { return Fail(fmt.Sprintf(format, a...)) }
func Skipf(format string, a ...any) Result { return Skip(fmt.Sprintf(format, a...)) }

// ShellQuote wraps s in single quotes for safe interpolation into a shell command
// (the importable analogue of charly's shellSingleQuote). Embedded single quotes are
// escaped as '\”.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// TrimPreview truncates s to a 200-char preview (trailing "…") for compact check-output
// display.
func TrimPreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// WrapContainerCommand guards an in-container command-check script against stdin-consuming
// subcommands. The runner delivers in-container scripts to the pod shell over a stdin heredoc
// ("stdin-attached exec"); without this guard the FIRST subcommand that reads stdin — adb shell,
// ssh, read, cat — consumes the REST of the heredoc (the not-yet-executed script lines), silently
// truncating the check to its first command. Wrapping the whole script in a brace group with stdin
// redirected from /dev/null fixes it generically: the shell reads the entire group before executing
// it (so the heredoc is fully drained by parse time), then runs every subcommand with stdin tied to
// /dev/null. The host path (a plain `sh -c` argv) is unaffected.
func WrapContainerCommand(script string) string {
	return "{ " + script + "\n} </dev/null"
}

// DecodeInput decodes an Op's plugin_input (map[string]any) into a candy's
// CUE-generated typed params struct via a JSON round-trip. A nil/empty input leaves
// out at its zero value; the host has already validated the input against the served schema.
func DecodeInput(in map[string]any, out any) {
	if len(in) == 0 {
		return
	}
	b, err := json.Marshal(in)
	if err != nil {
		return
	}
	_ = json.Unmarshal(b, out)
}

// CheckVerbProvider is the typed in-process contract a host-coupled check-verb candy
// implements. Reserved() is the verb word; RunVerb runs the probe against the live
// CheckContext and returns a Result. The authored plugin_input rides op.PluginInput
// (decode it into the candy's CUE-generated params struct).
type CheckVerbProvider interface {
	Reserved() string
	RunVerb(ctx context.Context, cc CheckContext, op *spec.Op) Result
}

// StepKindName names the TYPED install-plan step a step-providing verb lowers into. The
// host maps it to its internal StepKind enum; kept a string so the kit need not import
// charly's package main.
type StepKindName string

const (
	// StepKindServicePackaged — the `service` verb (enable a packaged unit; load-bearing reversals).
	StepKindServicePackaged StepKindName = "service-packaged"
	// StepKindSystemPackages — the `package` verb (install system packages).
	StepKindSystemPackages StepKindName = "system-packages"
)

// ServicePackagedDesc is the candy-decodable construction input for a service-packaged
// step: the host materializer adds the op-resolved scope + candy name and keeps the
// load-bearing Reverse() (disable / restore-enabled / remove-dropin) in package main.
type ServicePackagedDesc struct {
	Unit   string
	Enable bool
}

// SystemPackagesDesc is the candy-decodable construction input for a system-packages step
// (the `package` verb): the authored package name + per-distro map. The host materializer
// resolves the cross-distro name (ResolvePackageName against the image's tags), sets the
// image format + PhaseInstall, and builds the SystemPackagesStep.
type SystemPackagesDesc struct {
	Package    string
	PackageMap map[string]string
}

// ResolvePackageName picks the correct package name for the running image's distro: if
// packageMap has a key matching any of the image's distro tags (first match wins — tags
// are authored most-specific-first, "fedora:43" before "fedora"), that mapping is used;
// otherwise the bare pkg name. The single cross-distro name resolver shared by the
// `package` candy's check + act AND the host's step materializer (R3).
func ResolvePackageName(pkg string, packageMap map[string]string, distros []string) string {
	if len(packageMap) == 0 {
		return pkg
	}
	for _, tag := range distros {
		if name, ok := packageMap[tag]; ok && name != "" {
			return name
		}
	}
	return pkg
}

// StepDescriptor is the candy-decodable construction input for a TYPED install-plan step
// (the build/deploy install timeline). Exactly one variant is non-nil; the host
// materializer rebuilds the real package-main InstallStep from it (computing the
// package-main-only inputs — scope from op.RunAs+img, candy name — and keeping the
// load-bearing Reverse() in package main, so the candy never imports an IR type).
type StepDescriptor struct {
	ServicePackaged *ServicePackagedDesc
	SystemPackages  *SystemPackagesDesc
}

// StepProvider is the OPTIONAL third role of a host-coupled verb candy: a verb whose
// build/deploy ACT lowers into a TYPED install-plan step (service → service-packaged,
// package → system-packages) rather than a shell (ProvisionActor) or a generic OpStep.
// StepKind names the target step (static); ConstructStepDescriptor returns the
// candy-decodable construction inputs for one op. The host wraps a candy implementing
// this in an adapter that satisfies package-main's TypedStepProvider, materializing the
// descriptor into the real IR step.
type StepProvider interface {
	StepKind() StepKindName
	ConstructStepDescriptor(op *spec.Op) StepDescriptor
}

// ProvisionActor is the OPTIONAL second role of a host-coupled verb candy: the do:act
// renderer for a state-provision verb (kernel_param/mount/user/unix_group/file/command/
// service/package), rendering the shell that ENACTS the op under the live init / package
// manager. It is reached at install COMPILE+EMIT (a `run: {plugin: <verb>}` step → the
// build-act RUN in emitTasks, and the local/vm deploy act) AND at runtime act. A candy
// whose verb type implements this ALONGSIDE CheckVerbProvider is registered as a
// multi-role provider (the host adapter then also satisfies the package-main
// ProvisionActor). op is the spec.Op (the verb's plugin_input rides op.PluginInput);
// distros is the image's distro tag list for package-name resolution. Returns
// (script, ok); ok=false means "no act form for this op" (the host skips/errors per its
// act path). This is the SHELL-string act role — a verb that instead lowers into a typed
// InstallPlan step (service/package) additionally needs the kit step contract.
type ProvisionActor interface {
	Reserved() string
	RenderProvisionScript(op *spec.Op, distros []string) (script string, ok bool)
}
