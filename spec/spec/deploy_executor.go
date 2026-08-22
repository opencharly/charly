package spec

// deploy_executor.go — the InstallPlan IR's EXECUTION contract (P4 "IR E-envelope"):
// the DeployExecutor interface every host-side executor implements, the BuilderRunOpts
// it carries, and the EmitOpts cross-cutting toggle set. These live in spec (not
// deploykit) because they are part of the E-envelope an out-of-process deploy/step
// plugin resolves against — EmitOpts.ParentExec threads a DeployExecutor through the
// nested-deploy tree, and the executor IMPLEMENTATIONS (sdk/kit) implement THIS
// interface. Homing them in spec keeps sdk/kit's own reverse-channel executor
// (kit.DeployExecutor, a DISTINCT interface) collision-free.

import (
	"context"
	"errors"
	"io"
)

// ErrNotSupported is returned by RunInteractive/RunStream on a venue that cannot host a
// LIVE-STDIO session (a capture-only / non-interactive executor). Callers surface it as
// "shell/logs not supported on this substrate" — an explicit, compile-forced failure, never
// a silent no-op (F12; the honest form of the executor-matrix ErrNotSupported path).
var ErrNotSupported = errors.New("live-stdio (interactive/stream) not supported on this venue")

// Process is one exact-argv, full-duplex child running in a deployment venue.
// It is deliberately a behaviour contract rather than a wire shape: target
// routes and all data sent over the process are CUE-generated, while this seam
// only exposes the OS pipes that carry the generated gRPC protocol.
//
// Close must be idempotent, close stdin, terminate the complete child process
// group when it has not exited, and reap it. Wait may be called more than once.
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Wait() error
	Close() error
}

// ProcessExecutor is an optional capability implemented by deployment
// executors that can host a bidirectional process. Keeping it separate from
// DeployExecutor preserves compatibility with capture-only executors and test
// doubles. Callers must return ErrNotSupported when this capability is absent.
type ProcessExecutor interface {
	StartProcess(ctx context.Context, launch ProcessLaunch) (Process, error)
}

