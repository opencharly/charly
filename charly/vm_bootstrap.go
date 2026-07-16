package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/opencharly/sdk/spec"
	"os"
	"path/filepath"
	"strings"
)

// BootstrapVMResult mirrors CloudImageBuildResult so callers in
// vm_build.go can branch uniformly across VM source kinds.
type BootstrapVMResult struct {
	DiskPath        string
	SeedIsoPath     string
	InstanceID      string
	BaseImageSHA256 string
	CloudInitDigest string
}

// bootstrapRootfsExtractTar extracts the bootstrap rootfs.tar.gz into the VM
// disk's mounted root. --xattrs-include='*' is REQUIRED: GNU tar's --xattrs
// default-EXCLUDES the security.* namespace on extract, which silently drops
// file capabilities (security.capability). Without it, /usr/bin/newuidmap +
// newgidmap lose cap_setuid/cap_setgid and rootless podman in the guest cannot
// map user namespaces (and ping loses cap_net_raw). The privileged builder runs
// this as root, so it can restore the security.* xattrs. Verified empirically:
// a plain `tar --xattrs` round-trip drops cap_setuid; the include preserves it.
// (The create-side tars in the embedded build vocabulary (charly/charly.yml) carry the same flag — TestBootstrapTarPreservesFileCaps.)
const bootstrapRootfsExtractTar = `tar -C /mnt --xattrs --xattrs-include='*' --acls -xzf /in/rootfs.tar.gz`

// BuildBootstrapVM creates a fresh VM disk by:
//  1. Resolving the bootstrap builder + distro from the embedded build vocabulary
//  2. Running the bootstrap builder via RunPrivileged (pacstrap /
//     debootstrap / alpine-bootstrap → rootfs.tar.gz)
//  3. Partitioning a sparse disk + extracting the rootfs + running
//     the distro's bootloader install template inside chroot
//  4. Converting raw → qcow2
//  5. Rendering the cloud-init seed ISO when spec.CloudInit is set
//
// Mirrors BuildCloudImage in shape so callers in vm_build.go can swap
// implementations behind the source.kind discriminator.
func BuildBootstrapVM(
	vmSpec *VmSpec,
	outputDir, vmStateDir string,
	existingState *spec.VmDeployState,
	distroCfg *DistroConfig,
	builderCfg *BuilderConfig,
) (BootstrapVMResult, error) {
	if vmSpec.Source.Kind != "bootstrap" {
		return BootstrapVMResult{}, fmt.Errorf("BuildBootstrapVM called with source.kind=%q (expected bootstrap)", vmSpec.Source.Kind)
	}
	if builderCfg == nil || builderCfg.Builder == nil {
		return BootstrapVMResult{}, fmt.Errorf("the builder: section of the embedded vocabulary (charly/charly.yml) is empty; cannot resolve %q", vmSpec.Source.Builder)
	}
	builder, ok := builderCfg.Builder[vmSpec.Source.Builder]
	if !ok {
		return BootstrapVMResult{}, fmt.Errorf("builder %q not declared in the embedded build vocabulary (charly/charly.yml)", vmSpec.Source.Builder)
	}
	if !builder.IsBootstrap() {
		return BootstrapVMResult{}, fmt.Errorf("builder %q is not kind: bootstrap", vmSpec.Source.Builder)
	}
	if distroCfg == nil {
		return BootstrapVMResult{}, fmt.Errorf("the distro: section of the embedded vocabulary (charly/charly.yml) is empty; cannot resolve %q", vmSpec.Source.Distro)
	}
	distro, ok := distroCfg.Distro[vmSpec.Source.Distro]
	if !ok {
		return BootstrapVMResult{}, fmt.Errorf("distro %q not declared in the embedded build vocabulary (charly/charly.yml)", vmSpec.Source.Distro)
	}
	distro = distroCfg.ResolveInherits(distro, 10)
	if distro.Bootloader == nil {
		return BootstrapVMResult{}, fmt.Errorf("distro %q has no bootloader: block in the embedded build vocabulary (charly/charly.yml) (required for VM bootstrap)", vmSpec.Source.Distro)
	}
	if vmSpec.Source.BuilderImage == "" {
		return BootstrapVMResult{}, fmt.Errorf("source.builder_image is required for bootstrap VMs")
	}
	if vmSpec.DiskSize == "" {
		return BootstrapVMResult{}, fmt.Errorf("disk_size is required for bootstrap VMs")
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return BootstrapVMResult{}, fmt.Errorf("creating output dir: %w", err)
	}
	buildDir := filepath.Join(vmStateDir, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return BootstrapVMResult{}, fmt.Errorf("creating build dir: %w", err)
	}

	rootfsTar, builderRef, err := buildBootstrapRootfs(vmSpec, builder, distro, buildDir)
	if err != nil {
		return BootstrapVMResult{}, err
	}

	diskPath, err := buildBootstrapDisk(vmSpec, distro, builderRef, rootfsTar, outputDir)
	if err != nil {
		return BootstrapVMResult{}, err
	}

	return buildBootstrapSeedISO(vmSpec, diskPath, rootfsTar, outputDir, vmStateDir, existingState)
}

