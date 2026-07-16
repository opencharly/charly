package vm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// vm_build.go — the command:vm `charly vm build` DRIVE (P8b-rest: the disk-build ENGINE moved
// HERE from charly core — the same inversion candy/plugin-build's podman DRIVE already went
// through behind HostBuild("build-prep") in P8b). The host resolves the kind:vm entity + the
// build vocabulary + the per-source-kind image refs into the spec.VmBuildReply envelope
// (HostBuild("vm-build") — LoadUnified, LoadBuildConfigForBox, resolveBootcImageRef,
// ensureBuilderImageBuilt are loader + box-store Mechanisms a sdk-only candy cannot run); this
// command runs the actual privileged-container / qemu-img / bootc-install / cloud-init exec
// itself and prints its own progress to the shared stdio (compiled-in, so os.Stderr is the
// operator's terminal).
type VmBuildCmd struct {
	Box       string `arg:"" help:"Bootc image name"`
	Size      string `long:"size" help:"Override disk size (e.g. 20G, '20 GiB')"`
	RootSize  string `long:"root-size" help:"Override root partition size (e.g. 10G)"`
	Tag       string `long:"tag" help:"Image tag override"`
	Type      string `long:"type" default:"qcow2" help:"Output format: qcow2, raw"`
	Transport string `long:"transport" help:"Image transport: registry, containers-storage, oci, oci-archive"`
	Console   bool   `long:"console" help:"Enable console output for debugging"`
	Force     bool   `long:"force" help:"Rebuild the disk base even when content-fresh (default: skip if the base already matches the source). SINGLE-BED ONLY — do NOT force-rebuild a base that live per-domain overlays back onto (it mutates a read-only backing file); the concurrent-bed R10 uses idempotent-skip, never --force."`
}

func (c *VmBuildCmd) Run() error {
	switch c.Type {
	case "qcow2", "raw":
	case "iso":
		return fmt.Errorf("iso format is not supported — use qcow2 or raw")
	default:
		return fmt.Errorf("unsupported disk type %q (valid: qcow2, raw)", c.Type)
	}

	if cmdExec == nil {
		return fmt.Errorf("vm build: no host reverse channel (command not compiled-in?)")
	}
	reqJSON, err := json.Marshal(spec.VmBuildRequest{
		Box: c.Box, Size: c.Size, RootSize: c.RootSize, Tag: c.Tag,
		Type: c.Type, Transport: c.Transport, Console: c.Console, Force: c.Force,
	})
	if err != nil {
		return err
	}
	replyJSON, err := cmdExec.HostBuild(cmdCtx, "vm-build", reqJSON)
	if err != nil {
		return err
	}
	var reply spec.VmBuildReply
	if err := json.Unmarshal(replyJSON, &reply); err != nil {
		return fmt.Errorf("decoding vm-build resolve reply: %w", err)
	}

	var vmSpec VmSpec
	if err := json.Unmarshal(reply.VmJSON, &vmSpec); err != nil {
		return fmt.Errorf("decoding resolved vm spec: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Building VM %q (source.kind=%s)\n", c.Box, reply.SourceKind)

	// Per-ENTITY build flock: serialize concurrent `vm build <entity>` so N beds sharing this
	// entity's disk base never race on output/qcow2/<entity>/ (and a second build never
	// rewrites a base a live per-domain overlay backs onto). Blocking — the first builds, the
	// rest wait then idempotent-skip. Released on return (BEFORE `vm create`), so per-domain
	// overlay-creates stay unserialized.
	unlock, lockErr := kit.AcquireFileLock(filepath.Join(reply.OutputDir, ".build.lock"), true)
	if lockErr != nil {
		return fmt.Errorf("acquiring vm build lock for %s: %w", c.Box, lockErr)
	}
	defer func() { _ = unlock() }()

	switch reply.SourceKind {
	case "cloud_image":
		res, err := BuildCloudImage(&vmSpec, reply.OutputDir, reply.VmStateDir, reply.ExistingState, reply.Force)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Wrote %s (base sha256=%s)\n", res.DiskPath, res.BaseImageSHA256)
		fmt.Fprintf(os.Stderr, "Wrote %s\n", res.SeedIsoPath)
		fmt.Fprintf(os.Stderr, "Instance-id: %s\n", res.InstanceID)
		return nil

	case "bootc":
		res, err := BuildBootcVM(&vmSpec, reply.OutputDir, reply.VmStateDir, reply.ExistingState, reply.BootcImageRef)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Wrote %s\n", res.DiskPath)
		if res.SeedIsoPath != "" {
			fmt.Fprintf(os.Stderr, "Wrote %s\n", res.SeedIsoPath)
		}
		return nil

	case "bootstrap":
		var distro DistroDef
		if err := json.Unmarshal(reply.DistroJSON, &distro); err != nil {
			return fmt.Errorf("decoding resolved distro: %w", err)
		}
		var builder BuilderDef
		if err := json.Unmarshal(reply.BuilderJSON, &builder); err != nil {
			return fmt.Errorf("decoding resolved builder: %w", err)
		}
		res, err := BuildBootstrapVM(&vmSpec, reply.OutputDir, reply.VmStateDir, reply.ExistingState, &distro, &builder, reply.BuilderImageRef)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Wrote %s (rootfs sha256=%s)\n", res.DiskPath, res.BaseImageSHA256)
		if res.SeedIsoPath != "" {
			fmt.Fprintf(os.Stderr, "Wrote %s\n", res.SeedIsoPath)
		}
		return nil

	default:
		return fmt.Errorf("vm %q: unsupported source.kind %q (want one of cloud_image, bootc, bootstrap)", c.Box, reply.SourceKind)
	}
}