// DeployExecutor is the in-process host-side executor: the concrete venue an
// InstallPlan's steps are applied against (ShellExecutor on the operator's
// machine, SSHExecutor over ssh, NestedExecutor through a jump). The three
// implementations live in sdk/kit; the InstallPlan IR (EmitOpts.ParentExec)
// carries one through the nested-deploy tree. DISTINCT from kit.DeployExecutor
// (the reverse-channel executor a plugin serves over gRPC) — same role, different
// wire shape (typed EmitOpts + file paths here; JSON-marshalled opts + byte content there).
type DeployExecutor interface {
	// Venue returns a stable identifier for where this executor's
	// commands physically run. Examples:
	//
	//   "local"                            — ShellExecutor.
	//   "ssh://arch@127.0.0.1:2224"        — SSHExecutor.
	//   "nested:podman exec stack/local"   — NestedExecutor over local.
	//   "nested:ssh vm/local"              — NestedExecutor over SSH.
	//
	// The string is used as a map key for per-venue ledgers, so it
	// must be stable across invocations for the same logical target.
	// Not a URL — don't parse it; just compare.
	Venue() string

	// RunSystem executes a shell script with root privileges. On the
	// host, this is `sudo bash -s <<<script`; on the VM target, it's
	// `ssh <user>@<host> sudo bash -s <<<script`. The script body runs
	// with set -e semantics at the caller's discretion.
	//
	// The interpreter is NOT guaranteed to be bash. Those two implementations
	// pin bash, but NestedExecutor selects the far-side interpreter at run time
	// inside the target and degrades to `sh` on a base that ships no bash (see
	// exec.nestedShellProbe). A script body handed to this interface must
	// therefore be POSIX sh — a bashism runs on the host and silently fails
	// inside a busybox container or guest.
	RunSystem(ctx context.Context, script string, opts EmitOpts) error

	// RunUser executes a shell script as the invoking user (no sudo).
	// On the host, it's `bash -s <<<script`; on VM, `ssh <user>@<host>
	// bash -s <<<script` where <user> is the unprivileged guest user.
	// Same interpreter caveat as RunSystem — write POSIX sh.
	RunUser(ctx context.Context, script string, opts EmitOpts) error

	// RunBuilder invokes the multi-stage builder image (podman run
	// <builder>) to compile pixi/npm/cargo/aur artifacts. On the host
	// this calls the existing BuilderRun helper. On VM deploys, the
	// builder runs *on the host* and artifacts are scp'd into the
	// guest via PutFile — podman inside the guest is not required.
	RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error)

	// PutFile places a file at a remote path. ownerRoot == true means
	// the file is chown'd to root:root and chmod'd according to mode.
	// On the host, this is a plain os.WriteFile (plus sudo chown when
	// ownerRoot). On VM, this is scp into a tmp location followed by
	// `sudo install -m <mode> -o root -g root` on the guest.
	PutFile(ctx context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, opts EmitOpts) error

	// GetFile retrieves the contents of a file on the venue. asRoot==true
	// runs the read via sudo to handle paths the deploying user cannot
	// access (e.g. /etc/rancher/k3s/k3s.yaml on a k3s server). On the
	// host, this is os.ReadFile (or `sudo cat` when asRoot). On VM, this
	// is `ssh <host> sudo cat <path>` with stdout captured. On nested
	// executors, delegates through the jump via the parent's own RunSystem
	// semantics. Used by layer_artifacts.go to publish files back to the
	// operator after deploy completion.
	GetFile(ctx context.Context, remotePath string, asRoot bool, opts EmitOpts) ([]byte, error)

	// RunCapture executes a single shell command (or short POSIX-sh script) on
	// the venue and returns stdout/stderr/exit/err separately. Used by the
	// declarative test runner (testrun.go) to probe target state without
	// the streamed-output ergonomics of RunSystem/RunUser. No root
	// escalation — callers add `sudo` explicitly when needed; mirrors the
	// previous test-time Executor.Exec semantics. After the executor-
	// hierarchy cutover (2026-04), this is the single capture-output
	// method used by every probe across `charly check live`, `charly check box`, and
	// `charly check` scoring.
	RunCapture(ctx context.Context, script string) (stdout, stderr string, exit int, err error)

	// RunInteractive runs a command on the venue wired to the operator's LIVE TTY:
	// stdin/stdout/stderr are INHERITED from the host charly process (the reverse-channel
	// server runs IN that process, so os.Std* IS the operator's terminal). The child
	// (`podman exec -it` / `ssh -t`) owns the PTY, resize (SIGWINCH), and Ctrl-C. Blocks
	// until the session ends; returns its exit code. NOT ctx-deadlined — the TTY owns its
	// lifetime (the hostBuildCli doctrine). Stdio NEVER crosses the plugin wire — only the
	// script + the exit code (F12; the live-stdio sibling of RunCapture). Consumers: `charly
	// shell` (-it) and `charly cmd` (-i, stdin piped). A venue that cannot host an interactive
	// session returns ErrNotSupported.
	RunInteractive(ctx context.Context, script string) (exit int, err error)

	// RunStream runs a command on the venue streaming stdout/stderr LIVE to the operator
	// (inherited os.Stdout/os.Stderr; NO stdin). Blocks until exit. Same host-holds-the-
	// terminal semantics as RunInteractive (F12). Consumer: `charly logs --follow`. Returns
	// ErrNotSupported on a venue that cannot stream.
	RunStream(ctx context.Context, script string) (exit int, err error)

	// Kind returns a coarse classification of the venue used by the test
	// runner for reporting and skip decisions. Values:
	//   "host"      — ShellExecutor (operator's machine)
	//   "container" — NestedExecutor with JumpPodmanExec / JumpDockerExec
	//   "vm"        — SSHExecutor or NestedExecutor with JumpSSH/JumpVirshConsole
	// Replaces the test-time Executor.Kind() method deleted in the
	// 2026-04 executor-hierarchy cutover.
	Kind() string

	// ResolveHome returns the absolute path of $HOME for the named user
	// on the venue. Empty user means "the executor's default user" (the
	// invoking operator for ShellExecutor; the SSH login user for
	// SSHExecutor). Implementations consult `getent passwd` so they
	// don't depend on $HOME being set in the calling environment — that
	// matters for SSH executors where the operator's $HOME has nothing
	// to do with the remote user's home, and for ShellExecutor when the
	// caller wants a different user's home (e.g. running as root but
	// resolving an unprivileged user's home).
	//
	// Bundled as part of the 2026-05 shell:-schema cutover. Replaces the
	// `the local deploy target.HostHome = os.Getenv("HOME")` static-field
	// initialization that mis-targeted SSH deploys: the operator's
	// $HOME is not the remote user's home, so every shell-rc edit
	// (env.d sourcing block included) was landing in the wrong place
	// for `host: user@machine` deploys.
	ResolveHome(ctx context.Context, user string) (string, error)
}