// buildBootstrapRootfs runs Step 1: bootstrap a rootfs.tar.gz via the
// privileged builder. Returns the rootfs tarball path and the resolved
// (auto-built) builder image ref reused by the disk-build step.
func buildBootstrapRootfs(vmSpec *VmSpec, builder *BuilderDef, distro *spec.ResolvedDistro, buildDir string) (string, string, error) {
	rootfsCtx := struct {
		Distro            *spec.ResolvedDistro
		Packages          []string
		ExtraPacmanConf   string
		RuntimePacmanConf string
		ExtraAptSources   string
		Arch              string
		Variant           string
	}{
		Distro:   distro,
		Packages: append(append([]string{}, baseBootstrapPackages(distro)...), vmSpec.Source.Package...),
		Arch:     vmSpec.Source.BootstrapArch,
		Variant:  vmSpec.Source.BootstrapVariant,
	}
	// Inject CachyOS / other distro-specific repo blocks (+ Architecture for
	// microarch repos, + per-repo SigLevel) into pacman.conf inside the
	// bootstrap container before pacstrap runs. Shared renderer with the image
	// bootstrap path — previously this path open-coded the loop and dropped
	// SigLevel, breaking GPGME verification for SigLevel=Never repos.
	rootfsCtx.ExtraPacmanConf = renderPacstrapExtraConf(distro.Pacstrap)
	// The booted-guest runtime /etc/pacman.conf is rendered from the SAME
	// extra_repo source (single source of truth — see renderRuntimePacmanConf).
	runtimeConf, rerr := renderRuntimePacmanConf(distro.Pacstrap)
	if rerr != nil {
		return "", "", rerr
	}
	rootfsCtx.RuntimePacmanConf = runtimeConf
	// Inject optional extra apt sources (security/backports) into
	// /etc/apt/sources.list.d/ inside the chroot before stage-2 install.
	if distro.Debootstrap != nil && len(distro.Debootstrap.ExtraRepos) > 0 {
		var rb strings.Builder
		for _, r := range distro.Debootstrap.ExtraRepos {
			suite := r.Suite
			if suite == "" {
				suite = distro.Debootstrap.Suite
			}
			components := r.Components
			if components == "" {
				components = distro.Debootstrap.Components
				if components == "" {
					components = "main"
				}
			}
			fmt.Fprintf(&rb, "echo 'deb %s %s %s' > /target/etc/apt/sources.list.d/%s.list\n", r.URL, suite, components, r.Name)
		}
		rootfsCtx.ExtraAptSources = rb.String()
	}
	bootstrapScript, err := renderBootstrapScript(builder, rootfsCtx)
	if err != nil {
		return "", "", fmt.Errorf("rendering bootstrap script: %w", err)
	}
	rootfsTar := filepath.Join(buildDir, "rootfs.tar.gz")
	output := builder.OutputArtifact
	if output == "" {
		output = "/out/rootfs.tar.gz"
	}
	// Resolve the builder image ref. Internal kind:box names need
	// resolution to a newest local CalVer tag — sudo podman won't auto-
	// pull from registries that don't have the build.
	// Resolve + auto-build the bootstrap builder image on demand (fully
	// automatic — no manual `charly box build <builder>` prerequisite).
	builderRef := vmSpec.Source.BuilderImage
	rt, _ := ResolveRuntime()
	engine := "podman"
	if rt != nil {
		engine = EngineBinary(rt.RunEngine)
	}
	builderRef, err = ensureBuilderImageBuilt(engine, builderRef)
	if err != nil {
		return "", "", err
	}
	if err := RunPrivileged(PrivilegedRun{
		Image:      builderRef,
		Script:     bootstrapScript,
		OutputPath: output,
		OutputDest: rootfsTar,
	}); err != nil {
		return "", "", fmt.Errorf("running bootstrap builder %q: %w", vmSpec.Source.Builder, err)
	}
	return rootfsTar, builderRef, nil
}

