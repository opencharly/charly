package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// --- Init Config ---

// InitConfig represents the `init:` section of the embedded vocabulary (charly/charly.yml).
// Each init system defines how to detect, build, assemble, and manage services.
type InitConfig struct {
	Init map[string]*InitDef `yaml:"init" json:"init"`
}

// FragmentContext is the template context for fragment_template rendering.
type FragmentContext struct {
	Content   string
	CandyName string
	Index     int
}

// RelayContext is the template context for relay_template rendering.
type RelayContext struct {
	Port      int
	CandyName string
	Index     int
}

// StageFragmentContext is the template context for stage_fragment_copy rendering.
type StageFragmentContext struct {
	BoxName     string
	FragmentDir string
	FileName    string
}

// SystemEnableContext is the template context for system_enable_template rendering.
type SystemEnableContext struct {
	Units []string
}

// ServiceCommandContext is the template context for management_commands rendering.
type ServiceCommandContext struct {
	Service string
}

// DetectCandyInit returns which init system names a candy triggers,
// based on its candy manifest fields and file patterns.
func (ic *InitConfig) DetectCandyInit(ly *CandyYAML, candyPath string) []string {
	if ic == nil {
		return nil
	}
	var result []string
	for initName, def := range ic.Init {
		if detectsInit(def, ly, candyPath) {
			result = append(result, initName)
		}
	}
	sortStrings(result)
	return result
}

// detectsInit checks if a candy matches an init system's detection criteria.
// Schema-driven: iterates the unified service: list + per-entry init routing
// (IsPackaged → ServiceSchema.SupportsPackaged; custom exec → ServiceSchema.ServiceTemplate).
func detectsInit(def *InitDef, ly *CandyYAML, candyPath string) bool {
	if ly == nil {
		return false
	}
	// candy_field: [service] gates schema-driven detection.
	participatesInSchema := slices.Contains(def.CandyFields, "service")
	if participatesInSchema {
		for i := range ly.Service {
			entry := &ly.Service[i]
			if entry.IsPackaged() {
				if def.ServiceSchema != nil && def.ServiceSchema.SupportsPackaged {
					return true
				}
			} else {
				if def.ServiceSchema != nil && def.ServiceSchema.ServiceTemplate != "" {
					return true
				}
			}
		}
	}

	// candy_file: glob the candy dir (file_copy model — systemd *.service units).
	for _, pattern := range def.CandyFiles {
		matches, _ := filepath.Glob(filepath.Join(candyPath, pattern))
		if len(matches) > 0 {
			return true
		}
	}

	return false
}

// ResolveInitSystem determines the active init system for an image.
// Priority: explicit override → auto-detect from candies.
// Returns ("", nil) if no init system is needed.
//
// Candy capability requirements (RequiresCapabilities) are checked
// against the aggregated candy caps for the composition; init systems
// whose requirements aren't met are filtered out. The aggregated caps
// are also consulted for the bootc-prefer-systemd heuristic via
// PreserveUser (the canonical signal that this is a bootc-flavored
// composition).
func (ic *InitConfig) ResolveInitSystem(layers map[string]*Candy, candyOrder []string, explicit string) (string, *InitDef) {
	if ic == nil {
		return "", nil
	}

	// Explicit override
	if explicit != "" {
		if def, ok := ic.Init[explicit]; ok {
			return explicit, def
		}
	}

	caps, _ := AggregateCandyCapabilities(layers, candyOrder)
	if caps == nil {
		caps = &AggregatedCandyCaps{Provided: map[string]bool{}}
	}

	// Auto-detect: find the init system that candies trigger
	initHits := make(map[string]bool)
	for _, candyName := range candyOrder {
		layer, ok := layers[candyName]
		if !ok {
			continue
		}
		for initName := range layer.InitSystems {
			initHits[initName] = true
		}
		// port_relay triggers the init system with a relay_template
		if len(layer.PortRelayPorts) > 0 {
			for initName, def := range ic.Init {
				if def.RelayTemplate != "" {
					initHits[initName] = true
				}
			}
		}
	}

	// Filter by capability requirements
	for initName := range initHits {
		def := ic.Init[initName]
		if !initDefRequirementsMet(def, caps) {
			delete(initHits, initName)
		}
	}

	// For bootc-flavored compositions (preserve_user) prefer systemd over supervisord
	if caps.PreserveUser && initHits["systemd"] {
		return "systemd", ic.Init["systemd"]
	}

	// For container images, prefer supervisord
	if initHits["supervisord"] {
		return "supervisord", ic.Init["supervisord"]
	}

	// Return first remaining init system
	for initName := range initHits {
		return initName, ic.Init[initName]
	}

	return "", nil
}