// BuilderRunOpts describes one `podman run <builder>` invocation. The BuilderRun
// implementation (host-engine builder exec) lives in sdk/kit; this options struct
// is spec-homed because DeployExecutor.RunBuilder carries it across the IR.
type BuilderRunOpts struct {
	Engine       string // "podman" or "docker"; default "podman"
	BuilderImage string // full image ref, e.g. "ghcr.io/opencharly/fedora-builder:latest"
	CandyDir     string // absolute path to candy source (bind-mounted as /work)
	ScriptBody   string // shell script contents to pass to bash -c

	// ResolveImage + EnsureImage inject the image-presence behavior that used to
	// read *Config directly (Config stays a charly kernel type; the executor is an
	// sdk mechanism reached through this seam). ResolveImage maps a short/namespaced
	// builder ref to its concrete podman storage key (side-effect-free — safe in the
	// dry-run path). EnsureImage guarantees the image is present, falling back to a
	// local `charly box build <basename>` when it is project-buildable. The charly
	// callers close over Cfg + ProjectDir; see charly/host_build_box_ref_resolve.go
	// (ResolveImage) and charly/dispatch_build_ensure.go (EnsureImage).
	ResolveImage func(image string) (string, error)
	EnsureImage  func(ctx context.Context, image string) error

	// Bind-mounts. Keys are container paths; values are host paths.
	// Set by the caller based on the builder kind — pixi/npm/cargo use
	// the same HOME-subdir layout, aur uses a tmpdir for /tmp/aur-pkgs.
	BindMounts map[string]string

	// Env vars to set inside the container (in addition to HOME).
	Env map[string]string

	// HostHome is the invoking user's absolute home dir. Set via HOME=
	// inside the container so path-baking (pixi shebangs, etc.) resolves
	// to a path that's valid both inside (via bind-mount) and outside.
	HostHome string

	// DryRun returns the command line that would run without executing.
	// Used for --dry-run deploy.
	DryRun bool

	// RunAsRoot spawns the container as UID 0 instead of the host's
	// UID. Needed for builders whose script body uses `sudo` against
	// users that don't exist in the builder image's /etc/passwd —
	// e.g. AUR's makepkg+yay flow inside a non-OCI-staged builder
	// image. Under rootless podman, root-in-container maps to the
	// host's UID, so file ownership in bind-mounts stays correct.
	RunAsRoot bool
}

