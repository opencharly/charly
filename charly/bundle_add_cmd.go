package main

// deploy_add_cmd.go — `charly bundle add <name> [<ref>]` and
// `charly bundle del <name>`. Generic wiring on top of the unified deploy
// targets: this file does ref resolution, plan compilation, deployID
// stamping, and dry-run printing, then routes through ResolveTarget →
// target.Add / target.Del. There is NO per-kind dispatch switch — every
// kind-specific construction + deploy lives behind its UnifiedDeployTarget
// adapter (unified_targets_*.go), which consumes the dispatch-merged node
// from the DeployContext (never re-reading it from disk).
//
// Name semantics:
//   - literal "host" → deploy to the local machine (target: local)
//   - any other name → a named container deployment (target: pod), or
//     whatever target: the resolved charly.yml node declares.
//
// P13-KERNEL walk-port precision note (corrects an over-broad "drives from
// the plugin" claim in the walk-port commit message/CHANGELOG): the
// pre-order tree WALK itself (the loop deciding traversal order across
// nested nodes) moved to candy/plugin-bundle/walk.go. The per-node TERMINAL
// orchestration — dispatchNode below, plus its compile-selection helpers
// (resolveNodeOverlays/loadConfigForDeploy/compileNodePlans) and the final
// ResolveTarget dispatch anchor — did NOT move; it remains fully host-side,
// reached from the plugin behind the ONE coarse
// HostBuild("deploy-node-dispatch") seam (host_build_deploy_node_dispatch.go).
// This is genuinely-tracked residue, not an oversight: loadConfigForDeploy
// calls LoadConfig → LoadUnified, so the compile-selection half is K1-blocked
// (the K1-blocked family register entry covers it); the ResolveTarget
// dispatch anchor is registry-coupled the same way every other terminal
// dispatch point is. Further seam decomposition — splitting dispatchNode's
// body into narrower host-builders the way the pod-config direction-flip did
// for BoxConfigSetupCmd/BoxConfigRemoveCmd — is a FLOOR-SLIM/#118-
// reconciliation candidate, not part of this wave's contract.

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// deployAddCmd is the host-side orchestration for `charly bundle add <name> [<ref>]`.
// The CLI GRAMMAR moved to the command:bundle plugin (candy/plugin-bundle); this struct
// is reconstructed from spec.DeployAddRequest by the deploy-add host-build seam and its
// Run() logic runs VERBATIM in charly's own process.
type deployAddCmd struct {
	Name string
	Ref  string

	// Candy overlays (repeatable).
	AddCandy []string

	// Plan-level flags.
	Tag      string
	DryRun   bool
	NodeOnly bool
	Format   string
	Pull     bool
	Verify   bool

	// Host-only gates.
	WithServices     bool
	AllowRepoChanges bool
	AllowRootTasks   bool
	SkipIncompatible bool
	BuilderImage     string
	AssumeYes        bool

	// Disposable + lifecycle classification (see /charly-internals:disposable).
	// --disposable writes `disposable: true` into the charly.yml
	// entry and authorizes autonomous `charly update`. --lifecycle writes
	// the informational tier tag; it has NO effect on disposability
	// (no derivation).
	Disposable bool
	Lifecycle  string

	// vmEntity is the resolved kind:vm entity name this deploy targets,
	// populated per-node by dispatchNode from the node's `vm:` cross-ref
	// (kind:check beds + charly.yml target:vm entries) OR the "vm:<name>"
	// deploy-key prefix (the CLI `charly bundle add vm:<name>` form). The candy
	// compiler reads it to build plans against the GUEST's distro/format
	// (apt/dnf), not the operator host's. Host-derived during dispatch.
	vmEntity string

	// builderImageOverride is this deploy's effective builder-image override —
	// opts.BuilderImageOverride, i.e. --builder-image (CLI) with
	// install_opts.builder_image (deployment / template) merged beneath it — captured
	// per-node before compileNodePlans so the deploy compile methods can seed
	// hostCtx.BuilderImage (compileHostContext). Without it a kind:local / vm deploy
	// whose synthetic box carries no builder map entry for a candy's detection builder
	// (npm/pixi/cargo/aur) leaves the compiled BuilderStep.BuilderImage EMPTY; the
	// install_opts.builder_image reached only EmitOpts at APPLY, which does NOT cross
	// into the out-of-process local/vm deploy walk, so builderStepImage there failed
	// "no builder image for <builder>". Seeding it at compile makes the image travel IN
	// the step view (step_view.go round-trips BuilderImage) to the out-of-process walk.
	// Mirrors the vmEntity per-node field. Host-derived during dispatch.
	builderImageOverride string
}

