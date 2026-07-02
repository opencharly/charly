package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// VmCpBoxCmd loads a locally-built container image into a running VM guest's
// podman storage via `podman save | scp | podman load`. This is the host→guest
// delivery path for images that are NOT on a registry — the case the
// nested-pod-in-VM capability hits: deployNestedPodsInGuest host-builds a nested
// pod's image (e.g. `cachyos.selkies-kde-nvidia`), cp-boxes it in as
// `localhost/charly-<child>:latest`, then the guest's own `charly bundle from-box`
// brings it up as a persistent quadlet — all offline, no registry.
//
// Idempotent: skips the transfer when the guest already has the image.
type VmCpBoxCmd struct {
	VM       string `arg:"" help:"kind:vm entity name (uses its managed charly-<name> ssh alias)"`
	Image    string `arg:"" help:"image ref (short name or full ref) present in host podman storage"`
	As       string `long:"as" help:"after load, tag the image in the guest under this stable ref (e.g. localhost/charly-selkies-kde:latest)"`
	Rootless bool   `long:"rootless" help:"load into the guest USER's rootless podman storage instead of root's — so a rootless --user quadlet (e.g. a nested-pod-in-VM deploy) can run it"`
}

func (c *VmCpBoxCmd) Run() error {
	ref := c.Image
	// Resolve a short name (e.g. "cachyos.selkies-kde-nvidia") to a concrete local ref.
	if !hostImageExists("podman", ref) {
		if resolved, err := ResolveNewestLocalCalVer("podman", ref); err == nil && resolved != "" {
			ref = resolved
		}
	}
	if !hostImageExists("podman", ref) {
		return fmt.Errorf("image %q not found in host podman storage — build it first (charly box build)", c.Image)
	}
	guest := sshParamsForVm(c.VM)
	return TransferImageToGuest(context.Background(), guest, "podman", ref, c.As, c.Rootless, EmitOpts{})
}

// hostImageExists reports whether the host engine has the image locally.
func hostImageExists(engine, ref string) bool {
	cmd := exec.Command(engine, "image", "exists", ref)
	return cmd.Run() == nil
}

// TransferImageToGuest streams a host image into a VM guest's podman storage by
// piping `podman save <ref>` straight into `ssh <guest> [sudo] podman load` — NO
// intermediate tarball on either side. (A file-based copy fails for a multi-GB
// image because the guest's /tmp is a size-limited tmpfs.)
//
// rootless selects WHICH guest podman storage:
//   - rootless == false → ROOT storage (`sudo podman`). For a `sudo podman run
//     --device nvidia.com/gpu=all` consumer that needs /dev/nvidia* via root.
//   - rootless == true  → the SSH user's ROOTLESS storage (`podman`, no sudo).
//     This is what the nested-pod-in-VM deploy needs: deployNestedPodsInGuest
//     brings the pod up with the guest user's own `charly bundle from-box` (a
//     --user quadlet), which reads the USER's podman storage — so the image must
//     land there, not in root's. Rootless GPU works via CDI (/dev/nvidia* are
//     world-rw; the nvidia-driver candy's boot service writes a world-readable
//     /etc/cdi/nvidia.yaml).
//
// VERIFIED transfer: `podman load` can exit 0 on a TRUNCATED stream and
// register an image whose overlay layers are incomplete — a `podman run` then
// fails with `faccessat …/storage/overlay/<hash>: no such file`. So the
// transfer is not trusted on the load exit code alone:
//   - The idempotency skip fires only when the guest already holds the target
//     ref AND that image is verified intact (a name-only check would wrongly
//     skip a present-but-torn image — the case a disposable VM bed hits when
//     `charly update` recreates the domain over the SAME persistent qcow2, so the
//     guest's prior — possibly partial — image survives).
//   - After a fresh load, the image is probed; on the overlay-corruption
//     signature it is dropped and re-streamed ONCE; a second failure is a hard
//     error (surfaced, never silently shipped as a broken image).
//
// Requires an *SSHExecutor (the VM case). The GPU check bed is the first caller.
func TransferImageToGuest(ctx context.Context, de DeployExecutor, hostEngine, ref, as string, rootless bool, opts EmitOpts) error {
	if de == nil {
		return fmt.Errorf("TransferImageToGuest: nil executor")
	}
	if hostEngine == "" {
		hostEngine = "podman"
	}

	// The stable `as` name (when set) is what the consumer references; else the
	// streamed ref itself.
	probeRef := ref
	if as != "" {
		probeRef = as
	}

	// Verified idempotency.
	if guestHasImage(ctx, de, probeRef, rootless) {
		if !guestImageCorrupt(ctx, de, probeRef, rootless) {
			fmt.Fprintf(os.Stderr, "cp-box: guest already has %s (verified intact) — skipping transfer\n", probeRef)
			return nil
		}
		fmt.Fprintf(os.Stderr, "cp-box: guest %s is present but corrupt (torn overlay) — re-loading\n", probeRef)
		removeGuestImages(ctx, de, rootless, probeRef, ref)
	}

	sshExec, ok := de.(*SSHExecutor)
	if !ok {
		return fmt.Errorf("TransferImageToGuest: requires an SSH executor (got %T)", de)
	}

	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "[dry-run] %s save %s | ssh <guest> %s load\n", hostEngine, ref, podmanCmd(rootless))
		return nil
	}

	if err := streamLoadAndTag(ctx, sshExec, de, hostEngine, ref, as, rootless, opts); err != nil {
		return err
	}
	if guestImageCorrupt(ctx, de, probeRef, rootless) {
		fmt.Fprintf(os.Stderr, "cp-box: load produced a corrupt %s — re-streaming once\n", probeRef)
		removeGuestImages(ctx, de, rootless, probeRef, ref)
		if err := streamLoadAndTag(ctx, sshExec, de, hostEngine, ref, as, rootless, opts); err != nil {
			return err
		}
		if guestImageCorrupt(ctx, de, probeRef, rootless) {
			return fmt.Errorf("cp-box: %s is still corrupt in guest storage after a clean re-load — transfer unreliable", probeRef)
		}
	}
	fmt.Fprintf(os.Stderr, "cp-box: %s is now in guest storage (verified intact)\n", probeRef)
	return nil
}

