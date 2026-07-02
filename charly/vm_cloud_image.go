package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// CloudImageBuildResult summarizes what `buildCloudImage` produced so
// the caller (vm_build.go) can populate output paths and record state.
type CloudImageBuildResult struct {
	// DiskPath is the absolute path to the output qcow2 (a COW overlay
	// on top of the cached base image).
	DiskPath string

	// SeedIsoPath is the absolute path to the NoCloud cidata ISO.
	// Empty when the renderer produced no user-data (shouldn't happen
	// for cloud_image sources, but defensive).
	SeedIsoPath string

	// InstanceID is the stable UUIDv4 persisted into VmDeployState.
	InstanceID string

	// BaseImageSHA256 is the sha256 of the fetched cached qcow2.
	// Useful for audit / migration detection.
	BaseImageSHA256 string

	// CloudInitDigest is sha256 of the rendered user-data — used by
	// the vm lifecycle to detect whether the seed ISO needs regeneration
	// (drift from last-recorded digest).
	CloudInitDigest string
}

// BuildCloudImage is the pipeline for preparing a cloud-image VM disk:
//
//  1. Fetch the base qcow2 via FetchQcow2 (resumable, sha256-verified).
//  2. qemu-img create a copy-on-write overlay at outputDir/disk.qcow2
//     with the cached base as its backing file.
//  3. qemu-img resize the overlay to spec.DiskSize (cloud-utils-growpart
//     inside the guest expands the partition at first boot).
//  4. Resolve VmSSH key injection channels (D13 auto-defaults) and pick
//     the SSH public key per spec.SSH.KeySource.
//  5. Render cloud-init via RenderCloudInit.
//  6. Pack user-data / meta-data / network-config into outputDir/seed.iso
//     via WriteSeedISO.
//
// The caller (vm_build.go) passes outputDir (e.g. "output/qcow2/" from
// the working project tree) and vmStateDir (e.g.
// ~/.local/share/charly/vm/charly-<vm>/) for runtime state persistence.
//
// Idempotent: if existingState.InstanceID is set, the same instance-id
// is reused so cloud-init treats the VM as the same instance and
// honors first-boot-only directives the way the user expects.
func BuildCloudImage(
	spec *VmSpec,
	outputDir, vmStateDir string,
	existingState *VmDeployState,
) (CloudImageBuildResult, error) {
	if spec.Source.Kind != "cloud_image" {
		return CloudImageBuildResult{}, fmt.Errorf("BuildCloudImage called with source.kind=%q (expected cloud_image)", spec.Source.Kind)
	}

	// --- Step 1: Fetch base qcow2. ---
	fetched, err := FetchQcow2(spec.Source)
	if err != nil {
		return CloudImageBuildResult{}, fmt.Errorf("fetch qcow2: %w", err)
	}

	// --- Step 2: Prepare output paths + COW overlay. ---
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return CloudImageBuildResult{}, fmt.Errorf("creating output dir: %w", err)
	}
	diskPath := filepath.Join(outputDir, "disk.qcow2")
	seedPath := filepath.Join(outputDir, "seed.iso")

	// Always recreate the overlay so a new base or a new disk_size
	// takes effect. The overlay is cheap — it points at the cached
	// base file.
	_ = os.Remove(diskPath)
	if err := qemuImgCreateOverlay(fetched.Path, diskPath); err != nil {
		return CloudImageBuildResult{}, err
	}

	// --- Step 3: Grow disk to requested size. ---
	if spec.DiskSize != "" {
		if err := qemuImgResize(diskPath, spec.DiskSize); err != nil {
			return CloudImageBuildResult{}, err
		}
	}

	// --- Step 4: Resolve runtime params for the cloud-init renderer. ---
	instanceID := ""
	if existingState != nil && existingState.InstanceID != "" {
		instanceID = existingState.InstanceID
	} else {
		instanceID = newUUID4()
	}

	_, cloudInitEnabled := ResolveKeyInjectionChannels(spec)

	pubKey, err := resolveSSHPubKeyForSpec(spec, vmStateDir)
	if err != nil {
		return CloudImageBuildResult{}, fmt.Errorf("resolving ssh pubkey: %w", err)
	}

	hostname := ""
	if spec.CloudInit != nil {
		hostname = spec.CloudInit.Hostname
	}

	rt := CloudInitRuntimeParams{
		SSHPublicKey:          pubKey,
		InstanceID:            instanceID,
		Hostname:              hostname,
		InjectKeyViaCloudInit: cloudInitEnabled,
	}

	// --- Step 5: Render cloud-init. ---
	userData, metaData, networkConfig, err := RenderCloudInit(spec, rt)
	if err != nil {
		return CloudImageBuildResult{}, fmt.Errorf("rendering cloud-init: %w", err)
	}
	digest := sha256.Sum256([]byte(userData))

	// --- Step 6: Pack seed ISO. ---
	if err := WriteSeedISO(seedPath, userData, metaData, networkConfig); err != nil {
		return CloudImageBuildResult{}, fmt.Errorf("writing seed iso: %w", err)
	}

	return CloudImageBuildResult{
		DiskPath:        diskPath,
		SeedIsoPath:     seedPath,
		InstanceID:      instanceID,
		BaseImageSHA256: fetched.SHA256,
		CloudInitDigest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

// RegenerateSeedISO re-renders cloud-init user-data/meta-data/network-config
// from the current VmSpec and overwrites the seed ISO in place. Used by
// `charly vm create` to pick up vm.yml edits (new runcmd entries, packages,
// network config, etc.) without requiring a full `charly vm build` rerun.
//
// The qcow2 disk is left untouched — only the 180-sector seed ISO is
// regenerated, which is cheap (xorriso is fast). Reuses the stored
// VmDeployState.InstanceID when supplied so cloud-init still treats the
// VM as the same instance (first-boot directives re-fire per instance-id
// change, which callers may or may not want).
func RegenerateSeedISO(spec *VmSpec, seedPath, vmStateDir string, existingState *VmDeployState) error {
	// Source-kind agnostic: any VM with a non-nil cloud_init: block gets a
	// seed ISO. Cloud_image and bootstrap-VM both consume cloud-init via
	// the NoCloud datasource; bootc-VM optionally does too when its image
	// includes the cloud-init candy.
	if spec.CloudInit == nil {
		return nil
	}

	instanceID := ""
	if existingState != nil && existingState.InstanceID != "" {
		instanceID = existingState.InstanceID
	} else {
		instanceID = newUUID4()
	}
	_, cloudInitEnabled := ResolveKeyInjectionChannels(spec)
	pubKey, err := resolveSSHPubKeyForSpec(spec, vmStateDir)
	if err != nil {
		return fmt.Errorf("resolving ssh pubkey: %w", err)
	}
	hostname := ""
	if spec.CloudInit != nil {
		hostname = spec.CloudInit.Hostname
	}
	rt := CloudInitRuntimeParams{
		SSHPublicKey:          pubKey,
		InstanceID:            instanceID,
		Hostname:              hostname,
		InjectKeyViaCloudInit: cloudInitEnabled,
	}
	userData, metaData, networkConfig, err := RenderCloudInit(spec, rt)
	if err != nil {
		return fmt.Errorf("rendering cloud-init: %w", err)
	}
	if err := WriteSeedISO(seedPath, userData, metaData, networkConfig); err != nil {
		return fmt.Errorf("writing seed iso: %w", err)
	}
	return nil
}

// qemuImgCreateOverlay runs `qemu-img create -f qcow2 -F qcow2 -b
// <base> <overlay>` to produce a copy-on-write overlay.
func qemuImgCreateOverlay(basePath, overlayPath string) error {
	cmd := exec.Command("qemu-img", "create",
		"-f", "qcow2",
		"-F", "qcow2",
		"-b", basePath,
		overlayPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img create overlay: %w", err)
	}
	return nil
}

// qemuImgResize runs `qemu-img resize <disk> <size>`. The guest's
// cloud-utils-growpart expands the root partition at first boot to
// match the new total size.
func qemuImgResize(diskPath, size string) error {
	cmd := exec.Command("qemu-img", "resize", diskPath, size)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("qemu-img resize %s %s: %w", diskPath, size, err)
	}
	return nil
}

