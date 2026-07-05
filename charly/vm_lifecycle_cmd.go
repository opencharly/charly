package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk/spec"
)

// vm_lifecycle_cmd.go — the hidden `charly __vm-lifecycle <op> <name>` command (M4b). The `vm` deploy
// substrate lifecycle is externalized to candy/plugin-deploy-vm, but the vm VENUE lifecycle is
// DEEPLY host-coupled ("it needs core", CLAUDE.md doctrine): LoadUnified → spec.Vm, the libvirt
// domain boot, the managed ssh-config stanza, the guest-readiness waits + EnsureCharlyInGuest, and
// VmDeployState persistence. So the host-coupled logic STAYS host-side in vmSubstrateLifecycle, and
// the out-of-process plugin reaches it over the GENERIC "cli" host seam: the plugin's lifecycle
// Invoke forwards each Op to `charly __vm-lifecycle <op> <name>`, which dispatches to the SAME
// vmSubstrateLifecycle method and serializes the reply the grpcSubstrateLifecycle proxy expects.
// This is the vm analog of pod's HostBuild("overlay") — the host-coupled engine kept core (a generic
// seam), the plugin the substrate INTERFACE. The compiled-in registration is deleted; the plugin
// (Lifecycle:true) owns the word at plugin-load. The vmSubstrateLifecycle methods are UNCHANGED —
// only their dispatch path (proxy → plugin → cli → here) is new.

// VmLifecycleCmd is the hidden dispatch command wrapping the vm venue lifecycle.
type VmLifecycleCmd struct {
	Op           string   `arg:"" help:"lifecycle op: prepare-venue|post-apply|post-teardown|teardown-executor|artifact-key|start|stop|status|logs|shell|rebuild"`
	Name         string   `arg:"" help:"deploy name"`
	KeepImage    bool     `long:"keep-image" help:"PostTeardown: preserve images (vm ignores; pod-specific)"`
	RebuildImage bool     `long:"rebuild-image" help:"Rebuild: charly vm build before recreate"`
	DryRun       bool     `long:"dry-run" help:"Rebuild: print without executing"`
	Follow       bool     `long:"follow" help:"Logs: stream"`
	Tail         int      `long:"tail" help:"Logs: trailing lines"`
	Cmd          []string `long:"cmd" help:"Shell: command to run (repeatable)"`
}

func (c *VmLifecycleCmd) Run() error {
	life := vmSubstrateLifecycle{}
	ctx := context.Background()
	node := vmLifecycleNode(c.Name)

	switch c.Op {
	case "prepare-venue":
		exec, err := life.PrepareVenue(ctx, c.Name, "", node, nil, EmitOpts{})
		if err != nil {
			return err
		}
		// The preflight already persisted VmDeployState host-side, so no State patch to ship.
		return printVmLifecycleJSON(spec.PrepareVenueReply{Venue: venueDescriptorOf(exec)})
	case "post-apply":
		// PostApply (deployNestedPodsInGuest) runs over the guest executor — rebuild it (same
		// managed alias, no boot), exactly the TeardownExecutor form.
		exec, err := life.TeardownExecutor(c.Name, node)
		if err != nil {
			return err
		}
		return life.PostApply(ctx, c.Name, "", node, exec, EmitOpts{})
	case "post-teardown":
		if err := life.PostTeardown(c.Name, node, c.KeepImage); err != nil {
			return err
		}
		// vm removes its charly.yml entry host-side inside PostTeardown → no RemoveEntries to ship.
		return printVmLifecycleJSON(spec.PostTeardownReply{})
	case "teardown-executor":
		exec, err := life.TeardownExecutor(c.Name, node)
		if err != nil {
			return err
		}
		return printVmLifecycleJSON(venueDescriptorOf(exec))
	case "artifact-key":
		return printVmLifecycleJSON(map[string]string{"key": life.ArtifactKey(c.Name, node)})
	case "start":
		return life.Start(ctx, c.Name, node)
	case "stop":
		return life.Stop(ctx, c.Name, node)
	case "status":
		si, err := life.Status(ctx, c.Name, node)
		if err != nil {
			return err
		}
		return printVmLifecycleJSON(si)
	case "logs":
		return life.Logs(ctx, c.Name, node, LogsOpts{Follow: c.Follow, Tail: c.Tail})
	case "shell":
		return life.Shell(ctx, c.Name, node, c.Cmd)
	case "rebuild":
		return life.Rebuild(ctx, c.Name, node, RebuildOpts{RebuildImage: c.RebuildImage, DryRun: c.DryRun})
	}
	return fmt.Errorf("__vm-lifecycle: unknown op %q", c.Op)
}

// vmLifecycleNode resolves the merged deploy node for name (nil when absent — the vm lifecycle
// methods re-resolve the entity from the deploy config themselves).
func vmLifecycleNode(name string) *BundleNode {
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	tree, err := resolveTreeRoot(dir)
	if err != nil {
		return nil
	}
	if n, ok := tree[name]; ok {
		return &n
	}
	return nil
}

// venueDescriptorOf serializes a live DeployExecutor into the wire VenueDescriptor the proxy
// re-materializes (the live executor never crosses the wire).
func venueDescriptorOf(exec DeployExecutor) spec.VenueDescriptor {
	switch e := exec.(type) {
	case *SSHExecutor:
		return spec.VenueDescriptor{Kind: "ssh", User: e.User, Host: e.Host, Port: e.Port, Args: e.Args, ConnectTimeout: e.ConnectTimeout}
	case ShellExecutor:
		return spec.VenueDescriptor{Kind: "shell"}
	}
	return spec.VenueDescriptor{}
}

func printVmLifecycleJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}
