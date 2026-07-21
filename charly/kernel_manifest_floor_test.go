package main

// The KERNEL-MANIFEST floor gate (P16a of the core-minimization program) — the
// mechanical enforcement of CLAUDE.md's "The kernel/plugin boundary law":
// charly/'s non-test .go file set is PINNED to the CORE-FABRIC allowlist below.
// A file absent from the allowlist is an R-item (a concrete kind's
// schema / typed shape / deep validation / behaviour / produced artifact) that
// leaked into the kernel — an "incomplete seam" — and is tracked here for
// removal by its owning cutover. The kernel may keep ONLY four kind-AGNOSTIC
// things (E envelope / M mechanism / B bootstrap / D data — the four escapes
// the boundary law names), and the ONLY in-core M-mechanisms are plugin
// loading, prescan-dispatch, the kind-decode MATERIALIZE, and the wire broker;
// every other kind-blind mechanism (parse / render / resolve / walk / engine)
// is sdk-kit consumed by plugins, i.e. tracked-for-removal residue.
//
// Authored RED at program T0 as the living residue tracker: the failure output
// enumerates every residue file grouped by its owning cutover, so each
// in-flight wave (P8b, P11–P15; P15 absorbs the K1 loader-orchestration and K5
// seam-death folds) can shrink the delta. It merges LAST (P16), GREEN, after
// which core is mechanically un-growable past the fabric floor. Adding a file
// to the allowlist requires a boundary-law justification (E/M/B/D) recorded in
// the floorEntry clause in the SAME commit; a residue file that becomes fabric
// moves from residueOwner to kernelFloor; a residue file that moves to a
// plugin / is deleted simply vanishes from the directory (prune its stale
// residueOwner entry). Those two tables are the ONLY edit surface as cutovers
// land — the test logic is fixed.
//
// Documented hardware-blocked exception (operator-directed, C14 of the program
// plan — revisitable on GPU hardware): the GPU host-detection legs are listed
// in the allowlist with a GPU tag; they would fold into candy/plugin-gpu under
// P15 once GPU R10 is possible on this host (no NVIDIA GPU today).

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// floorEntry is one CORE-FABRIC file + the boundary-law clause that keeps it in
// the kernel (E envelope / M mechanism / B bootstrap / D data / GPU exception).
type floorEntry struct {
	file   string
	clause string
}

