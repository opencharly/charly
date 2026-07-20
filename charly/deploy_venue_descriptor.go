package main

import (
	"fmt"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// deploy_venue_descriptor.go — the K4-C venue-descriptor ENCODER (the host-only half of the
// venue-scoped-executor-session seam: a live deploykit.DeployExecutor cannot cross the wire, so
// the host encodes it into a spec.VenueDescriptor before handing it to a plugin, and DECODES a
// descriptor a plugin hands back via the existing venueFromDescriptor (substrate_lifecycle_grpc.go,
// extended below for the "nested" case). This is the same decouple point PrepareVenue already
// uses for a ROOT venue, generalized to a NESTED tree hop: deriveChildExecutorForPath's BODY
// (bundle_add_cmd.go) is UNCHANGED — it still constructs the concrete kit.ShellExecutor /
// *kit.SSHExecutor / *kit.NestedExecutor values; this file only translates the RESULT to/from the
// wire so a plugin-initiated dispatch (or, later, a plugin-driven tree walk) can thread it.

// venueDescriptorForExecutor encodes a live executor into its wire-safe spec.VenueDescriptor.
// nil → nil (no venue / share-parent, mirroring venueFromDescriptor's Kind=="" case).
func venueDescriptorForExecutor(exec deploykit.DeployExecutor) (*spec.VenueDescriptor, error) {
	switch e := exec.(type) {
	case nil:
		return nil, nil
	case kit.ShellExecutor:
		return &spec.VenueDescriptor{Kind: "shell"}, nil
	case *kit.SSHExecutor:
		return &spec.VenueDescriptor{
			Kind:           "ssh",
			User:           e.User,
			Host:           e.Host,
			Port:           e.Port,
			Args:           e.Args,
			ConnectTimeout: e.ConnectTimeout,
		}, nil
	case *kit.NestedExecutor:
		parent, err := venueDescriptorForExecutor(e.Parent)
		if err != nil {
			return nil, err
		}
		jumpKind, err := jumpKindToWire(e.Jump.Kind)
		if err != nil {
			return nil, err
		}
		return &spec.VenueDescriptor{
			Kind: "nested",
			Jump: &spec.NestedJumpDescriptor{
				Kind:      jumpKind,
				Target:    e.Jump.Target,
				ExtraArgs: e.Jump.ExtraArgs,
			},
			Parent: parent,
		}, nil
	default:
		return nil, fmt.Errorf("venue descriptor: cannot encode executor type %T", exec)
	}
}

// jumpKindToWire / jumpKindFromWire translate kit.JumpKind's int enum to/from the wire's string
// form, so the wire never depends on the enum's numeric values directly.
func jumpKindToWire(k kit.JumpKind) (string, error) {
	switch k {
	case kit.JumpPodmanExec:
		return "podman-exec", nil
	case kit.JumpDockerExec:
		return "docker-exec", nil
	case kit.JumpSSH:
		return "ssh", nil
	case kit.JumpVirshConsole:
		return "virsh-console", nil
	default:
		return "", fmt.Errorf("venue descriptor: unknown jump kind %d", k)
	}
}

func jumpKindFromWire(s string) (kit.JumpKind, error) {
	switch s {
	case "podman-exec":
		return kit.JumpPodmanExec, nil
	case "docker-exec":
		return kit.JumpDockerExec, nil
	case "ssh":
		return kit.JumpSSH, nil
	case "virsh-console":
		return kit.JumpVirshConsole, nil
	default:
		return 0, fmt.Errorf("venue descriptor: unknown jump kind %q", s)
	}
}
