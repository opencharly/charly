package box

// validate_graph.go — the B-GRAPH resolution-graph rules + the CAPABILITIES rules (task #60, Unit C).
// The graph rules RE-RUN the deploykit resolution functions (ResolveBoxOrder / GlobalCandyOrder /
// ResolveCandyOrder / CollectAllBoxCandies) over the envelope adapters (vc.bk buildkit.ResolvedBox +
// vc.dk deploykit.CandyModel) — the SAME functions charly box build/generate use — to catch cycles +
// missing-builder/engine/data/port-relay/init invariants. Namespace re-resolution is NOT redone here
// (the projector already resolved namespaces host-side; an unresolvable namespaced base is a host
// diagnostic), so this is DAG-cycle detection over the already-resolved box set, per the Unit-C scoping
// NOTE. The capabilities rules read the per-candy preserve_user off the envelope (ruling a).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// validateBoxDAG re-runs the DAG-cycle checks over the resolved box set (ResolveBoxOrder +
// GlobalCandyOrder). It does NOT re-resolve namespaces — the projector did, and a namespaced-base
// resolve failure already rode back as a host diagnostic.
func validateBoxDAG(vc *vctx, e *vErr) {
	if len(vc.bk) == 0 {
		return
	}
	_, orderErr := deploykit.ResolveBoxOrder(vc.bk, vc.dk)
	if orderErr != nil {
		var cycleErr *deploykit.CycleError
		if errors.As(orderErr, &cycleErr) {
			e.Add("box dependency cycle: %s", strings.Join(cycleErr.Cycle, " -> "))
		} else {
			e.Add("box DAG error: %v", orderErr)
		}
		// A cyclic/broken DAG makes the global candy order meaningless — stop here.
		return
	}
	if _, glErr := deploykit.GlobalCandyOrder(vc.bk, vc.dk); glErr != nil {
		e.Add("global candy order: %v", glErr)
	}
}

// validateCandyDAG checks each box's candy chain for cycles.
func validateCandyDAG(vc *vctx, e *vErr) {
	for boxName := range vc.boxes {
		_, err := deploykit.ResolveCandyOrder(vc.boxes[boxName].Candy, vc.dk, nil)
		if err == nil {
			continue
		}
		var cycleErr *deploykit.CycleError
		if errors.As(err, &cycleErr) {
			e.Add("box %q: candy dependency cycle: %s", boxName, strings.Join(cycleErr.Cycle, " -> "))
		} else {
			e.Add("box %q: candy resolution error: %v", boxName, err)
		}
	}
}

// validateBuilders runs the graph-portable candy-needs-builder DETECTION: for each resolved candy,
// each builder in the env.Builder vocabulary that DETECTS the candy (by file OR by format section,
// distro-gated) must be configured in the box's resolved builder map.
//
// NOTE (task #60): the defaults.builder + per-box builder/produce REFERENCE checks (cfg.Defaults.Builder,
// ValidBuilderType, and the namespace-aware resolveBoxRef existence/capability lookups) are host-coupled
// — neither cfg.Defaults nor the namespace resolver is carried on the envelope — so they stay host-side
// and are NOT ported here. Listed in the report.
func validateBuilders(vc *vctx, e *vErr) {
	if vc.env == nil {
		return
	}
	for boxName := range vc.boxes {
		box := vc.boxes[boxName]
		resolved := buildkit.BuilderMap(box.Builder)
		buildFmtSet := make(map[string]bool, len(box.BuildFormats))
		for _, f := range box.BuildFormats {
			buildFmtSet[f] = true
		}
		candyOrder, err := deploykit.ResolveCandyOrder(box.Candy, vc.dk, nil)
		if err != nil {
			continue
		}
		for _, candyName := range candyOrder {
			dk, ok := vc.dk[candyName]
			if !ok {
				continue
			}
			for builderName, builderDef := range vc.env.Builder {
				if builderDef == nil {
					continue
				}
				fileMatched := false
				for _, f := range builderDef.DetectFiles {
					if dk.HasFile(f) {
						fileMatched = true
						break
					}
				}
				configMatched := builderDef.DetectConfig != "" && candyHasFormatConfig(dk, builderDef.DetectConfig)
				if !fileMatched && !configMatched {
					continue
				}
				// Distro-aware gate: a format-section-only match is unreachable when the image's
				// build formats don't include that format (the IR compiler iterates BuildFormats).
				if !fileMatched && configMatched && !buildFmtSet[builderDef.DetectConfig] {
					continue
				}
				if !resolved.HasBuilder(builderName) {
					e.Add("box %q: candy %q needs builder %q but no builder.%s configured", boxName, candyName, builderName, builderName)
				}
			}
		}
	}
}