// EmitOpts carries cross-cutting toggles passed by command-line flags.
// Gates are checked per-step by the target; target-specific options (the
// container target's registry auth, the host target's --assume-yes, --dry-run)
// are gathered here too.
type EmitOpts struct {
	DryRun               bool
	FormatJSON           bool // print IR as JSON on stdout instead of table
	AllowRepoChanges     bool
	AllowRootTasks       bool
	WithServices         bool
	SkipIncompatible     bool
	AssumeYes            bool // skip sudo preflight, confirmation prompts
	Verify               bool // run layer tests after install
	Pull                 bool // force re-fetch of remote refs / image pull
	BuilderImageOverride string

	// DevLocalPkg marks a DISPOSABLE CHECK BED's deploy — the deploy-side twin of `charly box
	// build --dev-local-pkg`, set by the bed runner and by nothing else. It changes ONE decision:
	// a localpkg candy whose package source cannot be located becomes a hard failure instead of
	// the benign "the candy's curl/COPY task installs it instead" skip. A bed exists to prove the
	// IN-DEVELOPMENT package builds and installs, so a silent skip there makes the bed assert
	// something it never tested — which is exactly what happened when an uninitialized pkg/fedora
	// submodule let a bed skip building the rpm and fail only later, at an unrelated live check.
	DevLocalPkg bool

	// LocalPkgBuild is the dev-local-pkg build context: the in-development binary +
	// plugins + CalVer + arch + charly.yml the executor uses to build the
	// in-development package via the `charly generate-packages` plugin (the
	// `--dev-local-pkg` build path). Populated by the check-bed runner (charly
	// core) when DevLocalPkg is set; nil → the executor discovers defaults
	// (os.Executable(), runtime.GOARCH, `<binary> version`, the baked plugins dir,
	// `<CandyDir>/charly.yml`).
	LocalPkgBuild *LocalPkgBuildContext

	// ParentExec is the DeployExecutor of the parent deployment in a
	// nested tree. Non-nil iff this target is dispatched as a child of
	// another — FleetAddCmd's tree walker builds the chain root-first
	// and passes the immediate ancestor's executor here. Targets that
	// support being nested (host, container, vm) compose their own
	// executor over ParentExec via NestedExecutor; leaf-only targets
	// (kubernetes) ignore it and error if non-nil.
	//
	// When nil, the target runs against its natural root venue
	// (ShellExecutor for host, a fresh SSHExecutor for vm, etc.)
	// — preserving the flat-schema behavior for v2 configs that happen
	// to have no `children:`.
	ParentExec DeployExecutor

	// ParentNode is the FleetNode above this target in the tree.
	// Useful for targets that need parent-level context beyond the
	// executor (e.g. a vm child wants to know its parent container's
	// name to wire network forwarding). nil at the root.
	ParentNode *FleetNode

	// Path is the dotted-path identifier of this node (e.g.
	// "stack.web.db"). Used for logging + ledger keying.
	Path string
}

// LocalPkgBuildContext is the dev-local-pkg build context: the in-development
// binary + plugins + CalVer + arch + charly.yml the executor uses to build the
// in-development package via the `charly generate-packages` plugin. Every field
// is optional — the executor discovers a default when one is empty (see
// EmitOpts.LocalPkgBuild).
type LocalPkgBuildContext struct {
	// Binary is the in-development charly binary to package (default:
	// os.Executable()).
	Binary string
	// PluginsDir is the dir of in-development plugin binaries + .providers to
	// package (default: the baked plugins dir /usr/lib/charly/plugins/).
	PluginsDir string
	// CalVer is the in-development release CalVer (default: `<Binary> version`).
	CalVer string
	// Arch is the target GOARCH name (default: runtime.GOARCH).
	Arch string
	// CandyYAML is the charly candy's charly.yml — the `--candy` input the
	// plugin reads the packaging section from (default: `<CandyDir>/charly.yml`).
	CandyYAML string
}

// ContextOrDefault returns opts' context if one's attached, or a background context.
func (o EmitOpts) ContextOrDefault() context.Context {
	return context.Background()
}

// EmitOptsFromResolveTargetAdd rebuilds the FINAL resolved EmitOpts for the resolve-target-add
// seam from the plugin-sent gate flags + the re-derived parent executor (K-wave 2 cone R2 bank D
// thin — the seam's inline construction moved here). ParentExec/Path are the two live/local pieces
// filled host-side; DryRun never reaches the seam (a dry-run prints plugin-side and returns), so
// it is absent from the request and left false.
func EmitOptsFromResolveTargetAdd(req DeployResolveTargetAddRequest, parentExec DeployExecutor) EmitOpts {
	return EmitOpts{
		AllowRepoChanges:     req.AllowRepoChanges,
		AllowRootTasks:       req.AllowRootTasks,
		WithServices:         req.WithServices,
		SkipIncompatible:     req.SkipIncompatible,
		AssumeYes:            req.AssumeYes,
		Verify:               req.Verify,
		Pull:                 req.Pull,
		BuilderImageOverride: req.BuilderImage,
		DevLocalPkg:          req.DevLocalPkg,
		ParentExec:           parentExec,
		Path:                 req.Path,
	}
}
