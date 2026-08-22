package main

import (
	"os"

	"github.com/opencharly/spec/spec"
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
// candy/plugin-pod). Constructed at host_build_pod_lifecycle_dispatch.go (op="update") and
// dispatched via dispatchByDeployTarget (update_deploy_dispatch.go); command:update resolves the
// deploy node PLUGIN-SIDE and threads it in as Node (#PodLifecycleRequest.node, K-wave 2 cone
// CONTESTED — the former TreeJSON whole-tree thread is DELETED). The verb handles the destroy-free
// update path for every target and NEVER regenerates the user-overlay deploy entry — user-overlay
// config (ports/volumes/env/tunnel) is preserved across updates.
type podUpdateCmd struct {
	Box       string
	Tag       string
	Build     bool
	Instance  string
	Seed      bool
	ForceSeed bool
	DataFrom  string
	// Node is the resolved deploy entry command:update (plugin-pod) resolved PLUGIN-SIDE
	// (loaderkit.ResolveMergedTreeViaExecutor → fleet.ResolveNodePath) and threaded on the
	// "pod-lifecycle" op="update" request.
	Node *spec.Deploy
}