// candyHasFormatConfig reports whether the candy has a non-empty package section for formatName.
func candyHasFormatConfig(dk deploykit.CandyModel, formatName string) bool {
	section := dk.FormatSection(formatName)
	return section != nil && len(section.Packages) > 0
}

// validatePackagedServices validates use_packaged: entries + warns on packaged-only services in a
// non-preserve_user composition.
func validatePackagedServices(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		for i := range m.Service {
			entry := &m.Service[i]
			if !entry.IsPackaged() {
				continue
			}
			unit := entry.UsePackaged
			if unit == "" {
				e.Add("candy %q: service[%d] use_packaged cannot be empty", name, i)
			}
			if strings.Contains(unit, "/") || strings.Contains(unit, " ") {
				e.Add("candy %q: service[%d] use_packaged %q must be a unit name (no paths or spaces)", name, i, unit)
			}
		}
		if candyHasPackaged(m) && !candyHasAnyPackages(m) {
			e.Add("candy %q: use_packaged entries require candy packages (distro tag sections or top-level package:) that provide those units", name)
		}
	}
	// Warn (non-fatal) when a non-preserve_user box includes an orphan-packaged candy.
	for boxName := range vc.boxes {
		box := vc.boxes[boxName]
		if boxPreserveUser(vc, box.Candy) {
			continue
		}
		for _, candyRef := range box.Candy {
			bare := deploykit.BareRef(candyRef)
			m, ok := vc.models[bare]
			if !ok || !candyHasOrphanPackaged(m) {
				continue
			}
			fmt.Fprintf(os.Stderr, "Warning: box %q includes candy %q with a packaged-only service (no custom-exec sibling), but composition does not preserve_user (its systemd unit will be ignored)\n", boxName, bare)
		}
	}
}

// candyHasPackaged reports whether any service entry reuses a distro-shipped unit.
func candyHasPackaged(m spec.CandyModel) bool {
	for i := range m.Service {
		if m.Service[i].IsPackaged() {
			return true
		}
	}
	return false
}

// candyHasOrphanPackaged reports whether the candy has a use_packaged service entry with NO custom-exec
// sibling of the same name (only such orphan units are dropped under supervisord).
func candyHasOrphanPackaged(m spec.CandyModel) bool {
	customNames := make(map[string]bool)
	for i := range m.Service {
		s := &m.Service[i]
		if !s.IsPackaged() && s.Exec != "" && s.Name != "" {
			customNames[s.Name] = true
		}
	}
	for i := range m.Service {
		s := &m.Service[i]
		if s.IsPackaged() && !customNames[s.Name] {
			return true
		}
	}
	return false
}

// validateEngineConfig detects conflicting candy engine (docker|podman) requirements within a box.
func validateEngineConfig(vc *vctx, e *vErr) {
	for boxName := range vc.boxes {
		resolved, err := deploykit.ResolveCandyOrder(vc.boxes[boxName].Candy, vc.dk, nil)
		if err != nil {
			continue
		}
		engineSources := make(map[string]string)
		for _, candyName := range resolved {
			m, ok := vc.models[candyName]
			if !ok {
				continue
			}
			if eng := m.Engine; eng != "" {
				if _, exists := engineSources[eng]; !exists {
					engineSources[eng] = candyName
				}
			}
		}
		if len(engineSources) > 1 {
			conflicts := make([]string, 0, len(engineSources))
			for eng, l := range engineSources {
				conflicts = append(conflicts, fmt.Sprintf("%s (from candy %s)", eng, l))
			}
			sort.Strings(conflicts)
			e.Add("box %q: conflicting engine requirements: %s", boxName, strings.Join(conflicts, ", "))
		}
	}
}

// validatePortRelay validates candy port_relay declarations + the box-level socat requirement.
func validatePortRelay(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		if len(m.PortRelayPorts) == 0 {
			continue
		}
		portSet := make(map[int]bool)
		for _, port := range m.PortRelayPorts {
			if portSet[port] {
				e.Add("candy %q port_relay: duplicate port %d", name, port)
			}
			portSet[port] = true
		}
		v := vc.views[name]
		if len(v.Ports) > 0 {
			candyPorts := make(map[int]bool)
			for _, p := range v.Ports {
				candyPorts[int(p)] = true
			}
			for _, port := range m.PortRelayPorts {
				if !candyPorts[port] {
					e.Add("candy %q port_relay: port %d is not declared in the candy's ports", name, port)
				}
			}
		} else {
			e.Add("candy %q port_relay: candy has no ports declared", name)
		}
	}

	for boxName := range vc.boxes {
		resolved, err := deploykit.ResolveCandyOrder(vc.boxes[boxName].Candy, vc.dk, nil)
		if err != nil {
			continue
		}
		hasRelay, hasSocat := false, false
		for _, candyName := range resolved {
			m, ok := vc.models[candyName]
			if !ok {
				continue
			}
			if len(m.PortRelayPorts) > 0 {
				hasRelay = true
			}
			if m.Name == "socat" {
				hasSocat = true
			}
		}
		if hasRelay && !hasSocat {
			e.Add("box %q: has port_relay candies but missing \"socat\" candy (add it to the box candies or as a dependency)", boxName)
		}
	}
}

