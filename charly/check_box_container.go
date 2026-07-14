package main

import (
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"

	"github.com/opencharly/sdk/kit"
)

// checkBoxContainerSeq makes each check-box container name unique across concurrent
// `charly check box` runs in ONE process (the pid already separates processes).
var checkBoxContainerSeq atomic.Uint64

// checkBoxContainerChain is the R44 Option-A box-mode executor: instead of a fresh
// `podman run --rm <img> bash` per check step (N container SETUPS — each a rootfs mount +
// temp-/etc/passwd generation that races concurrent build-store churn, the exit-127
// passwd-file-race root cause), it creates ONE long-lived container from imageRef and returns
// a ContainerChain exec'ing into it (O(N)→O(1) setups), unifying box-mode with check-live's
// `podman exec` model. The per-step isolation of `--rm` is intentionally traded for the shared
// container: `check:` steps are idempotent goss-style probes and check-live ALREADY runs them
// against ONE shared container, so box-mode merely aligns (see the check skill; any check
// secretly relying on fresh-container isolation is flushed by the all-images gate).
//
// The single container-CREATE is the one remaining setup that can race the store; its failure
// is CLASSIFIED (kit.ClassifyContainerInfraFailure): an infra failure returns a MARKED error so
// the caller exits the INFRA class (a plain error → exit 1), never checks-failed — a residual
// setup failure is surfaced LOUDLY, never absorbed by a retry (classify-only, R44 ruling).
//
// Returns the exec chain + a teardown closure (always safe to call, even on the error paths).
func checkBoxContainerChain(engine, imageRef string) (DeployExecutor, func(), error) {
	name := fmt.Sprintf("charly-checkbox-%d-%d", os.Getpid(), checkBoxContainerSeq.Add(1))
	// `--pull=never`: imageRef is already resolved-local (resolveLocalImageRef verified it), so
	// the check must NEVER reach the registry (a pull under churn can 403/hang). `--entrypoint=`
	// clears any baked entrypoint (matching the per-step probe); `sleep infinity` (coreutils,
	// present in every image) keeps the container alive for the per-step `podman exec` probes.
	create := exec.Command(engine, "run", "-d", "--name", name, "--pull=never",
		"--entrypoint=", imageRef, "sleep", "infinity")
	_, stderr, code, err := kit.RunCaptureCmd(create)
	if err != nil || code != 0 {
		if sig, ok := kit.ClassifyContainerInfraFailure(code, stderr); ok {
			return nil, func() {}, kit.ContainerInfraError(sig, code, kit.TrimPreview(stderr))
		}
		if err != nil {
			return nil, func() {}, fmt.Errorf("check-box container create failed: %w (stderr: %s)", err, kit.TrimPreview(stderr))
		}
		return nil, func() {}, fmt.Errorf("check-box container create failed (exit %d): %s", code, kit.TrimPreview(stderr))
	}
	teardown := func() {
		rm := exec.Command(engine, "rm", "-f", name)
		_, _, _, _ = kit.RunCaptureCmd(rm)
	}
	return ContainerChain(engine, name), teardown, nil
}
