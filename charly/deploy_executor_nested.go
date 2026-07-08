package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// deploy_executor_nested.go — the composable executor that turns the
// flat DeployTarget interface into a recursive dispatcher.
//
// Every IR-consuming DeployTarget today (the local deploy target, the external vm deploy,
// PodDeployTarget) runs InstallStep primitives
// through a single DeployExecutor. When a deployment is nested inside
// another — a container inside a VM, a VM inside a container, a host-
// deploy inside any of the above — the child's executor is a
// NestedExecutor that wraps the parent's executor and prepends a
// "shell jump" to every primitive.
//
// Example: container-in-vm. Outer VM's executor is an SSHExecutor
// talking to the guest. The child container's executor is:
//
//    NestedExecutor{
//        Parent: outerVmSSH,
//        Jump:   NestedJump{Kind: JumpPodmanExec, Target: "mychild"},
//    }
//
// When the child calls RunSystem("pacman -Sy"), NestedExecutor passes
// it to the parent as "podman exec -i mychild sudo bash -c 'pacman
// -Sy'". The parent's SSHExecutor ships that one line through ssh,
// and the in-guest podman lands it inside the child container.
//
// Composition stacks arbitrarily: container-in-vm-in-container is
// NestedExecutor{Parent: NestedExecutor{Parent: localExec, Jump: …},
// Jump: …}. Each level adds one more shell jump.
//
// File transfer (PutFile) is the subtle part. Rather than try to
// stream files through stacked ssh/podman pipelines, NestedExecutor
// picks a FileTransport that knows the protocol of the outermost
// jump: podman-cp for containers, scp for SSH hops, virt-copy-in for
// libvirt guests where SSH isn't available. The transport delegates
// intermediate hops back to the parent executor.

// JumpKind classifies how NestedExecutor enters the child environment.
// This determines both the shell-wrapper syntax for RunSystem/RunUser
// and the file-transport strategy for PutFile.
type JumpKind int

const (
	// JumpPodmanExec enters a rootful or rootless podman container
	// via `podman exec -i <name>`. The parent must have podman
	// available (the container-nesting candy provides this for
	// container-in-container; the virtualization candy is unrelated
	// here — container children of container parents).
	JumpPodmanExec JumpKind = iota + 1

	// JumpDockerExec enters a docker container via `docker exec -i`.
	// Separate from podman because docker and podman CLIs differ in
	// exit-code propagation and stdin handling.
	JumpDockerExec

	// JumpSSH makes an additional ssh hop — used when the child
	// itself is an SSH-reachable VM inside an already-SSH-reachable
	// parent (vm-in-container, vm-in-vm).
	JumpSSH

	// JumpVirshConsole attaches to a libvirt guest's serial console
	// for emergency-only shell access (used when the guest is
	// unreachable over SSH). Best-effort; primarily a diagnostic path.
	JumpVirshConsole

	// JumpPodmanRun spawns a fresh disposable container per invocation
	// via `podman run --rm <Target> bash`. Replaces the deleted
	// ImageExecutor — `charly check box` (build-section) uses this jump
	// to get the same "ephemeral image probe" semantics through the
	// unified chain primitive. Each call starts a new container; state
	// does NOT persist across calls.
	JumpPodmanRun

	// JumpDockerRun is the docker counterpart of JumpPodmanRun.
	JumpDockerRun
)

