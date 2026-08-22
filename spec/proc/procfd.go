package proc

import (
	"os"
	"path/filepath"
	"strconv"
)

// procfd.go — the ONE /proc/<pid>/fd walk.
//
// Two callers ask different questions of the same traversal: the temp sweep asks
// "which paths under my roots does anyone hold open?" (readlink + prefix match,
// keyed on the PATH), and the lock-holder lookup asks "who holds THIS file open?"
// (stat + inode compare, keyed on the PID). Those comparators are deliberately
// different and must stay so — an inode compare cannot answer a prefix question,
// and a prefix match on a path string is exactly the false-negative the holder
// lookup must avoid. What is NOT different is the walk itself, so it lives here
// once rather than in each caller.
//
// Processes owned by other users are skipped silently: their /proc/<pid>/fd is not
// readable, and neither caller can act on what it cannot inspect.

// walkProcFDs calls visit for every open descriptor of every readable process.
// Returning false from visit stops scanning THAT process and moves to the next —
// the shape a caller wants once it has its answer for a pid.
func walkProcFDs(visit func(pid int, fdPath string) (keepScanningPid bool)) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !allDigits(e.Name()) {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if !visit(pid, filepath.Join(fdDir, fd.Name())) {
				break
			}
		}
	}
}
