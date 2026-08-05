package main

import (
	"os"
)

// isTerminal reports whether stdout is connected to a terminal. Package-level var for testability.
// Relocated from the deleted shell.go (Cutover B unit 2) — used by host_build_pod_lifecycle_dispatch.go's
// hostBuildPodShell (the TTY-detection invariant documented there).
var isTerminal = defaultIsTerminal

func defaultIsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// podUpdateCmd is the host-side dispatch struct for `charly update` (now command:update in
// candy/plugin-pod). Constructed at host_build_pod_lifecycle_dispatch.go:186 (op="update") and
// dispatched via dispatchByDeployTarget (update_deploy_dispatch.go); command:update resolves the
// deploy tree PLUGIN-SIDE and threads it in as TreeJSON (#55 Cone A Unit 3b). The verb handles the
// destroy-free update path for every target and NEVER regenerates the user-overlay deploy entry —
// user-overlay config (ports/volumes/env/tunnel) is preserved across updates.
type podUpdateCmd struct {
	Box       string
	Tag       string
	Build     bool
	Instance  string
	Seed      bool
	ForceSeed bool
	DataFrom  string
	// TreeJSON is the merged deploy tree command:update (plugin-pod) resolved PLUGIN-SIDE and
	// threaded into the "pod-lifecycle" op="update" payload (#55 Cone A Unit 3b). Marshalled
	// map[string]spec.BundleNode.
	TreeJSON []byte
}
