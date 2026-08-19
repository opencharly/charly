package check

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/opencharly/spec/proc"
)

// stop_cmd.go — `charly check stop <bed>`, the SCOPED way to stop a bed run.
//
// Why this verb exists, stated plainly because the gap it closes has already cost
// real work: there was no charly verb that stops a running bed. The only way to stop
// one was to kill the process, and the reachable form of that is `pkill -x charly` —
// which matches `comm` exactly and is blind to cwd, session, systemd scope and cgroup.
// It is not "a kill that needs a careful flag"; it is unscoped BY CONSTRUCTION, so on
// a host running more than one session it cannot stop your bed without stopping
// everyone's. That is exactly what happened: one session stopping four of its own bed
// runs terminated two unrelated R10 runs mid-flight, and its own verification —
// counting charly processes machine-wide and finding zero — read as local success
// while measuring the whole host.
//
// R4 names that shape precisely: a capability no charly verb expresses is a charly
// GAP, and the gap manufactures the workaround. This closes it.
//
// HOW it scopes, and why not the obvious ways:
//
//   - NOT by process name. See above; unscoped by construction.
//   - NOT by a pidfile. spec/lock deliberately writes NOTHING into its lock files,
//     because the kernel drops an flock when the holder dies — the file's presence
//     proves nothing and its absence proves nothing. A `pid=` line there previously
//     misled three readers into trusting the file over the process table.
//   - BY LOCK HOLDER. A bed run holds an flock on `.check/<bed>/.lock` for its whole
//     lifetime (bed_session.go). Asking the kernel who holds that specific file open
//     identifies this bed's runner and nothing else — no other bed, no other session,
//     no unrelated charly.
//
// The signal is SIGTERM first so the runner's own shutdown hooks run (they deregister
// temp dirs and release the lock); SIGKILL only after a grace period, and only for a
// holder that ignored the term.
type CheckStopCmd struct {
	Bed   string `arg:"" help:"Bed (a disposable: true deploy) whose in-flight run should be stopped."`
	Grace int    `name:"grace" default:"20" help:"Seconds to wait for the runner to exit after SIGTERM before escalating to SIGKILL."`
}

func (c *CheckStopCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	lockPath := filepath.Join(cwd, ".check", c.Bed, ".lock")

	holders := proc.PIDsHoldingPath(lockPath)
	if len(holders) == 0 {
		// Idempotent and quiet: a bed that is not running is the desired end state, so
		// this is a success, not an error. It is also the honest answer when the bed
		// never ran at all — the lock file may not even exist.
		fmt.Printf("check stop: no run in flight for %q (nothing holds %s)\n", c.Bed, lockPath)
		return nil
	}

	for _, pid := range holders {
		fmt.Printf("check stop: signalling the %q runner (pid %d) with SIGTERM\n", c.Bed, pid)
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("check stop: SIGTERM pid %d: %w", pid, err)
		}
	}

	deadline := time.Now().Add(time.Duration(c.Grace) * time.Second)
	for time.Now().Before(deadline) {
		if len(proc.PIDsHoldingPath(lockPath)) == 0 {
			fmt.Printf("check stop: %q stopped cleanly\n", c.Bed)
			return c.reportResidue(cwd)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Escalate only against whoever is STILL holding the lock — re-derived, never the
	// original list, because a pid that already exited must not be signalled again (its
	// number can be reused by an unrelated process in the meantime).
	stubborn := proc.PIDsHoldingPath(lockPath)
	for _, pid := range stubborn {
		fmt.Printf("check stop: pid %d ignored SIGTERM for %ds — escalating to SIGKILL\n", pid, c.Grace)
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			return fmt.Errorf("check stop: SIGKILL pid %d: %w", pid, err)
		}
	}
	fmt.Printf("check stop: %q stopped\n", c.Bed)
	return c.reportResidue(cwd)
}

// reportResidue names what the stopped run left behind rather than tearing it down.
// A bed run that is interrupted mid-sequence can leave a pod or a libvirt domain up,
// and charly already has scoped verbs for both. Removing them from here would make a
// "stop" silently destructive, and the deploy may be exactly what the operator wants
// to inspect — the same reason a FAILED bed leaves its target running for debugging.
func (c *CheckStopCmd) reportResidue(cwd string) error {
	runDir := filepath.Join(cwd, ".check", c.Bed)
	if _, err := os.Stat(runDir); err == nil {
		fmt.Printf("check stop: run artifacts remain under %s\n", runDir)
	}
	fmt.Printf("check stop: if the run left a target up, tear it down with the scoped verb —\n")
	fmt.Printf("  pod: charly remove %s\n", c.Bed)
	fmt.Printf("  vm:  charly vm destroy <entity> --domain %s --if-exists\n", c.Bed)
	return nil
}
