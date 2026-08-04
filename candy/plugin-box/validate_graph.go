package box

// validate_graph.go — the B-GRAPH resolution-graph rules + the CAPABILITIES rules (task #60, Unit C).
// The graph rules RE-RUN the deploykit resolution functions (ResolveBoxOrder / GlobalCandyOrder /
// ResolveCandyOrder) over the envelope adapters (vc.bk buildkit.ResolvedBox +
// vc.dk deploykit.CandyModel) — the SAME functions charly box build/generate use — to catch cycles +
// missing-builder/engine/data/port-relay invariants. Namespace re-resolution is NOT redone here
// (the projector already resolved namespaces host-side; an unresolvable namespaced base is a host
// diagnostic), so this is DAG-cycle detection over the already-resolved box set, per the Unit-C scoping
// NOTE. The capabilities rules read the per-candy preserve_user off the envelope (ruling a).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
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
		resolved := spec.BuilderMap(box.Builder)
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

// The init `depends_candy:` presence check that used to live here is GONE: a box can no longer be
// missing its init's dependency candy, because deploykit.InjectInitDependsCandy now ADDS the active
// init's candy to the box's AUTHORED composition (cfg) ahead of box resolution, at all three
// composition chokepoints (the build path, this envelope's own fresh-box projection, and the
// namespaced set) — so by the time any rule here reads box.Candy, the candy is already there.
// The check was also unable to catch the case that actually broke deploys: it waived
// itself (via a dual-init fallback) for exactly the mixed `use_packaged:`/`exec:` service shape the
// layer skill recommends everywhere, so a box composing only e.g. sshd validated and built clean and
// then failed at `[start]` with no supervisord installed.

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
