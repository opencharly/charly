package vm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/opencharly/sdk/vmshared"
)

// vm_bootstrap_engine.go — the pacstrap/debootstrap VM-bootstrap disk-build engine (P8b-rest:
// ported from charly/vm_bootstrap.go). The host resolves + inheritance-flattens the DistroDef,
// looks up the BuilderDef, and pre-resolves+builds the builder image ref (ensureBuilderImageBuilt
// needs charly.yml + local podman storage, core-only Mechanisms) into spec.VmBuildReply — this
// engine takes those already-resolved values and drives the privileged-container exec itself.

// BootstrapVMResult mirrors CloudImageBuildResult so callers can branch uniformly across VM
// source kinds.
type BootstrapVMResult struct {
	DiskPath        string
	SeedIsoPath     string
	InstanceID      string
	BaseImageSHA256 string
	CloudInitDigest string
}

// bootstrapRootfsExtractTar extracts the bootstrap rootfs.tar.gz into the VM disk's mounted
// root. --xattrs-include='*' is REQUIRED: GNU tar's --xattrs default-EXCLUDES the security.*
// namespace on extract, which silently drops file capabilities (security.capability). Without
// it, /usr/bin/newuidmap + newgidmap lose cap_setuid/cap_setgid and rootless podman in the
// guest cannot map user namespaces (and ping loses cap_net_raw). The privileged builder runs
// this as root, so it can restore the security.* xattrs. Verified empirically: a plain `tar
// --xattrs` round-trip drops cap_setuid; the include preserves it. (The create-side tars in the
// embedded build vocabulary (charly/charly.yml) carry the same flag.)
const bootstrapRootfsExtractTar = `tar -C /mnt --xattrs --xattrs-include='*' --acls -xzf /in/rootfs.tar.gz`

// pacstrapMicroarchRe matches pacman microarchitecture-level tokens (e.g. x86_64_v3) embedded
// in a repo Server URL. CachyOS's cachyos-v3 repos serve such packages; pacman rejects them
// unless the matching token is in Architecture. (P8b-rest: ported from charly/build.go — the
// SAME single source of truth for both the image-bootstrap path, which stays core, and this
// VM-bootstrap path; duplicated rather than shared via sdk/kit because build.go's own copy is
// out of scope for this cutover, mirroring the RunPrivileged decision above.)
var pacstrapMicroarchRe = regexp.MustCompile(`x86_64_v[0-9]+`)

// renderPacstrapExtraConf builds the pacman.conf fragment appended to /etc/pacman.conf inside
// the bootstrap container before `pacstrap` runs. Emits, in order: (1) an [options]
// Architecture directive whenever any repo Server declares a microarch variant (e.g.
// x86_64_v3) — pacman's default Architecture otherwise rejects those packages; (2) each
// [repo] block with its Server and (when set) SigLevel.
func renderPacstrapExtraConf(p *vmshared.PacstrapDef) string {
	if p == nil || len(p.ExtraRepos) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var microarch []string
	for _, r := range p.ExtraRepos {
		for _, m := range pacstrapMicroarchRe.FindAllString(r.Server, -1) {
			if !seen[m] {
				seen[m] = true
				microarch = append(microarch, m)
			}
		}
	}
	sort.Strings(microarch)

	var b strings.Builder
	if len(microarch) > 0 {
		fmt.Fprintf(&b, "[options]\nArchitecture = x86_64 %s\n", strings.Join(microarch, " "))
	}
	for _, r := range p.ExtraRepos {
		fmt.Fprintf(&b, "[%s]\nServer = %s\n", r.Name, r.Server)
		if r.SigLevel != "" {
			fmt.Fprintf(&b, "SigLevel = %s\n", r.SigLevel)
		}
	}
	return b.String()
}