// kernelFloor is the CORE-FABRIC allowlist: every non-test .go file the kernel
// may contain, each justified by a boundary-law clause. Sorted by file.
var kernelFloor = []floorEntry{
	{"agent_target_cmd.go", "M — the __agent-target serve CLI reentry serving the generic remote Provider/PluginMeta gRPC channel (kind-blind; wire broker transport)"},
	{"bootstrap_phase.go", "M — the bootstrap phase machinery (plugin-loading phase dispatch)"},
	{"check_endpoint_resolve.go", "M — generic host-endpoint reverse-legs served back over CheckContextService (class-generic, never a per-verb RPC)"},
	{"cli_model_cmd.go", "M — the __cli-model host seam (Kong command-tree reflection for the externalized MCP server); a CLI/prescan-adjacent host seam"},
	{"cue_kind_box.go", "B — the box⊆candy image factory bootstrap root (the discovered-candy pre-check calls it directly)"},
	{"cue_kind_candy.go", "B — the candy⊻box factory bootstrap root (must exist before any plugin can load; candyIsImage/buildCandy stay core)"},
	{"cue_loader.go", "M — the per-entity CUE decode (the kind-decode MATERIALIZE: fold parsed config into the typed project view)"},
	{"cue_normalize.go", "M — the CUE-loader shorthand canonicalizer (a materialize decode helper, kind-blind)"},
	{"cue_schema.go", "M — the per-entity CUE validator (validateKindValueCUE) the host materialize consults by word; kind-blind registry dispatch"},
	{"deploy_builtins.go", "B — the deploy-target provider signpost (all five substrates are external; a registry seed)"},
	{"deploy_preresolve.go", "M — the general per-substrate deploy preresolver hook (OpPreresolve dispatch, kind-blind)"},
	{"deploy_substrate_lifecycle.go", "M — the substrateLifecycle interface (the wire broker's venue-lifecycle contract, kind-blind)"},
	{"deploy_target_external.go", "M — externalDeployTarget, the out-of-process substrate adapter over the executor reverse channel (wire broker)"},
	{"deploy_target_unified.go", "M — the UnifiedDeployTarget / LifecycleTarget interface (the broker's deploy-routing contract, kind-blind)"},
	{"devices.go", "GPU — hardware-blocked fold (C14); would fold into plugin-gpu under P15 once GPU R10 is possible"},
	{"gpu_allocate.go", "GPU — hardware-blocked fold (C14); revisitable on GPU hardware"},
	{"gpu_imply.go", "GPU — hardware-blocked fold (C14); revisitable on GPU hardware"},
	{"gpu_shim.go", "GPU — hardware-blocked fold (C14); revisitable on GPU hardware"},
	{"host_build_cli.go", "M — the HostBuild(\"cli\") generic reentry host-builder (cli reentry the broker keeps)"},
	{"loader_threaded.go", "D — the kind-recognition threaded-data snapshot the host fills from the registry and threads to the kind-blind parse (the boundary law's canonical D example; untying the loader↔registry cycle)"},
	{"main.go", "B — the Kong parse/dispatch spine + the bootstrap entry point"},
	{"main_freshness.go", "D — the binary freshness self-identity (os.Executable() vs cwd)"},
	{"main_repo.go", "B — the --repo project-directory resolver (bootstrap, pre-dispatch)"},
	{"materialize.go", "M — the host MATERIALIZE half of the K1 walk/materialize split (folds the loaderkit LoadedProject envelope; successor of loader_driver.go, mirrors node_parsed.go's P6 split)"},
	{"node_build.go", "M — the generic entity-body materialize (kind-decode MATERIALIZE)"},
	{"node_bundle.go", "M — the bundle / resource-member materialize (the ONE member-decode source of truth; kind-decode MATERIALIZE)"},
	{"node_bundle_venue.go", "M — venue-from-position for bundle plan steps (kind-decode MATERIALIZE)"},
	{"node_candy.go", "B — the candy constructor (candyIsImage/buildCandy bootstrap-critical routing stays core)"},
	{"node_desugar.go", "M — the parse-time plugin-verb sugar desugar (kind-blind; runs in the host materialize)"},
	{"node_normalize.go", "M — the normalizer dispatcher (the node-form materialize decode path; kind-decode MATERIALIZE)"},
	{"node_parse.go", "M — the parsed node-form genericNode TYPE the host materialize reconstructs (the PARSE moved to loaderkit; the type stays)"},
	{"node_parsed.go", "M — the host MATERIALIZE half of the P6 parse/materialize seam (folds the loaderkit ParsedProject)"},
	{"plugin_checkcontext_reverse.go", "M — the CheckContextService reverse channel (wire broker leg, class-generic)"},
	{"plugin_command_prescan.go", "M — the early external-COMMAND-word prescan (CLI grammar wiring, kind-blind)"},
	{"plugin_dispatch_reverse.go", "M — the InvokeProvider / HostBuild broker (the wire broker's peer-dispatch + host-callback leg)"},
	{"plugin_executor_reverse.go", "M — the ExecutorService reverse channel (wire broker leg, class-generic)"},
	{"plugin_grpc.go", "M — the go-plugin gRPC transport (plugin loading)"},
	{"plugin_inproc.go", "M — the in-process provider transport (plugin loading)"},
	{"plugin_inproc_reverse.go", "M — the compiled-in reverse-channel adapter (plugin loading; placement-invisible broker leg)"},
	{"plugin_loader.go", "M — the plugin-unit load gate (plugin loading)"},
	{"plugin_prescan.go", "M — the byte-gated additive parse prescan (prescan-dispatch: substrate/command/kind words)"},
	{"plugin_provider_common.go", "M — shared provider wiring (plugin loading)"},
	{"plugin_step_external.go", "M — the ExternalPluginStep OpExecute reverse leg (wire broker leg)"},
	{"plugin_transport.go", "M — the InProc/Local transport pair (plugin loading)"},
	{"plugins_generated.go", "B — the compiled-in pluginsgen registry output (reproducibility-gated; a registry seed)"},
	{"provider.go", "M — the Provider interface (transport-invisible; plugin loading)"},
	{"provider_builder_external.go", "M — the builder-class word→plugin dispatch (prescan/word-dispatch)"},
	{"provider_checkenv.go", "M — the verb/check-context dispatch wiring (word→plugin dispatch)"},
	{"provider_command.go", "M — the builtin command dispatch (word→plugin dispatch)"},
	{"provider_command_external.go", "M — the external command dispatch (word→plugin dispatch)"},
	{"provider_deploy.go", "M — the deploy-class word→plugin dispatch (word→plugin dispatch)"},
	{"provider_invoke.go", "M — the generic Invoke (plugin loading)"},
	{"provider_kind.go", "M — the kind-class provider bijection gate (kind-decode dispatch)"},
	{"provider_kind_invoke.go", "M — runPluginKind / foldSubstrateKind / foldCandyKind (the kind-decode MATERIALIZE)"},
	{"provider_registry.go", "M — the provider registry (plugin loading)"},
	{"provider_step.go", "M — the step-class dispatch + bijection gate (word→plugin dispatch)"},
	{"provider_verb.go", "M — the verb-class dispatch (word→plugin dispatch)"},
	{"registry_bootstrap.go", "B — the provider-registry seed that must exist before any plugin loads"},
	{"reserved_registry.go", "B/D/M — the CUE-derived reserved-word sets (D), the VerbCatalog dispatch (M), and normalizeNodeInto (materialize); a bootstrap root"},
	{"step_builtins.go", "B — the compiled-in step-kind dispatch seed"},
	{"substrate_lifecycle_grpc.go", "M — the grpcSubstrateLifecycle proxy (wire broker venue-lifecycle leg, kind-blind)"},
	{"unified_targets.go", "M — the ResolveTarget deploy dispatcher + externalDeployTarget adapter (wire broker deploy routing, kind-blind)"},
	{"verb_builtins.go", "B — the compiled-in verb dispatch seed"},
	{"version.go", "D — the CalVer computation (kind-recognition/identity data)"},
}

