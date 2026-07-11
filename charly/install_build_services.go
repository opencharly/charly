package main

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
)

func init() { deploykit.CompileServiceSteps = compileServiceSteps }

func compileServiceSteps(layer CandyModel, img *ResolvedBox, hostCtx HostContext) []InstallStep {
	var out []InstallStep
	initIsSystemd := hostCtx.MachineVenue
	distros := serviceRenderDistros(img, hostCtx)

	// Detect mixed-entry pairs: which names have a use_packaged form? Only
	// entries that APPLY to this target's distro count — a Fedora/Arch-only
	// packaged form must not suppress a Debian/Ubuntu exec sibling of the same
	// name (see serviceEntryAppliesToDistro).
	namesWithPackaged := map[string]bool{}
	for i := range layer.Service() {
		if layer.Service()[i].IsPackaged() && serviceEntryAppliesToDistro(&layer.Service()[i], distros) {
			namesWithPackaged[layer.Service()[i].Name] = true
		}
	}

	// Lazy-loaded systemd InitDef + render context — only loaded if the
	// target is systemd AND at least one custom entry needs rendering.
	var systemdDef *ResolvedInit
	var renderCtx ServiceRenderContext
	loadedSystemd := false
	loadSystemd := func() bool {
		if loadedSystemd {
			return systemdDef != nil
		}
		loadedSystemd = true
		dir, err := os.Getwd()
		if err != nil {
			return false
		}
		_, _, initCfg, err := LoadBuildConfigForBox(dir)
		if err != nil || initCfg == nil {
			return false
		}
		def, ok := initCfg.Init["systemd"]
		if !ok || def == nil {
			return false
		}
		systemdDef = def
		renderCtx = ServiceRenderContext{
			Candy:         layer.GetName(),
			SystemUnitDir: "/etc/systemd/system",
		}
		// Service home, like shell-snippet home, must be the DESTINATION user's
		// home — not the build host's. For host/vm deploys defer it via the
		// {{.Home}} token (InstallPlan.ResolveHome substitutes the real guest /
		// host home at emit); for a container-systemd build the image's resolved
		// Home is the runtime home. (os.UserHomeDir() — the operator's home — was
		// the service-side instance of the VM $HOME bug.)
		svcHome := img.Home
		if hostCtx.MachineVenue {
			svcHome = HomeToken
		}
		if svcHome != "" {
			renderCtx.Home = svcHome
			renderCtx.UserUnitDir = svcHome + "/.config/systemd/user"
		}
		return true
	}

	for i := range layer.Service() {
		entry := &layer.Service()[i]
		// Per-distro filter: an entry with a distro: list renders only on the
		// named distros (see serviceEntryAppliesToDistro).
		if !serviceEntryAppliesToDistro(entry, distros) {
			continue
		}
		scope := ScopeSystem
		if entry.EffectiveScope() == "user" {
			scope = ScopeUser
		}

		if entry.IsPackaged() {
			// supervisord can't consume systemd packaged units.
			if !initIsSystemd {
				continue
			}
			out = append(out, &ServicePackagedStep{
				Unit:        ensureServiceSuffix(entry.UsePackaged),
				TargetScope: scope,
				Enable:      entry.Enable,
				CandyName:   layer.GetName(),
			})
			continue
		}

		// Custom-exec entry. On systemd targets, if a same-name
		// use_packaged sibling exists, the packaged form wins —
		// skip the custom entry entirely (mixed-pair polymorphism).
		if initIsSystemd && namesWithPackaged[entry.Name] {
			continue
		}

		step := &ServiceCustomStep{
			Name:        fmt.Sprintf("charly-%s-%s", layer.GetName(), entry.Name),
			TargetScope: scope,
			Enable:      entry.Enable,
			CandyName:   layer.GetName(),
		}

		// On systemd targets, pre-render the unit text now so the
		// executor doesn't need a lazy fallback. On supervisord
		// targets, the supervisord init pipeline renders its own
		// fragment — leave UnitText empty.
		if initIsSystemd && loadSystemd() {
			entryClone := *entry
			entryClone.Name = step.Name
			rendered, rerr := RenderService(&entryClone, systemdDef, renderCtx)
			if rerr == nil && rendered != nil {
				step.UnitText = rendered.UnitText
				step.UnitPath = rendered.UnitPath
			}
		}

		out = append(out, step)
	}
	return out
}
