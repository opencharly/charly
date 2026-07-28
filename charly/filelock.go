package main

// filelock.go — charly core's ONE remaining advisory-flock wrapper: acquireDeployConfigLock. The
// primitive itself lives in sdk/kit (kit.AcquireFileLock/kit.ErrLockBusy) so it is shared,
// byte-identical, with the compiled-in candy/plugin-preempt (the resource arbiter's ledger lock)
// across the module boundary (R3). The former acquireFileLock/errLockBusy pass-through aliases
// carried ZERO charly-specific behavior (a 1:1 signature match to the kit primitives) — deleted
// (#118, P15 host-seam classification); every caller now calls kit.AcquireFileLock/kit.ErrLockBusy
// directly, and their lock-semantics test coverage moved to sdk/kit/filelock_test.go (a genuine
// coverage gap sdk itself had — see that file's own header).
//
// acquireDeployConfigLock stays here for now: it is injected as a callback into
// deploykit.SaveVmDeployState/RemoveVmDeployEntry (host_build_config_resolve.go), a shape that
// predates the observation below and isn't part of this cutover's disjoint scope. Note for a
// future batch: DeployConfigPath is ITSELF a pure alias of kit.DefaultDeployConfigPath (deploy.go),
// so this function's entire body is now provably sdk-portable too — it could move to sdk/kit
// directly, dropping the injected acquireLock parameter from SaveVmDeployState/RemoveVmDeployEntry
// entirely. Flagged rather than folded in here since it touches an already-merged sdk signature
// outside this file's own disjoint slice.
//
// Contention semantics (kit.AcquireFileLock's `blocking` arg):
//   - per-bed check lock      .check/<bed>/.lock                    (fail-fast)
//   - AI-harness run lock     .check/<score>/.lock                 (fail-fast)
//   - deploy-config write     ~/.config/charly/charly.yml.lock     (blocking)
//   - install ledger          ~/.config/opencharly/installed/.lock (blocking)
//   - resource-arbiter ledger ~/.local/share/charly/preemption/.lock (blocking, IN the plugin)

import (
	"fmt"

	"github.com/opencharly/sdk/kit"
)

// acquireDeployConfigLock serializes the read-modify-write of the per-host deploy overlay
// (~/.config/charly/charly.yml) across concurrent charly processes. Blocking (a config write is
// brief, so serialize rather than fail).
func acquireDeployConfigLock() (func() error, error) {
	path, err := DeployConfigPath()
	if err != nil {
		return nil, fmt.Errorf("deploy-config lock path: %w", err)
	}
	return kit.AcquireFileLock(path+".lock", true)
}
