package box

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	specexec "github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/container"
)

// box_load.go — `charly box load`, the CONTAINER-venue image-delivery verb: it streams a
// locally-built image out of the host store and into the store of a podman running INSIDE a
// running pod deploy. It is the exact twin of `charly vm cp-box` (the VM venue), and both are
// bindings of the one venue-generic path, deploykit.TransferImageToVenue.
//
// Why this verb has to exist rather than a shell pipeline. A nested candybox — a pod composing
// container-nesting, with its own rootless podman serving an API socket at uid 1000 — has an
// image store that is genuinely separate from the host's. Anything that must run INSIDE that
// boundary (the Factory's incident spikes: an agentteams controller spawning its own
// Manager/Worker containers) needs its images in the NESTED store, because the alternative —
// binding the host's podman socket in — dissolves the boundary: the spawned containers land in
// the host store, beside everything else running there. A registry hop is not an escape either;
// the images in question are private, so an anonymous pull 401s. That leaves local delivery, and
// with no verb for it the only path was a hand-run
// `podman save … | podman --remote --url … load` — a manual container-engine command against a
// charly-managed deploy, which the rulebook forbids outright (R4). The missing capability was the
// bug; this closes it.
//
// The load lands through `podman exec -i` rather than from the host directly because the target
// socket lives in the container's own mount namespace — there is no host path to it.

// loadGrammar is the `charly box load <target> <image> [--as] [--socket] [--instance]` CLI surface.
type loadGrammar struct {
	Target   string `arg:"" help:"Running deploy (or box) whose venue receives the image — the same name charly shell/cp accept."`
	Image    string `arg:"" help:"Image ref present in HOST podman storage (build it first with charly box build)."`
	As       string `name:"as" help:"After load, tag the image in the venue under this stable ref (e.g. localhost/charly-agentteams-worker:latest)."`
	Socket   string `name:"socket" default:"/run/user/1000/podman/podman.sock" help:"In-venue podman API socket the nested store is served on. The default is the uid-1000 rootless path the container-nesting composition serves."`
	Instance string `name:"instance" help:"Deploy instance suffix, when the target runs more than one."`
}

// dispatchLoad kong-parses the load grammar and runs the verified transfer.
func dispatchLoad(args []string) error {
	var g loadGrammar
	done, err := parseLeaf("load", &g, args)
	if done || err != nil {
		return err
	}
	// An explicit ref that exists locally is used exactly as authored — naming a tag IS the
	// choice. A bare box name resolves through the STRICT resolver, which refuses to elect an
	// image older than the newest local build: delivering a stale artifact into a venue is the
	// same wrong-artifact class that `charly check box` refuses to certify, and it is far harder
	// to notice here, because the load succeeds and the venue simply runs the wrong image.
	ref := g.Image
	if !container.LocalImageExists("podman", ref) {
		resolved, err := container.ResolveBuiltImageRef("podman", ref)
		if err != nil {
			return fmt.Errorf("box load: %q is not in host podman storage and could not be resolved to a local build (build it first: charly box build %s): %w", g.Image, g.Image, err)
		}
		ref = resolved
	}

	// Resolve the running container the same way charly shell / charly cp do, so a name that
	// works for those works here — and so a stopped target fails with "is not running" rather
	// than a confusing exec error.
	engine, name, err := deploykit.ResolveContainer(g.Target, g.Instance)
	if err != nil {
		return fmt.Errorf("box load: %w", err)
	}
	if name == "" {
		return fmt.Errorf("box load: %q resolves to the local host, which has no nested store to load into", g.Target)
	}

	socketURL := "unix://" + g.Socket
	// One prefix drives the load, the integrity probe, the tag and any removal, so none of them
	// can address a different store than the image actually landed in. --remote --url is what
	// pins every one of them to the SOCKET's store rather than to whatever local store the
	// in-container podman would otherwise default to.
	podman := "podman --remote --url " + socketURL

	ctx := context.Background()
	venue := deploykit.ImageVenue{
		Exec:      &specexec.NestedExecutor{Parent: specexec.ShellExecutor{}, Jump: specexec.NestedJump{Kind: specexec.JumpPodmanExec, Target: name}},
		PodmanCmd: podman,
		Rootless:  true,
		Label:     "box load",
		NewLoadCmd: func() *exec.Cmd {
			return exec.CommandContext(ctx, engine, "exec", "-i", name,
				"podman", "--remote", "--url", socketURL, "load")
		},
	}
	if err := deploykit.TransferImageToVenue(ctx, venue, "podman", ref, g.As, deploykit.EmitOpts{}); err != nil {
		if strings.Contains(err.Error(), "Cannot connect") || strings.Contains(err.Error(), "no such file") {
			return fmt.Errorf("%w\n\nthe venue serves no podman API socket at %s — compose the nested-podman-socket candy into the box, or pass --socket", err, g.Socket)
		}
		return err
	}
	return nil
}
