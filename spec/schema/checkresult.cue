// CUE schema for the check-engine's per-step VERDICT envelope (FLOOR-SLIM Unit 4). NOT an
// authoring kind (never in #Node/#Op) — a pure generated wire/render struct, single-sourced
// here so `task cue:gen` produces the Go struct charly core's registry-coupled floor files
// (provider.go/provider_verb.go/verb_builtins.go/unified_targets.go/provider_checkenv.go)
// reference directly (spec.CheckResult), with zero new sdk/kit import.
//
// #CheckResult covers every field EXCEPT the engine-internal `DeadlineExceeded` retry
// signal — the ONE spike-proven exception (P12, cited at sdk/kit/checkrun_seam.go and
// sdk/schema/seam.cue): `DeadlineExceeded bool json:"-"` has no gengotypes construct (a
// field that exists in memory but is excluded from marshaling). sdk/kit.CheckResult embeds
// this generated type and adds ONLY that one hand-written field back.
//
// FLOOR-SLIM deliberately renames the wire keys to snake_case (Op→op, Verb→verb,
// Status→status, Message→message, Elapsed→elapsed) — the former hand-written type carried
// NO json tag on these fields, so encoding/json defaulted to the bare, inconsistent
// PascalCase Go field name. This is a documented, deliberate breaking wire-format fix for
// `--format json`/TAP consumers of `charly check box/live/run`, not an accident: it brings
// CheckResult in line with every other CUE-sourced wire type's snake_case convention. Every
// field that ALWAYS serialized before (op/verb/status/message/elapsed) stays REQUIRED (`!`)
// here so gengotypes omits `omitempty` — an omitempty regression would silently drop
// zero-valued fields from output, a SEPARATE wire change the rename must not introduce.
// Every field that already carried `,omitempty` (attempts/total_elapsed/captured_value)
// stays optional (`?`).
//
// Status is carried as a plain int (@go(Status,type=Status) — Status is the check-engine's
// pass/fail/skip enum, HAND-WRITTEN in spec/status_result.go: gengotypes has no construct
// for an iota-based enum + String() method, so CUE owns the wire VALUE SET (an int) and Go
// owns the formatting behavior (String()), mirroring the #SubstrateKind split (status.cue) —
// there the enum is string-backed and suppressed via @go(-); here it is int-backed and
// referenced directly since there is no separate disjunction def to suppress.
//
// Elapsed / TotalElapsed carry a nanosecond count wire-typed as time.Duration
// (@go(Elapsed,type=time.Duration)) — the RDD spike (T-P12, cited in /charly-internals:go)
// proved a custom-scalar @go(,type=…) override generates faithfully.
#CheckResult: {
	op!:             #Op    @go(Op, type=*Op)
	verb!:           string @go(Verb)
	status!:         int    @go(Status, type=Status)
	message!:        string @go(Message)
	elapsed!:        int    @go(Elapsed, type=time.Duration)
	attempts?:       int    @go(Attempts, type=int)
	total_elapsed?:  int    @go(TotalElapsed, type=time.Duration)
	captured_value?: string @go(CapturedValue)
}

// #CheckEnv is the SINGLE-SOURCED scalar snapshot of a check verb's invocation context (K1-unblock
// W3 Unit B) — the ONE #CheckEnv def now generating the struct all THREE of its consumers share
// (a hand-written mirror per consumer is the exact "wire type not CUE-generated" violation SDD
// forbids in fresh code): (1) charly/provider_checkenv.go's host-side CheckEnv, filled by
// snapshotCheckEnv from a live *kit.Runner and threaded to an out-of-process verb's Invoke
// envelope; (2) sdk's out-of-process verb-serve decode (sdk/checkverb.go), which reconstructs a
// kit.CheckContext's scalar legs from this exact snapshot; (3) candy/plugin-check's
// InvokeProvider-backed VerbResolver (verb_resolver.go), which marshals this same shape when
// asking the host to dispatch a verb on its behalf. A 4th consumer (charly/plugin_dispatch_reverse.go's
// InvokeProvider host handler) DECODES it host-side to construct a detached kit.CheckContext for
// a CheckVerbProvider target — the SAME snapshot, not a second shape.
//
// Every field is optional (a caller fills only what it has — an in-proc/live snapshot has a live
// Runner to read from; a box-mode run has no ContainerName/Venue; a detached construction may
// lack DialTimeoutNs). container_name/venue are HOST-COMPUTED-ONLY fields (never authored,
// carried for the out-of-process appium/vm-target verbs that need charly's naming convention
// without re-deriving it) — present on the wire regardless of which of the three marshal sites
// populates them, since all three now share this one shape.
#CheckEnv: {
	box?:             string   @go(Box)
	instance?:        string   @go(Instance)
	mode?:            string   @go(Mode) // "live" | "box"
	container_name?:  string   @go(ContainerName)
	distros?:         [...string] @go(Distros)
	venue?:           string   @go(Venue)      // r.Exec.Venue()
	venue_kind?:      string   @go(VenueKind)  // r.Exec.Kind()
	dial_timeout_ns?: int      @go(DialTimeoutNs, type=int64)
}