// deployDelCmd is the host-side orchestration for `charly bundle del <name>`.
// The CLI GRAMMAR moved to the command:bundle plugin (candy/plugin-bundle); this struct
// is reconstructed from spec.DeployDelRequest by the deploy-del host-build seam.
type deployDelCmd struct {
	Name string

	AssumeYes       bool
	KeepRepoChanges bool
	KeepServices    bool
	KeepImage       bool
	DryRun          bool

	// Runner routes reverse ops to the right privilege context. It is
	// carried onto the resolved the local deploy target by Run before Del. Nil
	// falls back to the local-exec path in reverse_ops.go. Set programmatically
	// by host-side teardown callers, never authored on the CLI.
	Runner kit.ReverseRunner
}

// deployDelCmd satisfies kit.ReverseExecutor via thin wrappers — keeps the
// flag-accessor protocol decoupled from the concrete command type.
func (c *deployDelCmd) ReverseDryRun() bool              { return c.DryRun }
func (c *deployDelCmd) ReverseKeepRepoChanges() bool     { return c.KeepRepoChanges }
func (c *deployDelCmd) ReverseKeepServices() bool        { return c.KeepServices }
func (c *deployDelCmd) ReverseRunner() kit.ReverseRunner { return c.Runner }

// deployDelArgv returns the argv (everything AFTER the charly binary) for a
// non-interactive `charly bundle del <name>`: the verb, the name, and the ONE valid
// skip-confirmation flag. Every programmatic teardown builds its command through
// this single helper — in-process (runCharlySubcommand), out-of-process
// (exec.Command), and the systemd-run TTL timer — so the flag can never drift
// across call sites again.
//
// The flag is `--assume-yes`, NOT `--yes`/`--force`: the command:bundle plugin's
// `charly bundle del` Kong grammar (candy/plugin-bundle) renders its AssumeYes field
// as --assume-yes because Kong derives the long name from the FIELD (the `long:"yes"`
// tag is a Kong no-op in the separate-tag form), with `-y` as the short form. A
// `--yes`/`--force` drift — neither of which Kong accepts — once aborted teardown at
// arg-parse and silently leaked the resource (see CHANGELOG/); the deploy-del-flag
// regression test guards this.
func deployDelArgv(name string) []string {
	return []string{"bundle", "del", name, "--assume-yes"}
}

// dispatchNode compiles plans for a single node and runs the
// appropriate target. Factored out of Run so the tree walker can call
// it once per node.
//
// path is the dotted identifier ("", "openclaw-stack", or
// "openclaw-stack.web.db"). It's propagated via opts.Path so the
// target's logging can identify which node is executing.
//
// node is the resolved BundleNode; nil when the caller provided
// an explicit ref (Ref != "") with no matching charly.yml entry.
//
// parentExec is the DeployExecutor of the enclosing environment; nil
// at the root. Non-nil means "this node is a child of something" —
// its target composes a NestedExecutor over parentExec.
func (c *deployAddCmd) dispatchNode(path string, node *spec.BundleNode, parentExec deploykit.DeployExecutor, dir string) error {
	opts, refStr, addCandies, tag, err := c.resolveNodeOverlays(path, node, parentExec)
	if err != nil {
		return err
	}

	cfg, distroCfg, builderCfg, err := loadConfigForDeploy(dir)
	if err != nil {
		return err
	}

	target := classifyNodeTarget(node, path)

	// Resolve the kind:vm entity this node targets (if any) so the candy
	// compiler builds plans against the GUEST's distro/format (apt/dnf on
	// debian/fedora) rather than the operator host's (cachyos→pac). The
	// `vm:` deploy-key prefix was the ONLY signal before — it missed every
	// kind:check bed and charly.yml target:vm entry whose name isn't
	// "vm:"-prefixed, routing them through syntheticHostBox → pacman.
	c.vmEntity = resolveVmEntity(c.Name, node)

	// Resolve a kind:local template, when referenced. Template fields
	// (candies + install_opts + env) merge BENEATH deployment-level
	// overrides — so the precedence is CLI > deployment > template.
	addCandies, opts, err = resolveNodeTemplate(target, path, dir, node, addCandies, opts)
	if err != nil {
		return err
	}

	// Capture the deploy's effective builder-image override (CLI --builder-image
	// over install_opts.builder_image, already merged in opts) so the compile
	// methods seed hostCtx.BuilderImage — see the builderImageOverride field.
	c.builderImageOverride = opts.BuilderImageOverride

	plans, base, candySet, err := c.compileNodePlans(target, refStr, tag, path, addCandies, cfg, distroCfg, builderCfg, dir)
	if err != nil {
		return err
	}

	deployID := deploykit.ComputeDeployID(base, candySet, addCandies)
	for _, p := range plans {
		p.DeployID = deployID
		// Union — don't clobber. The per-alPlan propagation loop above
		// already populated p.AddCandies with the overlay-candy names
		// (explicit add_candy + their transitive deps). Plain overwrite
		// with the user-facing addCandies list drops the transitive
		// entries, so (e.g.) an overlay declaring add_candy:[k3s-server]
		// would ship k3s-server but not its k3s base candy — runtime
		// failure.
		seen := make(map[string]bool, len(p.AddCandies))
		for _, al := range p.AddCandies {
			seen[al] = true
		}
		for _, al := range addCandies {
			if !seen[al] {
				p.AddCandies = append(p.AddCandies, al)
				seen[al] = true
			}
		}
	}

	if c.DryRun {
		return c.printPlans(plans, opts)
	}

	// UNIFIED dispatch — every kind routes through ResolveTarget → the
	// adapter's Add. There is no per-kind switch; the kind-specific
	// construction + deploy lives behind each adapter's Add (which
	// consumes the dispatch-merged node from dctx, never re-reading it
	// from disk). classifyNodeTarget already normalized the legacy
	// "container"/"kubernetes"/"host" spellings to canonical values.
	//
	// The deploy KEY is the node's identity. For a top-level deploy
	// that's c.Name; for a nested node it's the dotted path. Adapters
	// resolve any kind-specific name (the vm entity, the flattened pod
	// container name) from that + the node.
	deployName := c.Name
	if path != "" {
		deployName = path
	}

	// ResolveTarget needs a node carrying target:. For a ref-based deploy
	// with no charly.yml entry (node == nil), synthesize one from the
	// classified target so `charly bundle add host ./x.yml` still resolves.
	resolveNode := node
	if resolveNode == nil {
		resolveNode = &spec.BundleNode{Target: target}
	}

	utgt, err := ResolveTarget(resolveNode, deployName)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	if tt, ok := utgt.(*externalDeployTarget); ok {
		tt.nodeOnly = c.NodeOnly
	}

	dctx := &DeployContext{
		Node:       node,
		Name:       deployName,
		Dir:        dir,
		Cfg:        cfg,
		DistroCfg:  distroCfg,
		BuilderCfg: builderCfg,
		Base:       base,
	}

	return utgt.Add(context.Background(), dctx, plans, opts)
}