// newUUID4 generates an RFC 4122 v4 UUID. Used for cloud-init
// instance-id on first VM create (persisted into VmDeployState).
func newUUID4() string {
	var buf [16]byte
	_, err := rand.Read(buf[:])
	if err != nil {
		// Extremely unlikely; fall back to sha256 of pid+timestamp.
		h := sha256.Sum256([]byte(fmt.Sprintf("%d", os.Getpid())))
		copy(buf[:], h[:16])
	}
	// RFC 4122 section 4.4: set version to 4 and variant to 10xx.
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(buf[0:4]),
		hex.EncodeToString(buf[4:6]),
		hex.EncodeToString(buf[6:8]),
		hex.EncodeToString(buf[8:10]),
		hex.EncodeToString(buf[10:16]))
}

// resolveSSHPubKeyForSpec picks the SSH pubkey per VmSSH.KeySource
// semantics (auto | generate | none | <path>). vmStateDir is used as
// the home for generate-mode keys.
func resolveSSHPubKeyForSpec(spec *VmSpec, vmStateDir string) (string, error) {
	src := "auto"
	if spec.SSH != nil && spec.SSH.KeySource != "" {
		src = spec.SSH.KeySource
	}
	// Delegate to the existing resolveSSHPubKey helper in vm.go so
	// we benefit from the same auto-search + generate-path behavior.
	return resolveSSHPubKey(src, vmStateDir)
}