// validateDataCandies checks data src dirs exist + per-box data-volume references + data_image constraints.
func validateDataCandies(vc *vctx, e *vErr) {
	for name := range vc.models {
		m := vc.models[name]
		if len(m.Data) == 0 {
			continue
		}
		for _, d := range m.Data {
			if !dirExists(filepath.Join(m.SourceDir, d.Src)) {
				e.Add("candy %s: data src %q does not exist or is not a directory", name, d.Src)
			}
		}
	}

	for imgName := range vc.boxes {
		box := vc.boxes[imgName]
		resolved, err := deploykit.ResolveCandyOrder(box.Candy, vc.dk, nil)
		if err != nil {
			continue
		}
		volumeNames := make(map[string]bool)
		for _, candyName := range resolved {
			m, ok := vc.models[candyName]
			if !ok {
				continue
			}
			for _, vol := range m.Volumes {
				volumeNames[vol.Name] = true
			}
		}
		hasData := false
		for _, candyName := range resolved {
			m, ok := vc.models[candyName]
			if !ok || len(m.Data) == 0 {
				continue
			}
			hasData = true
			for _, d := range m.Data {
				if !volumeNames[d.Volume] {
					e.Add("box %s: candy %s data references volume %q which is not declared by any candy in the box", imgName, candyName, d.Volume)
				}
			}
		}
		if box.DataImage {
			if box.Base != "" {
				e.Add("box %s: data_image cannot specify base (always FROM scratch)", imgName)
			}
			if !hasData {
				e.Add("box %s: data_image has no candies with data declarations", imgName)
			}
			for _, candyName := range resolved {
				m, ok := vc.models[candyName]
				if !ok {
					continue
				}
				if len(m.Service) > 0 {
					e.Add("box %s: data_image includes candy %s which has service: declarations", imgName, candyName)
				}
				if len(vc.views[candyName].Ports) > 0 {
					e.Add("box %s: data_image includes candy %s which has port declarations", imgName, candyName)
				}
				if len(m.PortRelayPorts) > 0 {
					e.Add("box %s: data_image includes candy %s which has port_relay declarations", imgName, candyName)
				}
			}
		}
	}
}

// validateInitDependencies checks that a box using an init system carries the init's required
// dependency candy in its resolved chain (or base chain). The per-init detection + the preserve_user
// capability gate are recomputed from the envelope (env.Init vocabulary + per-candy service/candy_file
// + the per-candy preserve_user aggregate).
func validateInitDependencies(vc *vctx, e *vErr) {
	if vc.env == nil || len(vc.env.Init) == 0 {
		return
	}
	initCfg := vc.env.Init
	for imgName := range vc.boxes {
		box := vc.boxes[imgName]
		resolved, err := deploykit.ResolveCandyOrder(box.Candy, vc.dk, nil)
		if err != nil {
			continue
		}
		// The only capability the embedded init vocabulary gates on is preserve_user (the one cap the
		// envelope carries). initDefRequirementsMet reads this provided set.
		preserveUser := boxPreserveUser(vc, resolved)
		provided := map[string]bool{}
		if preserveUser {
			provided["preserve_user"] = true
		}
		isBootcFlavored := preserveUser

		for initName, def := range initCfg {
			if def == nil || def.DependsCandy == "" {
				continue
			}
			if !initDefRequirementsMet(def, provided) {
				continue
			}
			if checkInitSystemRequirements(vc, def, isBootcFlavored, resolved) {
				continue
			}
			needsInit := collectInitSystemNeeds(vc, initName, def, resolved)
			if len(needsInit) == 0 {
				continue
			}
			hasDepCandy := false
			for _, candyName := range resolved {
				if m, ok := vc.models[candyName]; ok && m.Name == def.DependsCandy {
					hasDepCandy = true
					break
				}
			}
			if !hasDepCandy {
				// Base-chain check over the resolved box set (no namespace enrichment — a namespaced
				// base outside vc.bk is skipped, consistent with the DAG-cycle-only scope).
				allCandies := deploykit.CollectAllBoxCandies(imgName, vc.bk, vc.dk)
				if slices.Contains(allCandies, def.DependsCandy) {
					hasDepCandy = true
				}
			}
			if !hasDepCandy && !checkDualInitFallback(vc, initName, resolved, initCfg) {
				e.Add("box %q has candies requiring %s (%s) but missing the %q candy in its dependency chain; add %q to the box's candies or a base image",
					imgName, initName, strings.Join(needsInit, ", "), def.DependsCandy, def.DependsCandy)
			}
		}
	}
}

