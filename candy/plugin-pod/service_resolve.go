package pod

import (
	"fmt"
	"slices"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// service_resolve.go — the `charly service` argv-building logic, moved IN-PLUGIN (Cutover B
// unit 2 completion): resolveServiceInit/validateServiceName/execInitCommand's argv construction
// was reconstructed VERBATIM in charly-core (service.go) behind HostBuild("pod-service") even
// though every one of its dependencies is genuinely portable — spec.ResolvedInit is already an
// sdk/spec alias (not core-only), ServiceCommandContext is a plain struct, buildkit.RenderTemplate
// is sdk-portable, and containerRunning/containerImage/ResolveRuntime/ResolveBoxEngineForDeploy/
// ExtractMetadata are all kit/deploykit calls a plugin already has. Only the FINAL
// dispatchLifecycleTarget + LifecycleTarget.Shell step is a core Mechanism (the plugin loader +
// provider registry) — that's the ONE thing host_build_pod_lifecycle_dispatch.go's
// hostBuildPodService still does, receiving the fully-built argv over #PodServiceRequest.

// wellKnownInitDefs is the legacy fallback for pre-init_def-label images — images built before
// the ai.opencharly.init_def label existed, whose labels cannot be re-baked. Current images carry
// their full init contract in that label, so resolveInitDefFromMeta reads it label-first and only
// consults this table when meta.InitDef is absent. Frozen at the two init systems that predate the
// label; do NOT add new ones (declare them in the embedded init: vocabulary instead, where they
// bake into the label).
var wellKnownInitDefs = map[string]*spec.ResolvedInit{
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

// resolveInitDefFromMeta returns the init contract for management-command rendering. Label-first:
// the build-resolved def is baked into the ai.opencharly.init_def label (meta.InitDef), so any
// vocabulary-declared init system — including custom ones — resolves at runtime. Falls back to
// wellKnownInitDefs only for pre-init_def-label images (built before the label existed).
func resolveInitDefFromMeta(meta *spec.BoxMetadata) (*spec.ResolvedInit, error) {
	if meta.InitDef != nil {
		return &spec.ResolvedInit{
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

// serviceCommandContext is the template context for management_commands rendering.
type serviceCommandContext struct {
	Service string
}

// initRenderManagementCommand renders a management command template with the given service name.
func initRenderManagementCommand(def *spec.ResolvedInit, operation, serviceName string) (string, error) {
	tmplStr, ok := def.ManagementCommands[operation]
	if !ok {
		return "", fmt.Errorf("init system %q has no management command for %q", def.ManagementTool, operation)
	}
	ctx := serviceCommandContext{Service: serviceName}
	return buildkit.RenderTemplate("mgmt-"+operation, tmplStr, ctx)
}

// resolveServiceInit resolves the container, engine, and init system for service management.
func resolveServiceInit(box, instance string) (engine, containerName string, initDef *spec.ResolvedInit, err error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return "", "", nil, err
	}
	boxName := kit.ResolveBoxName(box)
	runEngine := deploykit.ResolveBoxEngineForDeploy(boxName, instance, rt.RunEngine)
	engine = kit.EngineBinary(runEngine)
	containerName = kit.ContainerNameInstance(boxName, instance)
	if !kit.ContainerRunning(engine, containerName) {
		return "", "", nil, fmt.Errorf("container %s is not running", containerName)
	}

	// Determine init system from image labels
	imageRef := kit.ContainerImage(engine, containerName)
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

// validateServiceName checks that a service name exists in the image's service list.
func validateServiceName(engine, containerName, serviceName string) error {
	imageRef := kit.ContainerImage(engine, containerName)
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

// buildServiceArgv resolves + validates, then renders the FINAL `<engine> exec <container> <tool>
// <op> [svc]` argv the host will run over LifecycleTarget.Shell — the plugin-side twin of the
// former core execInitCommand, minus the dispatch step itself.
func buildServiceArgv(box, instance, operation, service string) ([]string, error) {
	engine, containerName, initDef, err := resolveServiceInit(box, instance)
	if err != nil {
		return nil, err
	}
	if operation != "status" {
		if err := validateServiceName(engine, containerName, service); err != nil {
			return nil, err
		}
	}
	rendered, err := initRenderManagementCommand(initDef, operation, service)
	if err != nil {
		return nil, err
	}
	return append([]string{engine, "exec", containerName, initDef.ManagementTool}, strings.Fields(rendered)...), nil
}