// NestedJump describes one hop into a nested environment. The Target
// string's meaning depends on Kind:
//
//	JumpPodmanExec / JumpDockerExec: container name.
//	JumpSSH:                         "[user@]host[:port]" or an
//	                                 ssh-config alias (e.g.
//	                                 "charly-<vmname>"). ssh(1) reads
//	                                 ~/.ssh/config + agent for keys
//	                                 and connection options — we
//	                                 contain zero credential state.
//	JumpVirshConsole:                libvirt domain name.
type NestedJump struct {
	Kind   JumpKind
	Target string

	// Extra arguments inserted before the shell invocation. Rarely
	// needed; useful for `podman exec --env FOO=bar` or
	// `ssh -o ProxyJump=…` style tweaks.
	ExtraArgs []string

	// ProbeName names the disposable container for a JumpPodmanRun/JumpDockerRun
	// jump so a probe KILLED before it exits (e.g. by a check ProbeTimeout) can be
	// force-reaped instead of orphaning a "Created" container that poisons the
	// podman container store. Set per-invocation by NestedExecutor.prepareJump;
	// empty for non-disposable jumps.
	ProbeName string
}

// String renders a jump as a human-readable venue segment. Used as a
// component of NestedExecutor.Venue().
func (j NestedJump) String() string {
	switch j.Kind {
	case JumpPodmanExec:
		return "podman-exec:" + j.Target
	case JumpDockerExec:
		return "docker-exec:" + j.Target
	case JumpSSH:
		return "ssh:" + j.Target
	case JumpVirshConsole:
		return "virsh:" + j.Target
	case JumpPodmanRun:
		return "podman-run:" + j.Target
	case JumpDockerRun:
		return "docker-run:" + j.Target
	}
	return "jump?"
}

// NestedExecutor wraps a parent DeployExecutor and rewrites each
// DeployExecutor primitive to run inside a nested environment via
// Jump. Stacks: a NestedExecutor can itself be the Parent of another
// NestedExecutor, yielding arbitrary-depth composition.
type NestedExecutor struct {
	Parent DeployExecutor
	Jump   NestedJump
}

// Venue returns "nested:<Jump>/<parentVenue>". The parent venue is
// appended so the full chain is preserved — useful for ledger keying
// and debug output (e.g. "nested:podman-exec:mychild/ssh://arch@…").
func (n *NestedExecutor) Venue() string {
	parent := ""
	if n.Parent != nil {
		parent = n.Parent.Venue()
	}
	return "nested:" + n.Jump.String() + "/" + parent
}

// run is the shared body of RunSystem/RunUser: it wraps the script for this
// executor's jump (asRoot bakes `sudo bash` into the jump), then hands the single
// wrapped shell line to the parent's RunUser — NOT RunSystem — because entering a
// container or ssh session already carries its own root-escalation semantics; we
// don't want `sudo ssh sudo bash` triple-escalation.
func (n *NestedExecutor) run(ctx context.Context, script string, asRoot bool, opts EmitOpts) error {
	wrapped, reap, err := n.prepareJump(script, asRoot)
	if err != nil {
		return err
	}
	if n.Parent == nil {
		return fmt.Errorf("NestedExecutor: nil Parent")
	}
	defer reap()
	return n.Parent.RunUser(ctx, wrapped, opts)
}

// RunSystem routes a bash script through the jump as root.
func (n *NestedExecutor) RunSystem(ctx context.Context, script string, opts EmitOpts) error {
	return n.run(ctx, script, true /*root*/, opts)
}

// RunUser runs as the unprivileged user inside the nested
// environment.
func (n *NestedExecutor) RunUser(ctx context.Context, script string, opts EmitOpts) error {
	return n.run(ctx, script, false /*root*/, opts)
}

// RunBuilder always runs on the outermost host (where podman with
// image-building capabilities lives). Delegates to the parent chain
// unchanged — nested executors never run builders inside themselves.
// Artifacts produced by the builder land on the outer host and are
// distributed to the nested environment via PutFile.
func (n *NestedExecutor) RunBuilder(ctx context.Context, opts BuilderRunOpts) ([]byte, error) {
	if n.Parent == nil {
		return nil, fmt.Errorf("NestedExecutor: nil Parent")
	}
	return n.Parent.RunBuilder(ctx, opts)
}

