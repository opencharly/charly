package box

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// inspect_list.go — the `charly box inspect` + `charly box list` handlers, relocated OUT of charly
// core (K5, Collection A). Both are DATA PROJECTIONS over the generic spec.ResolvedProject envelope
// the host resolves once and ships over the reverse channel (HostBuild("resolved-project")): the
// plugin never loads the project itself (pre-K1). The two overlay-only inspect formats
// (tunnel/bind_mounts) + the store-live `list tags` are the M-core residue the plugin reaches via a
// thin HostBuild("cli") reentry to a retained hidden core command.

// resolvedProject fetches the whole resolved-project envelope for the current project dir. Dir is
// passed explicitly (mirrors dispatchGenerate) though the compiled-in plugin shares charly's cwd; the
// host resolves an absent project to an EMPTY envelope (project-less dirs list nothing, exit 0).
func (h *hostClient) resolvedProject(includeDisabled bool) (*spec.ResolvedProject, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	reqJSON, err := json.Marshal(spec.ResolvedProjectRequest{Dir: dir, IncludeDisabled: includeDisabled})
	if err != nil {
		return nil, err
	}
	resJSON, err := h.exec.HostBuild(h.ctx, "resolved-project", reqJSON)
	if err != nil {
		return nil, err
	}
	var rp spec.ResolvedProject
	if uerr := json.Unmarshal(resJSON, &rp); uerr != nil {
		return nil, fmt.Errorf("box: decode resolved-project: %w", uerr)
	}
	return &rp, nil
}

// resolveStatus returns the effective status string (empty defaults to "testing"). The pure helper
// re-implemented in the plugin (formerly charly/generate.go resolveStatus) so the plugin owns its own
// status rendering with ZERO core import.
func resolveStatus(s string) string {
	if s == "" {
		return "testing"
	}
	return s
}

// sortedKeys returns a map's string keys in sorted order — deterministic list/inspect output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- box inspect ---

// inspectGrammar is the `charly box inspect <box> [--format X] [-i instance] [--include-disabled]`
// CLI surface (formerly the core InspectCmd).
type inspectGrammar struct {
	Box             string `arg:"" help:"Box name"`
	Format          string `long:"format" help:"Output a single field instead of full JSON"`
	Instance        string `short:"i" long:"instance" help:"Instance name"`
	IncludeDisabled bool   `long:"include-disabled" help:"Operate on boxes with enabled: false (does not modify charly.yml)"`
}

