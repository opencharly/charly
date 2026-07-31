package main

// check_bed_run.go — the HOST-side bed helpers the check-bed session seam
// (host_build_check_bed.go) shares with the deploy path (bundle_members.go).
//
// The `charly check run <bed>` R10 acceptance sequence (build → check box → deploy →
// check live → fresh update → tear down) lives in the compiled-in command:check plugin
// (candy/plugin-check); it drives the sequence over HostBuild("cli") + the check-bed
// session seam. Narrowed at Cutover B unit 6b (the InvokeProvider-generalization family):
// the orchestration (persistBedDeployOverrides/bedCheckLevel's classifier) moved to
// sdk/deploykit/bed_session.go — the SAME "portable orchestration in sdk, thin core call
// sites" pattern already applied to the credential family. The former core-side 1-line
// pass-through wrappers for the nested-local-children deploy, the VM-ssh-ready wait, and
// the container-ready wait were DELETED (P12/P14 core-minimization dedup sweep): every
// caller (bundle_members.go, host_build_check_bed.go) now calls the corresponding
// deploykit.* function directly — the wrappers added indirection with zero behavior and
// had no plugin caller that needed a core-side name (R3/R5). What stays here: bedCheckLevel
// (needs the loaded *UnifiedFile — K1-gated) and bedExternalInPlace — the ONE genuinely
// registry-coupled classification (isExternalDeploySubstrate queries the live provider
// registry) — now computed HOST-SIDE ONCE and threaded as a plain `bool` parameter into
// deploykit.PersistBedDeployOverrides (no new wire surface — there is no cross-process
// consumer for this data today). The former bedVmDomains/acquireVmDomainLock pair
// (CHECK-wave bed-session spike) dissolved into kit.BedVmDomains/kit.AcquireVmDomainLock
// earlier — pure over an already-stamped spec.BundleNode, zero core-state coupling.

import (
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// bedCheckLevel resolves the acceptance-depth rung for a bed from its box's authored
// check_level (none → DefaultCheckLevel). VM / local beds carry no box image, so they
// always run at the default rung. The classifier (ResolveBedCheckLevel) was a thin
// deploykit wrapper over the spec.DefaultCheckLevel / spec.ResolveCheckLevel helpers —
// now inlined to those spec helpers (#55 CHECK-ENGINE cone Option A — a pure string-ladder
// classifier charly core reaches importing zero deploykit), so this function's own job is
// resolving the box ref against the loaded project (uf.ProjectConfig(), core-only) before
// applying the spec ladder.
func bedCheckLevel(uf *spec.UnifiedFile, node spec.BundleNode) string {
	if node.Image == "" {
		return spec.DefaultCheckLevel
	}
	bc, _, ok := uf.ProjectConfig().ResolveBoxRef(node.Image)
	if !ok {
		return spec.DefaultCheckLevel
	}
	return spec.ResolveCheckLevel(bc.CheckLevel)
}

// bedExternalInPlace reports whether a bed ROOT's substrate is an EXTERNAL deploy substrate
// that applies its workload IN PLACE — local-like: NO container image to build, NO `charly
// config`/`charly start`, teardown via `charly bundle del` (replay the recorded reverse
// ops). local/android/k8s/exampledeploy are in-place (they carry no `image:`).
//
// pod is the ONE externalized substrate that is NOT in-place: it builds + runs a container
// image and keeps the FULL pod lifecycle (image build → config → start → check-live →
// `charly remove`), so the bed runner must drive it through the DEFAULT pod
// path exactly as the in-proc pod — only the `charly bundle add` overlay build internally
// routes through pod's external deploy target + lifecycle hook now (invisible to the bed
// runner). Excluding pod here is consistent with the bed runner's other substrate-identity
// checks (isVM = ssh venue, isLocal = host-rooted venue); vm sidesteps the in-place logic
// via its own `case isVM` branch, so this exclusion is the container-venue (pod) analogue.
// P9: exclude the CONTAINER venue by the stamped trait, not the substrate kind word.
func bedExternalInPlace(target string) bool {
	return isExternalDeploySubstrate(target) && deployTraitDescent(target).Venue != "container"
}

// persistBedDeployOverrides is the thin core wrapper — the orchestration lives in
// deploykit.PersistBedDeployOverrides (unit 6b). This function's own job is computing the
// ONE genuinely registry-coupled classification (bedExternalInPlace, which queries the live
// provider registry) and threading it through, plus supplying marshalDeployNode (the
// K1-tied struct→node-form serializer core alone can call).
//
// TRACKED-GATED deploykit import (#55 coneA): deploykit.PersistBedDeployOverrides calls
// deploykit.SaveBundleConfig which REQUIRES a non-nil marshalNode callback (deploy_file.go:161),
// and marshalDeployNode is the K1-tied struct→node serializer in charly/deploy_state_host.go:55
// (coneC's DeployStateHost seam — a Go func callback that cannot cross the process boundary; a
// plugin has no equivalent, confirmed by grep). Named exit: coneC's DeployStateHost seam-death /
// marshalDeployNode envelope — when that envelope lands (terminus #85 territory), marshalDeployNode
// + bedExternalInPlace become reachable over a HostBuild reverse leg, the plugin-check bed runner
// calls deploykit.PersistBedDeployOverrides itself, and check_bed_run sheds deploykit. This is the
// boundary-law "host-boundary-object" trap's legal form: the shed is BLOCKED until coneC's envelope
// lands (in-flight), NOT a permanence argument — when the envelope lands, this MUST shed.
func persistBedDeployOverrides(name string, node spec.BundleNode) {
	deploykit.PersistBedDeployOverrides(name, node, bedExternalInPlace(node.Target), marshalDeployNode)
}
