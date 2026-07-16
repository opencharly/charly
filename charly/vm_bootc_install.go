package main

import (
	"fmt"
	"github.com/opencharly/sdk/spec"
	"os"
	"path/filepath"
	"strings"
)

// BootcVMResult mirrors CloudImageBuildResult / BootstrapVMResult.
type BootcVMResult struct {
	DiskPath        string
	SeedIsoPath     string
	InstanceID      string
	BaseImageSHA256 string
	CloudInitDigest string
}

// resolveBootcImageRef maps a bootc source.image to a concrete OCI ref.
//
// A full ref (containing "/", e.g. "quay.io/fedora/fedora-bootc:43" or a
// pinned "…@sha256:…") passes through unchanged — bootc may pull it from a
// registry. An internal kind:image short name (e.g. "fedora-bootc") resolves
// against local podman storage to its newest CalVer tag via the shared
// resolveLocalImageRef: charly is CalVer-only, so there is NO `:latest` fallback —
// the bootc image must be built first (`charly box build <name>`), which is
// surfaced as an actionable error when it is missing.
func resolveBootcImageRef(engine, image string) (string, error) {
	if strings.Contains(image, "/") {
		return image, nil
	}
	resolved, err := resolveLocalImageRef(engine, image)
	if err != nil {
		return "", fmt.Errorf("resolving bootc image %q: %w (build it first with `charly box build %s`)", image, err, image)
	}
	return resolved, nil
}

// BuildBootcVM creates a fresh VM disk by running `bootc install
// to-disk` inside a privileged container that hosts the referenced
// kind:image entry. Replaces the Task-21 stub at vm_build.go:198.
//
// The bootc image carries its own kernel + initramfs + bootloader
// integration, so this path skips EmitDiskBuildScript (no chroot
// grub-install needed). It uses RunPrivileged for the privileged
// container and qemu-img convert raw → qcow2 (handled by bootc).
func BuildBootcVM(
	vmSpec *VmSpec,
	outputDir, vmStateDir string,
	existingState *spec.VmDeployState,
	engine string,
) (BootcVMResult, error) {
	if vmSpec.Source.Kind != "bootc" {
		return BootcVMResult{}, fmt.Errorf("BuildBootcVM called with source.kind=%q (expected bootc)", vmSpec.Source.Kind)
	}
	if vmSpec.Source.Box == "" {
		return BootcVMResult{}, fmt.Errorf("source.box is required for bootc VMs")
	}
	if vmSpec.DiskSize == "" {
		return BootcVMResult{}, fmt.Errorf("disk_size is required for bootc VMs")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return BootcVMResult{}, fmt.Errorf("creating output dir: %w", err)
	}

	rootfs := vmSpec.Source.Rootfs
	if rootfs == "" {
		rootfs = "ext4"
	}

	// Resolve the bootc image ref (full ref → as-is; internal short name →
	// newest local CalVer tag; NO `:latest` fallback — see resolveBootcImageRef).
	imageRef, err := resolveBootcImageRef(engine, vmSpec.Source.Box)
	if err != nil {
		return BootcVMResult{}, err
	}

	// Render bootc install script. We allocate the raw disk on the host,
	// bind-mount it into the container, and let bootc write to /dev/loopX
	// via a loop device the container creates.
	rawHost := filepath.Join(outputDir, "disk.raw")
	qcowHost := filepath.Join(outputDir, "disk.qcow2")
	rootSizeFlag := ""
	if vmSpec.Source.RootSize != "" {
		rootSizeFlag = fmt.Sprintf(" --root-size %s", vmSpec.Source.RootSize)
	}
	kargFlag := ""
	if strings.TrimSpace(vmSpec.Source.KernelArgs) != "" {
		kargFlag = fmt.Sprintf(" --karg %q", vmSpec.Source.KernelArgs)
	}
	script := fmt.Sprintf(`set -euo pipefail
truncate -s %s /out/disk.raw
LOOP=$(losetup --find --show /out/disk.raw)
trap 'losetup -d "$LOOP" 2>/dev/null || true' EXIT
bootc install to-disk \
  --filesystem %s%s%s \
  --target-no-signature-verification \
  "$LOOP"
sync
losetup -d "$LOOP"
trap - EXIT
qemu-img convert -O qcow2 /out/disk.raw /out/disk.qcow2
rm -f /out/disk.raw
`, vmSpec.DiskSize, rootfs, rootSizeFlag, kargFlag)

	if err := RunPrivileged(PrivilegedRun{
		Image:      imageRef,
		Script:     script,
		OutputPath: "/out/disk.qcow2",
		OutputDest: qcowHost,
	}); err != nil {
		return BootcVMResult{}, fmt.Errorf("running bootc install to-disk: %w", err)
	}
	_ = rawHost // raw is removed inside the container

	res := BootcVMResult{
		DiskPath: qcowHost,
	}
	if vmSpec.CloudInit != nil {
		seedPath := filepath.Join(outputDir, "seed.iso")
		if err := RegenerateSeedISO(vmSpec, seedPath, vmStateDir, existingState); err != nil {
			return BootcVMResult{}, fmt.Errorf("rendering cloud-init seed ISO: %w", err)
		}
		res.SeedIsoPath = seedPath
		if existingState != nil && existingState.InstanceID != "" {
			res.InstanceID = existingState.InstanceID
		}
	}
	return res, nil
}
