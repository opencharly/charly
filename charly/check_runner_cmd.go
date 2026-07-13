package main

// check_runner_cmd.go — host residue of the `charly check run` dispatcher.
//
// The `charly check run` CLI + AI-harness orchestration moved to the compiled-in
// command:check plugin (candy/plugin-check); the host keeps ONLY the loader-coupled
// Mechanisms the plugin cannot perform, reached over the check-config seam
// (host_build_check_config.go). scorePodTargetEntry is one such Mechanism: the
// per-host overlay lookup the iterate sandbox's pod-restart gate needs.

import "fmt"

// scorePodTargetEntry resolves a score's pod-target deploy entry from the
// per-host overlay. The harness restarts-but-never-creates its sandbox pod, so a
// missing entry is an operator precondition failure — fail fast with the
// remediation instead of letting podman surface a raw exec error against a
// container that cannot exist. Consumed by the check-config seam
// (host_build_check_config.go).
func scorePodTargetEntry(cfg *BundleConfig, scoreName, targetName string) (BundleNode, error) {
	if cfg != nil {
		if entry, ok := cfg.Bundle[targetName]; ok {
			return entry, nil
		}
	}
	return BundleNode{}, fmt.Errorf(
		"score %q targets pod %q but no deploy entry exists on this host — provision the harness sandbox first: `charly bundle add %s <ref> --disposable` then `charly start %s` (the sandbox is per-host operator config, never shipped by the repo; see /charly-check:check)",
		scoreName, targetName, targetName, targetName)
}