// RunCapture executes a script inside the nested venue and returns
// stdout/stderr/exit. The wrapped script is forwarded to the parent's
// own RunCapture, so multi-hop chains compose: harness sandbox (host) →
// VM (ssh) → inner-pod (podman exec) → nested-pod (podman exec)
// stacks four heredocs and captures the deepest stdout.
//
// asRoot is hard-coded to false here: test verbs probe state and add
// `sudo` explicitly when needed (matching the pre-cutover Executor.Exec
// semantics). Adding root escalation would silently change probe
// behaviour for every existing test.
func (n *NestedExecutor) RunCapture(ctx context.Context, script string) (string, string, int, error) {
	wrapped, reap, err := n.prepareJump(script, false /*root*/)
	if err != nil {
		return "", "", -1, err
	}
	if n.Parent == nil {
		return "", "", -1, fmt.Errorf("NestedExecutor: nil Parent")
	}
	defer reap()
	return n.Parent.RunCapture(ctx, wrapped)
}

// probeNameSeq makes disposable-probe container names unique within a process;
// combined with the pid it is unique across concurrent charly processes too.
var probeNameSeq atomic.Uint64

// prepareJump wraps a script for this executor's jump and, for a DISPOSABLE
// container jump (JumpPodmanRun/JumpDockerRun), names the throwaway container
// deterministically and returns a reap closure. A `podman run --rm` probe KILLED
// before it exits (e.g. by a check ProbeTimeout) leaves a "Created" container
// that --rm never cleans; such orphans accumulate and poison the podman
// container store (sqlite "database is locked" under concurrency). The reap
// force-removes the named container so a killed probe can never leave an orphan.
// A no-op reap for a non-disposable jump.
func (n *NestedExecutor) prepareJump(script string, asRoot bool) (string, func(), error) {
	j := n.Jump
	if j.Kind == JumpPodmanRun || j.Kind == JumpDockerRun {
		j.ProbeName = fmt.Sprintf("charly-probe-%d-%d", os.Getpid(), probeNameSeq.Add(1))
	}
	wrapped, err := wrapWithJump(j, script, asRoot)
	if err != nil {
		return "", func() {}, err
	}
	return wrapped, func() { n.reapDisposableProbe(j.Kind, j.ProbeName) }, nil
}