// boxPreserveUser is the per-box preserve_user aggregate: an OR of each named candy's
// CandyView.Capabilities.PreserveUser (order-independent), replacing AggregateCandyCapabilities.
func boxPreserveUser(vc *vctx, order []string) bool {
	for _, n := range order {
		if v, ok := vc.views[deploykit.BareRef(n)]; ok && v.Capabilities != nil && v.Capabilities.PreserveUser {
			return true
		}
	}
	return false
}

// initDefRequirementsMet reports whether the init def's RequiresCapability are all in provided.
func initDefRequirementsMet(def *spec.ResolvedInit, provided map[string]bool) bool {
	if def == nil || len(def.RequiresCapability) == 0 {
		return true
	}
	for _, req := range def.RequiresCapability {
		if !provided[req] {
			return false
		}
	}
	return true
}

// checkInitSystemRequirements skips the supervisord depends_candy check on a bootc-flavored composition
// when systemd is also triggered by a resolved candy.
func checkInitSystemRequirements(vc *vctx, def *spec.ResolvedInit, isBootcFlavored bool, resolved []string) bool {
	if len(def.RequiresCapability) == 0 && isBootcFlavored {
		systemdDef := vc.env.Init["systemd"]
		for _, candyName := range resolved {
			if m, ok := vc.models[candyName]; ok && candyHasInit(m, systemdDef) {
				return true
			}
		}
	}
	return false
}

// collectInitSystemNeeds lists the resolved candies that require the given init system (directly, or
// via a port_relay when the init def carries a relay template).
func collectInitSystemNeeds(vc *vctx, initName string, def *spec.ResolvedInit, resolved []string) []string {
	var needsInit []string
	for _, candyName := range resolved {
		m, ok := vc.models[candyName]
		if !ok {
			continue
		}
		if candyHasInit(m, def) {
			needsInit = append(needsInit, candyName+" ("+initName+")")
		}
		if deploykit.InitHasRelayTemplate(def) && len(m.PortRelayPorts) > 0 {
			needsInit = append(needsInit, candyName+" (port_relay)")
		}
	}
	return needsInit
}

// checkDualInitFallback reports whether ALL candies triggering initName also support an alternative
// init system (dual-init candies), in which case a missing depends_candy is not an error.
func checkDualInitFallback(vc *vctx, initName string, resolved []string, initCfg map[string]*spec.ResolvedInit) bool {
	allDualInit := true
	for _, candyName := range resolved {
		m, ok := vc.models[candyName]
		if !ok || !candyHasInit(m, initCfg[initName]) {
			continue
		}
		hasAlternativeInit := false
		for altName, altDef := range initCfg {
			if altName != initName && candyHasInit(m, altDef) {
				hasAlternativeInit = true
				break
			}
		}
		if !hasAlternativeInit {
			allDualInit = false
			break
		}
	}
	return allDualInit
}

// candyHasInit recomputes PopulateCandyInitSystem's per-init detection from the envelope: the init def
// participates in service-schema detection and the candy has a matching service entry (packaged unit
// when SupportsPackaged, custom exec when ServiceTemplate!=""), OR the candy has a file matching one of
// the init def's candy_files (live glob against SourceDir).
func candyHasInit(m spec.CandyModel, def *spec.ResolvedInit) bool {
	if def == nil {
		return false
	}
	if slices.Contains(def.CandyFields, "service") {
		for i := range m.Service {
			entry := &m.Service[i]
			if entry.IsPackaged() {
				if def.ServiceSchema != nil && def.ServiceSchema.SupportsPackaged {
					return true
				}
			} else if def.ServiceSchema != nil && def.ServiceSchema.ServiceTemplate != "" {
				return true
			}
		}
	}
	for _, pattern := range def.CandyFiles {
		matches, _ := filepath.Glob(filepath.Join(m.SourceDir, pattern))
		if len(matches) > 0 {
			return true
		}
	}
	return false
}
