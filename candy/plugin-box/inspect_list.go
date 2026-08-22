package box

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// inspect_list.go — the `charly box inspect` + `charly box list` handlers, relocated OUT of charly
// core (K5, Collection A). Both are DATA PROJECTIONS: inspect over the generic
// spec.ResolvedProject envelope the host resolves once and ships over the reverse channel
// (InvokeProvider("build","project") — candy/plugin-build's build:project word, #55 step3 unit
// 3b); `list tags` over the verb:retention engine's tag inventory,
// reached directly via InvokeProvider (listImageTags — #118, the former hidden-core
// __box-list-tags CLI reentry is DELETED). The plugin never loads the project itself (pre-K1) and,
// as of the list-tags move, never reenters core for `box list` either.

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
	resJSON, err := h.exec.InvokeProvider(h.ctx, "build", "project", sdk.OpResolve, reqJSON, nil, sdk.InvokeProviderOpts{})
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
	Format          string `name:"format" help:"Output a single field instead of full JSON"`
	Instance        string `short:"i" name:"instance" help:"Instance name"`
	IncludeDisabled bool   `name:"include-disabled" help:"Operate on boxes with enabled: false (does not modify charly.yml)"`
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
	// deploy-overlay volume/tunnel state is a pure sdk read (loaderkit.LoadHostFleetConfigViaExecutor,
	// the cycle-free plugin-side overlay read); the tunnel resolution's published-port set is the
	// projector-filled box-aggregate view.Ports (deploykit.TunnelConfigFromMetadata resolves off the
	// overlay Tunnel + that port set, no candy graph), so no host reentry / project reload is needed
	// — the former hidden __box-inspect-overlay core command is DELETED (K5 seam-death).
	switch g.Format {
	case "bind_mounts":
		return inspectBindMounts(hc.ctx, hc.exec, g.Box, g.Instance)
	case "tunnel":
		return inspectTunnel(hc.ctx, hc.exec, g.Box, g.Instance, view.Ports)
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
func inspectBindMounts(ctx context.Context, ex *sdk.Executor, box, instance string) error {
	if dc, derr := loaderkit.LoadHostFleetConfigViaExecutor(ctx, ex); derr == nil && dc != nil {
		if overlay, ok := dc.Lookup(box, instance); ok {
			for _, dv := range overlay.Volume {
				fmt.Printf("%s\t%s\t%s\t%s\n", dv.Name, dv.Host, dv.Path, dv.Type)
			}
		}
	}
	return nil
}

// inspectTunnel prints the DEPLOY-OVERLAY (charly.yml) tunnel config for a box. The tunnel resolves
// off the deploy overlay's Tunnel spec + the box-aggregate published-port set (boxPorts, the
// projector-filled view.Ports) via deploykit.TunnelConfigFromMetadata (a BoxMetadata built from the
// overlay's Tunnel + that port set). Ported byte-identically from the former core InspectOverlayCmd.
func inspectTunnel(ctx context.Context, ex *sdk.Executor, box, instance string, boxPorts []string) error {
	dc, derr := loaderkit.LoadHostFleetConfigViaExecutor(ctx, ex)
	if derr != nil || dc == nil {
		return nil
	}
	overlay, ok := dc.Lookup(box, instance)
	if !ok || overlay.Tunnel == nil {
		return nil
	}
	tc := deploykit.TunnelConfigFromMetadata(&spec.BoxMetadata{
		Box:    box,
		Tunnel: overlay.Tunnel,
		Port:   boxPorts,
	})
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
// resolved-project envelope; `tags` queries the podman STORE (store-live) directly via
// verb:retention (listImageTags) — the SAME peer-dispatch pruneAfterBuild already uses in this
// module, replacing the former hidden-core __box-list-tags CLI reentry (the K5-doomed residue
// its own header predicted; charly/volume_cp_tags_cmd.go, deleted, #118).
func dispatchList(hc *hostClient, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("box list: expected a subcommand (%s)", listSubcommands)
	}
	sub, rest := args[0], args[1:]

	if sub == "tags" {
		boxFilter := ""
		if len(rest) > 0 {
			boxFilter = rest[0]
		}
		return listImageTags(hc, boxFilter)
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
// (HasAnyInit || PortRelayPorts>0), reconstructed from the candy view's init_systems +
// port_relay fields. The aggregate has_init bool is never populated by the resolution
// pipeline (PopulateCandyInitSystem fills the per-init-system init_systems map; the
// aggregate folds into HasContent, not HasInit), so the predicate reads init_systems —
// non-empty means at least one init system (supervisord/systemd) triggers for this candy.
func listServices(rp *spec.ResolvedProject) {
	for _, name := range sortedKeys(rp.Candies) {
		c := rp.Candies[name]
		if len(c.InitSystems) > 0 || len(c.PortRelayPorts) > 0 {
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

// listImageTags serves `charly box list tags` — the store-live tag inventory
// (charly-labeled podman image tags). Reaches verb:retention directly over InvokeProvider (the
// SAME peer-dispatch pruneAfterBuild already uses in this module, box.go), replacing the former
// hidden-core __box-list-tags CLI reentry: this is a data projection over the SAME retention
// engine, not a distinct core Mechanism, so it needs no reentry at all. boxFilter narrows to one
// box short name ("" = every box).
func listImageTags(hc *hostClient, boxFilter string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	reqJSON, err := json.Marshal(spec.RetentionRequest{Dir: dir, List: true})
	if err != nil {
		return err
	}
	resJSON, err := hc.exec.InvokeProvider(hc.ctx, "verb", "retention", sdk.OpRun, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return err
	}
	var reply spec.RetentionReply
	if len(resJSON) > 0 {
		if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
			return uerr
		}
	}
	if reply.Error != "" {
		return fmt.Errorf("%s", reply.Error)
	}
	return printImageTags(reply.TagGroups, boxFilter)
}

// printImageTags formats + prints the tag inventory, newest-first per box — byte-equivalent to
// the former charly/volume_cp_tags_cmd.go's ListTagsCmd.Run() output. Split out from
// listImageTags for unit testability (no executor needed).
func printImageTags(tags []spec.TagInfo, boxFilter string) error {
	byBox := map[string][]int{}
	for i, t := range tags {
		if boxFilter != "" && t.Box != boxFilter {
			continue
		}
		byBox[t.Box] = append(byBox[t.Box], i)
	}
	if len(byBox) == 0 {
		if boxFilter != "" {
			return fmt.Errorf("no locally stored charly images for box %s", boxFilter)
		}
		return fmt.Errorf("no locally stored charly images")
	}
	boxes := make([]string, 0, len(byBox))
	for b := range byBox {
		boxes = append(boxes, b)
	}
	sort.Strings(boxes)
	for _, b := range boxes {
		for _, i := range byBox[b] {
			t := tags[i]
			inUse := ""
			if t.InUse {
				inUse = "\t(in use)"
			}
			fmt.Printf("%s\t%s\t%s%s\n", t.Box, t.Ref, t.Version, inUse)
		}
	}
	return nil
}