// reapDisposableProbe force-removes a named disposable probe container via the
// parent executor on a DETACHED context, so it runs even when the caller's ctx
// was cancelled (the ProbeTimeout kill path). No-op when name is empty.
func (n *NestedExecutor) reapDisposableProbe(kind JumpKind, name string) {
	if name == "" || n.Parent == nil {
		return
	}
	engine := "podman"
	if kind == JumpDockerRun {
		engine = "docker"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = n.Parent.RunUser(ctx, engine+" rm -f "+deployShellQuote(name)+" >/dev/null 2>&1 || true", EmitOpts{})
}

// ResolveHome returns $HOME for `user` inside the leaf environment of
// this nested executor. Implementation: shell out a small `getent passwd`
// command via RunCapture (which already routes through the jump chain).
// Empty user resolves to the leaf-shell's $HOME.
func (n *NestedExecutor) ResolveHome(ctx context.Context, user string) (string, error) {
	var script string
	if user == "" {
		script = "printf %s \"$HOME\""
	} else {
		quoted := strings.ReplaceAll(user, `'`, `'\''`)
		script = "entry=$(getent passwd '" + quoted + "' 2>/dev/null) && printf %s \"$(printf %s \"$entry\" | cut -d: -f6)\""
	}
	stdout, stderr, exit, err := n.RunCapture(ctx, script)
	if err != nil {
		return "", fmt.Errorf("NestedExecutor.ResolveHome(%q): %w (stderr: %s)", user, err, strings.TrimSpace(stderr))
	}
	if exit != 0 {
		return "", fmt.Errorf("NestedExecutor.ResolveHome(%q): exit %d (stderr: %s)", user, exit, strings.TrimSpace(stderr))
	}
	home := strings.TrimSpace(stdout)
	if home == "" {
		return "", fmt.Errorf("NestedExecutor.ResolveHome(%q): empty result", user)
	}
	return home, nil
}

// Kind reports the venue's coarse classification, derived from the
// LEAF jump (this NestedExecutor's own Jump). The parent chain doesn't
// affect Kind because tests care about what their probe lands in, not
// the path taken to reach it.
func (n *NestedExecutor) Kind() string {
	switch n.Jump.Kind {
	case JumpPodmanExec, JumpDockerExec:
		return "container"
	case JumpPodmanRun, JumpDockerRun:
		return "image"
	case JumpSSH, JumpVirshConsole:
		return "vm"
	}
	return "unknown"
}

// PutFile transfers a file from the invoking host into the nested
// venue. Implementation picks a FileTransport based on Jump.Kind:
// podman/docker containers use `<engine> cp`, SSH hops use scp,
// libvirt console uses virt-copy-in (best effort).
//
// For stacked jumps, the transfer is multi-stage: first land the
// file at a tmp path on the parent venue (via Parent.PutFile),
// then issue a one-shot move-into-child command via RunSystem. This
// preserves the "every executor only knows its own hop" invariant
// and keeps the FileTransport implementation simple.
func (n *NestedExecutor) PutFile(ctx context.Context, localPath, remotePath string, mode uint32, ownerRoot bool, opts EmitOpts) error {
	if n.Parent == nil {
		return fmt.Errorf("NestedExecutor: nil Parent")
	}

	// Stage the file on the parent venue first.
	base := filepath.Base(remotePath)
	stagePath := "/tmp/charly-nested-" + base
	if err := n.Parent.PutFile(ctx, localPath, stagePath, mode, false /*not root yet*/, opts); err != nil {
		return fmt.Errorf("staging %s on parent: %w", localPath, err)
	}

	// Compute the copy-in command for the jump kind.
	copyIn, err := copyIntoJumpCommand(n.Jump, stagePath, remotePath, mode, ownerRoot)
	if err != nil {
		return err
	}

	// Execute the copy-in via the parent's RunUser (no root needed
	// on the parent — the container/ssh tool handles the copy).
	cleanup := "rm -f " + deployShellQuote(stagePath)
	combined := copyIn + "\n" + cleanup
	return n.Parent.RunUser(ctx, combined, opts)
}

// GetFile retrieves a file from the nested environment. Current
// implementation is a minimal read-then-retrieve: the parent shell runs
// `<jump> cat <path>` and the output is captured via a staged tmp file
// on the parent venue, then Parent.GetFile pulls it back. Jumps without
// an obvious cat+stdout path (libvirt-console) return an explicit
// "not supported" error rather than silently producing empty bytes.
func (n *NestedExecutor) GetFile(ctx context.Context, remotePath string, asRoot bool, opts EmitOpts) ([]byte, error) {
	if n.Parent == nil {
		return nil, fmt.Errorf("NestedExecutor: nil Parent")
	}
	// Only podman/docker/ssh jumps support the stdout-cat approach.
	switch n.Jump.Kind {
	case JumpPodmanExec, JumpDockerExec, JumpSSH:
		// ok
	default:
		return nil, fmt.Errorf("NestedExecutor.GetFile: jump kind %d does not support file retricheck (add explicit support if needed)", int(n.Jump.Kind))
	}
	// Stage the file on the PARENT venue: run ONLY `cat <path>` through the
	// jump so the leaf's byte-clean stdout exits the jump verbatim, then apply
	// the staging `>` redirect on the PARENT shell (the `{ … }` group's
	// redirect lands parent-side). The old code baked the redirect INSIDE
	// wrapWithJump, so the stage file was written in the LEAF container while
	// Parent.GetFile read the parent's separate filesystem → a "no such file"
	// failure on every container→host (or VM→host) reverse-channel pull
	// (the first consumer to hit it green: the wl-screenshot PNG). The parent
	// `>` redirect is byte-level, so binary payloads survive intact — unlike
	// RunCapture's protobuf-string channel. Recurses for multi-hop: each level
	// stages on its own parent and Parent.GetFile pulls one hop closer.
	stage := "/tmp/charly-nested-get-" + filepath.Base(remotePath)
	cat := "cat " + deployShellQuote(remotePath)
	if asRoot {
		cat = "sudo " + cat
	}
	leafCat, err := wrapWithJump(n.Jump, cat, false /*staging writes are user-scope on parent*/)
	if err != nil {
		return nil, err
	}
	// leafCat ends with the heredoc terminator on its own line + a trailing
	// newline, so close the group with `}` after that newline — NOT `; }`,
	// which would place `;` illegally right after the terminator line.
	stageQ := deployShellQuote(stage)
	stageScript := "{ " + leafCat + "} > " + stageQ + ".tmp 2>/dev/null && mv " + stageQ + ".tmp " + stageQ
	if err := n.Parent.RunUser(ctx, stageScript, opts); err != nil {
		return nil, fmt.Errorf("nested GetFile stage: %w", err)
	}
	defer n.Parent.RunUser(ctx, "rm -f "+stageQ, opts) //nolint:errcheck
	return n.Parent.GetFile(ctx, stage, false, opts)
}

// wrapWithJump rewrites a script so it executes inside the nested
// environment when run by the parent executor. The return value is a
// single bash invocation (parent's shell) that internally invokes the
// child shell with the script fed via stdin.
//
// Heredoc-delim uniqueness across nesting depths: the delim is
// derived by counting how many `CHARLY_NESTED_SCRIPT_EOF` tokens already
// appear in the inner script. A 3-deep chain stacks three heredocs,
// each needing a DIFFERENT terminator — otherwise the OUTERMOST bash
// terminates its heredoc on the first occurrence (the innermost
// open) and the trailing closing delims are interpreted as
// commands. The count-and-suffix approach guarantees each level uses
// a delim absent from its inner content.
//
// Env-var propagation across container hops: when the jump kind is
// JumpPodmanExec / JumpDockerExec, a curated allowlist of session-
// related env vars (XDG_RUNTIME_DIR, DISPLAY, WAYLAND_DISPLAY,
// DBUS_SESSION_BUS_ADDRESS) is propagated via `--env KEY=VALUE` flags
// when set in the parent's environ. This is critical for libvirt
// session-socket lookup (libvirt: verbs find their socket at
// $XDG_RUNTIME_DIR/libvirt/libvirt-sock) and for any Wayland/X11
// verb that consults DISPLAY / WAYLAND_DISPLAY.
func wrapWithJump(jump NestedJump, script string, asRoot bool) (string, error) {
	shell := "bash"
	if asRoot {
		shell = "sudo bash"
	}

	// Choose a heredoc delimiter that does NOT already appear in the
	// inner script. For a non-nested call (script has zero inner
	// delims), use the bare base name. For a wrapping call (inner
	// already contains N copies of the base, from prior wrapWithJump
	// invocations at deeper levels), append a counter suffix so the
	// outer's open+close pair is distinct from every inner pair.
	baseDelim := "CHARLY_NESTED_SCRIPT_EOF"
	delim := baseDelim
	if n := strings.Count(script, baseDelim); n > 0 {
		delim = fmt.Sprintf("%s_%d", baseDelim, n)
	}

	switch jump.Kind {
	case JumpPodmanExec, JumpDockerExec:
		engine := "podman"
		if jump.Kind == JumpDockerExec {
			engine = "docker"
		}
		// Propagate session-related env vars across the container hop
		// so verbs that need them (libvirt session-socket lookup,
		// wayland/X11 display, dbus session bus) work the same as the
		// AI's manually-exported equivalent. Empty values are skipped.
		envFlags := buildContainerEnvFlags()
		extras := strings.Join(escapeTokens(jump.ExtraArgs), " ")
		// stdin-attached exec so the heredoc reaches the nested shell.
		// Layout: `<engine> exec -i [--env KEY=VALUE …] [extras] <target> <shell>`
		var execParts []string
		execParts = append(execParts, engine, "exec", "-i")
		if envFlags != "" {
			execParts = append(execParts, envFlags)
		}
		if extras != "" {
			execParts = append(execParts, extras)
		}
		execParts = append(execParts, deployShellQuote(jump.Target), shell)
		cmd := strings.Join(execParts, " ")
		return fmt.Sprintf("%s <<'%s'\n%s\n%s\n", cmd, delim, script, delim), nil

	case JumpPodmanRun, JumpDockerRun:
		// Disposable container per invocation: `<engine> run --rm
		// <imageref> bash`. State doesn't persist across calls — same
		// semantics as the deleted ImageExecutor. The image ref is in
		// jump.Target. --entrypoint='' clears any baked entrypoint so
		// bash actually runs (matches the pre-cutover ImageExecutor
		// shape).
		engine := "podman"
		if jump.Kind == JumpDockerRun {
			engine = "docker"
		}
		extras := strings.Join(escapeTokens(jump.ExtraArgs), " ")
		nameFlag := ""
		if jump.ProbeName != "" {
			// Name the disposable container so a probe killed before it exits can
			// be force-reaped (reapDisposableProbe) rather than orphaning a
			// "Created" container that poisons the podman store.
			nameFlag = "--name " + deployShellQuote(jump.ProbeName) + " "
		}
		cmd := fmt.Sprintf("%s run --rm %s-i --entrypoint= %s %s %s",
			engine, nameFlag, extras, deployShellQuote(jump.Target), shell)
		return fmt.Sprintf("%s <<'%s'\n%s\n%s\n", cmd, delim, script, delim), nil

	case JumpSSH:
		user, host, port := parseSSHTarget(jump.Target)
		if host == "" {
			return "", fmt.Errorf("NestedJump{JumpSSH}: target %q missing host", jump.Target)
		}
		portArg := ""
		if port > 0 {
			portArg = fmt.Sprintf("-p %d ", port)
		}
		userPrefix := ""
		if user != "" {
			userPrefix = user + "@"
		}
		extras := strings.Join(escapeTokens(jump.ExtraArgs), " ")
		// ssh(1) reads ~/.ssh/config + agent — no -i / -o overrides
		// from us. The Target is either an ssh-config alias (managed
		// charly-<vmname> stanza) or "[user@]host[:port]".
		cmd := fmt.Sprintf("ssh %s%s %s%s %s",
			portArg, extras, userPrefix, host, shell)
		return fmt.Sprintf("%s <<'%s'\n%s\n%s\n", cmd, delim, script, delim), nil

	case JumpVirshConsole:
		// virsh console is interactive and does not cleanly accept
		// scripted stdin. Reject with a clear error so callers know
		// to pick JumpSSH instead. Kept as a type so future work
		// (expect-scripted console) has a placeholder.
		return "", fmt.Errorf("JumpVirshConsole does not support scripted RunSystem/RunUser — use JumpSSH or wire the guest for SSH")
	}

	return "", fmt.Errorf("NestedJump: unknown JumpKind %d", jump.Kind)
}

// copyIntoJumpCommand emits the shell command that moves stagePath
// (already present on the parent venue) into remotePath inside the
// nested venue, with the requested mode and ownership.
func copyIntoJumpCommand(jump NestedJump, stagePath, remotePath string, mode uint32, ownerRoot bool) (string, error) {
	quote := deployShellQuote
	modeStr := permOctal(mode)

	switch jump.Kind {
	case JumpPodmanExec, JumpDockerExec:
		engine := "podman"
		if jump.Kind == JumpDockerExec {
			engine = "docker"
		}
		// `<engine> cp <stage> <container>:<remote>` then chown/chmod
		// via exec. Root ownership inside the container is cheap
		// (containers usually run as root by default) but we issue
		// chown explicitly so non-root containers behave correctly.
		chown := ""
		if ownerRoot {
			chown = fmt.Sprintf(" && %s exec %s chown root:root %s",
				engine, quote(jump.Target), quote(remotePath))
		}
		chmod := fmt.Sprintf(" && %s exec %s chmod %s %s",
			engine, quote(jump.Target), modeStr, quote(remotePath))
		return fmt.Sprintf("%s cp %s %s:%s%s%s",
			engine, quote(stagePath), quote(jump.Target), remotePath, chown, chmod), nil

	case JumpSSH:
		user, host, port := parseSSHTarget(jump.Target)
		portArg := ""
		if port > 0 {
			portArg = fmt.Sprintf("-P %d ", port)
		}
		userPrefix := ""
		if user != "" {
			userPrefix = user + "@"
		}
		target := fmt.Sprintf("%s%s:%s", userPrefix, host, remotePath)
		// ssh(1)/scp(1) read ~/.ssh/config + agent — no -i / -o
		// overrides from us. Target may be an ssh-config alias.
		installCmd := ""
		sshPort := ""
		if port > 0 {
			sshPort = fmt.Sprintf("-p %d ", port)
		}
		if ownerRoot {
			installCmd = fmt.Sprintf(" && ssh %s%s%s 'sudo chown root:root %s && sudo chmod %s %s'",
				sshPort, userPrefix, host, quote(remotePath), modeStr, quote(remotePath))
		} else {
			installCmd = fmt.Sprintf(" && ssh %s%s%s 'chmod %s %s'",
				sshPort, userPrefix, host, modeStr, quote(remotePath))
		}
		return fmt.Sprintf("scp %s%s %s%s",
			portArg, quote(stagePath), target, installCmd), nil
	}

	switch jump.Kind {
	case JumpPodmanRun, JumpDockerRun:
		return "", fmt.Errorf("NestedJump: PutFile is not supported for disposable-container jumps (JumpPodmanRun/JumpDockerRun) — files cannot persist across `run --rm` invocations")
	}
	return "", fmt.Errorf("NestedJump: PutFile not supported for JumpKind %d", jump.Kind)
}

// parseSSHTarget splits a "user@host:port" target string into its
// components. Missing user is "", missing port is 0.
func parseSSHTarget(target string) (user, host string, port int) {
	rest := target
	if u, r, ok := strings.Cut(rest, "@"); ok {
		user, rest = u, r
	}
	if idx := strings.LastIndex(rest, ":"); idx >= 0 {
		host = rest[:idx]
		if p, err := parsePositiveInt(rest[idx+1:]); err == nil {
			port = p
		}
	} else {
		host = rest
	}
	return user, host, port
}

// parsePositiveInt parses a non-empty positive decimal integer from s.
// Used for port numbers in parseSSHTarget.
func parsePositiveInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty integer")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-digit in %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// containerEnvPropagationKeys is the curated allowlist of environment
// variables that wrapWithJump propagates across container hops via
// `--env KEY=VALUE` flags. The list is small + load-bearing:
//
//   - XDG_RUNTIME_DIR: libvirt session-socket lookup
//     ($XDG_RUNTIME_DIR/libvirt/libvirt-sock). Without this, every
//     `charly check libvirt …` invocation inside a nested container fails
//     with "Cannot connect to socket" because the in-container
//     default ($XDG_RUNTIME_DIR=/run/user/1000) doesn't match the
//     host's pinned location ($HOME/.local/share/charly-runtime).
//   - DISPLAY: X11 / XWayland display selection.
//   - WAYLAND_DISPLAY: wayland compositor socket selection.
//   - DBUS_SESSION_BUS_ADDRESS: per-user dbus session bus path.
//
// Only NON-EMPTY values are propagated — an unset env in the parent
// stays unset in the child, preserving the existing in-container
// defaults for verbs that don't need the parent's session.
//
// A value that references the host's per-user session runtime dir
// (/run/user/<uid>) is ALSO skipped (see referencesHostSessionRuntimeDir):
// that path is created by the host login session and does not exist in a
// rootless pod, so forcing it breaks podman/buildah/rootless-libvirt. The
// libvirt-socket case above is unaffected — it pins XDG_RUNTIME_DIR to
// $HOME/.local/share/charly-runtime, which is not under /run/user/<uid>.
//
// The allowlist is deliberately narrow. New entries should require
// explicit justification (a verb that needs them, an actual
// reproducible bug from a canary).
var containerEnvPropagationKeys = []string{
	"XDG_RUNTIME_DIR",
	"DISPLAY",
	"WAYLAND_DISPLAY",
	"DBUS_SESSION_BUS_ADDRESS",
}

// hostSessionRuntimeDirPattern matches a reference to the host's per-user XDG
// session runtime directory, `/run/user/<numeric-uid>`. It matches both the
// bare directory (XDG_RUNTIME_DIR=/run/user/1000) and embedded forms
// (DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus).
var hostSessionRuntimeDirPattern = regexp.MustCompile(`/run/user/[0-9]+`)

// referencesHostSessionRuntimeDir reports whether a session-env value points at
// the host's per-user session runtime directory. Such a value must NEVER be
// forced into a target container: `/run/user/<uid>` is created by the host's
// login session (logind/pam_systemd) and does not exist inside a rootless pod,
// which bakes its OWN XDG_RUNTIME_DIR (desktop images use /tmp). Forcing the
// host path makes podman / buildah / rootless-libvirt `lstat` a non-existent
// directory and fail ("lstat /run/user/1000: no such file or directory" /
// "Cannot create user runtime directory '/run/user/1000/libvirt'"). Only an
// explicitly-pinned, bind-mounted runtime location (the libvirt-socket case in
// containerEnvPropagationKeys uses $HOME/.local/share/charly-runtime, NOT
// /run/user/<uid>) is safe to cross the container boundary.
func referencesHostSessionRuntimeDir(v string) bool {
	return hostSessionRuntimeDirPattern.MatchString(v)
}

// buildContainerEnvFlags returns a space-separated string of `--env
// KEY=VALUE` flags for the curated allowlist, suitable for inlining
// into a `<engine> exec ...` command. Returns "" when none of the
// allowlisted vars are set in the parent environ — the caller then
// emits a no-flags exec line, matching the pre-2026-04-27 behaviour
// when no propagation is needed. Values that reference the host's
// per-user session runtime dir (/run/user/<uid>) are skipped — see
// referencesHostSessionRuntimeDir — so the container's own baked
// XDG_RUNTIME_DIR (e.g. /tmp) stands and nested rootless podman /
// buildah / libvirt-session keep working.
func buildContainerEnvFlags() string {
	var flags []string
	for _, key := range containerEnvPropagationKeys {
		val := os.Getenv(key)
		if val == "" {
			continue
		}
		if referencesHostSessionRuntimeDir(val) {
			continue
		}
		flags = append(flags, "--env", deployShellQuote(key+"="+val))
	}
	return strings.Join(flags, " ")
}

// escapeTokens wraps each token in single quotes for safe embedding in
// a bash line. Empty input returns a nil slice so strings.Join yields
// "" rather than " ".
func escapeTokens(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	out := make([]string, len(tokens))
	for i, t := range tokens {
		out[i] = deployShellQuote(t)
	}
	return out
}

// Suppress "unused" complaints for stdlib packages referenced only in
// doc comments — the compile would reject a truly unused import,
// while these helpers use exec / os / bytes variants across branches.
// (Kept as an explicit reminder for code readers. Go's compiler
// enforces the rule for production imports.)
var _ = exec.Command
var _ = os.Stderr
var _ = bytes.NewReader
