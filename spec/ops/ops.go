// Package ops (github.com/opencharly/spec/ops, #55 import-purity) holds the operation
// selectors (the op.Op / InvokeRequest.Op wire value), the ResultJSON reply builder
// every out-of-process check verb uses, and InvokeProviderOpts — the optional extras to
// an Executor.InvokeProvider peer-dispatch call. It is a fabric slice of the spec
// contract module — pure Go constants + two tiny helpers over spec/proto, no heavy deps
// (#55 Rule 2) — relocated from the github.com/opencharly/sdk root package. This is the
// SINGLE SOURCE for the selectors (R3): charly's package main aliases these, and an
// out-of-tree / compiled-in plugin's Invoke dispatch compares req.GetOp() against them —
// so a kind candy checks ops.OpLoad, a step/deploy candy ops.OpEmit/ops.OpExecute, a
// builder candy ops.OpResolve. charly core imports this slice INSTEAD of the sdk root;
// the sdk root keeps a thin re-export during cutover then is deleted.
package ops

import (
	"encoding/json"

	pb "github.com/opencharly/spec/proto"
	"github.com/opencharly/spec/spec"
)

// Operation selectors (the op.Op / InvokeRequest.Op wire value). Each provider class uses
// the subset it needs. This is the SINGLE SOURCE for the selectors (R3): charly's package
// main aliases these (provider.go), and an out-of-tree / compiled-in plugin's Invoke
// dispatch compares req.GetOp() against them — so a kind candy checks ops.OpLoad, a
// step/deploy candy ops.OpEmit/ops.OpExecute, a builder candy ops.OpResolve.
const (
	OpRun      = "run"      // verb: run a check / live-container probe → CheckResult
	OpLoad     = "load"     // kind: decode a node into its typed entity
	OpValidate = "validate" // kind: closed/concrete CUE validation → Diagnostics
	OpEmit     = "emit"     // deploy/step: emit an InstallPlan / Containerfile fragment
	OpExecute  = "execute"  // deploy/step: execute against a venue (streamed)
	OpResolve  = "resolve"  // builder: resolve a builder image + steps (build-time multi-stage)
	OpBuild    = "build"    // build: dispatch the image-build / generate engine host-side (F10 HostBuild seam)

	// OpCompile is the K4-B deploy-COMPILE selector (command:fleet): the host's
	// deployAddCmd.compileNodePlans computes the per-node selection and Invokes the
	// command:fleet plugin's OpCompile with a spec.DeployCompileRequest; the plugin
	// re-hydrates the resolved-project envelope (InvokeProvider("build","project", OpResolve) —
	// the former HostBuild("resolved-project") seam is DELETED) +
	// loops deploykit.BuildDeployPlan, returning []spec.InstallPlanView the host
	// re-materializes. A generic action selector (never a provider word — F11).
	OpCompile = "compile"

	// OpCollectContext + OpReverse are the DEPLOY-TIME builder-IR legs of an externalized
	// detection-builder plugin (cargo/npm/pixi/aur). A builder's build-time multi-stage is
	// resolved by its OpResolve leg (C10); these two carry the per-builder deploy-time IR
	// shim — the stage-context the compiler records on a BuilderStep + that step's teardown
	// ops — out-of-process. BOTH are invoked HOST-SIDE in the build PRE-PASS (BEFORE the pure
	// BuildDeployPlan compile reads the result), never inside the pure compiler.
	OpCollectContext = "collect-context" // builder: per-candy stage-context keys → BuilderCollectReply
	OpReverse        = "reverse"         // builder: teardown ops for a resolved stage context → BuilderReverseReply

	// F6 — the SUBSTRATE LIFECYCLE selectors (host→plugin on Provider.Invoke): a deploy
	// substrate plugin brings its OWN host-side venue lifecycle. PrepareVenue/VenueExecutor
	// return a VenueDescriptor the HOST re-materializes into a real DeployExecutor (the live
	// executor never crosses the wire); the rest carry name/node/opts in, error/spec.DeployTargetStatus out.
	OpPrepareVenue     = "prepare-venue"     // lifecycle: build the venue → VenueDescriptor (re-materialized host-side)
	OpArtifactKey      = "artifact-key"      // lifecycle: the per-deploy artifact ledger key
	OpPostApply        = "post-apply"        // lifecycle: post-walk finalize on the venue
	OpTeardownExecutor = "teardown-executor" // lifecycle: the executor for Del → VenueDescriptor
	OpPostTeardown     = "post-teardown"     // lifecycle: drop venue artifacts (image/domain)
	OpStart            = "start"             // lifecycle: start the venue
	OpStop             = "stop"              // lifecycle: stop the venue
	OpStatus           = "status"            // lifecycle: venue status → spec.DeployTargetStatus
	OpLogs             = "logs"              // lifecycle: stream venue logs
	OpShell            = "shell"             // lifecycle: NON-interactive in-container exec CAPTURE (charly service — output-in-reply); interactive shell is OpAttach
	OpAttach           = "attach"            // F12 lifecycle: LIVE-STDIO attach — charly shell (-it TTY) + charly cmd (-i, stdin piped). The plugin exec.RunInteractive's a host-resolved #PodLiveStdioPlan.script; the host reverse-server holds the operator's terminal (stdio never crosses the wire)
	OpRebuild          = "rebuild"           // lifecycle: rebuild the venue (charly update)

	// OpConfigWrite is the POD config-WRITE selector (P11, Q1=(a)): the HOST `charly config`
	// command resolves the full QuadletConfig + the host-side target paths and Invokes the
	// deploy:pod plugin's OpConfigWrite with a spec.PodConfigWriteRequest; the plugin renders the
	// .container/.pod/sidecar/tunnel file CONTENTS (deploykit.GenerateQuadlet + the pod/sidecar/
	// tunnel generators) and os.WriteFiles them at the exact modes (same-host, compiled-in),
	// returning the written paths. RESOLVE + host side-effects (secret provisioning, saveDeployState,
	// enc-mount, data-seed, systemctl) stay in the host command — the plugin owns only the
	// config-WRITE (Ruling C). Distinct from the venue-lifecycle Ops: host-initiated, not a deploy.
	OpConfigWrite = "config-write"

	// OpConfigSetup / OpConfigRemove are the P13-KERNEL config-BODY selectors: the deploy:pod
	// plugin's Invoke handles these carrying #PodConfigSetupRequest / #PodConfigRemoveRequest
	// VERBATIM as Params — the direction-flip counterpart of OpConfigWrite (which stayed
	// host-initiated/plugin-rendered). The former "pod-config-setup"/"pod-config-remove" HostBuild
	// forwarders are DELETED (K-wave 2 cone R3): candy/plugin-pod's config leaves dispatch these ops
	// peer-to-peer via InvokeProvider (the pattern candy/plugin-fleet/from_box_pod.go proves),
	// threading HostEnvJSON as DATA on the OpRun envelope. The plugin runs the ported
	// BoxConfigSetupCmd/BoxConfigRemoveCmd orchestration in its own code.
	OpConfigSetup  = "config-setup"
	OpConfigRemove = "config-remove"

	OpStatusCollect = "status-collect" // command:status: programmatic status collection → []spec.DeploymentStatus (distinct from lifecycle OpStatus)

	// OpStatusCollectAll is the K6 whole-subsystem status FAN-OUT + deploy-cone ENRICHMENT
	// selector: verb:status-fanout (candy/plugin-substrate) serves it, taking a
	// spec.StatusSubstrateRequest and returning a spec.StatusSubstrateReply — the SAME wire
	// shape the "status-substrate" HostBuild seam already carries. The host's thin forward
	// (charly/status_substrate_host.go) is pure dispatch (no status business logic); the
	// plugin owns the fan-out (calling its own per-word OpStatusCollect handlers directly, an
	// in-package call — no registry needed for that leg), the pod/vm deploy-cone enrichment
	// (kit.ExtractMetadata/kit.ResolveBoxName/deploykit.QuadletDir/deploykit.
	// ResolveBoxEngineForDeploy — all sdk-portable), and the sort. Distinct from
	// OpStatusCollect (the single-word per-substrate collector op the SAME provider ALSO
	// serves on kind:pod/vm/kubernetes/local/android).
	OpStatusCollectAll = "status-collect-all"

	// OpPreresolve is the generalized host-side deploy preresolver (F6): a substrate plugin
	// declares a preresolve step the host runs BEFORE apply, returning the opaque JSON the host
	// ships in DeployVenue.Substrate (the wire-backed generalization of the in-core kubernetes/android
	// preresolvers).
	OpPreresolve = "preresolve"

	// OpBootstrap is the BOOTSTRAP-PHASE hook (F9): the kernel invokes a Phase=="bootstrap"
	// plugin BEFORE config validation, passing the RAW project config bytes
	// (params {"config": <bytes>}) and applying any transformed bytes the plugin returns
	// (reply {"config": <bytes>}) — a generic pre-validation transform hook (a no-op bootstrap
	// plugin returns the bytes unchanged). It is NOT the migration path: config-schema migration
	// is candy/plugin-migrate's command:migrate over OpRun (a whole-project file-walk that runs
	// on the config exactly when it cannot load), never a raw-byte bootstrap transform. Bootstrap
	// plugins are COMPILED-IN (in-proc), so this hook never re-enters the validated-config load.
	OpBootstrap = "bootstrap"

	// OpPreflight is the PREFLIGHT-PHASE hook (K5 seam-death of charly/main_freshness.go): the
	// kernel invokes every Phase=="preflight" plugin ONCE per CLI invocation, right after Kong
	// parses the command line and BEFORE dispatching to ANY command (main.go, before ctx.Run()) —
	// earlier than OpBootstrap, which fires only when a project actually loads. Params carry the
	// two host-only facts a compiled-in preflight plugin cannot compute itself (it CAN read
	// os.Args/os.Getwd/os.Executable directly, being in-process, but cannot call package-main's
	// CharlyVersion() — no package may import "main"): the parsed verb path and the binary's
	// stamped CalVer version. A refusing reply's Message is printed to stderr and the process
	// exits 1 — this is a HARD gate, not a data transform like OpBootstrap. Preflight plugins are
	// COMPILED-IN ONLY (same reasoning as bootstrap: they must run before any out-of-process
	// plugin could even be discovered).
	OpPreflight = "preflight"

	// OpEphemeralRegister / OpEphemeralTeardown are the command:fleet EPHEMERAL-LIFECYCLE
	// selectors (FINAL/K5 unit 6a): the host Invokes these as the first action of an ephemeral
	// deploy's Add and the last action of its Del, mirroring OpCompile's host→plugin dispatch
	// shape (a generic action selector, never a provider word — F11).
	OpEphemeralRegister = "ephemeral-register"
	OpEphemeralTeardown = "ephemeral-teardown"

	// OpDeployDispatch is the command:fleet S3b selector: the ONE generic host→plugin envelope
	// every UnifiedDeployTarget/LifecycleTarget method (Add/Update/Del/Start/Stop/Status/Logs/
	// Shell/Attach/Rebuild — Test DELETED, #55 W3 B3 remainder) dispatches through, discriminated by
	// spec.DeployTargetDispatchRequest.Op — a generic action selector (never a provider word,
	// F11), mirroring OpCompile's shape but carrying TEN former methods through ONE wire pair
	// instead of ten (R3 — the project rulebook's "generic over ad-hoc"). Core's thin
	// ResolveTarget proxy (charly/unified_targets.go) Invokes this; candy/plugin-fleet's handler
	// switches on Op and reaches the ACTUAL deploy-substrate provider (pod/vm/local/kubernetes/android)
	// via its OWN sdk.Executor.InvokeProvider (S1) — core never talks to the substrate directly
	// once this lands.
	OpDeployDispatch = "deploy-dispatch"

	// OpVerifyChecks is the command:check selector for the DEPLOY-VERIFY drive (#55 CHECK-ENGINE
	// cone, Unit 2): the host threads a live venue executor (flattened to a spec.VenueDescriptor)
	// over the in-proc reverse channel and asks the COMPILED-IN command:check to run a deploy-scope
	// check pass PLUGIN-SIDE — the deploy-lifecycle Test path (spec.VerifyChecksRequest.Ops → a raw
	// kit.Runner.Run pass) and the `target: local` --verify path (spec.VerifyChecksRequest.Plan → a
	// kit.RunPlan pass, plugin-rebuilt env/host-vars/target-resolver). This is what sheds charly
	// core's checkrun.go + planrun_adapter.go sdk/kit imports (the in-proc check-runner construction
	// moved plugin-side). The reply is the sanctioned sdk/kit []StepResult wire. A generic drive
	// selector, never a provider word (F11).
	OpVerifyChecks = "verify-checks"

	// OpResolveEndpoint/OpResolveImageLabel/OpDrainEndpointCleanups are verb:check-resolve
	// selectors (#55 W3 B7) discriminating its THREE capabilities — venue classification
	// (OpResolve, unchanged) plus the two relocated CheckContext reverse-RPC resolution
	// bodies + their shared cleanup-drain signal. See spec/schema/seam.cue's
	// #CheckEndpointResolveRequest/#CheckImageLabelResolveRequest/
	// #CheckDrainEndpointCleanupsRequest for the full placement rationale.
	OpResolveEndpoint       = "resolve-endpoint"
	OpResolveImageLabel     = "resolve-image-label"
	OpDrainEndpointCleanups = "drain-endpoint-cleanups"

	// EphemeralPanicMarker prefixes an error the command:fleet plugin converts from a
	// RECOVERED PANIC inside OpEphemeralRegister/OpEphemeralTeardown (RCA #5, FINAL/K5 unit 6a —
	// a nil-map write panic in persistEphemeralRuntime was previously UNRECOVERED and vanished
	// silently: the deploy still reported PASS). A STRING marker (not a typed Go error) because
	// this classification must survive the Provider.Invoke wire boundary dual-placement demands
	// stay portable — command:fleet is compiled-in TODAY, but the SAME code must behave
	// identically if ever served out-of-process, where only a string error crosses gRPC. The
	// host-side caller (charly's registerEphemeralIfMarked) checks for this marker to distinguish
	// a PANIC (a genuine bug — must FAIL the whole deploy Add, never silently continue) from an
	// ORDINARY registration error (e.g. systemd-run missing — an expected condition that stays a
	// soft, logged warning, unchanged).
	EphemeralPanicMarker = "ephemeral op panic:"
)