// renderRuntimePacmanConf renders the booted-guest /etc/pacman.conf for a pacstrap distro.
// `runtime_pacman_conf` is a Go text/template evaluated against the PacstrapDef, so the repo
// list is derived from the SINGLE `extra_repo` source rather than a second hand-maintained
// verbatim copy. A legacy verbatim config (no template actions) renders to itself. Returns ""
// when unset; surfaces malformed-template errors.
func renderRuntimePacmanConf(p *vmshared.PacstrapDef) (string, error) {
	if p == nil || strings.TrimSpace(p.RuntimePacmanConf) == "" {
		return "", nil
	}
	tmpl, err := template.New("runtime_pacman_conf").Parse(p.RuntimePacmanConf)
	if err != nil {
		return "", fmt.Errorf("parsing runtime_pacman_conf template: %w", err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, p); err != nil {
		return "", fmt.Errorf("rendering runtime_pacman_conf: %w", err)
	}
	return b.String(), nil
}

// BuildBootstrapVM creates a fresh VM disk by:
//  1. Running the bootstrap builder via RunPrivileged (pacstrap / debootstrap / alpine-bootstrap
//     → rootfs.tar.gz), against the HOST-RESOLVED builder image ref.
//  2. Partitioning a sparse disk + extracting the rootfs + running the distro's bootloader
//     install template inside chroot.
//  3. Converting raw → qcow2.
//  4. Rendering the cloud-init seed ISO when spec.CloudInit is set.
//
// Mirrors BuildCloudImage in shape so callers can swap implementations behind the
// source.kind discriminator. distro is already inheritance-flattened (host-side
// DistroConfig.ResolveInherits) and builderRef is already resolved + built
// (host-side ensureBuilderImageBuilt) — this engine only drives the exec.
func BuildBootstrapVM(
	spec *VmSpec,
	outputDir, vmStateDir string,
	existingState *VmDeployState,
	distro *DistroDef,
	builder *BuilderDef,
	builderRef string,
) (BootstrapVMResult, error) {
	if spec.Source.Kind != "bootstrap" {
		return BootstrapVMResult{}, fmt.Errorf("BuildBootstrapVM called with source.kind=%q (expected bootstrap)", spec.Source.Kind)
	}
	if distro == nil {
		return BootstrapVMResult{}, fmt.Errorf("BuildBootstrapVM: distro is nil")
	}
	if distro.Bootloader == nil {
		return BootstrapVMResult{}, fmt.Errorf("distro %q has no bootloader: block (required for VM bootstrap)", spec.Source.Distro)
	}
	if builder == nil {
		return BootstrapVMResult{}, fmt.Errorf("BuildBootstrapVM: builder is nil")
	}
	if spec.DiskSize == "" {
		return BootstrapVMResult{}, fmt.Errorf("disk_size is required for bootstrap VMs")
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return BootstrapVMResult{}, fmt.Errorf("creating output dir: %w", err)
	}
	buildDir := filepath.Join(vmStateDir, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return BootstrapVMResult{}, fmt.Errorf("creating build dir: %w", err)
	}

	rootfsTar, err := buildBootstrapRootfs(spec, builder, distro, buildDir, builderRef)
	if err != nil {
		return BootstrapVMResult{}, err
	}

	diskPath, err := buildBootstrapDisk(spec, distro, builderRef, rootfsTar, outputDir)
	if err != nil {
		return BootstrapVMResult{}, err
	}

	return buildBootstrapSeedISO(spec, diskPath, rootfsTar, outputDir, vmStateDir, existingState)
}

// buildBootstrapRootfs runs Step 1: bootstrap a rootfs.tar.gz via the privileged builder
// (builderRef, already host-resolved + built).
func buildBootstrapRootfs(spec *VmSpec, builder *BuilderDef, distro *DistroDef, buildDir, builderRef string) (string, error) {
	rootfsCtx := struct {
		Distro            *DistroDef
		Packages          []string
		ExtraPacmanConf   string
		RuntimePacmanConf string
		ExtraAptSources   string
		Arch              string
		Variant           string
	}{
		Distro:   distro,
		Packages: append(append([]string{}, baseBootstrapPackages(distro)...), spec.Source.Package...),
		Arch:     spec.Source.BootstrapArch,
		Variant:  spec.Source.BootstrapVariant,
	}
	// Inject CachyOS / other distro-specific repo blocks (+ Architecture for microarch repos, +
	// per-repo SigLevel) into pacman.conf inside the bootstrap container before pacstrap runs.
	// Shared renderer with the image bootstrap path — previously this path open-coded the loop
	// and dropped SigLevel, breaking GPGME verification for SigLevel=Never repos.
	rootfsCtx.ExtraPacmanConf = renderPacstrapExtraConf(distro.Pacstrap)
	// The booted-guest runtime /etc/pacman.conf is rendered from the SAME extra_repo source
	// (single source of truth — see renderRuntimePacmanConf).
	runtimeConf, rerr := renderRuntimePacmanConf(distro.Pacstrap)
	if rerr != nil {
		return "", rerr
	}
	rootfsCtx.RuntimePacmanConf = runtimeConf
	// Inject optional extra apt sources (security/backports) into /etc/apt/sources.list.d/
	// inside the chroot before stage-2 install.
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
		return "", fmt.Errorf("rendering bootstrap script: %w", err)
	}
	rootfsTar := filepath.Join(buildDir, "rootfs.tar.gz")
	output := builder.OutputArtifact
	if output == "" {
		output = "/out/rootfs.tar.gz"
	}
	if err := RunPrivileged(PrivilegedRun{
		Image:      builderRef,
		Script:     bootstrapScript,
		OutputPath: output,
		OutputDest: rootfsTar,
	}); err != nil {
		return "", fmt.Errorf("running bootstrap builder %q: %w", spec.Source.Builder, err)
	}
	return rootfsTar, nil
}