// #VerifyChecksRequest is the command:check OpVerifyChecks envelope (#55 CHECK-ENGINE cone,
// Unit 2): the host threads a live venue executor — flattened to #VenueDescriptor, since a live
// executor cannot cross the wire, and re-materialized PLUGIN-SIDE via kit.VenueFromDescriptor (the
// SAME mechanism candy/plugin-fleet's resolveRootExecutor uses) — over the in-proc reverse channel
// and asks the COMPILED-IN command:check to DRIVE a deploy-scope check pass PLUGIN-SIDE. This sheds
// charly core's checkrun.go + planrun_adapter.go sdk/kit imports (the in-proc kit.Runner
// construction moved plugin-side).
//
// ONE drive shape now: plan → the `target: local` --verify path (candy/plugin-fleet's
// verify_local.go, #55 W3 B3 — a PEER plugin now, not core): a PLUGIN-ASSEMBLED plan (kind:local
// template, resolved via node_resolve.go's lookupLocalTemplate — no LoadUnified — + deploy node;
// the per-host overlay merge happens on THIS side) driven via kit.RunPlan (verify-only/context/
// keyword gating). The plugin rebuilds the runtime env (USER/HOME/IMAGE/INSTANCE) + ${HOST:}
// host-vars + the cross-deployment TargetResolver from {dir, box, instance} — plugin-check ALREADY
// does this for check-live (verb_resolver.go / members.go), so those never cross the wire.
//
// The former SECOND drive shape (ops/only_ids — the deploy-lifecycle Test path, charly core's
// unified_targets.go runUnifiedTargetChecks feeding raw deploy-scope Op checks via kit.Runner.Run,
// no plan gating) is GONE (#55 W3 B3 remainder): its own sole production caller,
// pluginDeployTarget.Test (UnifiedDeployTarget's Test method), had ZERO real callers anywhere in
// the tree — `charly check live` reaches candy/plugin-check directly (live_gather.go) and never
// touches this interface method; the ONE caller was a unit test. Test()/runUnifiedTargetChecks/
// TestOpts (charly), verifyChecksRunOps/filterOpsByID (candy/plugin-check), and the dead "test" op
// in #DeployTargetDispatchRequest's enum are all deleted together. The box-mode context-skip
// regression coverage (TestLiveVerb_SkipsUnderBoxMode, charly/checkrun_charly_verbs_test.go) moved
// onto the surviving plan shape — RunOne (sdk/kit/planrun.go) is the SAME per-step primitive both
// kit.Runner.Run and kit.RunPlan dispatched through, so the context-vs-mode gate (opInContext) is
// identically exercised either way; no coverage was lost.
//
// The reply is []#StepResult (CUE-sourced in this file) — CONSUMED, not modified. All plain fields
// (plan/venue are spec envelope types; StepResult.Result is #CheckResult by value) →
// gengotypes-faithful, no @go(-).
#VerifyChecksRequest: {
	plan?:        [...#Step]        @go(Plan)
	mode?:        string            @go(Mode) // "live" | "box"
	box?:         string            @go(Box)
	instance?:    string            @go(Instance)
	verify_only?: bool              @go(VerifyOnly)
	dir?:         string            @go(Dir)
	venue?:       #VenueDescriptor  @go(Venue)
}

// #StepResult is one plan step's outcome — the step's identity (keyword/text/origin/step_id) plus
// the CheckResult of running it; the result reporters consume a []StepResult. CUE-sourced (SDD):
// a plain carrier — every field is a string or the CUE-sourced #CheckResult by value, with NO
// json:"-" and NO disjunction, so gengotypes generates it faithfully (byte-identical JSON to the
// former hand-written spec.StepResult). The P12 gengotypes exception (cited at
// sdk/kit/checkrun_seam.go + schema/seam.cue) covers ONLY kit.CheckResult's engine-internal
// `DeadlineExceeded bool json:"-"` — a field that exists in memory but is excluded from
// marshaling — which lives on the kit-internal ENGINE wrapper (struct { spec.CheckResult;
// DeadlineExceeded bool json:"-" }), NOT on this wire type: StepResult.Result is spec.CheckResult
// (the CUE-sourced base, no json:"-"), so the wire form carries only spec.CheckResult fields and
// the kit wrapper adds DeadlineExceeded in memory only, dropped at the StepResult boundary.
#StepResult: {
	keyword!: string      @go(Keyword)
	text!:    string      @go(Text)
	origin?:  string      @go(Origin)
	step_id!: string      @go(StepID)
	result!:  #CheckResult @go(Result)
}

// #CheckRunReply is the plugin-resolved result of a check-run. Steps is the per-step verdict list the
// plugin formats (FormatStepResults*) and tallies into an exit code. Image is the resolved image ref
// for the "Image: <ref>" header line. NoSteps signals the image declared no plan (the plugin prints
// "No plan steps defined for this image." and exits 0) — distinct from an empty Steps that ran zero
// scored steps. Header is the pre-formatted, kind-specific banner line the plugin builds (container
// name, ssh user/host/port, member count) — the plugin prints it, then the formatted Steps.
// Passthrough carries the one non-plan-run
// live path — a nested pod-in-VM leaf whose check is delegated to the guest over SSH,
// forwarded verbatim; nil for every plan-run mode. Score is the "score"-mode reply (the AI-harness
// SCORING result, #CheckRunResults), nil for the box/live/feature plan-run modes that carry their
// verdicts in Steps. CUE-sourced (SDD): plain carrier, no json:"-".
#CheckRunReply: {
	steps?:        [...#StepResult] @go(Steps)
	image?:        string           @go(Image)
	no_steps?:     bool             @go(NoSteps)
	header?:       string           @go(Header)
	passthrough?: #StepPass         @go(Passthrough,optional=nillable)
	score?:        #CheckRunResults  @go(Score,optional=nillable)
}

// #StepPass is the verbatim stdout/stderr/exit-code of a host-delegated guest sub-invocation (the
// nested pod-in-VM check-live delegation, runVm's guestNestedCheckCmd path). The plugin writes
// Stdout/Stderr and returns ExitCode unchanged, so a guest-run check reports byte-identically to a
// direct one. CUE-sourced (SDD): plain carrier, no json:"-".
#StepPass: {
	stdout?:    string @go(Stdout)
	stderr?:    string @go(Stderr)
	exit_code?: int    @go(ExitCode, type=int)
}