// resolveNodeOverlays computes the per-node emit opts, ref string, add-candy
// list and tag, applying the charly.yml entry's field overlays on top of the
// CLI flags. On the root this matches the pre-v2 behavior; on children the
// fields come from the child node (not c.Name's top-level entry). Returns an
// error only when neither a <ref> nor a charly.yml entry resolves a ref.
func (c *deployAddCmd) resolveNodeOverlays(path string, node *spec.BundleNode, parentExec deploykit.DeployExecutor) (deploykit.EmitOpts, string, []string, string, error) {
	opts := c.emitOpts()
	opts.ParentExec = parentExec
	opts.Path = path
	// Note: opts.ParentNode is populated by the walker when available.

	refStr := c.Ref
	addCandies := append([]string(nil), c.AddCandy...)
	tag := c.Tag
	if node != nil {
		if node.Version != "" {
			tag = node.Version
		} else if tag != "" {
			// Propagate an explicit --tag onto the in-memory node so downstream
			// resolvers that read node.Version (the k8s preresolver, the pod overlay
			// build) pin the EXACT tag rather than re-resolving the short name by a
			// newest-local-CalVer sort — the bed-scoped per-run tag #75, uniform with
			// the plain-pod kit.ResolveShellImageRef(c.Tag) path.
			node.Version = tag
		}
		if node.InstallOpts != nil {
			opts = deploykit.InstallOptsApplyTo(node.InstallOpts, opts)
		}
		if len(addCandies) == 0 && len(node.AddCandy) > 0 {
			addCandies = append([]string(nil), node.AddCandy...)
		}
	}
	if refStr == "" {
		if node == nil {
			return opts, "", addCandies, tag, fmt.Errorf("charly bundle add: no <ref> and charly.yml has no entry for %q", path)
		}
		// Schema v3: prefer the explicit `box:` cross-ref when set,
		// so deployment names like "sway-pod" don't need to match a
		// box name. Falls back to the deploy key for legacy entries.
		switch {
		case node.Image != "":
			refStr = node.Image
		default:
			refStr = pathLeaf(path)
		}
	}
	return opts, refStr, addCandies, tag, nil
}