// resultWire is the {status,message} wire form every out-of-process check verb returns (the
// host's pluginCheckResult). status ∈ "pass" | "fail" | "skip".
type resultWire struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ResultJSON builds the InvokeReply an out-of-process check verb's Invoke returns — the SAME
// {status,message} shape every verb plugin (and ServeCheckVerb) emits (R3).
func ResultJSON(status, msg string) (*pb.InvokeReply, error) {
	j, err := json.Marshal(resultWire{Status: status, Message: msg})
	if err != nil {
		return nil, err
	}
	return &pb.InvokeReply{ResultJson: j}, nil
}

// InvokeProviderOpts carries the OPTIONAL extras to an InvokeProvider peer-dispatch call. The zero
// value is byte-identical to the pre-S1 behavior: no venue descriptor, so the host threads the
// CALLING plugin's own enclosing executor (if any) onto the target — exactly as before this field
// existed.
type InvokeProviderOpts struct {
	// VenueDescriptor optionally supplies a SELF-DESCRIBED venue (S1 — the
	// venue-scoped-executor-session seam): the host re-materializes it into a FRESH DeployExecutor
	// and threads THAT onto the target's InvokeWithExecutor instead of the caller's own executor.
	// Use this when the calling plugin holds no enclosing executor of its own (e.g. a verb/kind
	// Invoke with no deploy-context broker) but still wants the target Invoked WITH a live venue.
	// Nil (the default) — no descriptor; the caller's own executor, if any, is forwarded unchanged.
	VenueDescriptor *spec.VenueDescriptor

	// ExtraRef optionally supplies a canonical candy ref (S3b — the Pass-2 lazy-connect gap) for
	// the host's S2 lazy-connect fallback: connectPluginByWordRef(class, word, ExtraRef). Empty
	// (the default) only ever reaches Pass-1 (the calling project's own candy closure) — a target
	// declared nowhere in that closure but resolvable via an explicit @github canonical ref (the
	// same Pass-2 fetch the credential/vm/kube host adapters already use) needs this set.
	ExtraRef string
}