// podmanCmd returns the guest podman invocation prefix for the chosen storage:
// "podman" (the SSH user's rootless storage) when rootless, else "sudo podman"
// (root storage). Used everywhere cp-box touches guest podman so root/rootless
// stays consistent across the load, the integrity probe, and the tag.
func podmanCmd(rootless bool) string {
	if rootless {
		return "podman"
	}
	return "sudo podman"
}

// guestHasImage reports whether the guest engine holds the image by name.
func guestHasImage(ctx context.Context, de DeployExecutor, ref string, rootless bool) bool {
	_, _, code, err := de.RunCapture(ctx, podmanCmd(rootless)+" image exists "+deployShellQuote(ref))
	return err == nil && code == 0
}

// guestImageCorrupt reports whether an existing guest image is unusable because
// its overlay storage is torn (a lower layer's diff dir is missing). It mounts
// the image's rootfs via a throwaway `podman run … /usr/bin/true` (no GPU, no
// entrypoint) — a torn layer fails container setup with a
// `…/storage/overlay/<hash>: no such file` error. ONLY that storage signature
// counts as corruption: any other failure (e.g. the probe binary is absent, an
// exotic entrypoint) means the overlay mounted fine, so the image is treated as
// intact — the probe is an integrity check, not an entrypoint test.
func guestImageCorrupt(ctx context.Context, de DeployExecutor, ref string, rootless bool) bool {
	stdout, stderr, code, err := de.RunCapture(ctx,
		podmanCmd(rootless)+" run --rm --entrypoint /usr/bin/true "+deployShellQuote(ref))
	if err == nil && code == 0 {
		return false
	}
	return strings.Contains(stdout+stderr, "storage/overlay")
}

// removeGuestImages best-effort removes the given refs (and their now-unused
// layers) from the guest so a subsequent load re-extracts clean overlay dirs.
// `podman rmi -f` on every ref that points at the torn image ID is required —
// dropping only one tag leaves the broken layers in storage, and a re-load that
// shares those layer digests would skip extraction and inherit the corruption.
func removeGuestImages(ctx context.Context, de DeployExecutor, rootless bool, refs ...string) {
	for _, r := range refs {
		if r == "" {
			continue
		}
		_, _, _, _ = de.RunCapture(ctx, podmanCmd(rootless)+" rmi -f "+deployShellQuote(r))
	}
}

// streamLoadAndTag streams `hostEngine save <ref>` into the guest's rootful
// podman storage (`ssh … sudo podman load`) with NO intermediate tarball, then
// (when `as` is set) tags the loaded ref under that stable name. Disk-backed on
// the guest (podman extracts into /var/lib/containers), so no tmpfs limit.
func streamLoadAndTag(ctx context.Context, sshExec *SSHExecutor, de DeployExecutor, hostEngine, ref, as string, rootless bool, opts EmitOpts) error {
	fmt.Fprintf(os.Stderr, "cp-box: streaming %s into guest podman (save | ssh load)...\n", ref)
	var sshArgs []string
	if rootless {
		sshArgs = append(sshExec.sshBaseArgs(), "podman", "load")
	} else {
		sshArgs = append(sshExec.sshBaseArgs(), "sudo", "podman", "load")
	}
	save := osExecCommand(ctx, hostEngine, "save", ref)
	load := osExecCommand(ctx, "ssh", sshArgs...)
	pipe, err := save.StdoutPipe()
	if err != nil {
		return fmt.Errorf("cp-box: stdout pipe: %w", err)
	}
	load.Stdin = pipe
	load.Stdout = os.Stderr
	load.Stderr = os.Stderr
	save.Stderr = os.Stderr
	if err := load.Start(); err != nil {
		return fmt.Errorf("cp-box: start ssh load: %w", err)
	}
	if err := save.Start(); err != nil {
		return fmt.Errorf("cp-box: start %s save: %w", hostEngine, err)
	}
	saveErr := save.Wait()
	loadErr := load.Wait()
	if saveErr != nil {
		return fmt.Errorf("cp-box: %s save %s: %w", hostEngine, ref, saveErr)
	}
	if loadErr != nil {
		return fmt.Errorf("cp-box: guest podman load: %w", loadErr)
	}
	if as != "" {
		// Tag in the SAME storage the load targeted: as the user (RunUser +
		// `podman tag`) for rootless, as root (RunSystem + `sudo podman tag`)
		// otherwise — a cross-scope tag would not see the just-loaded image.
		var tagErr error
		if rootless {
			tagErr = de.RunUser(ctx, "podman tag "+deployShellQuote(ref)+" "+deployShellQuote(as), opts)
		} else {
			tagErr = de.RunSystem(ctx, "sudo podman tag "+deployShellQuote(ref)+" "+deployShellQuote(as), opts)
		}
		if tagErr != nil {
			return fmt.Errorf("cp-box: guest podman tag %s -> %s: %w", ref, as, tagErr)
		}
	}
	return nil
}

// osExecCommand is a tiny indirection so tests can stub host command exec.
var osExecCommand = exec.CommandContext
