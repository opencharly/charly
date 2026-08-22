// spec/container/transfer.go — the container-engine image-transfer host primitive,
// RELOCATED from sdk/kit by #55 coneC (charly/ off sdk/kit): it pipes an image from
// one engine's local store to another via `save | load` (os/exec), the fabric twin of
// the LocalImageExists probe that already lives in this slice. sdk/kit re-exports it
// (var TransferImage = container.TransferImage) so every existing kit.TransferImage
// call site (candy/plugin-build, candy/plugin-box, candy/plugin-deploy-pod, …) is
// unchanged; charly core inlines from here. Co-located with EngineBinary (same package)
// and the LocalImageExists family this transfer path complements.
package container

import (
	"fmt"
	"os"
	"os/exec"
)

// StreamLoad pipes a `save` command's stdout straight into a `load` command's
// stdin — an image transfer with NO intermediate tarball on either side. It is
// the ONE streaming primitive behind every image-delivery path in the tree:
// TransferImage (engine→engine on one host), kit.TransferToRootful
// (rootless→rootful), the VM host→guest cp-box, and the container-venue
// host→nested-store `charly box load`. Each caller supplies its own pair of
// commands; the only thing they ever differed in was how the load side is
// reached, so that — and nothing else — stays at the call site.
//
// A tarball is not merely wasteful here: a multi-GB image cannot land in a
// size-limited tmpfs /tmp, which is what the destination of several of these
// paths actually has.
//
// Both commands' stderr is routed to the caller's stderr so a failure is
// visible rather than swallowed, and the load side's stdout goes there too —
// `podman load` writes its "Loaded image:" line to stdout, which is diagnostic
// output, not a value any caller parses.
//
// NOTE for callers: `podman load` can exit 0 on a TRUNCATED stream, registering
// an image whose overlay layers are incomplete. A green return from StreamLoad
// is therefore NOT proof of an intact image; a caller that cares must probe the
// loaded image afterwards. deploykit's venue transfer does exactly that.
func StreamLoad(save, load *exec.Cmd) error {
	pipe, err := save.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating pipe: %w", err)
	}
	load.Stdin = pipe
	load.Stdout = os.Stderr
	load.Stderr = os.Stderr
	save.Stderr = os.Stderr

	if err := load.Start(); err != nil {
		// Close BOTH ends before returning. save.StdoutPipe() created a pipe PAIR: this
		// `pipe` is the read end, and the WRITE end lives on save.Stdout. Normally
		// save.Wait() closes the write end — but save never started here, so nothing
		// will. Closing only the read end still leaks one descriptor per attempt, and
		// the venue transfer retries once on a torn overlay, so it is per-attempt.
		// (Measured: the coverage for this caught exactly that half-fix.)
		_ = pipe.Close()
		if w, ok := save.Stdout.(interface{ Close() error }); ok {
			_ = w.Close()
		}
		return fmt.Errorf("starting %s: %w", load.Path, err)
	}
	// Close the PARENT's copy of the read end, now that the load child has inherited
	// its own. Without this the pipe never reaches EOF-for-the-writer when the load
	// side exits early: `save` keeps writing into a pipe the parent still holds open,
	// blocks once the kernel buffer fills, and `save.Wait()` never returns — a
	// permanent hang rather than an error. With it, an early load exit gives `save`
	// EPIPE and the transfer fails loudly, which is the only acceptable outcome for a
	// primitive every image-delivery path in the tree runs through.
	// Cmd.Wait also closes this pipe; closing twice is harmless.
	_ = pipe.Close()
	if err := save.Start(); err != nil {
		// The load child is ALREADY RUNNING at this point, so returning here without
		// reaping it orphans a live process — and for `charly box load` that process is
		// an exec session inside somebody's deployed container, which nothing will ever
		// come back for. That part is measured: without this block the child survives the
		// call.
		//
		// The ORDER is load-bearing, but only under a condition worth naming exactly,
		// because I got this wrong twice in opposite directions. Reversing to
		// Wait-then-Kill is HARMLESS when the load side exits on EOF — exec.Cmd.Start's
		// deferred closeDescriptors closes save's write end when Start fails, so `cat`,
		// and a real `podman load`, see EOF and exit either way. It HANGS FOREVER when the
		// load side does not: one that ignores stdin, or is wedged on the far side of an
		// exec. `Wait` then blocks on a process nothing will ever end.
		//
		// Kill first. It costs nothing in the common case and is the difference between
		// returning and never returning in the uncommon one. Measured both ways: reversed,
		// the regression test times out; as written, it returns in ~4ms.
		_ = load.Process.Kill()
		_ = load.Wait()
		return fmt.Errorf("starting %s: %w", save.Path, err)
	}
	saveErr := save.Wait()
	loadErr := load.Wait()
	if saveErr != nil {
		return fmt.Errorf("%s save failed: %w", save.Path, saveErr)
	}
	if loadErr != nil {
		return fmt.Errorf("%s load failed: %w", load.Path, loadErr)
	}
	return nil
}

// TransferImage pipes an image from one engine to another via save | load.
func TransferImage(srcEngine, dstEngine, imageRef string) error {
	srcBinary := EngineBinary(srcEngine)
	dstBinary := EngineBinary(dstEngine)

	fmt.Fprintf(os.Stderr, "Transferring %s from %s to %s\n", imageRef, srcEngine, dstEngine)

	if err := StreamLoad(
		exec.Command(srcBinary, "save", imageRef),
		exec.Command(dstBinary, "load"),
	); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Transferred %s to %s\n", imageRef, dstEngine)
	return nil
}
