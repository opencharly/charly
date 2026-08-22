package proc

import (
	"io"
	"os/exec"
	"sort"
	"syscall"
	"time"

	"github.com/opencharly/spec/spec"
)

// ProcessShutdownGrace bounds each stage of the graceful process-group
// shutdown (ShutdownProcessGroup). Named + documented, not a tuned magic
// value: it is the guarantee that closing a stdio-carried process ALWAYS
// terminates instead of wedging the caller on a child that ignores its stdin
// EOF.
const ProcessShutdownGrace = 2 * time.Second

// ShutdownProcessGroup stops one Setpgid child process gracefully-first and
// reaps it. The escalation ladder is:
//
//  1. close stdin — the polite EOF signal every stdio-carried protocol
//     answers by exiting on its own;
//  2. one ProcessShutdownGrace for the process to exit;
//  3. SIGTERM to the whole process group — the explicit termination
//     request, so a well-behaved child can still shut down cleanly;
//  4. a second ProcessShutdownGrace;
//  5. only then SIGKILL to the group — the final escalation a child cannot
//     ignore — and reap.
//
// The ladder never opens with a force-kill: SIGKILL orphans nothing (the
// whole group dies), but it also gives no child the chance to flush protocol
// state, so it stays the last resort.
//
// done must be closed by the caller's cmd.Wait() monitor once the process is
// reaped; ShutdownProcessGroup returns only after that, so no zombie or
// running group member survives the call. Call it once per process — wrap in
// sync.Once when several paths may close the same process.
func ShutdownProcessGroup(cmd *exec.Cmd, stdin io.Closer, done <-chan struct{}) {
	_ = stdin.Close()
	select {
	case <-done:
		return
	case <-time.After(ProcessShutdownGrace):
	}
	signalProcessGroup(cmd, syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-time.After(ProcessShutdownGrace):
	}
	signalProcessGroup(cmd, syscall.SIGKILL)
	<-done
}

func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process != nil {
		// Negative pid = the whole process group (requires Setpgid).
		_ = syscall.Kill(-cmd.Process.Pid, sig)
	}
}

// SortedEnvPairs renders an environment map as a deterministically ordered
// "KEY=value" slice. Sorting makes launched-process environments and rendered
// remote commands byte-reproducible across runs (map iteration order is not).
func SortedEnvPairs(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

// RemoteLaunchCommand renders one exact-argv launch as the single command
// string OpenSSH must receive: POSIX single-quoting each token preserves the
// argv boundary and prevents interpolation by the remote login shell. The
// working directory and environment belong to the TARGET side, so they ride
// inside the remote command (`cd <dir> && env <K=V>… <argv>`); applying them
// to the local ssh carrier would conflate controller and target state.
func RemoteLaunchCommand(launch spec.ProcessLaunch) string {
	remote := ""
	if launch.WorkingDir != "" {
		remote = "cd " + spec.ShellQuote(launch.WorkingDir) + " && "
	}
	if len(launch.Env) > 0 {
		remote += "env"
		for _, pair := range SortedEnvPairs(launch.Env) {
			remote += " " + spec.ShellQuote(pair)
		}
		remote += " "
	}
	for i, arg := range launch.Argv {
		if i > 0 {
			remote += " "
		}
		remote += spec.ShellQuote(arg)
	}
	return remote
}