// resolveNodeTemplate merges a referenced kind:local template into addCandies
// and opts. Template fields merge BENEATH deployment-level overrides — the
// precedence is CLI > deployment > template — because InstallOptsConfig.ApplyTo
// is fill-empty, so applying the template's opts after the deployment's leaves
// the deployment's values intact and only fills the gaps.
func resolveNodeTemplate(target, path, dir string, node *spec.BundleNode, addCandies []string, opts deploykit.EmitOpts) ([]string, deploykit.EmitOpts, error) {
	if target == "local" && node != nil && node.From != "" {
		tmpl, ferr := findLocalSpec(dir, node.From)
		if ferr != nil {
			return addCandies, opts, fmt.Errorf("deployment %q: resolving kind:local template %q: %w", path, node.From, ferr)
		}
		if tmpl == nil {
			return addCandies, opts, fmt.Errorf("deployment %q: unknown kind:local template %q", path, node.From)
		}
		// Prepend template candies; deployment add_candy are appended.
		merged := append([]string(nil), tmpl.Candy...)
		merged = append(merged, addCandies...)
		addCandies = merged
		// Fill install_opts gaps from the template.
		opts = deploykit.InstallOptsApplyTo(tmpl.InstallOpts, opts)
	}
	return addCandies, opts, nil
}

// compileNodePlans compiles the InstallPlans for a node, dispatching on the
// classified target. Target-only deploys (local, vm, android) don't compile a
// primary image plan — everything comes from add_candy (for android: the
// candies' apk: packages installed onto the device). For pod/k8s targets the
// add_candy compiles against the BASE IMAGE's context (distro=fedora, pkg=rpm,
// …) rather than the operator host's context — otherwise the candy's install
// tasks pick the wrong distro section and the overlay build fails. Returns the
// plans, the base identity, and the candy set.
func (c *deployAddCmd) compileNodePlans(target, refStr, tag, path string, addCandies []string, cfg *Config, distroCfg *buildkit.DistroConfig, builderCfg *buildkit.BuilderConfig, dir string) ([]*deploykit.InstallPlan, string, []string, error) {
	var plans []*deploykit.InstallPlan
	var base string
	var candySet []string

	if target == "local" || isExternalDeploySubstrate(target) {
		// Target-only deploys (local + every EXTERNAL deploy substrate, incl. the
		// now-externalized vm/android/k8s — all covered by isExternalDeploySubstrate)
		// compile no primary image plan — the workload is entirely add_candy: (for an
		// external substrate, the candies whose plan views/specs the host marshals to the
		// out-of-process provider). base is the deploy path identity.
		base = path
	} else {
		ref, err := ResolveDeployRef(refStr, dir)
		if err != nil {
			return nil, "", nil, fmt.Errorf("resolving ref %q: %w", refStr, err)
		}
		// Save c.Tag for the compile selection; restore after.
		savedTag := c.Tag
		c.Tag = tag
		plans, base, candySet, err = c.compileRefSelection(ref, cfg, distroCfg, builderCfg, dir)
		c.Tag = savedTag
		if err != nil {
			return nil, "", nil, err
		}
	}

	// Only host/vm targets use syntheticHostBox / syntheticVmBox (handled
	// inside compileCandySelection); pod/k8s resolve the base image context here.
	var baseImg *buildkit.ResolvedBox
	if (target == "pod" || target == "k8s") && refStr != "" {
		if baseResolved, rerr := cfg.ResolveBox(refStr, tag, dir, ResolveOpts{}); rerr == nil {
			baseImg = baseResolved
			if distroCfg != nil {
				baseImg.DistroDef = distroCfg.ResolveDistro(baseImg.Distro)
			}
			if builderCfg != nil {
				baseImg.BuilderConfig = builderCfg
			}
		}
	}
	for _, al := range addCandies {
		alRef, err := ResolveDeployRefAsCandy(al, dir)
		if err != nil {
			return nil, "", nil, fmt.Errorf("resolving --add-candy %q: %w", al, err)
		}
		var alPlans []*deploykit.InstallPlan
		if baseImg != nil {
			alPlans, _, _, err = c.compileCandySelection(alRef, cfg, distroCfg, builderCfg, dir, baseImg)
		} else {
			alPlans, _, _, err = c.compileRefSelection(alRef, cfg, distroCfg, builderCfg, dir)
		}
		if err != nil {
			return nil, "", nil, fmt.Errorf("compiling --add-candy %q: %w", al, err)
		}
		// Mark each plan's own candy (plus transitive deps) as overlay
		// candies so the Pod target picks them ALL up — not just the
		// user-facing ref name (k3s-server without its k3s base dep).
		overlayNames := make([]string, 0, len(alPlans))
		for _, p := range alPlans {
			if p.Candy != "" {
				overlayNames = append(overlayNames, p.Candy)
			}
		}
		for _, p := range alPlans {
			p.AddCandies = append(p.AddCandies, overlayNames...)
		}
		plans = append(plans, alPlans...)
	}
	return plans, base, candySet, nil
}