// residueOwner maps every tracked-for-removal charly/*.go non-test file to its
// owning cutover. This map IS the living tracker that sequences the program.
//
// Owner values (the program's cutovers; P15 absorbs the K1 loader-orchestration
// and K5 seam-death folds):
//
//	P8b  — build engine → candy/plugin-build (+ the #67 render α-fold cluster)
//	P11  — pod deploy surface → plugin-deploy-pod (config-write, lifecycle, overlay, substrates)
//	P12  — check / ADE command family → compiled-in plugin-check
//	P13  — bundle CLI → command:bundle + the deploy.go state-model fold
//	P14  — status collectors / alias / scaffold / OCI registry+merge → plugins
//	P15  — residual folds + HostArbiter deletion + K1 loader-orchestration + K5 seam-death + misc CLI utils
var residueOwner = map[string]string{
	"agent_config.go":               "P12",
	"arbiter_host.go":               "P15",
	"builder_preresolve.go":         "P8b",
	"builder_venue.go":              "P8b",
	"build.go":                      "P8b",
	"build_overlay.go":              "P8b",
	"bundle_add_cmd.go":             "P13",
	"bundle_compile_seam.go":        "P13",
	"bundle_from_box_cmd.go":        "P13",
	"bundle_members.go":             "P11",
	"box_fetch_reentry.go":          "P15",
	"check_bed_run.go":              "P12",
	"check_cmd.go":                  "P12",
	"check_feature_run.go":          "P12",
	"check_image_preflight.go":      "P12",
	"check_kit_adapter.go":          "P12",
	"check_members.go":              "P12",
	"check_runner_live.go":          "P12",
	"checkrun.go":                   "P12",
	"checkrun_act.go":               "P12",
	"checkrun_charly_verbs.go":      "P12",
	"checkspec.go":                  "P12",
	"check_venue.go":                "P12",
	"cmd.go":                        "P15",
	"commands.go":                   "P11",
	"config.go":                     "P15",
	"config_image.go":               "P11",
	"config_secret_migration.go":    "P13",
	"container.go":                  "P12",
	"credential_plugin.go":          "P15",
	"cue_defaults.go":               "P15",
	"cue_kind_android_reg.go":       "P11",
	"cue_kind_check.go":             "P12",
	"cue_kind_deploy.go":            "P11",
	"cue_kind_k8s.go":               "P14",
	"cue_kind_local.go":             "P11",
	"cue_kind_pod.go":               "P11",
	"cue_kind_vm.go":                "P11",
	"cue_node.go":                   "P15",
	"deploy.go":                     "P13",
	"deploy_add_shared.go":          "P13",
	"deploy_nodeform.go":            "P13",
	"deploy_ref.go":                 "P13",
	"deploy_state_host.go":          "P13",
	"deploy_tree.go":                "P13",
	"distro.go":                     "P8b",
	"distro_resolve.go":             "P8b",
	"egress.go":                     "P15",
	"embed_defaults.go":             "P15",
	"enc.go":                        "P11",
	"engine.go":                     "P14",
	"ensure_image.go":               "P8b",
	"filelock.go":                   "P15",
	"format_config.go":              "P8b",
	"generate.go":                   "P8b",
	"hooks.go":                      "P11",
	"host_build_bake_plugins.go":    "P8b",
	"host_build_config_resolve.go":  "P15",
	"host_build_feature.go":         "P15",
	"host_build_hostprobe.go":       "P15",
	"host_build_render_seam.go":     "P8b",
	"host_build_retention.go":       "P15",
	"host_build_settings.go":        "P15",
	"host_build_vm_build.go":        "P8b",
	"host_exec.go":                  "P15",
	"image.go":                      "P14",
	"init_config.go":                "P8b",
	"install_build_act.go":          "P8b",
	"install_build_services.go":     "P8b",
	"k3s_post.go":                   "P11",
	"k8s_config.go":                 "P11",
	"k8s_deploy_from_box.go":        "P14",
	"k8s_generate.go":               "P11",
	"k8s_plugin.go":                 "P11",
	"layer_capabilities.go":         "P8b",
	"layer_secrets.go":              "P8b",
	"layers.go":                     "P8b",
	"local_spec.go":                 "P11",
	"namespace.go":                  "P15",
	"notify.go":                     "P15",
	"oci_step_emit.go":              "P8b",
	"pkg_cmd.go":                    "P15",
	"planrun_adapter.go":            "P12",
	"plugin_cmd.go":                 "P15",
	"plugin_command_cmd.go":         "P15",
	"plugin_command_ssh.go":         "P15",
	"plugin_providers_cmd.go":       "P15",
	"ports.go":                      "P11",
	"preempt.go":                    "P15",
	"privileged_runner.go":          "P15",
	"readiness_config.go":           "P11",
	"refs.go":                       "P15",
	"refs_threaded.go":              "P15",
	"remote_image.go":               "P14",
	"render_baked_metadata.go":      "P8b",
	"render_prep.go":                "P8b",
	"resource_resolve.go":           "P15",
	"retention.go":                  "P15",
	"run_subcommand.go":             "P15",
	"runtime_config_values.go":      "P15",
	"secrets.go":                    "P11",
	"security.go":                   "P11",
	"service_render.go":             "P8b",
	"shell_profile.go":              "P8b",
	"shellcollect.go":               "P11",
	"sidecar.go":                    "P11",
	"ssh.go":                        "P15",
	"step_emit_hostbuild.go":        "P8b",
	"substrate_template_resolve.go": "P15",
	"tasks.go":                      "P8b",
	"transfer.go":                   "P14",
	"tunnel.go":                     "P15",
	"uf_box_generic.go":             "P15",
	"uf_candy_generic.go":           "P15",
	"unified.go":                    "P15",
	"update_deploy_dispatch.go":     "P11",
	"validate.go":                   "P15",
	"validate_ephemeral.go":         "P11",
	"validate_preempt.go":           "P13",
	"vm_backend_lifecycle.go":       "P11",
	"vm_lifecycle_preresolve.go":    "P11",
	"vm_plugin_client.go":           "P11",
	"vm_qemu_client.go":             "P11",
	"vmshared_aliases.go":           "P15",
	"volume_cp_tags_cmd.go":         "P11",
	"volumes.go":                    "P11",
	// — files added by cutovers that landed after the T0 authoring (living tracker) —
	"alias_collect.go":              "P14",
	"box_inspect_overlay.go":        "P14",
	"build_resolve_host.go":         "P8b",
	"config_write_host.go":          "P11",
	"host_build_check_bed.go":       "P12",
	"host_build_check_run.go":       "P12",
	"host_build_deploy_from_box.go": "P13",
	"host_build_pod_disposable.go":  "P11",
	"intermediates_shim.go":         "P8b",
	"oci_plugin.go":                 "P14",
	"pod_lifecycle_dispatch.go":     "P11",
	"pod_lifecycle_resolve.go":      "P11",
	"pod_lifecycle_verb.go":         "P11",
	"resolved_project_host.go":      "P8b",
	"status_substrate_host.go":      "P14",
	"validate_project_host.go":      "P15",
	// — Cutover A (#168, deploy-dispatch kernel hard-cutover exit): the K4-C
	// deploy-tree walk port narrows the retired deploy-dispatch spike into 6
	// per-position seams (candy/plugin-bundle drives the walk; each seam calls
	// back a deploy-specific host body — deploy-dispatch is tracked K4 residue,
	// not permanent core, per the operator's boundary-law overrule) —
	"host_build_deploy_config_save.go":       "P13",
	"host_build_deploy_del_resolve.go":       "P13",
	"host_build_deploy_members.go":           "P13",
	"host_build_deploy_node_del_dispatch.go": "P13",
	"host_build_deploy_node_dispatch.go":     "P13",
	"host_build_deploy_tree_resolve.go":      "P13",
	// — Cutover A's P13-KERNEL direction-flip: each pod-lifecycle CLI command's
	// GRAMMAR moved to command:<word> (candy/plugin-pod), but the per-command
	// orchestration BODY (podStartCmd/podStopCmd/podShellCmd/... — R-items,
	// concrete pod-kind behaviour, not a kind-blind mechanism) still runs
	// host-side behind a thin "pod-<word>" HostBuild seam each; both the seam
	// and the orchestration it forwards to are P11 pod-deploy-surface residue —
	"host_build_pod_config.go":       "P11",
	"host_build_pod_config_seams.go": "P11",
	// vm_deploy_state.go — renamed from bundle_add_cmd_vm.go (was P13); its
	// surviving content is the charly.yml config-persist half of VM deploy add
	// (config-write/lifecycle), the same substrate-persistence theme as the
	// other P11 vm_*.go / config_write_host.go entries above.
	"vm_deploy_state.go": "P11",
	// host_build_pod_lifecycle_dispatch.go — Cutover B-1 (#169): the CONSOLIDATED
	// replacement for the 7 deleted host_build_pod_{start,stop,shell,logs,update,
	// service,remove}.go files above (host_build_pod_disposable.go is a separate
	// concern and keeps its own file/entry). Re-derived from the tree, NOT from
	// the file's own header comment (never trust code comments): every handler
	// only CALLS a pre-existing floor Mechanism (dispatchLifecycleTarget /
	// unified_targets.go's ResolveTarget) or the P15 arbiter (releaseResourceClaim,
	// arbiter_host.go — itself residue, not floor, contradicting the header's
	// "stays core" framing for the arbiter bracket) — the defines-vs-calls test
	// makes this an R-item, not a Mechanism. Same shape and same P11 pod-deploy-
	// surface classification as the 7 files it replaces; the file's own header
	// already declines "settled STAYS-CORE precedent" status for its siblings,
	// citing the deploy-dispatch boundary-law overrule (memory:
	// deploy-resolution-67-gated-cone.md) — no re-escalation needed.
	"host_build_pod_lifecycle_dispatch.go": "P11",
	// — FINAL/K5 unit 6 (#171, F6 preresolve + ephemeral cross-substrate move +
	// credential/bed-session consolidation): 4 new charly/*.go files. Re-derived
	// from the tree, NOT from any file's own header comment (never trust code
	// comments — host_build_deploy_entity_resolve.go's own header claims its
	// LoadUnified call is "a kernel Mechanism, R-E2 stands: it never moves
	// wholesale," but LoadUnified's own defining file, unified.go, is ITSELF
	// tracked P15 residue in this same table — a header asserting permanence for
	// a call target this table already tracks as residue is exactly the
	// incomplete-seam trap, not evidence).
	//
	// host_build_deploy_entity_resolve.go — the generalized "deploy-entity-
	// resolve" HostBuild seam: its default/"bundle" case calls resolveTreeRoot
	// (deploy_tree.go, P13), and its "k8s"/"android"/"vm" cases fold in the
	// entity-lookup bodies formerly split across the three now-deleted P11 files
	// (android_deploy_cmd.go, android_deploy_preresolve.go,
	// k8s_deploy_preresolve.go — pruned above). Classified with the K4-C
	// deploy-dispatch seam family (P13) per the defines-vs-calls test and the
	// deploy-dispatch boundary-law precedent (memory:
	// deploy-resolution-67-gated-cone.md) this PR's own prior reconciliation
	// passes already applied to the sibling host_build_deploy_{tree_resolve,
	// node_dispatch,node_del_dispatch,del_resolve,members,config_save}.go seams.
	"host_build_deploy_entity_resolve.go": "P13",
	// host_build_k8s_generate.go — single-purpose "k8s-generate-kustomize" seam
	// wrapping k8s_generate.go's GenerateK8sKustomize (P11) and explicitly
	// mirroring the deleted k8s_deploy_preresolve.go's (P11) TreeRoot
	// computation; classified with its k8s-substrate predecessor, P11.
	"host_build_k8s_generate.go": "P11",
	// host_build_ephemeral_register.go + ephemeral_dispatch.go — split of the
	// deleted ephemeral_lifecycle.go (P11, pruned above): the register/teardown
	// BODY moved to candy/plugin-bundle, leaving (a) a thin HostBuild seam
	// wrapping registerEphemeralIfMarked (deploy_add_shared.go) so the plugin
	// can trigger the one host-only side effect it cannot do itself, and (b) the
	// host→plugin dispatch into command:bundle's Op{Ephemeral,Teardown}
	// legs. Both stay in the "ephemeral" cross-substrate lifecycle family — the
	// SAME family as the still-present validate_ephemeral.go (P11) — rather than
	// following the P13 shape their own comments compare themselves to
	// (bundle_compile_seam.go's dispatch pattern); the family the seam SERVES
	// (ephemeral lifecycle, explicitly named in P11's "lifecycle" scope) governs
	// over incidental mechanism-shape similarity to a P13 sibling.
	"host_build_ephemeral_register.go": "P11",
	"ephemeral_dispatch.go":            "P11",
}

