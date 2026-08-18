package box

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/container"
	specexec "github.com/opencharly/spec/exec"
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
	// Shared with `charly vm cp-box` — both delivery verbs need exactly this resolution, and
	// each had grown its own copy until spec's ResolveDeliverableRef collapsed them (R3, in
	// the cutover whose own thesis is that a second venue costs a constructor, not a copy).
	ref, err := container.ResolveDeliverableRef("podman", g.Image)
	if err != nil {
		return fmt.Errorf("box load: %w", err)
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

	// The probe jump MUST follow the engine ResolveContainer just returned. Hardcoding
	// JumpPodmanExec while the load below honours `engine` makes the two halves address the
	// venue through different binaries on a docker deploy: the load runs `docker exec` and
	// the probes run `podman exec` against the same container. Both then fail at transport
	// level — and VenueHasImage / VenueImageCorrupt both return FALSE on error, so the
	// verified idempotency and the torn-overlay re-stream do not fail, they silently stop
	// meaning anything. That is the safety property this verb advertises, going vacuous
	// without a symptom. spec/exec/deploy_chain.go already pairs engine→jump this way; this
	// was a wiring omission, not a design question.
	engineJump := specexec.JumpPodmanExec
	if strings.Contains(engine, "docker") {
		engineJump = specexec.JumpDockerExec
	}

	ctx := context.Background()
	venue := deploykit.ImageVenue{
		Exec:      &specexec.NestedExecutor{Parent: specexec.ShellExecutor{}, Jump: specexec.NestedJump{Kind: engineJump, Target: name}},
		PodmanCmd: podman,
		Rootless:  true,
		Label:     "box load",
		NewLoadCmd: func() *exec.Cmd {
			return exec.CommandContext(ctx, engine, "exec", "-i", name,
				"podman", "--remote", "--url", socketURL, "load")
		},
	}
	if err := deploykit.TransferImageToVenue(ctx, venue, "podman", ref, g.As, deploykit.EmitOpts{}); err != nil {
		// The hint is attached UNCONDITIONALLY rather than keyed on substrings of the
		// error. The previous form matched "Cannot connect" / "no such file", but a
		// missing in-venue socket surfaces as `load failed: exit status 125` from the
		// exec'd process — so the hint never fired for the case it was written for, and
		// DID fire when the HOST podman binary was missing, telling the operator to
		// compose a candy into the box for a fault on their own machine. A hint that
		// cannot identify its case should not pretend to: it now names the socket it
		// tried and leaves the diagnosis to the reader.
		return fmt.Errorf("%w\n\nif the venue serves no podman API socket at %s, compose the "+
			"nested-podman-socket candy into its box or pass --socket", err, g.Socket)
	}
	return nil
}