// dispatchInspect prints a box's resolved configuration from the resolved-project envelope. The
// DEFAULT (no --format) marshals the ResolvedBoxView as snake_case+omitempty JSON — a DELIBERATE
// breaking output change from the old mixed-case json.Marshal(*ResolvedBox) (S-K5 ruling; golden-
// tested). Scalar + box-aggregate --formats read the view's fields; tunnel/bind_mounts reenter the
// hidden core overlay command (deploy-overlay state the envelope does not carry).
func dispatchInspect(hc *hostClient, args []string) error {
	var g inspectGrammar
	if done, err := parseLeaf("inspect", &g, args); err != nil || done {
		return err
	}

	rp, err := hc.resolvedProject(g.IncludeDisabled)
	if err != nil {
		return err
	}
	view, ok := rp.Boxes[g.Box]
	if !ok {
		return fmt.Errorf("box %q not found in charly.yml", g.Box)
	}

	// tunnel/bind_mounts read the DEPLOY OVERLAY (charly.yml), not the build-mode envelope. The
	// deploy-overlay volume/tunnel state is a pure sdk read (deploykit.LoadDeployConfigForRead); the
	// tunnel resolution's published-port set is the projector-filled box-aggregate view.Ports
	// (deploykit.ResolveTunnelConfig ignores the candy graph), so no host reentry / project reload is
	// needed — the former hidden __box-inspect-overlay core command is DELETED (K5 seam-death).
	switch g.Format {
	case "bind_mounts":
		return inspectBindMounts(g.Box, g.Instance)
	case "tunnel":
		return inspectTunnel(g.Box, g.Instance, view.Ports)
	}

	if g.Format == "" {
		data, err := json.MarshalIndent(view, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	return printInspectFormat(view, g.Format)
}

// inspectBindMounts prints the DEPLOY-OVERLAY (charly.yml) bind-mount config for a box — a pure sdk
// read of the deploy state (no build-mode envelope). Ported byte-identically from the former core
// InspectOverlayCmd (K5 seam-death — the hidden __box-inspect-overlay reentry is DELETED).
func inspectBindMounts(box, instance string) error {
	if overlay, ok := deploykit.LoadDeployConfigForRead("charly box inspect bind_mounts").Lookup(box, instance); ok {
		for _, dv := range overlay.Volume {
			fmt.Printf("%s\t%s\t%s\t%s\n", dv.Name, dv.Host, dv.Path, dv.Type)
		}
	}
	return nil
}

// inspectTunnel prints the DEPLOY-OVERLAY (charly.yml) tunnel config for a box. The tunnel resolves
// off the deploy overlay's Tunnel spec + the box-aggregate published-port set (boxPorts, the
// projector-filled view.Ports — deploykit.ResolveTunnelConfig ignores the candy-reader/candy-list
// args, so nil is passed). Ported byte-identically from the former core InspectOverlayCmd.
func inspectTunnel(box, instance string, boxPorts []string) error {
	overlay, ok := deploykit.LoadDeployConfigForRead("charly box inspect tunnel").Lookup(box, instance)
	if !ok || overlay.Tunnel == nil {
		return nil
	}
	tc := deploykit.ResolveTunnelConfig(overlay.Tunnel, box, "", nil, nil, map[string]string{}, boxPorts)
	if tc == nil || len(tc.Ports) == 0 {
		return nil
	}
	fmt.Println("PORT\tACCESS\tPROTOCOL\tHOSTNAME")
	for _, tp := range tc.Ports {
		access := "private"
		if tp.Public {
			access = "public"
		}
		hostname := tp.Hostname
		if hostname == "" {
			hostname = "-"
		}
		fmt.Printf("%d\t%s\t%s\t%s\n", tp.Port, access, tp.Protocol, hostname)
	}
	return nil
}

// printInspectFormat renders a single --format field from the resolved box view. Scalar fields print
// verbatim; list fields print one entry per line; the box-AGGREGATE fields (ports/volumes/aliases/
// engine) read the projector-filled aggregates; status defaults "" → "testing"; engine "" → the
// "(global default)" sentinel (the fallback the old command body applied).
func printInspectFormat(view spec.ResolvedBoxView, format string) error {
	switch format {
	case "tag":
		fmt.Println(view.FullTag)
	case "base":
		fmt.Println(view.Base)
	case "builder":
		for _, typ := range sortedKeys(view.Builder) {
			fmt.Printf("%s: %s\n", typ, view.Builder[typ])
		}
	case "builds":
		for _, b := range view.BuilderCapabilities {
			fmt.Println(b)
		}
	case "build":
		for _, b := range view.BuildFormats {
			fmt.Println(b)
		}
	case "distro":
		for _, d := range view.Distro {
			fmt.Println(d)
		}
	case "pkg":
		fmt.Println(view.Pkg)
	case "registry":
		fmt.Println(view.Registry)
	case "platforms":
		for _, p := range view.Platforms {
			fmt.Println(p)
		}
	case "candy":
		for _, l := range view.Candy {
			fmt.Println(l)
		}
	case "network":
		fmt.Println(view.Network)
	case "version":
		fmt.Println(view.Version)
	case "status":
		fmt.Println(resolveStatus(view.Status))
	case "info":
		fmt.Println(view.Info)
	case "ports":
		for _, p := range view.Ports {
			fmt.Println(p)
		}
	case "volumes":
		for _, vol := range view.Volumes {
			fmt.Printf("%s\t%s\n", vol.VolumeName, vol.ContainerPath)
		}
	case "aliases":
		for _, a := range view.Aliases {
			fmt.Printf("%s\t%s\n", a.Name, a.Command)
		}
	case "engine":
		engine := view.Engine
		if engine == "" {
			engine = "(global default)"
		}
		fmt.Println(engine)
	default:
		return fmt.Errorf("unknown format field: %s", format)
	}
	return nil
}

// --- box list ---

// listSubcommands names the `charly box list` subcommands (for the no-subcommand usage hint).
const listSubcommands = "aliases|boxes|candies|routes|services|targets|volumes|tags"

// dispatchList routes a `charly box list <sub>` word. Every subcommand but `tags` reads the
// resolved-project envelope; `tags` queries the podman STORE (store-live) and reenters the retained
// hidden core __box-list-tags command.
func dispatchList(hc *hostClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("box list: expected a subcommand (%s)", listSubcommands)
	}
	sub, rest := args[0], args[1:]

	if sub == "tags" {
		argv := append([]string{"__box-list-tags"}, rest...)
		r, err := hc.cli(false, true, argv...)
		if err != nil {
			return err
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("box list tags failed (exit %d)", r.ExitCode)
		}
		return nil
	}

	rp, err := hc.resolvedProject(false)
	if err != nil {
		return err
	}
	switch sub {
	case "boxes":
		listBoxes(rp)
	case "candies":
		listCandies(rp)
	case "targets":
		listTargets(rp)
	case "services":
		listServices(rp)
	case "routes":
		listRoutes(rp)
	case "volumes":
		listVolumes(rp)
	case "aliases":
		listAliases(rp)
	default:
		return fmt.Errorf("box list: unknown subcommand %q (want %s)", sub, listSubcommands)
	}
	return nil
}

// listBoxes prints enabled boxes. Boxes author no status, so the effective rung is always "testing"
// (resolveStatus("")) — the effective worst-of-candy rung is computed at generate time for the label.
func listBoxes(rp *spec.ResolvedProject) {
	for _, name := range sortedKeys(rp.Boxes) {
		status := resolveStatus("")
		if status != "working" {
			fmt.Printf("%s [%s]\n", name, status)
		} else {
			fmt.Println(name)
		}
	}
}

// listCandies prints every scanned candy, annotating remote candies with their repo path and any
// non-"working" status rung.
func listCandies(rp *spec.ResolvedProject) {
	for _, name := range sortedKeys(rp.Candies) {
		c := rp.Candies[name]
		status := resolveStatus(c.Status)
		var tags []string
		if c.Remote {
			tags = append(tags, c.RepoPath)
		}
		if status != "working" {
			tags = append(tags, status)
		}
		if len(tags) > 0 {
			fmt.Printf("%s [%s]\n", name, strings.Join(tags, ", "))
		} else {
			fmt.Println(name)
		}
	}
}

// listTargets prints build targets in dependency order (auto-intermediates flagged `[auto]`).
func listTargets(rp *spec.ResolvedProject) {
	for _, bt := range rp.BuildTargets {
		if bt.Auto {
			fmt.Printf("%s [auto]\n", bt.Name)
		} else {
			fmt.Println(bt.Name)
		}
	}
}

// listServices prints candies that trigger any init system — the InitCandy predicate
// (HasAnyInit || PortRelayPorts>0), reconstructed from the candy view's has_init + port_relay fields.
func listServices(rp *spec.ResolvedProject) {
	for _, name := range sortedKeys(rp.Candies) {
		c := rp.Candies[name]
		if c.HasInit || len(c.PortRelayPorts) > 0 {
			fmt.Println(name)
		}
	}
}

// listRoutes prints candies that declare a route (name + host + port).
func listRoutes(rp *spec.ResolvedProject) {
	for _, name := range sortedKeys(rp.Candies) {
		c := rp.Candies[name]
		if c.Route == nil {
			continue
		}
		fmt.Printf("%s\thost=%s\tport=%s\n", name, c.Route.Host, c.Route.Port)
	}
}

// listVolumes prints each candy's declared volumes (candy name + volume name + path).
func listVolumes(rp *spec.ResolvedProject) {
	for _, name := range sortedKeys(rp.Candies) {
		for _, vol := range rp.Candies[name].Volumes {
			fmt.Printf("%s\t%s\t%s\n", name, vol.Name, vol.Path)
		}
	}
}

// listAliases prints each candy's declared aliases (candy name + alias name + command).
func listAliases(rp *spec.ResolvedProject) {
	for _, name := range sortedKeys(rp.Candies) {
		for _, a := range rp.Candies[name].Aliases {
			fmt.Printf("%s\t%s\t%s\n", name, a.Name, a.Command)
		}
	}
}