func TestKernelManifest_CoreIsPinnedToTheFabricFloor(t *testing.T) {
	floorSet := make(map[string]bool, len(kernelFloor))
	for _, e := range kernelFloor {
		floorSet[e.file] = true
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	present := make(map[string]bool)
	// residue by owner → sorted files; the failure message IS the living tracker.
	byOwner := make(map[string][]string)
	var unowned []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		base := filepath.Base(name)
		present[base] = true
		if floorSet[base] {
			continue
		}
		owner, ok := residueOwner[base]
		if !ok {
			unowned = append(unowned, base)
			continue
		}
		byOwner[owner] = append(byOwner[owner], base)
	}

	// Stale allowlist entries (a floor file no longer present): either renamed
	// or misclassified — surface so the table is corrected.
	var staleFloor []string
	for _, e := range kernelFloor {
		if !present[e.file] {
			staleFloor = append(staleFloor, e.file)
		}
	}
	// Stale residue entries (an owner-mapped file already moved to a plugin /
	// deleted by a landed cutover): prune the map entry. Informational only —
	// never blocks the GREEN end state.
	var staleResidue []string
	for f := range residueOwner {
		if !present[f] {
			staleResidue = append(staleResidue, f)
		}
	}
	sort.Strings(staleFloor)
	sort.Strings(staleResidue)
	sort.Strings(unowned)

	// Ordered owner list so the tracker output is stable across runs.
	owners := make([]string, 0, len(byOwner))
	for o := range byOwner {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	for _, files := range byOwner {
		sort.Strings(files)
	}

	var residueCount int
	for _, files := range byOwner {
		residueCount += len(files)
	}

	// GREEN only when the program completes (zero residue, zero unowned, zero
	// stale floor). Stale residue entries (already-moved files) do NOT block
	// GREEN — they are cleanup clutter, logged for the next pruning edit.
	if residueCount == 0 && len(unowned) == 0 && len(staleFloor) == 0 {
		if len(staleResidue) > 0 {
			t.Logf("KERNEL-MANIFEST gate: at the fabric floor; prune %d stale residueOwner entr%s: %s",
				len(staleResidue), pluralY(staleResidue), strings.Join(staleResidue, ", "))
		} else {
			t.Log("KERNEL-MANIFEST gate: charly/ core is at the fabric floor — program complete.")
		}
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "KERNEL-MANIFEST gate (P16a): charly/ core is NOT yet at the fabric floor.\n")
	fmt.Fprintf(&b, "  FLOOR files:        %d (E/M/B/D fabric + the C14 GPU exception)\n", len(kernelFloor))
	fmt.Fprintf(&b, "  RESIDUE files:      %d (tracked-for-removal; each tagged with its owning cutover)\n", residueCount)
	fmt.Fprintf(&b, "  UNOWNED residue:    %d (a new file with no residueOwner entry — classify it)\n", len(unowned))
	fmt.Fprintf(&b, "  STALE floor:        %d (allowlist entry names a missing file — rename or re-classify)\n", len(staleFloor))
	fmt.Fprintf(&b, "  STALE residue:      %d (owner entry names a file already moved/deleted — prune it)\n", len(staleResidue))
	fmt.Fprintf(&b, "\nResidue by owning cutover (the living tracker — this shrinks to zero as P8b/P11–P15 land):\n")
	for _, o := range owners {
		fmt.Fprintf(&b, "  %s (%d):\n", o, len(byOwner[o]))
		for _, f := range byOwner[o] {
			fmt.Fprintf(&b, "    %s\n", f)
		}
	}
	if len(unowned) > 0 {
		fmt.Fprintf(&b, "\nUNOWNED — a residueOwner entry is required for each (R1: classify before it can hide):\n")
		for _, f := range unowned {
			fmt.Fprintf(&b, "    %s\n", f)
		}
	}
	if len(staleFloor) > 0 {
		fmt.Fprintf(&b, "\nSTALE FLOOR — allowlist names a missing file (rename or move to residueOwner):\n")
		for _, f := range staleFloor {
			fmt.Fprintf(&b, "    %s\n", f)
		}
	}
	if len(staleResidue) > 0 {
		fmt.Fprintf(&b, "\nSTALE RESIDUE — already moved/deleted (informational; prune the residueOwner entry):\n")
		for _, f := range staleResidue {
			fmt.Fprintf(&b, "    %s\n", f)
		}
	}
	t.Errorf("%s", b.String())
}

// pluralY returns "y" for one element, "ies" for many — only used in a log line.
func pluralY(s []string) string {
	if len(s) == 1 {
		return "y"
	}
	return "ies"
}
