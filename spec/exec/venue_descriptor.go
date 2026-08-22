package exec

import (
	"fmt"

	"github.com/opencharly/spec/spec"
)

// VenueFromDescriptor re-materializes a spec.VenueDescriptor into a real DeployExecutor — the
// decouple point that lets a substrate lifecycle plugin run out-of-process: it hands back a
// serializable venue description over the wire, and whichever side needs a LIVE executor
// (previously only the host, via charly/substrate_lifecycle_grpc.go's now-deleted
// venueFromDescriptor) re-materializes it locally. Promoted here (S3b, Unit-6 design) because the
// function has ZERO core-only dependency (pure sdk/kit + sdk/spec) and now has TWO callers that
// must construct the byte-identical executor from the same descriptor: the host (still
// re-materializing it for --verify / subsequent dispatch calls on the same target) and
// candy/plugin-fleet (materializing its OWN executor for the substrate's Execute call, immediately
// after driving that same substrate's OpPrepareVenue). R3 — one function, not a duplicate in each
// package.
func VenueFromDescriptor(d spec.VenueDescriptor) (spec.DeployExecutor, error) {
	switch d.Kind {
	case "":
		return nil, nil // no venue (e.g. VenueExecutor declining → caller keeps its executor)
	case "shell":
		return ShellExecutor{}, nil
	case "ssh":
		return &SSHExecutor{User: d.User, Host: d.Host, Port: d.Port, Args: d.Args, ConnectTimeout: d.ConnectTimeout}, nil
	case "container":
		return ContainerChainFromDescriptor(d.Engine, d.ContainerName), nil
	default:
		return nil, fmt.Errorf("venue descriptor: unknown kind %q", d.Kind)
	}
}

// ContainerChainFromDescriptor rebuilds the exact single-hop NestedExecutor
// deploykit.ContainerChain(engine, containerName) produces — the SAME shape, without sdk/kit
// importing sdk/deploykit (deploykit already imports kit; importing back would cycle).
// deploykit.ContainerChain itself calls this (R3 — one construction, not two). engine defaults to
// "podman" (matching ContainerChain's own JumpPodmanExec default) for any value other than
// "docker".
func ContainerChainFromDescriptor(engine, containerName string) spec.DeployExecutor {
	jumpKind := JumpPodmanExec
	if engine == "docker" {
		jumpKind = JumpDockerExec
	}
	return &NestedExecutor{
		Parent: ShellExecutor{},
		Jump:   NestedJump{Kind: jumpKind, Target: containerName},
	}
}

// DescriptorFromExecutor is the pure INVERSE of VenueFromDescriptor: it derives a serializable
// spec.VenueDescriptor from an already-materialized, LIVE executor value. Three round-trippable
// shapes are recognized ("shell"/"ssh"/"container" — the last added K1-unblock W3 Unit B, see
// below); any OTHER concrete type (e.g. a genuinely composed multi-hop *NestedExecutor, which
// cannot be flattened into one descriptor tuple) returns the zero VenueDescriptor{} — callers
// treat that exactly like VenueFromDescriptor's own "" case (no venue; keep whatever executor is
// already in hand). This does NOT generalize to arbitrary N-hop composition.
//
// The bed-regression fix this promotion serves (FIX ROUND, S3b follow-up): a NESTED external
// deploy's ancestor executor (deploykit.RootExecutorForDeployNode's "ssh" result, threaded core-
// side as spec.EmitOpts.ParentExec via the ancestor-chain walk in charly/fleet_add_cmd.go's
// deriveChildExecutorForPath) is ALWAYS a plain ShellExecutor/*SSHExecutor for a single hop into a
// vm guest — never a NestedExecutor — so it round-trips through this exact pair of functions.
// charly/unified_targets.go's pluginDeployTarget.Add uses this to convert that live ancestor
// executor into venue_json BEFORE dispatch, since a live Go interface value cannot itself cross
// the []byte wire to candy/plugin-fleet (mirrors candy/plugin-fleet's OWN identically-shaped
// former venueDescriptorFromExecutor, now deleted — R3, one function for both directions' callers).
//
// The "container" arm (K1-unblock W3 Unit B) recognizes the ONE enumerable *NestedExecutor shape
// deploykit.ContainerChain always produces — Parent a plain ShellExecutor{}, a single
// JumpPodmanExec/JumpDockerExec hop, no ExtraArgs — the venue every plain pod/container check
// runs against (the check-runner family's most common case, needed so a plugin-constructed
// ContainerChain venue can round-trip to the host over InvokeProvider's VenueDescriptor seam). Any
// OTHER *NestedExecutor shape (a different Parent, JumpSSH/JumpVirshConsole, non-empty ExtraArgs —
// i.e. genuine multi-hop composition) falls through to the zero-value default, unchanged.
func DescriptorFromExecutor(exec spec.DeployExecutor) spec.VenueDescriptor {
	switch e := exec.(type) {
	case ShellExecutor:
		return spec.VenueDescriptor{Kind: "shell"}
	case *SSHExecutor:
		return spec.VenueDescriptor{Kind: "ssh", User: e.User, Host: e.Host, Port: e.Port, Args: e.Args, ConnectTimeout: e.ConnectTimeout}
	case *NestedExecutor:
		if _, ok := e.Parent.(ShellExecutor); ok {
			if len(e.Jump.ExtraArgs) == 0 {
				switch e.Jump.Kind {
				case JumpPodmanExec:
					return spec.VenueDescriptor{Kind: "container", Engine: "podman", ContainerName: e.Jump.Target}
				case JumpDockerExec:
					return spec.VenueDescriptor{Kind: "container", Engine: "docker", ContainerName: e.Jump.Target}
				}
			}
		}
		return spec.VenueDescriptor{}
	default:
		return spec.VenueDescriptor{}
	}
}
