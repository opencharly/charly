package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// podServiceCmd is the host-side reconstruction of the former ServiceCmd's four leaves (now
// command:service in candy/plugin-pod, unified behind ONE Kong grammar with an Operation
// discriminator) — hostBuildPodService (host_build_pod_service.go) runs its Run() body VERBATIM.
// TRACKED P13-KERNEL EXIT: dispatchLifecycleTarget/LifecycleTarget (deploy_target_unified.go,
// pod_lifecycle_verb.go) are registered P13-KERNEL migration inventory (see start.go's header) —
// this resolver moves through the same venue-scoped-executor-session seam when that wave lands.
type podServiceCmd struct {
	Operation string // "start" | "stop" | "status" | "restart"
	Box       string
	Service   string
	Instance  string
}

func (c *podServiceCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Box, c.Instance)
	if err != nil {
		return err
	}
	if c.Operation == "status" {
		return execInitCommand(c.Box, c.Instance, engine, name, initDef, "status")
	}
	if err := validateServiceName(engine, name, c.Service); err != nil {
		return err
	}
	return execInitCommand(c.Box, c.Instance, engine, name, initDef, c.Operation, c.Service)
}

// resolveServiceInit resolves the container, engine, and init system for service management.
func resolveServiceInit(box, instance string) (engine, containerName string, initDef *ResolvedInit, err error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return "", "", nil, err
	}
	boxName := kit.ResolveBoxName(box)
	runEngine := deploykit.ResolveBoxEngineForDeploy(boxName, instance, rt.RunEngine)
	engine = kit.EngineBinary(runEngine)
	containerName = kit.ContainerNameInstance(boxName, instance)
	if !containerRunning(engine, containerName) {
		return "", "", nil, fmt.Errorf("container %s is not running", containerName)
	}

	// Determine init system from image labels
	imageRef := containerImage(engine, containerName)
	if imageRef == "" {
		return "", "", nil, fmt.Errorf("cannot determine image for container %s", containerName)
	}
	meta, err := deploykit.ExtractMetadata(engine, imageRef)
	if err != nil {
		return "", "", nil, fmt.Errorf("cannot read image metadata: %w", err)
	}
	if meta == nil || meta.Init == "" {
		return "", "", nil, fmt.Errorf("no init system configured for container %s (rebuild image with the embedded init: vocabulary)", containerName)
	}

	// Load init config to get management commands
	initDef, err = resolveInitDefFromMeta(meta)
	if err != nil {
		return "", "", nil, err
	}

	return engine, containerName, initDef, nil
}

// wellKnownInitDefs is the legacy fallback for pre-init_def-label images —
// images built before the ai.opencharly.init_def label existed, whose labels
// cannot be re-baked. Current images carry their full init contract in that
// label, so resolveInitDefFromMeta / resolveEntrypointFromMeta read it
// label-first and only consult this table when meta.InitDef is absent.
//
// Because the build-resolved def now travels in the label, init systems
// declared ONLY in the embedded init: vocabulary (charly/charly.yml) work at
// runtime too — they no longer need an entry here. This table is frozen at
// the two init systems that predate the label; do NOT add new ones (declare
// them in the vocabulary instead, where they bake into the label).
var wellKnownInitDefs = map[string]*ResolvedInit{
	"supervisord": {
		Entrypoint:     []string{"supervisord", "-n", "-c", "/etc/supervisord.conf"},
		ManagementTool: "supervisorctl",
		ManagementCommands: map[string]string{
			"status":  "status",
			"start":   "start {{.Service}}",
			"stop":    "stop {{.Service}}",
			"restart": "restart {{.Service}}",
		},
	},
	"systemd": {
		// Systemd-on-bootc boots via VM init; container has no entrypoint.
		Entrypoint:     nil,
		ManagementTool: "systemctl",
		ManagementCommands: map[string]string{
			"status":  "--user status {{.Service}}",
			"start":   "--user start {{.Service}}",
			"stop":    "--user stop {{.Service}}",
			"restart": "--user restart {{.Service}}",
		},
	},
}

// resolveInitDefFromMeta returns the init contract for management-command
// rendering. Label-first: the build-resolved def is baked into the
// ai.opencharly.init_def label (meta.InitDef), so any vocabulary-declared init
// system — including custom ones — resolves at runtime. Falls back to
// wellKnownInitDefs only for pre-init_def-label images (built before the
// label existed).
func resolveInitDefFromMeta(meta *spec.BoxMetadata) (*ResolvedInit, error) {
	if meta.InitDef != nil {
		return &ResolvedInit{
			Entrypoint:         meta.InitDef.Entrypoint,
			FallbackEntrypoint: meta.InitDef.FallbackEntrypoint,
			ManagementTool:     meta.InitDef.ManagementTool,
			ManagementCommands: meta.InitDef.ManagementCommands,
		}, nil
	}
	if def, ok := wellKnownInitDefs[meta.Init]; ok {
		return def, nil
	}
	return nil, fmt.Errorf("unknown init system %q; cannot determine management commands (image predates the ai.opencharly.init_def label — rebuild it to bake the init contract)", meta.Init)
}

// execInitCommand runs a service-management command INSIDE the container (the K4 deep-body move).
// The HOST resolves the full `<engine> exec <container> <tool> <op> [svc]` argv from the image's baked
// init contract, and the owning deploy PLUGIN executes it over the served executor via
// LifecycleTarget.Shell — capturing the output, REPRINTING it, and PRESERVING the container command's
// exit code exactly (grpcSubstrateLifecycle.Shell → the pod plugin's podExec). This replaces the
// former inline host `podman exec` (a passthrough exec that lived in core); interactive `charly shell`
// stays a CORE command (host-process TTY, F12/#62) — service is non-interactive, so it moves.
func execInitCommand(box, instance, engine, containerName string, initDef *ResolvedInit, operation string, args ...string) error {
	serviceName := ""
	if len(args) > 0 {
		serviceName = args[0]
	}

	rendered, err := initRenderManagementCommand(initDef, operation, serviceName)
	if err != nil {
		return err
	}

	argv := append([]string{engine, "exec", containerName, initDef.ManagementTool}, strings.Fields(rendered)...)
	lt, err := dispatchLifecycleTarget("service", box, instance)
	if err != nil {
		return err
	}
	return lt.Shell(context.Background(), argv)
}

// validateServiceName checks that a service name exists in the image's service list.
func validateServiceName(engine, containerName, serviceName string) error {
	imageRef := containerImage(engine, containerName)
	if imageRef == "" {
		return fmt.Errorf("cannot determine image for container %s", containerName)
	}
	meta, err := deploykit.ExtractMetadata(engine, imageRef)
	if err != nil {
		return fmt.Errorf("cannot read image metadata: %w", err)
	}
	if meta == nil {
		return fmt.Errorf("no opencharly metadata found for container %s", containerName)
	}
	if slices.Contains(meta.ServiceNames, serviceName) {
		return nil
	}
	return fmt.Errorf("service %q not found in image (available: %s)", serviceName, strings.Join(meta.ServiceNames, ", "))
}
