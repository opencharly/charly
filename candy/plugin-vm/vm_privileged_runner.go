package vm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// vm_privileged_runner.go — the privileged-container exec primitive for the VM-disk build
// engine (P8b-rest: the disk-build ENGINE moved here from charly core, mirroring how
// candy/plugin-build already runs `podman build` itself instead of asking the host). Every
// dependency this needs is already sdk-importable (kit.ResolveRuntime/EngineBinary/
// TransferToRootful, vmshared.RegisterTempCleanup/UnregisterTempCleanup,
// buildkit.TemplateFuncs/BuilderPhaseTemplate), so this is a straight port of
// charly/privileged_runner.go's RunPrivileged — kept as its own copy (not a shared sdk/kit
// function) because charly core's OWN box-build privileged bootstrap
// (BuildCmd.runPrivilegedBootstrap) still calls its copy directly; unifying the two into one
// kit.RunPrivileged is a Tier-2 finding, not part of this move.

// PrivilegedRun describes a single privileged-container invocation: a script body executed
// inside a builder image with --privileged -v /dev:/dev. Used for pacstrap/debootstrap
// rootfs bootstrap and for VM disk-build chroots.
type PrivilegedRun struct {
	// Image is the builder image ref (e.g. arch-pacstrap-builder:CALVER).
	Image string
	// Script is the bash body executed inside the container. Run via
	// `bash -s <<'EOF' ... EOF` so quoting in the script body is preserved.
	Script string
	// Env lists KEY=VALUE pairs forwarded to the container.
	Env []string
	// Mounts lists "src:dst[:opts]" host-path bind mounts. /dev is always
	// added so loop devices created by losetup are visible.
	Mounts []string
	// OutputPath is an absolute path inside the container whose contents
	// must be copied out after the container exits successfully. May be
	// empty when the script writes directly to a host-bind-mounted path.
	OutputPath string
	// OutputDest is the absolute host path where OutputPath is written.
	// Required when OutputPath is set; ignored otherwise.
	OutputDest string
}

