package proc

import (
	"os"
)

// holders.go — "which processes hold this file open?", the kernel-backed answer.
//
// This exists because the alternatives are all wrong in ways that look right:
//
//   - A PIDFILE is wrong. spec/lock deliberately writes NOTHING into its lock files,
//     because the kernel releases an flock when the holder dies: the file's presence
//     proves nothing and its absence proves nothing. A `pid=` line there previously
//     misled three separate readers into trusting the file instead of the process
//     table, which is why it was removed.
//   - Matching by process NAME is wrong. `pkill -x charly` matches `comm` exactly and
//     is blind to cwd, session, systemd scope and cgroup, so it cannot distinguish one
//     session's run from every other session's on the same host. That is not a flag
//     that was missed — it is unscoped by construction.
//
// What IS sound is asking the kernel who currently has the file open, which is what
// fuser(1) does: walk /proc/<pid>/fd and compare by INODE, not by path string, so a
// relative path, a symlink, or a bind mount cannot produce a false negative.
//
// Linux-specific by nature (/proc), and deliberately so — it degrades to "nobody holds
// it" rather than erroring on a platform without /proc, which is the safe direction for
// every caller: a stop verb that finds no holder does nothing.

// PIDsHoldingPath returns the PIDs of every process holding path open, excluding the
// calling process. An empty result means no live holder — which, for a lock file, is
// the only trustworthy way to say "no run is in flight".
//
// Processes owned by other users are skipped silently: their /proc/<pid>/fd is not
// readable, and a caller that cannot signal them has no use for their PIDs anyway.
func PIDsHoldingPath(path string) []int {
	target, err := os.Stat(path)
	if err != nil {
		return nil
	}
	self := os.Getpid()
	var pids []int
	walkProcFDs(func(pid int, fdPath string) bool {
		if pid == self {
			return false
		}
		fi, err := os.Stat(fdPath)
		if err != nil {
			return true
		}
		if os.SameFile(fi, target) {
			pids = append(pids, pid)
			return false // this pid is answered; move on
		}
		return true
	})
	return pids
}