// classifyNodeTarget picks the target discriminator for a node. Uses
// node.Target when non-empty (canonical pod|vm|k8s|local|android, set from
// the node-form kind by bundleTargetForDisc).
//
// For ref-based deploys with no charly.yml entry (e.g. `charly bundle add
// foo ./box.yml` where foo isn't declared), the deploy name itself
// is the hint: literal `host` → host target; anything else → pod.
// The legacy `vm:<name>` name-prefix heuristic was removed — VM
// deploys are now always tree-backed with explicit target:vm.
func classifyNodeTarget(node *spec.BundleNode, path string) string {
	if node != nil && node.Target != "" {
		return node.Target
	}
	if pathLeaf(path) == "host" || pathLeaf(path) == "local" {
		return "local"
	}
	return "pod"
}

// pathLeaf returns the last segment of a dotted path. "foo.bar.baz"
// → "baz"; "foo" → "foo"; "" → "".
func pathLeaf(path string) string {
	if idx := strings.LastIndexByte(path, '.'); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// deriveChildExecutorForPath builds the child executor for a nested node:
// it supplies the current node's flattened container name (derived from the
// dotted path) for a container target, hops through vmChildExecutor for a vm
// child, and otherwise shares the parent executor.
//
// E/M/D VERIFIED (P13-KERNEL): the outer switch dispatches on
// deployTraitDescent(...).Transport — a small DECLARED closed vocabulary
// (none|container-exec|ssh|reject) every substrate provider maps itself onto, NOT a
// switch on the concrete kind word (vm/pod/local/k8s/android never appear here) — so
// this is legitimate D-data-driven dispatch, not an incomplete per-kind seam. Each case
// CONSTRUCTS a live deploykit.DeployExecutor from that transport — structurally the SAME
// shape as substrateLifecycle's already-sanctioned OpPrepareVenue->VenueDescriptor
// pattern, just for a NESTED hop instead of the root venue.
//
// K4-C WALK PORT (landed): the tree WALK now runs plugin-side (candy/plugin-bundle/walk.go).
// This function's BODY is UNCHANGED and stays host-side — it is registry-coupled
// (deployTraitDescent needs the providerRegistry) — but its CALL SITE moved: the
// deploy-node-dispatch host-builder (host_build_deploy_node_dispatch.go) re-runs it once per
// ANCESTOR, reconstructing the WHOLE parentExec chain from the ancestor path/node lists the
// plugin's walk sends, rather than the caller passing a live parentExec through directly. A
// live DeployExecutor never crosses the wire — no venue-descriptor encoding needed for this
// hop; the plugin only ever holds paths + nodes.
func deriveChildExecutorForPath(path string, node *spec.BundleNode, parentExec deploykit.DeployExecutor) (deploykit.DeployExecutor, error) {
	if node == nil {
		return parentExec, nil
	}
	if !node.HasChildren() {
		return parentExec, nil
	}
	// P9: classifyNodeTarget produces the child's substrate WORD (dispatch classification,
	// with the ref-based host/local pathLeaf fallback); the executor HOP is then selected by
	// that word's DECLARED descent transport (the same closed nesting vocabulary
	// AppendHopForFlatPath consumes), never by a second switch on the kind word.
	switch deployTraitDescent(classifyNodeTarget(node, path)).Transport {
	case "none":
		// local (host-rooted shell) + android (parent venue) share the parent venue: android
		// reaches the device via published ports / the endpoint; no executor hop.
		if parentExec != nil {
			return parentExec, nil
		}
		return kit.ShellExecutor{}, nil
	case "container-exec":
		// The podman container `charly start`/the pod lifecycle creates is
		// `charly-<flat-path>` (containerName's `charly-` prefix), so the nested
		// executor MUST target that exact name — every other NestedContainerName
		// consumer (android_deploy_cmd.go, check_venue.go, build_overlay.go)
		// prepends `charly-`; omitting it here made a nested-child deploy exec into a
		// nonexistent bare-named container (exit 125 "no such container").
		name := "charly-" + kit.NestedContainerName(path)
		engineJump := kit.JumpPodmanExec
		if node.Engine == "docker" {
			engineJump = kit.JumpDockerExec
		}
		if parentExec == nil {
			parentExec = kit.ShellExecutor{}
		}
		return &kit.NestedExecutor{
			Parent: parentExec,
			Jump:   kit.NestedJump{Kind: engineJump, Target: name},
		}, nil
	case "ssh":
		return deploykit.VmChildExecutor(parentExec, path)
	case "reject":
		return nil, fmt.Errorf("k8s targets cannot have children")
	}
	return parentExec, nil
}

// resolveDelNode resolves the BundleNode + canonical kind for a
// `charly bundle del` invocation. Precedence:
//   - literal "host" name → synthetic local node (legacy)
//   - "vm:<name>" prefix  → synthetic vm node (legacy ref-based del)
//   - charly.yml entry    → the merged node (canonical target)
//   - no entry, pod artifact present → synthetic pod node (ref-based pod del)
//   - no entry, nothing present      → "no such deployment" error
//
// The returned node always carries a non-empty Target so ResolveTarget can
// dispatch. For a ref-based pod deploy with no charly.yml entry (e.g. the entry
// was removed while the deploy is still up) the node is synthesized — but ONLY
// when a real pod artifact exists (a quadlet unit, or a live container for a
// direct-mode deploy). A mistyped/unknown name has no artifact and is rejected
// loudly, instead of being silently synthesized into a pod del that tears down
// nothing and then fails with a misleading "unknown target pod".
func (c *deployDelCmd) resolveDelNode() (*spec.BundleNode, string, error) {
	if c.Name == "host" {
		return &spec.BundleNode{Target: "local"}, "local", nil
	}
	if strings.HasPrefix(c.Name, "vm:") {
		return &spec.BundleNode{Target: "vm"}, "vm", nil
	}
	if cwd, _ := os.Getwd(); cwd != "" {
		if tree, _ := resolveTreeRoot(cwd); tree != nil {
			if node, ok := tree[c.Name]; ok && node.Target != "" {
				n := node
				return &n, n.Target, nil
			}
		}
	}
	if podDeploymentArtifactExists(c.Name) {
		return &spec.BundleNode{Target: "pod"}, "pod", nil
	}
	return nil, "", fmt.Errorf("no such deployment %q — run `charly bundle list` to see "+
		"deployments (a VM deploy is torn down as `charly bundle del vm:%s`)", c.Name, c.Name)
}

// podDeploymentArtifactExists reports whether a pod deploy named `name` has a persisted artifact on
// this host: a quadlet unit (`.container`/`.pod`, written by `charly config`/`charly start`) OR a
// live container (a direct-mode `engine.run=direct` deploy has no quadlet). It is the discriminator
// that lets a ref-based `charly bundle del <name>` with no charly.yml entry still tear a real pod
// down, while a mistyped name (no artifact) is rejected.
func podDeploymentArtifactExists(name string) bool {
	cn := kit.NestedContainerName(name)
	if dir, err := kit.QuadletDir(); err == nil {
		for _, suffix := range []string{".container", ".pod"} {
			if _, err := os.Stat(filepath.Join(dir, "charly-"+cn+suffix)); err == nil {
				return true
			}
		}
	}
	return containerExists("", "charly-"+cn)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (c *deployAddCmd) emitOpts() deploykit.EmitOpts {
	return deploykit.EmitOpts{
		DryRun:               c.DryRun,
		FormatJSON:           c.Format == "json",
		AllowRepoChanges:     c.AllowRepoChanges,
		AllowRootTasks:       c.AllowRootTasks,
		WithServices:         c.WithServices,
		SkipIncompatible:     c.SkipIncompatible,
		AssumeYes:            c.AssumeYes,
		Verify:               c.Verify,
		Pull:                 c.Pull,
		BuilderImageOverride: c.BuilderImage,
	}
}

// scanCandiesForRef scans the candy set needed to compile `ref`, returning the
// candy map plus the map KEY for ref. A LOCAL candy ref keys by its short name.
// A REMOTE ref (`@host/org/repo/candy/<name>:ver`) is fetched + scanned with
// its transitive deps — by augmenting cfg with a synthetic image that carries
// the ref, so the existing CollectRemoteRefs/ScanAllCandy machinery pulls it —
// and keys by its bare ref. This makes `charly bundle add --add-layer <remote>`
// (e.g. the VM check beds' add_candy:) fully automatic with no manual pre-fetch.
func (c *deployAddCmd) scanCandiesForRef(ref *DeployRef, cfg *Config, dir string) (map[string]spec.CandyReader, string, error) {
	scanCfg := cfg
	candyKey := ref.Name
	if ref.Source == RefSourceRemote {
		aug := *cfg
		aug.Box = make(boxMap, len(cfg.Box)+1)
		maps.Copy(aug.Box, cfg.Box)
		aug.Box["__charly_addlayer_fetch__"] = encodeBox(spec.BoxConfig{Candy: []string{ref.Raw}})
		scanCfg = &aug
		candyKey = deploykit.BareRef(ref.Raw)
	}
	layers, err := ScanAllCandyWithConfig(dir, scanCfg)
	if err != nil {
		return nil, "", err
	}
	if _, ok := layers[candyKey]; !ok {
		return nil, "", fmt.Errorf("candy %q not found", ref.Raw)
	}
	return layers, candyKey, nil
}

// pruneContainerInitForSystemd drops the `supervisord` candy (the CONTAINER
// init system) from a resolved DEPLOY candy order when the target is systemd
// (host / vm). On a systemd target the OS init is the one and only init system
// — every candy's `service:` entries render as systemd units — so pulling in
// supervisord is wrong (it lands installed-but-unused, a second init). Pod/k8s
// deploys and OCI image builds keep supervisord (it IS their init), so this
// only affects host/vm deploys. Candies that `require: supervisord` purely for
// graph ordering are unaffected at runtime — their services run under systemd
// regardless of whether the supervisord package is present.
func pruneContainerInitForSystemd(order []string, hostCtx deploykit.HostContext) []string {
	if !hostCtx.MachineVenue {
		return order
	}
	out := make([]string, 0, len(order))
	for _, n := range order {
		if n == "supervisord" {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (c *deployAddCmd) printPlans(plans []*deploykit.InstallPlan, opts deploykit.EmitOpts) error {
	if opts.FormatJSON {
		return json.NewEncoder(os.Stdout).Encode(plans)
	}
	for _, p := range plans {
		fmt.Println(deploykit.DescribePlan(p))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Small glue helpers.
// ---------------------------------------------------------------------------

// detectHostContext builds the HostContext struct used by the compiler
// for host-target deploys. Returns a zero-value struct for container
// deploys (the compiler ignores host-only fields there).
func detectHostContext() deploykit.HostContext {
	hd, _ := DetectHostDistro()
	glibc, _ := DetectHostGlibc()
	if hd == nil {
		return deploykit.HostContext{}
	}
	return deploykit.HostContext{
		MachineVenue: true,
		Distro:       hd.PrimaryTag(),
		GlibcVersion: glibc,
	}
}

// compileHostContext returns the deploy-compile HostContext: detectHostContext with
// this deploy's effective builder-image override (c.builderImageOverride —
// --builder-image / install_opts.builder_image) seeded onto BuilderImage, so
// resolveBuilderImage sets the compiled BuilderStep.BuilderImage from it (R3 — the
// SAME hostCtx.BuilderImage > img.Builder priority every compile already uses). The
// image then travels IN the step view (step_view.go round-trips BuilderImage) across
// the process boundary to the out-of-process local/vm deploy walk, where
// builderStepImage reads it — the ONLY path by which install_opts.builder_image
// reaches an out-of-process deploy's builder-step image resolution. Empty override →
// the unchanged path (resolveBuilderImage falls through to img.Builder). The ref (e.g.
// a namespaced fedora.fedora-builder) is resolved to a concrete image later by
// BuilderRun → EnsureImagePresent (builder_run.go), so it need not be a full registry
// ref.
func (c *deployAddCmd) compileHostContext() deploykit.HostContext {
	hostCtx := detectHostContext()
	if c.builderImageOverride != "" {
		hostCtx.BuilderImage = c.builderImageOverride
	}
	return hostCtx
}

// preresolveBuildersInto runs the host-side builder PRE-PASS (builder_preresolve.go) and returns
// hostCtx with BuilderContext populated, so the subsequent PURE BuildDeployPlan compile reads
// pre-resolved builder data (stage context + teardown ops) and NEVER dials a builder plugin. The
// pre-pass connects EXACTLY the externalized builder plugins the deploy's resolved closure triggers,
// on-demand + distro-gated (so a fedora deploy never connects aur), using cfg/dir to scan + load
// scoped to those words. A pre-pass error (an externalized builder whose plugin won't connect) is
// FATAL, never a silent skip (R4). Called at every BuildDeployPlan compile site so the purity
// invariant holds uniformly.
func preresolveBuildersInto(hostCtx deploykit.HostContext, cfg *Config, dir string, order []string, layers map[string]spec.CandyReader, img *buildkit.ResolvedBox) (deploykit.HostContext, error) {
	bc, err := preresolveBuilderContexts(context.Background(), cfg, dir, order, layers, img)
	if err != nil {
		return hostCtx, err
	}
	hostCtx.BuilderContext = bc
	return hostCtx, nil
}

// syntheticHostBox returns a minimal ResolvedBox suitable for
// compiling a single-candy plan against the host. Used when the user
// invokes `charly bundle add host <candy-ref>` without a containing image.
//
// UID/GID/User/Home come from the operator's own process so a candy
// task carrying `user: ${USER}` resolves to the operator (not root).
// Without this, resolveUserSpec's `${USER}` branch returns img.UID
// which would default to 0 — quietly routing the task through
// ScopeSystem (sudo), installing user-scoped tooling like
// `cargo install` to /root/.cargo/bin instead of $HOME/.cargo/bin.
func syntheticHostBox() *buildkit.ResolvedBox {
	hd, _ := DetectHostDistro()
	img := &buildkit.ResolvedBox{
		Name:         "host-adhoc",
		Home:         os.Getenv("HOME"),
		User:         os.Getenv("USER"),
		UID:          os.Getuid(),
		GID:          os.Getgid(),
		BuildFormats: []string{},
	}
	if hd != nil {
		img.Distro = append(img.Distro, hd.Tags...)
		if hint := hd.FormatHint(); hint != "" {
			img.Pkg = hint
			img.BuildFormats = []string{hint}
		}
	}
	return img
}

// resolveVmEntity returns the kind:vm entity a deploy targets, or "" when it
// targets no VM. A node's explicit `vm:` cross-ref wins (kind:check beds and
// charly.yml target:vm entries, whose names are NOT "vm:"-prefixed); otherwise
// the "vm:<name>" deploy-key prefix (the CLI `charly bundle add vm:<name>` form).
// This is the single signal the candy compiler uses to pick syntheticVmBox
// over syntheticHostBox — the prefix alone missed bed/target:vm deploys.
func resolveVmEntity(deployName string, node *spec.BundleNode) string {
	if node != nil && node.From != "" {
		return node.From
	}
	if strings.HasPrefix(deployName, "vm:") {
		if vmName, perr := vmNameFromDeployName(deployName); perr == nil {
			return vmName
		}
	}
	return ""
}

// syntheticVmBox returns a ResolvedBox tuned for `charly bundle add
// vm:<name>` — the User/UID/GID/Home fields come from the VM spec's SSH
// config (not the host's env), so `${USER}` in a candy's `user:` field
// resolves to the GUEST user (e.g. `arch`) and task scope classification
// dispatches user-scoped tasks to RunUser (bare ssh bash -s) instead of
// RunSystem (ssh sudo bash -s). Without this, `cargo install taplo-cli`
// under the pre-commit candy ends up in /root/.cargo/bin/ instead of
// /home/<user>/.cargo/bin/, and $HOME-anchored candy tests fail.
//
// The guest's distro + primary package format are resolved from the VM
// spec (NOT hardcoded), so a candy deploy onto a debian/ubuntu/fedora VM
// installs its packages — and the `charly` localpkg — through the guest's own
// package manager (apt/dnf) instead of pacman. The distro key is the
// bootstrap `distro:` field (debootstrap/pacstrap VMs) or, for cloud_image
// VMs, the base_user (cloud images name the default account after the
// distro: arch/debian/ubuntu/fedora); the format (pac/deb/rpm) comes from
// the resolved DistroDef's PrimaryFormat.
//
// Cloud-image VMs conventionally use uid/gid 1000 for the first non-root
// user (cloud-init's adopt path respects that). bootc VMs default to
// root, in which case we fall back to the same syntheticHostBox()
// semantics (System scope, no per-user path).
func syntheticVmBox(spec *VmSpec, distroCfg *buildkit.DistroConfig) *buildkit.ResolvedBox {
	user := vmshared.ResolveCloudInitSSHUser(spec)
	if user == "" || user == "root" {
		img := syntheticHostBox()
		img.Name = "vm-adhoc"
		img.User = "root"
		img.Home = "/root"
		return img
	}
	img := &buildkit.ResolvedBox{
		Name: "vm-adhoc",
		User: user,
		UID:  1000,
		GID:  1000,
		Home: "/home/" + user,
	}
	distroKey := spec.Source.Distro
	if distroKey == "" {
		distroKey = spec.Source.BaseUser
	}
	if distroKey != "" {
		if def := distroCfg.ResolveDistro([]string{distroKey}); def != nil {
			// Full most-specific-first chain (e.g. [ubuntu:24.04, ubuntu]) so a
			// target:vm deploy reaches per-version tag sections, not only the bare
			// distro tag — image/VM parity for the distro-cascade resolver. Then
			// expand inherit_packages: ancestors (a cachyos VM → [cachyos, arch]
			// so `arch:` candy blocks reach it), mirroring the image-resolve path.
			img.Distro = distroCfg.ExpandPackageInheritance(buildkit.DistroTagChain(distroKey, def.Version))
			if pf := def.PrimaryFormat(); pf != "" {
				img.Pkg = pf
				img.BuildFormats = []string{pf}
			}
		} else {
			img.Distro = []string{distroKey}
		}
	}
	return img
}

// resolveDistroDef returns the DistroDef for a given distro tag.
func resolveDistroDef(cfg *buildkit.DistroConfig, distroTag string) *spec.ResolvedDistro {
	if cfg == nil || distroTag == "" {
		return nil
	}
	return cfg.ResolveDistro([]string{distroTag})
}

// loadConfigForDeploy loads charly.yml + the embedded build vocabulary for the
// current project directory. Runs RegisterBuildVocabulary as a side effect since
// the candy scanner needs it.
func loadConfigForDeploy(dir string) (*Config, *buildkit.DistroConfig, *buildkit.BuilderConfig, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	distroCfg, builderCfg, _, err := LoadDefaultBuildConfig(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	RegisterBuildVocabulary(distroCfg)
	return cfg, distroCfg, builderCfg, nil
}

var _ = context.Background // silence "imported and not used" if future work removes the Background ref