// buildBootstrapDisk runs Step 2: partition + format the disk, extract the
// rootfs, and run the distro bootloader install inside the privileged builder.
// Returns the qcow2 disk path.
func buildBootstrapDisk(vmSpec *VmSpec, distro *spec.ResolvedDistro, builderRef, rootfsTar, outputDir string) (string, error) {
	rootfsKind := vmSpec.Source.Rootfs
	if rootfsKind == "" {
		rootfsKind = "ext4"
	}
	prelude, finalize, err := EmitDiskBuildScript(DiskLayout{
		SizeBytesOrSuffix: vmSpec.DiskSize,
		Rootfs:            rootfsKind,
		Mnt:               "/mnt",
	})
	if err != nil {
		return "", fmt.Errorf("emitting disk build script: %w", err)
	}
	sshUser := ""
	if vmSpec.SSH != nil {
		sshUser = vmSpec.SSH.User
	}
	bootloaderScript, err := renderBootloaderScript(distro, "/mnt", vmSpec.Source.KernelArgs, rootfsKind, sshUser)
	if err != nil {
		return "", fmt.Errorf("rendering bootloader script: %w", err)
	}

	installBody := fmt.Sprintf("%s\n%s\n", bootstrapRootfsExtractTar, bootloaderScript)
	fullScript := prelude + installBody + finalize

	diskPath := filepath.Join(outputDir, "disk.qcow2")
	if err := RunPrivileged(PrivilegedRun{
		Image:      builderRef,
		Script:     fullScript,
		Mounts:     []string{fmt.Sprintf("%s:/in/rootfs.tar.gz:ro", rootfsTar)},
		OutputPath: "/out/disk.qcow2",
		OutputDest: diskPath,
	}); err != nil {
		return "", fmt.Errorf("building bootstrap VM disk: %w", err)
	}
	return diskPath, nil
}

// buildBootstrapSeedISO runs Step 3: render the cloud-init seed ISO when
// spec.CloudInit is set and assemble the BootstrapVMResult (including the
// rootfs tarball hash for traceability).
func buildBootstrapSeedISO(vmSpec *VmSpec, diskPath, rootfsTar, outputDir, vmStateDir string, existingState *spec.VmDeployState) (BootstrapVMResult, error) {
	res := BootstrapVMResult{
		DiskPath: diskPath,
	}
	if vmSpec.CloudInit != nil {
		seedPath := filepath.Join(outputDir, "seed.iso")
		if err := RegenerateSeedISO(vmSpec, seedPath, vmStateDir, existingState); err != nil {
			return BootstrapVMResult{}, fmt.Errorf("rendering cloud-init seed ISO: %w", err)
		}
		res.SeedIsoPath = seedPath
		if existingState != nil && existingState.InstanceID != "" {
			res.InstanceID = existingState.InstanceID
		}
	}

	// Hash the rootfs tarball for traceability (mirrors cloud_image's BaseImageSHA256).
	if rootfsBytes, err := os.ReadFile(rootfsTar); err == nil {
		sum := sha256.Sum256(rootfsBytes)
		res.BaseImageSHA256 = hex.EncodeToString(sum[:])
	}
	return res, nil
}