// ActiveInit returns all init systems that are active for the given image.
// An image can have multiple active inits (e.g., supervisord + systemd on
// bootc-flavored compositions).
func (ic *InitConfig) ActiveInit(layers map[string]*Candy, candyOrder []string) map[string]*InitDef {
	if ic == nil {
		return nil
	}

	caps, _ := AggregateCandyCapabilities(layers, candyOrder)
	if caps == nil {
		caps = &AggregatedCandyCaps{Provided: map[string]bool{}}
	}

	result := make(map[string]*InitDef)
	for _, candyName := range candyOrder {
		layer, ok := layers[candyName]
		if !ok {
			continue
		}
		for initName := range layer.InitSystems {
			if def, ok := ic.Init[initName]; ok {
				if !initDefRequirementsMet(def, caps) {
					continue
				}
				result[initName] = def
			}
		}
		// port_relay triggers init systems with relay_template
		if len(layer.PortRelayPorts) > 0 {
			for initName, def := range ic.Init {
				if def.RelayTemplate != "" && initDefRequirementsMet(def, caps) {
					result[initName] = def
				}
			}
		}
	}

	return result
}

// initDefRequirementsMet reports whether the init definition's
// RequiresCapabilities are all present in the aggregated caps.
func initDefRequirementsMet(def *InitDef, caps *AggregatedCandyCaps) bool {
	if def == nil || len(def.RequiresCapability) == 0 {
		return true
	}
	if caps == nil || caps.Provided == nil {
		return false
	}
	for _, req := range def.RequiresCapability {
		if !caps.Provided[req] {
			return false
		}
	}
	return true
}

// HasRelayTemplate returns true if this init definition has a relay template.
func initHasRelayTemplate(def *InitDef) bool {
	return def.RelayTemplate != ""
}

// RenderManagementCommand renders a management command template with the given service name.
func initRenderManagementCommand(def *InitDef, operation, serviceName string) (string, error) {
	tmplStr, ok := def.ManagementCommands[operation]
	if !ok {
		return "", fmt.Errorf("init system %q has no management command for %q", def.ManagementTool, operation)
	}
	ctx := ServiceCommandContext{Service: serviceName}
	return RenderTemplate("mgmt-"+operation, tmplStr, ctx)
}

// --- Loading ---
// Init config is loaded as part of LoadBuildConfigForBox in format_config.go.
// The `init:` section of the embedded vocabulary (charly/charly.yml) is optional — absent/empty means no init system.

// InitNames returns a sorted list of all init system names.
func (ic *InitConfig) InitNames() []string {
	if ic == nil {
		return nil
	}
	names := make([]string, 0, len(ic.Init))
	for name := range ic.Init {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// RenderStageFragmentCopy renders the stage_fragment_copy template.
func initRenderStageFragmentCopy(def *InitDef, boxName, fileName string) (string, error) {
	if def.StageFragmentCopy == "" {
		return "", nil
	}
	ctx := StageFragmentContext{
		BoxName:     boxName,
		FragmentDir: def.FragmentDir,
		FileName:    fileName,
	}
	return RenderTemplate("stage-fragment-copy", def.StageFragmentCopy, ctx)
}

// RenderFragmentTemplate was the legacy path that took raw-INI service
// content from a candy manifest `service: |STRING|` and re-rendered it via an
// init-system template. Replaced by RenderService per F3 of the services
// refactor — each ServiceEntry is rendered via ServiceSchema.ServiceTemplate.
// Function deleted; fragment_template field removed from InitDef.

// RenderRelayTemplate renders the relay_template for a port relay.
func initRenderRelayTemplate(def *InitDef, port int, candyName string, index int) (string, error) {
	if def.RelayTemplate == "" {
		return "", fmt.Errorf("init system has no relay_template")
	}
	ctx := RelayContext{
		Port:      port,
		CandyName: candyName,
		Index:     index,
	}
	result, err := RenderTemplate("relay", def.RelayTemplate, ctx)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, nil
}

// RenderAssemblyTemplate renders the assembly_template.
func initRenderAssemblyTemplate(def *InitDef) (string, error) {
	if def.AssemblyTemplate == "" {
		return "", nil
	}
	return RenderTemplate("assembly", def.AssemblyTemplate, nil)
}

// RenderSystemEnableTemplate renders the system_enable_template.
func initRenderSystemEnableTemplate(def *InitDef, units []string) (string, error) {
	if def.SystemEnableTemplate == "" || len(units) == 0 {
		return "", nil
	}
	ctx := SystemEnableContext{Units: units}
	return RenderTemplate("system-enable", def.SystemEnableTemplate, ctx)
}

// RenderPostAssemblyTemplate renders the post_assembly_template.
func initRenderPostAssemblyTemplate(def *InitDef) (string, error) {
	if def.PostAssemblyTemplate == "" {
		return "", nil
	}
	return RenderTemplate("post-assembly", def.PostAssemblyTemplate, nil)
}