// buildBootstrapDisk runs Step 2: partition + format the disk, extract the rootfs, and run the
// distro bootloader install inside the privileged builder. Returns the qcow2 disk path.
func buildBootstrapDisk(spec *VmSpec, distro *DistroDef, builderRef, rootfsTar, outputDir string) (string, error) {
	rootfsKind := spec.Source.Rootfs
	if rootfsKind == "" {
		rootfsKind = "ext4"
	}
	prelude, finalize, err := EmitDiskBuildScript(DiskLayout{
		SizeBytesOrSuffix: spec.DiskSize,
		Rootfs:            rootfsKind,
		Mnt:               "/mnt",
	})
	if err != nil {
		return "", fmt.Errorf("emitting disk build script: %w", err)
	}
	sshUser := ""
	if spec.SSH != nil {
		sshUser = spec.SSH.User
	}
	bootloaderScript, err := renderBootloaderScript(distro, "/mnt", spec.Source.KernelArgs, rootfsKind, sshUser)
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

// buildBootstrapSeedISO runs Step 3: render the cloud-init seed ISO when spec.CloudInit is set
// and assemble the BootstrapVMResult (including the rootfs tarball hash for traceability).
func buildBootstrapSeedISO(spec *VmSpec, diskPath, rootfsTar, outputDir, vmStateDir string, existingState *VmDeployState) (BootstrapVMResult, error) {
	res := BootstrapVMResult{
		DiskPath: diskPath,
	}
	if spec.CloudInit != nil {
		seedPath := filepath.Join(outputDir, "seed.iso")
		if err := RegenerateSeedISO(spec, seedPath, vmStateDir, existingState); err != nil {
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

// baseBootstrapPackages returns the per-distro base package list declared on the appropriate
// sub-block (Pacstrap / Debootstrap / AlpineBootstrap). Used as the kernel set passed to the
// bootstrap template's `.Packages` field alongside any spec-supplied additions.
//
// Per-distro semantics:
//   - Pacstrap: positional args to `pacstrap -K -G /target <pkgs>`; the entire .Packages list
//     installs in one invocation.
//   - Debootstrap: stage-2 chroot apt-get install list. Stage-1 debootstrap's --variant +
//     --include come from d.Debootstrap.{Variant,IncludePackages} read directly from the
//     template; only stage-2 reads from .Packages.
func baseBootstrapPackages(d *DistroDef) []string {
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

// renderBootloaderScript renders distro.bootloader.install_template + initramfs_template +
// fstab_template against {{.Mnt}} (the chroot target) and {{.KernelArgs}} (from
// VmSource.KernelArgs). Each template is optional; missing templates emit nothing.
//
// When KernelArgs is non-empty, the script also writes GRUB_CMDLINE_LINUX_DEFAULT to
// /etc/default/grub before grub-mkconfig so the kernel boots with `console=ttyS0` etc. and
// serial output reaches the host's QEMU console (otherwise VMs boot to a black-hole console
// with no diagnostic visibility).
func renderBootloaderScript(d *DistroDef, mnt, kernelArgs, rootfs, sshUser string) (string, error) {
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