// RunPrivileged executes the container described by p. Returns an error when the container
// exits non-zero or when the output file capture fails. Stdout/stderr stream live to the
// caller. Always passes --privileged + --rm + -v /dev:/dev. Callers do NOT need to repeat
// those mounts in p.Mounts.
func RunPrivileged(p PrivilegedRun) error {
	if p.Image == "" {
		return fmt.Errorf("RunPrivileged: image is required")
	}
	if p.Script == "" {
		return fmt.Errorf("RunPrivileged: script is empty")
	}
	if p.OutputPath != "" && p.OutputDest == "" {
		return fmt.Errorf("RunPrivileged: OutputPath %q has no OutputDest", p.OutputPath)
	}

	stagingDir := ""
	hostStaging := ""
	// Always --user 0 because pacstrap / debootstrap / bootc install require root inside the
	// container (they call mount, mkfs, chroot, pacman-key, etc.). --privileged alone doesn't
	// override the image's USER directive.
	// -i required so podman attaches stdin to bash -s; without it the piped script is silently
	// dropped and the container exits immediately (no error, no stdout).
	// --net host because pacstrap / debootstrap / bootc install fetch packages over the
	// network from the host's mirrors. Rootful podman's default network mode (slirp/pasta)
	// doesn't always provide working outbound connectivity in privileged contexts.
	args := []string{"run", "--privileged", "--rm", "-i", "--user", "0", "--net", "host", "-v", "/dev:/dev"}
	for _, e := range p.Env {
		args = append(args, "-e", e)
	}
	for _, m := range p.Mounts {
		args = append(args, "-v", m)
	}
	if p.OutputPath != "" {
		// Bind-mount a host directory at the parent of OutputPath so the script can write
		// directly to OutputDest's location without a post-run copy step.
		var err error
		hostStaging, err = os.MkdirTemp("", "charly-priv-")
		if err != nil {
			return fmt.Errorf("creating staging dir: %w", err)
		}
		vmshared.RegisterTempCleanup(hostStaging)
		defer vmshared.UnregisterTempCleanup(hostStaging)
		stagingDir = filepath.Dir(p.OutputPath)
		args = append(args, "-v", fmt.Sprintf("%s:%s", hostStaging, stagingDir))
	}
	args = append(args, p.Image, "bash", "-s")

	// Honor the runtime's engine.rootful setting. Rootless podman blocks pacstrap/bootc-install's
	// `mount /target/dev` even with --privileged because the user namespace has no CAP_SYS_ADMIN
	// equivalent for arbitrary bind mounts. `sudo podman` runs in the host namespace and bypasses
	// that constraint.
	bin := os.Getenv("CHARLY_PRIV_RUNNER")
	useSudo := false
	if bin == "" {
		bin = "podman"
		if rt, err := kit.ResolveRuntime(); err == nil && rt.Rootful == "sudo" {
			useSudo = true
		}
	}
	// When running via sudo, the rootful podman storage is independent of the user's rootless
	// storage. Locally-built images (the typical case for builder:pacstrap / builder:debootstrap)
	// won't be visible to `sudo podman run`, which would then fall back to a registry pull that
	// 403s for unpublished builder images. Stage the image into rootful storage first via
	// podman save | sudo podman load. Idempotent — TransferToRootful skips when the image already
	// exists in rootful storage.
	if useSudo {
		if err := kit.TransferToRootful(p.Image); err != nil {
			if hostStaging != "" {
				_ = os.RemoveAll(hostStaging)
			}
			return fmt.Errorf("staging %s into rootful storage: %w", p.Image, err)
		}
	}
	var cmd *exec.Cmd
	if useSudo {
		cmd = exec.Command("sudo", append([]string{bin}, args...)...)
	} else {
		cmd = exec.Command(bin, args...)
	}
	cmd.Stdin = strings.NewReader(p.Script)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if os.Getenv("CHARLY_PRIV_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "+ %s %s\n", bin, strings.Join(args, " "))
		fmt.Fprintln(os.Stderr, "--- script begin ---")
		fmt.Fprintln(os.Stderr, p.Script)
		fmt.Fprintln(os.Stderr, "--- script end ---")
	}
	if err := cmd.Run(); err != nil {
		if hostStaging != "" {
			_ = os.RemoveAll(hostStaging)
		}
		return fmt.Errorf("privileged run %s failed: %w", p.Image, err)
	}

	if p.OutputPath != "" {
		// Copy the output from the staging dir to OutputDest.
		srcPath := filepath.Join(hostStaging, filepath.Base(p.OutputPath))
		if err := os.MkdirAll(filepath.Dir(p.OutputDest), 0o755); err != nil {
			_ = os.RemoveAll(hostStaging)
			return fmt.Errorf("creating output destination dir: %w", err)
		}
		if err := copyFileBytes(srcPath, p.OutputDest); err != nil {
			_ = os.RemoveAll(hostStaging)
			return fmt.Errorf("capturing privileged output %s -> %s: %w", srcPath, p.OutputDest, err)
		}
		_ = os.RemoveAll(hostStaging)
	}
	return nil
}

// copyFileBytes is a small helper that mirrors os.CopyFile (Go 1.21+), kept inline to avoid an
// extra dependency on a newer stdlib version.
func copyFileBytes(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}

// renderBootstrapScript renders the install template for a privileged bootstrap builder
// against a render context (the fields are documented at the VM-bootstrap call site,
// vm_bootstrap_engine.go's buildBootstrapRootfs).
func renderBootstrapScript(builder *BuilderDef, ctx any) (string, error) {
	tmpl := buildkit.BuilderPhaseTemplate(builder, spec.PhaseInstall, spec.VenueContainerBuilder)
	if tmpl == "" {
		return "", fmt.Errorf("builder has no phase.install.container template")
	}
	var buf strings.Builder
	t, err := template.New("bootstrap-script").Funcs(buildkit.TemplateFuncs).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parsing bootstrap script template: %w", err)
	}
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("rendering bootstrap script: %w", err)
	}
	return buf.String(), nil
}