// baseBootstrapPackages returns the per-distro base package list
// declared on the appropriate sub-block (Pacstrap / Debootstrap /
// AlpineBootstrap). Used as the kernel set passed to the bootstrap
// template's `.Packages` field alongside any spec-supplied additions.
//
// Per-distro semantics:
//   - Pacstrap: positional args to `pacstrap -K -G /target <pkgs>`;
//     the entire .Packages list installs in one invocation.
//   - Debootstrap: stage-2 chroot apt-get install list. Stage-1
//     debootstrap's --variant + --include come from
//     d.Debootstrap.{Variant,IncludePackages} read directly from the
//     template; only stage-2 reads from .Packages.
func baseBootstrapPackages(d *spec.ResolvedDistro) []string {
	if d == nil {
		return nil
	}
	if d.Pacstrap != nil {
		return d.Pacstrap.BasePackages
	}
	if d.Debootstrap != nil {
		return d.Debootstrap.BasePackages
	}
	return nil
}

// renderBootloaderScript renders distro.bootloader.install_template +
// initramfs_template + fstab_template against {{.Mnt}} (the chroot
// target) and {{.KernelArgs}} (from VmSource.KernelArgs). Each template
// is optional; missing templates emit nothing.
//
// When KernelArgs is non-empty, the script also writes
// GRUB_CMDLINE_LINUX_DEFAULT to /etc/default/grub before grub-mkconfig
// so the kernel boots with `console=ttyS0` etc. and serial output
// reaches the host's QEMU console (otherwise VMs boot to a black-hole
// console with no diagnostic visibility).
func renderBootloaderScript(d *spec.ResolvedDistro, mnt, kernelArgs, rootfs, sshUser string) (string, error) {
	if d == nil || d.Bootloader == nil {
		return "", fmt.Errorf("no bootloader: declared on distro")
	}
	ctx := struct {
		Mnt        string
		KernelArgs string
		Rootfs     string
		SSHUser    string
	}{Mnt: mnt, KernelArgs: kernelArgs, Rootfs: rootfs, SSHUser: sshUser}
	var b strings.Builder
	if kernelArgs != "" {
		fmt.Fprintf(&b, "echo 'GRUB_CMDLINE_LINUX_DEFAULT=\"%s\"' > %s/etc/default/grub.d/00-charly-kernel-args.cfg || \\\n", kernelArgs, mnt)
		fmt.Fprintf(&b, "  { mkdir -p %s/etc/default/grub.d && echo 'GRUB_CMDLINE_LINUX_DEFAULT=\"%s\"' > %s/etc/default/grub.d/00-charly-kernel-args.cfg; }\n", mnt, kernelArgs, mnt)
		fmt.Fprintf(&b, "sed -i 's|^GRUB_CMDLINE_LINUX_DEFAULT=.*|GRUB_CMDLINE_LINUX_DEFAULT=\"%s\"|' %s/etc/default/grub 2>/dev/null || true\n", kernelArgs, mnt)
	}
	for _, tmplStr := range []string{d.Bootloader.FstabTemplate, d.Bootloader.InstallTemplate, d.Bootloader.InitramfsTemplate} {
		if tmplStr == "" {
			continue
		}
		rendered, err := renderTmpl("bootloader", tmplStr, ctx)
		if err != nil {
			return "", err
		}
		b.WriteString(rendered)
		if !strings.HasSuffix(rendered, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}
