package vm

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/container"
)

// VmCpBoxCmd loads a locally-built container image into a running VM guest's
// podman storage via `podman save | ssh … podman load`. This is the host→guest
// delivery path for images that are NOT on a registry — the case the
// nested-pod-in-VM capability hits: plugin-deploy-vm's PostApply host-builds a nested
// pod's image (e.g. `cachyos.selkies-kde-nvidia`), cp-boxes it in as
// `localhost/charly-<child>:latest`, then the guest's own `charly fleet from-box`
// brings it up as a persistent quadlet — all offline, no registry.
//
// Idempotent: skips the transfer when the guest already has the image, verified
// intact. The container-venue twin of this verb is `charly box load`.
type VmCpBoxCmd struct {
	VM       string `arg:"" help:"kind:vm entity name (uses its managed charly-<name> ssh alias)"`
	Image    string `arg:"" help:"image ref (short name or full ref) present in host podman storage"`
	As       string `name:"as" help:"after load, tag the image in the guest under this stable ref (e.g. localhost/charly-selkies-kde:latest)"`
	Rootless bool   `name:"rootless" help:"load into the guest USER's rootless podman storage instead of root's — so a rootless --user quadlet (e.g. a nested-pod-in-VM deploy) can run it"`
	Domain   string `name:"domain" help:"per-deploy domain identity (ssh alias charly-<domain>); absent for a direct cp-box (domain = entity). A check bed's domain is the BED name, not the entity — pass --domain <bed> there."`
}

func (c *VmCpBoxCmd) Run() error {
	// Shared with `charly box load` — see spec's ResolveDeliverableRef. Both delivery verbs
	// need exactly this resolution and each had grown its own copy (R3).
	ref, err := container.ResolveDeliverableRef("podman", c.Image)
	if err != nil {
		return fmt.Errorf("vm cp-box: %w", err)
	}
	guest := sshParamsForVm(domainOr(c.VM, c.Domain))
	return TransferImageToGuest(context.Background(), guest, "podman", ref, c.As, c.Rootless, EmitOpts{})
}

// TransferImageToGuest streams a host image into a VM guest's podman storage.
// It is the VM binding of the venue-generic delivery path — see
// deploykit.TransferImageToVenue for the streaming, verified-idempotency and
// torn-overlay recovery semantics every venue shares.
//
// rootless selects WHICH guest podman storage:
//   - rootless == false → ROOT storage (`sudo podman`). For a `sudo podman run
//     --device nvidia.com/gpu=all` consumer that needs /dev/nvidia* via root.
//   - rootless == true  → the SSH user's ROOTLESS storage (`podman`, no sudo).
//     This is what the nested-pod-in-VM deploy needs: plugin-deploy-vm's PostApply
//     brings the pod up with the guest user's own `charly fleet from-box` (a
//     --user quadlet), which reads the USER's podman storage — so the image must
//     land there, not in root's. Rootless GPU works via CDI (/dev/nvidia* are
//     world-rw; the nvidia-driver candy's boot service writes a world-readable
//     /etc/cdi/nvidia.yaml).
//
// Requires an *SSHExecutor (the VM case): the load side is reached over ssh.
func TransferImageToGuest(ctx context.Context, de DeployExecutor, hostEngine, ref, as string, rootless bool, opts EmitOpts) error {
	if de == nil {
		return fmt.Errorf("TransferImageToGuest: nil executor")
	}
	sshExec, ok := de.(*SSHExecutor)
	if !ok {
		return fmt.Errorf("TransferImageToGuest: requires an SSH executor (got %T)", de)
	}
	// The guest podman invocation prefix for the chosen storage: "podman" (the SSH
	// user's rootless storage) or "sudo podman" (root storage). One value drives the
	// load, the integrity probe and the tag, so they cannot disagree about the store.
	podman := "sudo podman"
	if rootless {
		podman = "podman"
	}
	return deploykit.TransferImageToVenue(ctx, deploykit.ImageVenue{
		Exec:      de,
		PodmanCmd: podman,
		Rootless:  rootless,
		Label:     "cp-box",
		NewLoadCmd: func() *exec.Cmd {
			args := sshExec.SSHBaseArgs()
			if rootless {
				args = append(args, "podman", "load")
			} else {
				args = append(args, "sudo", "podman", "load")
			}
			return exec.CommandContext(ctx, "ssh", args...)
		},
	}, hostEngine, ref, as, opts)
}
