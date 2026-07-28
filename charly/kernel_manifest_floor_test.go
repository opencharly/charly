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
	{"check_venue_resolve.go", "M — the host half of the venue-classification seam (#118 check broker-envelope-out): reaches plugin-check's verb:check-resolve classifier (the kind-decode lives THERE, never here) and re-materializes the returned generic spec.VenueDescriptor into a live host DeployExecutor via the kind-blind kit.VenueFromDescriptor / deploykit.ResolveDeployChain — a live executor never crosses the wire. ZERO classification; a thin generic host-forward+re-materialize seam serving the check reverse channel"},
	{"cli_model_cmd.go", "M — the __cli-model host seam (Kong command-tree reflection for the externalized MCP server); a CLI/prescan-adjacent host seam"},
	{"cue_kind_box.go", "B — the box⊆candy image factory bootstrap root (the discovered-candy pre-check calls it directly)"},
	{"cue_kind_candy.go", "B — the candy⊻box factory bootstrap root (must exist before any plugin can load; candyIsImage/buildCandy stay core)"},
	{"cue_loader.go", "M — the per-entity CUE decode (the kind-decode MATERIALIZE: fold parsed config into the typed project view)"},
	{"cue_normalize.go", "M — the CUE-loader shorthand canonicalizer (a materialize decode helper, kind-blind)"},
	{"cue_schema.go", "M — the per-entity CUE validator (validateKindValueCUE) the host materialize consults by word; kind-blind registry dispatch"},
	{"deploy_builtins.go", "B — the deploy-target provider signpost (all five substrates are external; a registry seed)"},
	{"deploy_target_unified.go", "M — the UnifiedDeployTarget / LifecycleTarget interface (the broker's deploy-routing contract, kind-blind)"},
	{"devices.go", "GPU — hardware-blocked fold (C14); would fold into plugin-gpu under P15 once GPU R10 is possible"},
	{"gpu_allocate.go", "GPU — hardware-blocked fold (C14); revisitable on GPU hardware"},
	{"gpu_imply.go", "GPU — hardware-blocked fold (C14); revisitable on GPU hardware"},
	{"gpu_shim.go", "GPU — hardware-blocked fold (C14); revisitable on GPU hardware"},
	{"host_build_cli.go", "M — the HostBuild(\"cli\") generic reentry host-builder (cli reentry the broker keeps)"},
	{"image.go", "B — the charly box CLI grammar spine (BoxCmd, embeds kong.Plugins for plugin-box's nested subcommands) + FormatCLIError, the top-level Kong error formatter main() calls; BoxPullCmd already externalized to candy/plugin-box (K3 #39)"},
	{"loader_threaded.go", "D — the kind-recognition threaded-data snapshot the host fills from the registry and threads to the kind-blind parse (the boundary law's canonical D example; untying the loader↔registry cycle)"},
	{"main.go", "B — the Kong parse/dispatch spine + the bootstrap entry point"},
	{"main_freshness.go", "D — the binary freshness self-identity (os.Executable() vs cwd)"},
	{"main_repo.go", "B — the --repo project-directory resolver (bootstrap, pre-dispatch)"},
	{"materialize.go", "M/B — the host-coupled materialize leaf LEGS the relocated loaderkit orchestration (loaderkit.MaterializeLoadedProject, #48) calls back through (hostMaterializeProjectSeams): registry kind-decode dispatch (materializeProject/materializeNodeInto), the bootstrap candyIsImage discovered-manifest fold (foldDiscoveredManifests), and the embedded-defaults merge (materializeDocStream). The walk/merge ORCHESTRATION itself MOVED to sdk/loaderkit at #48"},
	{"node_build.go", "M — the generic entity-body materialize (kind-decode MATERIALIZE)"},
	{"node_bundle.go", "M — the bundle / resource-member materialize (the ONE member-decode source of truth; kind-decode MATERIALIZE)"},
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
	{"unified_targets.go", "M — the ResolveTarget deploy dispatcher + externalDeployTarget adapter (wire broker deploy routing, kind-blind)"},
	{"verb_builtins.go", "B — the compiled-in verb dispatch seed"},
	{"version.go", "D — the CalVer computation (kind-recognition/identity data)"},
	{"host_build_config_resolve.go", "M — the config-resolve F10 host-builder (loader/registry reverse-leg the plugin calls back; kind-blind)"},
	{"host_build_feature.go", "M — the feature F10 host-builder reverse-channel seam (kind-blind)"},
	{"host_build_hostprobe.go", "M — the host-probe F10 host-builder (host-only hardware/env probe reverse-leg)"},
	{"host_build_render_seam.go", "M — the render F10 host-builder (registry-consult reverse-leg; kind-blind)"},
	{"host_build_check_bed.go", "M — the check-bed session reverse-channel seam (flock fd + preempt lease + env for forked children; host-process-bound)"},
	{"host_build_check_run.go", "M — the check-run reverse-channel seam (preflight + host feature dispatch; kind-blind)"},
	{"host_build_deploy_from_box.go", "M — the deploy-from-box F10 host-builder (reverse-channel; kind-blind)"},
	{"host_build_pod_disposable.go", "M — the pod-disposable F11 seam serving plugin-check (reverse-channel; kind-blind)"},
	{"host_build_deploy_config_save.go", "M — the deploy-config-save F10 host-builder (charly.yml persist host-only reverse-leg)"},
	{"host_build_deploy_del_resolve.go", "M — the deploy-del-resolve F10 host-builder (reverse-channel; kind-blind)"},
	{"host_build_deploy_members.go", "M — the deploy-members F10 host-builder (reverse-channel; kind-blind)"},
	{"host_build_deploy_node_del_dispatch.go", "M — the deploy-node-del dispatch F10 host-builder (kind-blind dispatch)"},
	{"host_build_resolve_target_add.go", "M — the resolve-target-add F10 host-builder (K4-C shape-2: the per-node deploy TERMINAL step — reconstruct ancestor exec chain + loadConfigForDeploy + ResolveTarget + Add; kind-blind deploy dispatch, floor-M per the deploy-dispatch ruling). Replaced host_build_deploy_node_dispatch.go when the per-node COMPILE moved plugin-side (double-bounce elimination)"},
	{"host_build_deploy_plugins_connect.go", "M — the deploy-plugins-connect F10 host-builder (reverse-channel; kind-blind — the K1-LOADER witness preamble: loadDeployPlugins + project dir)"},
	{"host_build_loader_floor.go", "M/D — the PERMANENT loader reverse legs (loader-bootstrap = bootstrap-phase plugin dispatch; loader-walk = prescan+connectDeclaredKindPlugins; loader-threaded = the registry-derived Threaded D-snapshot) a plugin-side loader always calls back for; the transitional loader legs live in host_build_loader.go (residue)"},
	{"host_build_pod_config.go", "M — the pod-config F10 reverse-channel dispatch seam (hostEnvJSON host-only; kind-blind)"},
	{"host_build_pod_config_seams.go", "M — the pod-config F10 host-builders the plugin calls back (loader/registry/credential-coupled reverse-legs)"},
	{"host_build_pod_lifecycle_dispatch.go", "M — the pod-lifecycle dispatch F10 host-builder wrapping dispatchLifecycleTarget (ResolveTarget+loader+live-executor; kind-blind M)"},
	{"host_build_ephemeral_register.go", "M — the ephemeral-register generic substrate-agnostic F10 seam"},
	{"cue_kind_pod.go", "D — the registerCueKind pod clause-D kind-recognition-data registration (host-side PodValue gate validateKindValueCUE consults)"},
	{"cue_kind_vm.go", "D — the registerCueKind vm clause-D kind-recognition-data registration"},
	{"cue_kind_local.go", "D — the registerCueKind local clause-D kind-recognition-data registration"},
	{"cue_kind_deploy.go", "D — the registerCueKind deploy clause-D kind-recognition-data registration"},
	{"cue_kind_android_reg.go", "D — the registerCueKind android clause-D kind-recognition-data registration"},
	{"cue_kind_check.go", "D — the registerCueKind check clause-D kind-recognition-data registration"},
	{"cue_kind_k8s.go", "D — the registerCueKind k8s clause-D kind-recognition-data registration"},
	{"planrun_adapter.go", "M — check-plan verb dispatch via providerRegistry.ResolveVerb (in-core prescan-dispatch M; perf-gated fast-path, TestPerfGate_BuiltinVerbsSkipEnvelope)"},
	{"checkrun_act.go", "M — check-op verb dispatch via providerRegistry.ResolveVerb (in-core prescan-dispatch M; perf-gated)"},
	{"checkspec.go", "M — check-spec verb dispatch via providerRegistry.ResolveVerb (in-core prescan-dispatch M; perf-gated)"},
	{"checkrun.go", "M — the newCheckRunner seam wiring the perf-gated builtin-verb fast-path (prescan-dispatch M)"},
	{"check_kit_adapter.go", "M — the in-proc kit-verb registration bridge (plugin-loading/prescan-dispatch M)"},
	{"pod_lifecycle_dispatch.go", "M — the F6 host-plan-hook word-table + arbiter-bracket host-process env gating (CHARLY_PREEMPT_LEASE; kind-blind)"},
	{"pod_lifecycle_verb.go", "M — the pod-lifecycle verb dispatch (F6 host-plan-hook; kind-blind)"},
	{"vm_plugin_client.go", "M — the host to plugin libvirt client (InvokeProvider dispatch; wire broker)"},
	{"deploy_target_dispatch.go", "M — the deploy-target dispatch F10 host-builder to command:bundle OpDeployDispatch (one envelope, all substrates; kind-blind)"},
	{"dispatch_build_ensure.go", "M — the build:ensure in-proc reverse-channel dispatch to plugin-build (mirrors build.go dispatchBuild; kind-blind)"},
	{"host_build_arbiter_bracket.go", "M — the arbiter-bracket F10 host-builder (os.Setenv CHARLY_PREEMPT_LEASE must run in THE host process; kind-blind)"},
	{"host_build_box_ref_resolve.go", "M — the box-ref-resolve host-only piece of build:ensure (generic BoxRefResolveRequest; orchestration moved to plugin-build)"},
	{"host_build_check_load_plugins.go", "M — the check-load-plugins host seam (plugin-LOADING, an in-core M; connects an out-of-proc candy)"},
	{"host_build_construct_step.go", "M — the construct-step host-builder (registry-resolve of run: plugin: word; clause-M mechanism)"},
	{"host_build_deploy_config_save_state.go", "M — the deploy-config-save-state substrate-neutral F10 host-builder (generic deploy-state persist)"},
	{"host_build_remote_image_resolve.go", "M — the remote-image-resolve seam for plugin-build's ensure fallback AND candy/plugin-box's `box build @ref` (thin ResolveRemoteImage wrapper; drops non-wire fields)"},
	{"host_build_render_service.go", "M — the render-service host-builder (wraps plugin-init OpResolve + M16 egress gate; two registry consults)"},
	{"host_exec.go", "M — the --host CLI pre-dispatch reexec decision (shouldReexecForHost runs before Kong dispatches, reading the core CLI struct + command path — the prescan-dispatch spine, mirroring plugin_command_prescan.go / main_repo.go's --repo; ReexecOverSSH body already moved to kit)"},
	// P8b render-glue remainder (CONE B) — spike-verified by call-graph (never trusting a file's
	// own "stays host" comment): the movable render DRIVE already left in prior cutovers (#67
	// render-DRIVE move, K5-Unit-6b, P11c, FLOOR-SLIM-proper Unit-8, K5-A item 2 — into
	// candy/plugin-build, candy/plugin-deploy-pod, candy/plugin-installstep, sdk/deploykit's
	// header_copy_remote.go/NewRenderGeneratorFromProject). These 5 are genuine thin registry-
	// dispatch/wire-forward M with ZERO capability logic — team-lead-audited and accepted (the
	// Generator-cluster files — generate.go/tasks.go/build_overlay.go/builder_preresolve.go — hold
	// real build-engine RENDER/PREP logic and were REJECTED as floor; they are the dominant
	// remaining #118 gate, tracked residue again below, moving into candy/plugin-build/
	// plugin-deploy-pod).
	{"service_render.go", "M — RenderService: thin providerRegistry.ResolveKind(\"init\") dispatch, the direct callee of the already-floor host_build_render_service.go; also carries the egress-validation dispatch merged from the deleted charly/egress.go (coneB-buildtail) — thin verb:egress registry-dispatch plus the load-bearing vmshared/kit init-seam wiring those SDK packages' own function-var injection points require (they cannot import charly core)"},
	{"format_config.go", "M/D — LoadBuildConfigForBox: loader-glue (LoadUnified, K1) + registry-callback wiring, a shared cross-cone utility (P13/P15/P11/K3 + candy/plugin-vm all call it)"},
	{"oci_step_emit.go", "M — dispatchOCIStep: thin word→plugin registry dispatch + reverse-channel forwarder, same shape as the already-floor dispatch_build_ensure.go/deploy_target_dispatch.go"},
	{"step_emit_hostbuild.go", "M — the generic \"step-emit\" F10 HostBuild seam (word-keyed stepEmitters dispatch, kind-blind — same shape as the ~25 other host_build_*.go floor entries)"},
	{"intermediates_shim.go", "M — ComputeIntermediates/GlobalCandyOrder: a *Config→deploykit.IntermediateDefaults adapter wired as a loaderkit.ResolveProjectSeams callback consumed by resolved_project_host.go"},
	{"resolved_project_host.go", "M — the trimmed \"resolved-project\" F11 resolve handle (K5-Unit-0 keystone); team-lead-ruled to stay core, floor-reclassified here per the same boundary-law shape as its sibling F10/F11 seams"},
	// Build-tail cone (coneB-buildtail) — spike-verified by call-graph, applying the SAME "thin
	// backing-body of an already-accepted floor seam" bar the team lead corrected round 1 against
	// (no capability logic moved to floor this round — only genuine dispatch/bootstrap glue).
	{"host_build_buildengine.go", "M — the shrunk render-seam-floor consumer (hostBuildPrep/hostBuildContextIgnoreBaseline) + the 6 other buildengine-* K1-loader-witness legs (bootstrap-delicate local scan, git clone/cache, build-time plugin CONNECT registry-M, namespaced-box nested scan+render-prep) resolveBuildEngine reaches for genuinely host-only steps — mirrors host_build_loader.go's loader-* legs, already floor"},
	{"distro.go", "M/D — detectDistro/installHints/distroPackageManagers/distroFamilyMap: bootstrap-embedded host-detection data + the /etc/os-release parse, SOLE consumer is the already-floor host_build_hostprobe.go; splitting the file to move only the pure parse half saves nothing and inlining the rest would be the forbidden cosmetic-gaming pattern"},
	{"distro_resolve.go", "M — resolveDistroViaPlugin: a thin providerRegistry-coupled dispatch callback, the direct callee of the already-floor format_config.go (loaderkit.ProjectDistroConfig) — byte-for-byte the same shape as service_render.go's resolveInitConfigViaPlugin, accepted round 1"},
	// Build-remnant cone (coneB-buildremnant) — the Generator-cluster reconcile the round-1 rejection
	// (above, "REJECTED as floor") deferred until the render DRIVE finished leaving core (K3/#67/P11c/
	// K5-Unit-6b/coneB-buildtail/coneB-pkgcmd): every capability body has now left; what remains in
	// these 3 files is verified by call-graph (every exported func has a live, non-test caller; zero
	// dead code) to be thin registry-dispatch/wire-forward M with ZERO capability logic — the SAME bar
	// the already-floor P8b render-glue cluster above was accepted against.
	{"generate.go", "M — the shared render-seam-floor Generator TYPE + its two constructors (NewGenerator/newCandyScanGenerator), consumed ONLY by the already-floor F10 host-builders (build_overlay.go's hostBuildOverlay, host_build_buildengine.go, host_build_render_seam.go) — call-graph verified, no caller exists outside those 3 + 2 parity tests. Also carries the registry-coupled verb:oci adopt-user dispatch (ociProvider/invokeOciInspectUser) and the shared OpResolve builder-stage dispatch (resolveBuilderStage/resolveExternalBuilder) — thin providerRegistry.ResolveBuilder/resolve(ClassVerb)-coupled M dispatch, the same shape as the other floor OpResolve seams above. baselineContextIgnore additionally has a hard structural floor reason: it //go:embeds charly/charly.yml, only possible from this module"},
	{"tasks.go", "M — Generator.toDeploykit() + the 3 ResolveXStageSeam closures + EmitPluginOp: 100% providerRegistry-coupled seam-wiring bridging deploykit's render engine to the core provider registry (EnsureBuildersConnected/ResolveDetectionBuilderStage/ResolveExternalBuilderStage/ResolveInlineBuilder) — zero movable render logic remains, the render DRIVE itself already moved to deploykit in prior cutovers (K3-A/K5-Unit-6b); every function call-graph-verified referenced, no dead code"},
	{"builder_preresolve.go", "M — ensureBuildersConnected is genuine plugin-loading Mechanism: build-connects the not-yet-connected externalized builder plugin(s) via providerRegistry.ResolveBuilder + loadProjectPlugins, both core-private registry/plugin-loading mechanics no plugin can reach — the connect step tasks.go's EnsureBuildersConnected closure and host_build_render_seam.go call into"},
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
	"builder_venue.go":              "P8b", // buildEngineContext (the type) is floor-worthy core-dispatch infra; runVenueBuilderStep/runVenueHomeArtifactBuilder look like coneA3's deploy-vm domain by function — flagged, not unilaterally split (team-lead ruling)
	"host_build_vm_build.go":        "P8b", // the kind:vm bootstrap-builder pre-pass (resolveVmBuildBootstrap/ensureBuilderImageBuilt) is real vm-build capability, not thin dispatch — grouped with builder_venue.go for coneA3's deploy-vm pass to decide move-to-plugin-deploy-vm vs genuine floor (team-lead ruling, reverted from an over-broad floor call)
	"build_overlay.go":              "P8b",
	"bundle_add_cmd.go":             "P13",
	"bundle_from_box_cmd.go":        "P13",
	"bundle_members.go":             "P11",
	"box_fetch_reentry.go":          "P15",
	"check_bed_run.go":              "P12",
	"check_cmd.go":                  "P12",
	"check_graphics_endpoint.go":    "P12",
	"check_members.go":              "P12",
	"checkrun_charly_verbs.go":      "P12",
	"check_venue.go":                "P12",
	"cmd.go":                        "P15",
	"commands.go":                   "P11",
	"config.go":                     "P15",
	"config_image.go":               "P11",
	"config_secret_migration.go":    "P13",
	"credential_plugin.go":          "P15",
	"cue_defaults.go":               "P15",
	"cue_node.go":                   "P15",
	"deploy.go":                     "P13",
	"deploy_nodeform.go":            "P13",
	"deploy_state_host.go":          "P13",
	"deploy_tree.go":                "P13",
	"embed_defaults.go":             "P15",
	"enc.go":                        "P11",
	"filelock.go":                   "P15",
	"k8s_config.go":                 "P11",
	"layer_secrets.go":              "P8b",
	"layers.go":                     "P8b",
	"local_spec.go":                 "P11",
	"namespace.go":                  "P15",
	"plugin_cmd.go":                 "P15",
	"plugin_providers_cmd.go":       "P15",
	"preempt.go":                    "P15",
	"readiness_config.go":           "P11",
	"refs.go":                       "P15",
	"refs_threaded.go":              "P15",
	"remote_image.go":               "P14", // ResolveRemoteImage: a build capability consuming EnsureRepoDownloaded's refs orchestration — gated on coneC's Cluster A refs->loaderkit seam landing (reconciled: not floor, per coneC-loader)
	"resource_resolve.go":           "P15",
	"secrets.go":                    "P11",
	"sidecar.go":                    "P11",
	"substrate_template_resolve.go": "P15",
	"unified.go":                    "P15",
	"update_deploy_dispatch.go":     "P11",
	"validate.go":                   "P15",
	"vm_backend_lifecycle.go":       "P11",
	"vm_lifecycle_preresolve.go":    "P11",
	"volume_cp_tags_cmd.go":         "P11",
	// — files added by cutovers that landed after the T0 authoring (living tracker) —
	"config_write_host.go":     "P11",
	"validate_project_host.go": "P15",
	// — Cutover A (#168, deploy-dispatch kernel hard-cutover exit): the K4-C
	// deploy-tree walk port narrows the retired deploy-dispatch spike into 6
	// per-position seams (candy/plugin-bundle drives the walk; each seam calls
	// back a deploy-specific host body — deploy-dispatch is tracked K4 residue,
	// not permanent core, per the operator's boundary-law overrule) —
	// — Cutover A's P13-KERNEL direction-flip: each pod-lifecycle CLI command's
	// GRAMMAR moved to command:<word> (candy/plugin-pod), but the per-command
	// orchestration BODY (podStartCmd/podStopCmd/podShellCmd/... — R-items,
	// concrete pod-kind behaviour, not a kind-blind mechanism) still runs
	// host-side behind a thin "pod-<word>" HostBuild seam each; both the seam
	// and the orchestration it forwards to are P11 pod-deploy-surface residue —
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
	// host_build_deploy_candy_secrets.go + host_build_deploy_artifacts_retrieve.go — Cone A
	// shape 3 (deploy_add_shared.go + k8s_deploy_from_box.go → candy/plugin-bundle): two new
	// thin HostBuild seams wrapping the genuine floor-M halves of the former core-resident
	// prepareCandySecrets/retrieveArtifactsAndK3s — the project candy scan (CandyForPlan →
	// ScanAllCandyWithConfig) + the credential-store touch (ResolveSecretForCandy) + the live
	// artifact fetch (deploykit.RetrieveCandyArtifacts over a re-materialized venue). The
	// ORCHESTRATION (inject secrets before dispatch, retrieve+dispatch register hints after) now
	// runs plugin-side (candy/plugin-bundle/secrets_artifacts.go); K3sPostProvision's
	// registry-coupled kube dispatch moved fully plugin-side via exec.InvokeProvider. Classified
	// with the SAME K4-C deploy-dispatch seam family (P13) as the sibling
	// host_build_deploy_entity_resolve.go above.
	"host_build_deploy_candy_secrets.go":      "P13",
	"host_build_deploy_artifacts_retrieve.go": "P13",
	// host_build_ephemeral_register.go + ephemeral_dispatch.go — split of the
	// deleted ephemeral_lifecycle.go (P11, pruned above): the register/teardown
	// BODY moved to candy/plugin-bundle, leaving (a) a thin HostBuild seam
	// wrapping registerEphemeralIfMarked (now defined directly in
	// host_build_ephemeral_register.go itself — Cone A shape 3's floor-M adjudication moved it
	// verbatim out of the deleted deploy_add_shared.go) so the plugin
	// can trigger the one host-only side effect it cannot do itself, and (b) the
	// host→plugin dispatch into command:bundle's Op{Ephemeral,Teardown}
	// legs. Both stay in the "ephemeral" cross-substrate lifecycle family — the
	// SAME family as the ephemeral load-time validators now relocated to
	// sdk/loaderkit (validate_ephemeral.go, K1-LOADER RELOCATION) — rather than
	// following the P13 shape their own comments compare themselves to
	// (bundle_compile_seam.go's dispatch pattern); the family the seam SERVES
	// (ephemeral lifecycle, explicitly named in P11's "lifecycle" scope) governs
	// over incidental mechanism-shape similarity to a P13 sibling.
	"ephemeral_dispatch.go":            "P11",
	"host_build_retention_defaults.go": "P15",
	"retention_plugin.go":              "P15",
	// load_executor_host.go + host_build_loader.go — Unit C/B of the K1-LOADER
	// RELOCATION (make loaderkit.LoadUnified plugin-callable). load_executor_host.go
	// is the compiled-in TYPED loaderkit.LoaderExecutor (the charly→loaderkit
	// wrapper charly.LoadUnified drives through LoadSeamsFromExecutor). host_build_
	// loader.go holds ONLY the TRANSITIONAL reverse legs — loader-materialize
	// (drives the RELOCATED loaderkit.MaterializeLoadedProject orchestration over the
	// host leaf seams, #48 done) + loader-android-validate + loader-preempt-validate
	// (capability validators that
	// move to loaderkit reaching the registry via the generic Threaded/InvokeProvider
	// seam). Both dissolve as the loader's call sites finish moving into their owning
	// plugin (K1 loader-orchestration, P15 — the SAME owner as unified.go). The
	// PERMANENT loader legs (bootstrap-phase dispatch / prescan+connect / the D
	// snapshot) live in host_build_loader_floor.go, classified FLOOR (kernelFloor
	// above) so residue→0 GREEN stays reachable.
	"load_executor_host.go": "P15",
	"host_build_loader.go":  "P15",
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
