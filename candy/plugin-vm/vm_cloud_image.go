package vm

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/opencharly/sdk/kit"
)

// vmBuildStamp is the content signature of a built disk base (output/qcow2/<entity>/disk.qcow2),
// recorded at output/qcow2/<entity>/.build.stamp AFTER a successful build. `vm build` compares
// the CURRENT source signature to the recorded one to decide whether the base is content-fresh
// and the (expensive + hazardous) overlay-create + resize can be skipped (P8b-rest: ported
// verbatim from charly/vm_cloud_image.go).
type vmBuildStamp struct {
	BaseSHA256 string `json:"base_sha256"` // sha256 of the fetched cached base qcow2 (kit.FetchQcow2)
	DiskSize   string `json:"disk_size"`   // the resize target (spec.DiskSize) baked into the overlay
	SourceURL  string `json:"source_url"`  // the source image URL (a changed source rebuilds)
}

func vmBuildStampPath(outputDir string) string { return filepath.Join(outputDir, ".build.stamp") }

func readVmBuildStamp(outputDir string) (vmBuildStamp, bool) {
	data, err := os.ReadFile(vmBuildStampPath(outputDir))
	if err != nil {
		return vmBuildStamp{}, false
	}
	var s vmBuildStamp
	if json.Unmarshal(data, &s) != nil {
		return vmBuildStamp{}, false
	}
	return s, true
}

func writeVmBuildStamp(outputDir string, s vmBuildStamp) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(vmBuildStampPath(outputDir), data, 0o644)
}

// diskBaseFresh reports whether the built base at diskPath is content-fresh for the given
// signature: the recorded stamp matches AND disk.qcow2 is present + a readable qcow2 (a
// torn/half-written base from an interrupted build fails `qemu-img info`, so it is never
// mistaken for fresh). The stamp is written LAST on a build, so a matching stamp already
// implies a complete build.
func diskBaseFresh(outputDir, diskPath string, want vmBuildStamp) bool {
	got, ok := readVmBuildStamp(outputDir)
	if !ok || got != want {
		return false
	}
	if _, err := os.Stat(diskPath); err != nil {
		return false
	}
	return exec.Command("qemu-img", "info", diskPath).Run() == nil
}

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
//  1. Fetch the base qcow2 via kit.FetchQcow2 (resumable, sha256-verified).
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
// The caller (vm_build.go) passes outputDir (e.g. "output/qcow2/" from the working project
// tree) and vmStateDir (e.g. ~/.local/share/charly/vm/charly-<vm>/) for runtime state
// persistence.
//
// Idempotent: if existingState.InstanceID is set, the same instance-id is reused so
// cloud-init treats the VM as the same instance and honors first-boot-only directives the
// way the user expects. force rebuilds the disk base even when content-fresh; the default
// idempotent-skips a fresh base so N concurrent beds sharing this entity (serialized on the
// caller's per-entity build flock) build it ONCE and the rest reuse it — never rewriting a
// base a live per-domain overlay backs onto (P33). (P8b-rest: ported verbatim from
// charly/vm_cloud_image.go.)
func BuildCloudImage(
	spec *VmSpec,
	outputDir, vmStateDir string,
	existingState *VmDeployState,
	force bool,
) (CloudImageBuildResult, error) {
	if spec.Source.Kind != "cloud_image" {
		return CloudImageBuildResult{}, fmt.Errorf("BuildCloudImage called with source.kind=%q (expected cloud_image)", spec.Source.Kind)
	}

	// --- Step 1: Fetch base qcow2. ---
	fetched, err := kit.FetchQcow2(spec.Source)
	if err != nil {
		return CloudImageBuildResult{}, fmt.Errorf("fetch qcow2: %w", err)
	}

	// --- Step 2: Prepare output paths + COW overlay. ---
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return CloudImageBuildResult{}, fmt.Errorf("creating output dir: %w", err)
	}
	diskPath := filepath.Join(outputDir, "disk.qcow2")
	seedPath := filepath.Join(outputDir, "seed.iso")

	// Idempotent skip of the (expensive + hazardous) overlay-create + resize: rebuild the base
	// ONLY when the source signature drifted (a rotated `latest` upstream → new base sha, or a
	// changed disk_size/url) or --force is set. A fresh base is left untouched so a live
	// per-domain overlay that already backs onto it is never mutated. The seed ISO below is
	// cheap + non-hazardous and is always (re)rendered so a vm.yml cloud_init edit still takes
	// effect.
	sig := vmBuildStamp{BaseSHA256: fetched.SHA256, DiskSize: spec.DiskSize, SourceURL: spec.Source.URL}
	if !force && diskBaseFresh(outputDir, diskPath, sig) {
		fmt.Fprintf(os.Stderr, "Base disk %s is content-fresh (base sha256=%s) — skipping rebuild\n", diskPath, fetched.SHA256)
	} else {
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
		// Record the content signature LAST — a matching stamp then implies a COMPLETE build.
		if err := writeVmBuildStamp(outputDir, sig); err != nil {
			return CloudImageBuildResult{}, fmt.Errorf("writing build stamp: %w", err)
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
// The qcow2 disk is left untouched — only the seed ISO is regenerated, which is cheap.
// Reuses the stored VmDeployState.InstanceID when supplied so cloud-init still treats the
// VM as the same instance.
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
