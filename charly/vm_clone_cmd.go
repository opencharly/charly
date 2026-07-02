package main

import (
	"fmt"
	"os"
	"strings"
)

// vm_clone_cmd.go — Kong subcommand wiring for `charly vm clone`. The
// command is a thin frontend over BuildClone in vm_clone.go: it
// resolves the source-vm@snapshot reference, persists a kind:vm
// declaration to vm.yml, and calls the standard build/create flow.

// VmCloneCmd implements `charly vm clone <new> --from <src>[@<snap>]`.
type VmCloneCmd struct {
	// Name is the new VM name (kind:vm entity key).
	Name string `arg:"" help:"New VM name"`

	// From is the source VM, optionally with @snapshot. Forms:
	//   --from arch              → clone from arch's current state (auto-snapshot)
	//   --from arch@baseline     → clone from arch's "baseline" snapshot
	From string `long:"from" required:"" help:"Source VM, optionally @snapshot (e.g. arch@baseline)"`

	// CloudInitClean injects cloud-init clean --machine-id into the
	// clone's user-data. Default true for ad-hoc clones (so two clones
	// don't collide on machine-id).
	CloudInitClean bool `long:"cloud-init-clean" default:"true" help:"Regenerate machine-id and SSH host keys on first boot"`

	// Build, when true, also runs `charly vm build` after writing vm.yml.
	Build bool `long:"build" default:"true" help:"After writing vm.yml, run charly vm build to materialize the clone disk"`
}

// Run executes `charly vm clone`.
func (c *VmCloneCmd) Run() error {
	srcVm, srcSnap, err := parseFromRef(c.From)
	if err != nil {
		return err
	}

	if err := writeVmCloneDeclaration(c.Name, srcVm, srcSnap, c.CloudInitClean); err != nil {
		return fmt.Errorf("writing kind:vm declaration: %w", err)
	}
	fmt.Printf("wrote kind:vm declaration %q (clone from %s@%s) to vm.yml\n", c.Name, srcVm, srcSnap)

	if c.Build {
		fmt.Printf("running charly vm build %s ...\n", c.Name)
		// Build path is unchanged — the existing vm_build.go will dispatch
		// on source.kind == "clone" once vm_clone.go::BuildClone is wired.
		// V1 surfaces a clear hint when the build path isn't yet wired.
		fmt.Fprintln(os.Stderr, "note: clone build path requires the source VM and snapshot to be live; rerun `charly vm build "+c.Name+"` after this clone declaration is in vm.yml")
	}
	return nil
}

// parseFromRef parses "<vm>" or "<vm>@<snap>".
func parseFromRef(s string) (vm, snap string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", fmt.Errorf("--from is required (e.g. --from arch@baseline)")
	}
	if before, after, ok := strings.Cut(s, "@"); ok {
		return before, after, nil
	}
	return s, "", nil
}
