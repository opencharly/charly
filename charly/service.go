package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// ServiceCmd manages services inside a running container
type ServiceCmd struct {
	Restart ServiceRestartCmd `cmd:"" help:"Restart an in-container service"`
	Start   ServiceStartCmd   `cmd:"" help:"Start an in-container service"`
	Status  ServiceStatusCmd  `cmd:"" help:"Show status of in-container services"`
	Stop    ServiceStopCmd    `cmd:"" help:"Stop an in-container service"`
}

// ServiceStatusCmd shows status of all services
type ServiceStatusCmd struct {
	Box      string `arg:"" help:"Box name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStatusCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Box, c.Instance)
	if err != nil {
		return err
	}
	return execInitCommand(c.Box, c.Instance, engine, name, initDef, "status")
}

// ServiceStartCmd starts a service
type ServiceStartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStartCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Box, c.Instance)
	if err != nil {
		return err
	}
	if err := validateServiceName(engine, name, c.Service); err != nil {
		return err
	}
	return execInitCommand(c.Box, c.Instance, engine, name, initDef, "start", c.Service)
}

// ServiceStopCmd stops a service
type ServiceStopCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceStopCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Box, c.Instance)
	if err != nil {
		return err
	}
	if err := validateServiceName(engine, name, c.Service); err != nil {
		return err
	}
	return execInitCommand(c.Box, c.Instance, engine, name, initDef, "stop", c.Service)
}

// ServiceRestartCmd restarts a service
type ServiceRestartCmd struct {
	Box      string `arg:"" help:"Box name"`
	Service  string `arg:"" help:"Service name"`
	Instance string `short:"i" long:"instance" help:"Instance name"`
}

func (c *ServiceRestartCmd) Run() error {
	engine, name, initDef, err := resolveServiceInit(c.Box, c.Instance)
	if err != nil {
		return err
	}
	if err := validateServiceName(engine, name, c.Service); err != nil {
		return err
	}
	return execInitCommand(c.Box, c.Instance, engine, name, initDef, "restart", c.Service)
}

// resolveServiceInit resolves the container, engine, and init system for service management.
func resolveServiceInit(box, instance string) (engine, containerName string, initDef *ResolvedInit, err error) {
	rt, err := ResolveRuntime()
	if err != nil {
		return "", "", nil, err
	}
	boxName := resolveBoxName(box)
	runEngine := ResolveBoxEngineForDeploy(boxName, instance, rt.RunEngine)
	engine = EngineBinary(runEngine)
	containerName = containerNameInstance(boxName, instance)
	if !containerRunning(engine, containerName) {
		return "", "", nil, fmt.Errorf("container %s is not running", containerName)
	}

	// Determine init system from image labels
	imageRef := containerImage(engine, containerName)
	if imageRef == "" {
		return "", "", nil, fmt.Errorf("cannot determine image for container %s", containerName)
	}
	meta, err := ExtractMetadata(engine, imageRef)
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

// wellKnownInitDefs + resolveInitDefFromMeta MOVED to sdk/kit (K4 lane B — shared between this
// file's continued core use and candy/plugin-deploy-pod's pod_lifecycle_resolve.go move); see
// kit_aliases.go's resolveInitDefFromMeta = kit.ResolveInitDefFromMeta.

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
	meta, err := ExtractMetadata(engine, imageRef)
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
