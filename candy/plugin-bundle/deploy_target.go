package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// deploy_target.go — the S3b move: the ORCHESTRATION bulk of the former core-resident
// deploy-target dispatcher and substrate lifecycle proxy (see CHANGELOG/2026.203.0212.md for the
// full migration narrative), ported here behind the ONE generic sdk.OpDeployDispatch
// selector. What did NOT move (per the Unit-6 design note's Q1-Q4 dispositions, verified against
// the actual call graph, not assumed):
//
//   - The arbiter acquire/release BRACKET (Start/Stop) — CHARLY_PREEMPT_LEASE is a process-env
//     mutex that only behaves correctly in the SAME OS process as the host, so a bare in-plugin
//     acquire/release would silently break the nested-subprocess env-inheritance skip for the
//     out-of-process placement. `charly/arbiter_bracket.go` keeps the call sites TODAY (MIGRATION
//     INVENTORY: UNTIL-K4, deploy-state/arbitration family — see that file's header), wrapping the
//     dispatch call this file's handleStart/handleStop make; the tracked exit is the SAME
//     HostBuild-reverse-leg pattern host_build_deploy_config_save_state.go already uses (Q2): a
//     HostBuild kind the plugin calls back for its own acquire/release, so the actual
//     os.Setenv/os.Getenv still run host-side while ownership of the bracket moves here.
//   - The pod/vm Start/Stop/Attach/Logs PLAN-HOOK tables (charly/pod_lifecycle_dispatch.go) — pure
//     ctx-opts marshals with zero core-only dependency of their own, so there is no reason to move
//     them; the core-side dispatch caller reads them and threads the result as this file's
//     OptsJSON request field.
//   - Secret injection (prepareCandySecrets) + artifact retrieval (retrieveArtifactsAndK3s) +
//     --verify (checkLocalDeployScope) — all three are K1/registry-family core siblings of
//     bundle_add_cmd.go with their own core-only dependencies; they stay core, wrapping THIS
//     file's dispatch call for the substrate-apply step alone.
//
// What DID move (the substrate-generic core): PrepareVenue, the views+venue marshal +
// InvokeProvider dispatch to the ACTUAL substrate provider (S1), recordDeploy (the ledger write —
// DistroCfg now travels as a plain marshalled field, not the core-only buildEngineContext
// wrapper), recordVenueLedger, prepareReverseState, Del's ledger-read+teardown+PostTeardown,
// ArtifactKey, PostApply, ready-to-dispatch Start/Stop/Status/Logs/Shell/Attach/Rebuild bodies.
//
// The type these methods used to hang off (the former core-resident deploy target / substrate
// lifecycle proxy) held a core-private *grpcProvider — a shape that CANNOT move here (core
// constructs it at plugin-CONNECT time, a clause-M mechanism). So every function below is
// constructed fresh, per dispatch call,
// from PLAIN DATA (word, name, node) + the *sdk.Executor this Invoke was given — never a stored
// registry object. Reaching the ACTUAL substrate provider (candy/plugin-deploy-pod, -vm, -local,
// candy/plugin-kube, candy/plugin-adb) goes through exec.InvokeProvider (S1) — placement-agnostic,
// whether that substrate is compiled-in or (today, always) out-of-process.

// runDeployDispatch serves command:bundle's Invoke(OpDeployDispatch) — the ONE generic envelope
// every former UnifiedDeployTarget/LifecycleTarget method dispatches through, discriminated by
// req.Op.
func runDeployDispatch(ctx context.Context, ireq *pb.InvokeRequest) (*pb.InvokeReply, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, ireq.GetExecutorBrokerId())
	if err != nil {
		return nil, fmt.Errorf("bundle deploy-dispatch: reach host reverse channel: %w", err)
	}
	var req spec.DeployTargetDispatchRequest
	if err := json.Unmarshal(ireq.GetParamsJson(), &req); err != nil {
		return nil, fmt.Errorf("bundle deploy-dispatch: decode request: %w", err)
	}
	var reply spec.DeployTargetDispatchReply
	switch req.Op {
	case "add":
		reply, err = handleDeployApply(ctx, exec, req, false)
	case "update":
		reply, err = handleDeployApply(ctx, exec, req, true)
	case "del":
		reply, err = handleDeployDel(ctx, exec, req)
	case "start":
		reply, err = handleLifecycleSimple(ctx, exec, req, sdk.OpStart)
	case "stop":
		reply, err = handleLifecycleSimple(ctx, exec, req, sdk.OpStop)
	case "status":
		reply, err = handleDeployStatus(ctx, exec, req)
	case "logs":
		reply, err = handleLifecycleSimple(ctx, exec, req, sdk.OpLogs)
	case "shell":
		reply, err = handleDeployExec(ctx, exec, req, sdk.OpShell)
	case "attach":
		reply, err = handleDeployExec(ctx, exec, req, sdk.OpAttach)
	case "rebuild":
		reply, err = handleDeployRebuild(ctx, exec, req)
	default:
		return nil, fmt.Errorf("bundle deploy-dispatch: unknown op %q", req.Op)
	}
	if err != nil {
		return nil, err
	}
	replyJSON, jerr := json.Marshal(reply)
	if jerr != nil {
		return nil, fmt.Errorf("bundle deploy-dispatch %s: marshal reply: %w", req.Op, jerr)
	}
	return &pb.InvokeReply{ResultJson: replyJSON}, nil
}

// --- shared wire helpers (ported verbatim from substrate_lifecycle_grpc.go) -----------------

// marshalDeployOpParams marshals the common (name, dir, node, extra) args for a plugin→substrate
// deploy Op (the lifecycle Ops + the preresolver, R3 — mirrors the pre-move core function
// byte-for-byte). node MUST be stored as json.RawMessage, never a plain []byte: encoding/json
// base64-encodes an untyped []byte value found inside a map[string]any (its dynamic type has no
// MarshalJSON method), silently mangling "node" into an opaque base64 STRING instead of an
// embedded JSON object — exactly the R10 bed-found bug (a nested vm child's `From` read back
// empty: the receiving substrate's json.Unmarshal(p.Node, &node) failed on the type mismatch and
// had its error discarded, leaving node at its zero value). The pre-move core helper this mirrors
// used marshalJSON (returning json.RawMessage), never plain json.Marshal, for exactly this reason.
func marshalDeployOpParams(name, dir string, node *spec.Deploy, extra map[string]any) (json.RawMessage, error) {
	params := map[string]any{"name": name}
	if dir != "" {
		params["dir"] = dir
	}
	if node != nil {
		nj, err := json.Marshal(node)
		if err != nil {
			return nil, err
		}
		params["node"] = json.RawMessage(nj)
	}
	for k, v := range extra {
		params[k] = v
	}
	return json.Marshal(params)
}

// lifecycleInvoke reaches the ACTUAL substrate provider (word) via exec.InvokeProvider (S1),
// optionally overriding the venue with venueDesc (nil — the default forwarded caller executor,
// used only when this Invoke's own broker executor is already the right one; non-nil — a FRESH
// venue the host re-materializes, used once PrepareVenue has produced one this call must reuse for
// every SUBSEQUENT substrate Op). hostEnv is the marshalled spec.HostEnv core computed for THIS
// dispatch call (charly/unified_targets.go's hostEnvJSON, R10 bed bug #5) — forwarded verbatim,
// never recomputed here: only core reliably knows its OWN binary path regardless of ANY plugin's
// placement (os.Executable() called plugin-side would resolve to the PLUGIN binary for an
// out-of-process placement).
func lifecycleInvoke(ctx context.Context, exec *sdk.Executor, word, op, name, dir string, node *spec.Deploy, extra map[string]any, venueDesc *spec.VenueDescriptor, hostEnv json.RawMessage) ([]byte, error) {
	pj, err := marshalDeployOpParams(name, dir, node, extra)
	if err != nil {
		return nil, err
	}
	opts := sdk.InvokeProviderOpts{}
	if venueDesc != nil {
		opts.VenueDescriptor = venueDesc
	}
	return exec.InvokeProvider(ctx, "deploy", word, op, pj, hostEnv, opts)
}

// venueDescriptorFromExecutor reports a hookless substrate's ROOT executor back to core in the
// dispatch reply (so core re-materializes the SAME venue for --verify) — a thin forward to the
// shared kit.DescriptorFromExecutor. Promoted there (FIX ROUND, S3b follow-up) once a SECOND
// caller needed this exact conversion: charly/unified_targets.go's pluginDeployTarget.Add now
// converts a NESTED child's live ParentExec into venue_json before it crosses the wire (the
// missing swap that made every plain-vm nested `local:`/`android:`/`k8s:` child apply on the
// operator's host instead of the parent venue) — so the former single-caller "R3-narrow"
// duplicate is no longer accurate; one function serves both directions' callers (R3).
func venueDescriptorFromExecutor(exec deploykit.DeployExecutor) spec.VenueDescriptor {
	return kit.DescriptorFromExecutor(exec)
}

// resolveRootExecutor returns the LOCAL executor value this dispatch call should use for
// non-substrate-provider work (prepareReverseState, recordVenueLedger) + the matching descriptor
// to report back to core. A hookless substrate (local/k8s/android) computes it directly from the
// node (deploykit.RootExecutorForDeployNode — already 100% sdk-portable, no duplication); a
// lifecycle substrate (pod/vm) instead re-materializes whatever PrepareVenue (or a prior call's
// reported venue_json) produced.
func resolveRootExecutor(req spec.DeployTargetDispatchRequest) (deploykit.DeployExecutor, spec.VenueDescriptor, error) {
	if len(req.VenueJSON) > 0 {
		var d spec.VenueDescriptor
		if err := json.Unmarshal(req.VenueJSON, &d); err != nil {
			return nil, spec.VenueDescriptor{}, fmt.Errorf("decode venue descriptor: %w", err)
		}
		exec, err := kit.VenueFromDescriptor(d)
		if err != nil {
			return nil, spec.VenueDescriptor{}, err
		}
		if exec != nil {
			return exec, d, nil
		}
	}
	exec, err := deploykit.RootExecutorForDeployNode(req.Node)
	if err != nil {
		return nil, spec.VenueDescriptor{}, err
	}
	return exec, venueDescriptorFromExecutor(exec), nil
}

// --- Add / Update — the shared "apply" body -------------------------------------------------

// handleDeployApply is the substrate-generic core of Add/Update (mirrors the former core-resident
// deploy target's apply body + substrate lifecycle proxy's PrepareVenue). Secret injection,
// artifact retrieval, and --verify STAY
// core-side (see the file header) — this handles ONLY the substrate-apply step: PrepareVenue (for
// a lifecycle substrate), the views+venue marshal, the actual substrate dispatch, recordDeploy,
// recordVenueLedger.
func handleDeployApply(ctx context.Context, exec *sdk.Executor, req spec.DeployTargetDispatchRequest, isUpdate bool) (spec.DeployTargetDispatchReply, error) {
	var reply spec.DeployTargetDispatchReply
	// Decode directly into the wire-safe spec.LifecycleOpts (R10 bed fix, S3b) — NEVER
	// deploykit.EmitOpts, whose live ParentExec/ParentNode interface/pointer fields cannot
	// cross the []byte wire; core (unified_targets.go's Add/Update) already projects onto this
	// exact shape before marshaling (spec.LifecycleOptsFromEmit), so no plugin-side conversion
	// is needed — opts IS the shape both handleDeployApply and its lifecycle sub-dispatches need.
	var opts spec.LifecycleOpts
	if len(req.OptsJSON) > 0 {
		if err := json.Unmarshal(req.OptsJSON, &opts); err != nil {
			return reply, fmt.Errorf("deploy-dispatch %s: decode opts: %w", req.Op, err)
		}
	}

	var views []spec.InstallPlanView
	if len(req.PlansJSON) > 0 {
		if err := json.Unmarshal(req.PlansJSON, &views); err != nil {
			return reply, fmt.Errorf("deploy-dispatch %s: decode plans: %w", req.Op, err)
		}
	}
	plans := make([]*deploykit.InstallPlan, 0, len(views))
	for _, v := range views {
		p, err := deploykit.PlanFromView(v)
		if err != nil {
			return reply, fmt.Errorf("deploy-dispatch %s: rematerialize plan: %w", req.Op, err)
		}
		plans = append(plans, p)
	}

	dryRun := opts.DryRun
	var venueDesc spec.VenueDescriptor
	var localExec deploykit.DeployExecutor

	if !dryRun {
		if req.HasLifecycle {
			extra := map[string]any{"opts": opts}
			if req.Node != nil {
				extra["image"] = req.Node.Image
				extra["version"] = req.Node.Version
			}
			resJSON, err := lifecycleInvoke(ctx, exec, req.Word, sdk.OpPrepareVenue, req.Name, req.Dir, req.Node, extra, nil, req.HostEnvJSON)
			if err != nil {
				return reply, fmt.Errorf("deploy-dispatch %s: prepare venue: %w", req.Op, err)
			}
			var pvReply spec.PrepareVenueReply
			if len(resJSON) > 0 {
				if err := json.Unmarshal(resJSON, &pvReply); err != nil {
					return reply, fmt.Errorf("deploy-dispatch %s: decode prepare-venue reply: %w", req.Op, err)
				}
			}
			for _, note := range pvReply.Notes {
				fmt.Println(note)
			}
			if len(pvReply.State) > 0 {
				if err := persistDeployState(ctx, exec, req.Name, pvReply.State); err != nil {
					return reply, err
				}
			}
			venueDesc = pvReply.Venue
			localExec, err = kit.VenueFromDescriptor(venueDesc)
			if err != nil {
				return reply, fmt.Errorf("deploy-dispatch %s: materialize prepared venue: %w", req.Op, err)
			}
		}
		if localExec == nil {
			var err error
			localExec, venueDesc, err = resolveRootExecutor(req)
			if err != nil {
				return reply, fmt.Errorf("deploy-dispatch %s: resolve root executor: %w", req.Op, err)
			}
		}
		if venueJSON, err := json.Marshal(venueDesc); err == nil {
			reply.VenueJSON = venueJSON
		}

		if err := prepareReverseState(ctx, localExec, plans); err != nil {
			return reply, fmt.Errorf("deploy-dispatch %s: %w", req.Op, err)
		}
		// Re-marshal the views AFTER prepareReverseState's home-resolution + stateful-reverse
		// capture (matching the pre-move ordering exactly).
		views = views[:0]
		for _, p := range plans {
			views = append(views, deploykit.WireView(p))
		}
	}

	viewsJSON, err := json.Marshal(views)
	if err != nil {
		return reply, fmt.Errorf("deploy-dispatch %s: marshal plans: %w", req.Op, err)
	}
	venue := spec.DeployVenue{DeployName: req.Name, Env: deploykit.BuildArtifactEnv(nil, req.Node)}
	if !dryRun && req.HasPreresolve {
		payload, perr := preresolveSubstrate(ctx, exec, req)
		if perr != nil {
			return reply, fmt.Errorf("deploy-dispatch %s: preresolve substrate %q: %w", req.Op, req.Word, perr)
		}
		venue.Substrate = payload
	}
	envJSON, err := json.Marshal(venue)
	if err != nil {
		return reply, fmt.Errorf("deploy-dispatch %s: marshal venue: %w", req.Op, err)
	}

	if dryRun {
		fmt.Printf("[dry-run] external deploy %s (target=%s): would apply %d plan(s) via the reverse channel\n",
			req.Name, req.Word, len(views))
		return reply, nil
	}

	invokeOpts := sdk.InvokeProviderOpts{VenueDescriptor: &venueDesc}
	resJSON, err := exec.InvokeProvider(ctx, "deploy", req.Word, sdk.OpExecute, viewsJSON, envJSON, invokeOpts)
	if err != nil {
		return reply, fmt.Errorf("deploy-dispatch %s: %w", req.Op, err)
	}
	var dreply spec.DeployReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &dreply); err != nil {
			return reply, fmt.Errorf("deploy-dispatch %s: decode reply: %w", req.Op, err)
		}
	}

	if err := recordDeploy(req.Name, req.Word, req.DistroCfgJSON, req.LedgerRoot, dreply); err != nil {
		return reply, err
	}
	if err := recordVenueLedger(localExec, plans, req.Name, req.Word, req.LedgerRoot); err != nil {
		return reply, err
	}

	// ArtifactKey (mirrors the former core-resident deploy target's Add post-apply key lookup —
	// independent of PrepareVenue's live venue, runs on a plain ShellExecutor{} like the pre-move
	// code).
	if req.HasLifecycle {
		akJSON, err := lifecycleInvoke(ctx, exec, req.Word, sdk.OpArtifactKey, req.Name, "", req.Node, nil, nil, req.HostEnvJSON)
		if err == nil && len(akJSON) > 0 {
			var out struct {
				Key string `json:"key"`
			}
			if json.Unmarshal(akJSON, &out) == nil {
				reply.ArtifactKey = out.Key
			}
		}
	}

	// PostApply (skipped under --node-only, exactly like the pre-move code).
	if req.HasLifecycle && !req.NodeOnly {
		_, err := lifecycleInvoke(ctx, exec, req.Word, sdk.OpPostApply, req.Name, req.Dir, req.Node,
			map[string]any{"opts": opts}, &venueDesc, req.HostEnvJSON)
		if err != nil {
			return reply, fmt.Errorf("deploy-dispatch %s: post-apply: %w", req.Op, err)
		}
	}

	return reply, nil
}

// persistDeployState routes a PrepareVenue reply's opaque State patch through the
// "deploy-config-save-state" HostBuild seam (Q2, S3b — reused + renamed from
// "pod-config-save-deploy-state": deploykit.SaveDeployState has a silent DeployStateHost==nil
// no-op guard set only by charly-core's own init(), so calling it directly from THIS process would
// silently drop the write; the seam runs it host-side, where the guard is always satisfied).
func persistDeployState(ctx context.Context, exec *sdk.Executor, name string, stateJSON json.RawMessage) error {
	var in deploykit.SaveDeployStateInput
	if err := json.Unmarshal(stateJSON, &in); err != nil {
		return fmt.Errorf("decode prepare-venue state: %w", err)
	}
	boxKey, instKey := deploykit.ParseDeployKey(name)
	inputJSON, err := json.Marshal(in)
	if err != nil {
		return err
	}
	reqJSON, err := json.Marshal(spec.DeployConfigSaveStateRequest{Box: boxKey, Instance: instKey, InputJSON: inputJSON})
	if err != nil {
		return err
	}
	_, err = exec.HostBuild(ctx, "deploy-config-save-state", reqJSON)
	return err
}

// preresolveSubstrate reaches an android/k8s substrate's own OpPreresolve (F1/F6) — the
// substrate-specific host-resolved venue payload a hookless-but-preresolving substrate needs
// (mirrors deploy_preresolve.go's wireDeployPreresolver, relocated here since the CALLER — the
// generic apply body — is now plugin-side).
func preresolveSubstrate(ctx context.Context, exec *sdk.Executor, req spec.DeployTargetDispatchRequest) (json.RawMessage, error) {
	var views []spec.InstallPlanView
	var extra map[string]any
	if len(req.PlansJSON) > 0 {
		if json.Unmarshal(req.PlansJSON, &views) == nil && len(views) > 0 {
			extra = map[string]any{"plans": views}
		}
	}
	pj, err := marshalDeployOpParams(req.Name, req.Dir, req.Node, extra)
	if err != nil {
		return nil, err
	}
	return exec.InvokeProvider(ctx, "deploy", req.Word, sdk.OpPreresolve, pj, nil, sdk.InvokeProviderOpts{})
}

// prepareReverseState is the Fork-A pre-pass (mirrors the former core-resident deploy target's
// prepareReverseState exactly): resolve {{.Home}} + capture deploy-time-stateful reverse inputs on the LIVE local
// executor BEFORE the views are (re-)marshalled, so each step's host-computed Reverse() is
// faithful. Skipped when the plan carries neither a home-token step nor a ServicePackagedStep (the
// SAME guard the pre-move code used — an unconditional ResolveHome call is a live podman
// exec/ssh that hard-fails when the venue isn't reachable yet).
func prepareReverseState(ctx context.Context, exec deploykit.DeployExecutor, plans []*deploykit.InstallPlan) error {
	needsHome := false
	needsServiceProbe := false
	for _, p := range plans {
		if p == nil {
			continue
		}
		for _, step := range p.Steps {
			switch step.(type) {
			case *deploykit.ShellHookStep, *deploykit.ShellSnippetStep, *deploykit.FileStep:
				needsHome = true
			case *deploykit.ServicePackagedStep:
				needsServiceProbe = true
			}
		}
	}
	if !needsHome && !needsServiceProbe {
		return nil
	}
	var home string
	if needsHome {
		h, err := exec.ResolveHome(ctx, "")
		if err != nil {
			return fmt.Errorf("resolve venue home: %w", err)
		}
		home = h
	}
	for _, p := range plans {
		if p == nil {
			continue
		}
		if home != "" {
			deploykit.ResolveHome(p, home)
		}
		for _, step := range p.Steps {
			switch s := step.(type) {
			case *deploykit.ShellHookStep:
				if s.EnvFile == "" && home != "" {
					s.EnvFile = kit.EnvdFilePath(home, s.CandyName)
				}
			case *deploykit.ServicePackagedStep:
				s.PriorEnabled = venueUnitEnabled(ctx, exec, s.Unit, s.TargetScope)
			}
		}
	}
	return nil
}

// venueUnitEnabled mirrors the pre-move core function verbatim.
func venueUnitEnabled(ctx context.Context, exec deploykit.DeployExecutor, unit string, scope spec.Scope) bool {
	cmd := "systemctl is-enabled --quiet " + kit.ShQuoteArg(unit)
	if scope == spec.ScopeUser {
		cmd = "systemctl --user is-enabled --quiet " + kit.ShQuoteArg(unit)
	}
	_, _, exit, err := exec.RunCapture(ctx, cmd)
	return err == nil && exit == 0
}

// ledgerPathsFor resolves the ledger root — ledgerRoot overrides kit.DefaultLedgerPaths() when
// non-empty (a TEST redirecting to a temp dir; the pre-S3b former core-resident deploy target
// carried a settable `paths *kit.LedgerPaths` field for exactly this, see req.LedgerRoot's doc comment in
// sdk/schema/seam.cue). Mirrors kit.DefaultLedgerPaths's own path derivation.
func ledgerPathsFor(ledgerRoot string) (*kit.LedgerPaths, error) {
	if ledgerRoot == "" {
		return kit.DefaultLedgerPaths()
	}
	return &kit.LedgerPaths{
		Root:     ledgerRoot,
		Deploys:  filepath.Join(ledgerRoot, "deploys"),
		Candies:  filepath.Join(ledgerRoot, "layers"),
		LockFile: filepath.Join(ledgerRoot, ".lock"),
	}, nil
}

// recordDeploy persists the external deploy's teardown ops + provenance into the ledger via the
// SAME sdk/kit install_ledger.go path the pre-move core function used. DistroCfgJSON (a plain
// marshalled *buildkit.DistroConfig — a sdk type with zero core-only coupling, unlike the
// core-only buildEngineContext wrapper it used to travel inside) fills a package-remove ReverseOp's
// UninstallCmd, matching recordDeploy's original renderUninstall closure exactly.
func recordDeploy(name, word string, distroCfgJSON json.RawMessage, ledgerRoot string, reply spec.DeployReply) error {
	paths, err := ledgerPathsFor(ledgerRoot)
	if err != nil {
		return err
	}
	candy := reply.Record.Candy
	if candy == "" {
		candy = name
	}
	id := deploykit.ComputeDeployID(name, nil, nil)
	reverseOps := reply.ReverseOps

	var distroCfg *buildkit.DistroConfig
	if len(distroCfgJSON) > 0 {
		distroCfg = &buildkit.DistroConfig{}
		if err := json.Unmarshal(distroCfgJSON, distroCfg); err != nil {
			return fmt.Errorf("deploy-dispatch: decode distro config: %w", err)
		}
	}
	kit.FillReverseUninstallCmds(reverseOps, func(format string, packages []string) string {
		if distroCfg == nil {
			return ""
		}
		fd := distroCfg.FindFormat(format)
		if fd == nil || strings.TrimSpace(fd.UninstallTemplate) == "" {
			return ""
		}
		ictx := &spec.InstallContext{Packages: append([]string(nil), packages...)}
		rendered, err := buildkit.RenderTemplate(format+"-uninstall", fd.UninstallTemplate, ictx)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(rendered)
	})

	if err := kit.AddCandyDeployment(paths, candy, id, func(rec *kit.CandyRecord) {
		rec.Version = reply.Record.Version
		rec.ReverseOps = append([]spec.ReverseOp(nil), reverseOps...)
	}); err != nil {
		return fmt.Errorf("deploy-dispatch: record candy: %w", err)
	}
	return kit.WriteDeployRecord(paths, &kit.DeployRecord{
		DeployID:   id,
		Image:      name,
		Target:     word,
		Candy:      []string{candy},
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// recordVenueLedger mirrors the pre-move core function verbatim — a no-op on a host-local venue
// (ShellExecutor), the venue-side ledger write for a remote venue (a vm guest, or a nested
// target:local child).
//
// R10 bed-found bug #6 in this cluster's move: the pre-move core function's recordVenueLedger
// set DeployRecord.Target to the receiver's t.prov.word (the substrate word — "vm"/"local"/…).
// The free-function port dropped that field entirely (no `word` parameter existed), leaving
// Target as the zero value "". That passed silently until the egress schema's
// `target: string & !=""` constraint on the written ledger JSON started rejecting it at
// WriteDeployRecordVia time — caught live via the vm-guest ledger write in check-sidecar-pod's
// ephvm step ("target: invalid value \"\" (out of bound !=\"\")"). Fixed by threading `word`
// through from the one call site's req.Word, restoring parity with the original.
func recordVenueLedger(exec deploykit.DeployExecutor, plans []*deploykit.InstallPlan, name, word, ledgerRoot string) error {
	if exec == nil {
		return nil
	}
	if _, isLocal := exec.(kit.ShellExecutor); isLocal {
		return nil
	}
	paths, err := ledgerPathsFor(ledgerRoot)
	if err != nil {
		return err
	}
	id := deploykit.ComputeDeployID(name, nil, nil)
	for _, p := range plans {
		if p != nil && p.DeployID != "" {
			id = p.DeployID
			break
		}
	}
	deployRec := &kit.DeployRecord{
		DeployID:   id,
		Image:      name,
		Target:     word,
		DeployedAt: time.Now().UTC().Format(time.RFC3339),
	}
	for _, p := range plans {
		if p == nil || p.Candy == "" {
			continue
		}
		ver := p.Version
		if verr := kit.AddCandyDeploymentVia(exec, paths, p.Candy, id, func(rec *kit.CandyRecord) {
			rec.Version = ver
		}); verr != nil {
			return fmt.Errorf("deploy-dispatch: venue ledger candy %s: %w", p.Candy, verr)
		}
		deployRec.Candy = append(deployRec.Candy, p.Candy)
	}
	if werr := kit.WriteDeployRecordVia(exec, paths, deployRec); werr != nil {
		return fmt.Errorf("deploy-dispatch: venue ledger deploy record: %w", werr)
	}
	return nil
}

// --- Del ---------------------------------------------------------------------------------

// handleDeployDel replays the RECORDED ReverseOps for this deploy (mirrors the former
// core-resident deploy target's Del exactly): read the ledger record, resolve the teardown executor +
// reverse runner (a lifecycle substrate supplies the guest/venue executor via VenueExecutor so the
// ops replay IN the guest), replay via deploykit.TeardownHostDeploy, then the substrate's
// PostTeardown host-side cleanup.
func handleDeployDel(ctx context.Context, exec *sdk.Executor, req spec.DeployTargetDispatchRequest) (spec.DeployTargetDispatchReply, error) {
	var reply spec.DeployTargetDispatchReply
	var opts spec.DeployTargetDelOpts
	if len(req.OptsJSON) > 0 {
		if err := json.Unmarshal(req.OptsJSON, &opts); err != nil {
			return reply, fmt.Errorf("deploy-dispatch del: decode opts: %w", err)
		}
	}
	paths, err := ledgerPathsFor(req.LedgerRoot)
	if err != nil {
		return reply, err
	}
	deployID := deploykit.ComputeDeployID(req.Name, nil, nil)
	rec, err := kit.ReadDeployRecord(paths, deployID)
	if err != nil {
		return reply, err
	}
	if rec == nil {
		return reply, nil // nothing recorded — idempotent teardown
	}
	if opts.DryRun {
		fmt.Printf("[dry-run] would tear down external deploy %s (target=%s, %d candies)\n", rec.DeployID, rec.Target, len(rec.Candy))
		return reply, nil
	}

	teardownExec, venueDesc, err := resolveRootExecutor(req)
	if err != nil {
		return reply, fmt.Errorf("deploy-dispatch del: resolve root executor: %w", err)
	}
	if req.HasLifecycle {
		ve, err := lifecycleInvoke(ctx, exec, req.Word, sdk.OpTeardownExecutor, req.Name, "", req.Node, nil, &venueDesc, req.HostEnvJSON)
		if err != nil {
			return reply, fmt.Errorf("deploy-dispatch del: teardown executor: %w", err)
		}
		if len(ve) > 0 {
			var d spec.VenueDescriptor
			if err := json.Unmarshal(ve, &d); err != nil {
				return reply, fmt.Errorf("deploy-dispatch del: decode venue descriptor: %w", err)
			}
			if e2, err := kit.VenueFromDescriptor(d); err == nil && e2 != nil {
				teardownExec = e2
			}
		}
	}

	var runner kit.ReverseRunner
	if sshExec, isSSH := teardownExec.(*kit.SSHExecutor); isSSH {
		runner = &kit.SSHReverseRunner{Exec: sshExec}
	}
	re := &deploykit.HostReverseExec{
		DryRun:          opts.DryRun,
		KeepRepoChanges: opts.KeepRepoChanges,
		KeepServices:    opts.KeepServices,
		Runner:          runner,
	}
	if err := deploykit.TeardownHostDeploy(paths, rec, "", re); err != nil {
		return reply, err
	}

	if req.HasLifecycle {
		// podDeployEngine's logic, inlined (trivial: node.Engine when set, else "podman" — zero
		// core dependency, no need for a seam).
		engine := "podman"
		if req.Node != nil && req.Node.Engine != "" {
			engine = req.Node.Engine
		}
		ptJSON, err := lifecycleInvoke(ctx, exec, req.Word, sdk.OpPostTeardown, req.Name, "", req.Node,
			map[string]any{"keep_image": opts.KeepImage, "engine_bin": kit.EngineBinary(engine)}, nil, req.HostEnvJSON)
		if err != nil {
			return reply, fmt.Errorf("deploy-dispatch del: post-teardown: %w", err)
		}
		// The plugin cannot touch charly.yml itself — remove the reply's deploy-entry keys via
		// the EXISTING "config-persist" HostBuild seam (Remove=true), which already wraps the
		// SAME core-only removeVmDeployEntry the pre-move code called directly.
		if len(ptJSON) > 0 {
			var ptReply spec.PostTeardownReply
			if err := json.Unmarshal(ptJSON, &ptReply); err != nil {
				return reply, fmt.Errorf("deploy-dispatch del: decode post-teardown reply: %w", err)
			}
			for _, key := range ptReply.RemoveEntries {
				cpReqJSON, merr := json.Marshal(spec.ConfigPersistRequest{Key: key, Remove: true})
				if merr != nil {
					return reply, merr
				}
				if _, err := exec.HostBuild(ctx, "config-persist", cpReqJSON); err != nil {
					fmt.Printf("warning: deploy-dispatch del: removing charly.yml entry %q: %v\n", key, err)
				}
			}
		}
	}
	fmt.Printf("Removed external deploy %s (%s)\n", rec.DeployID, rec.Target)
	return reply, nil
}

// --- Start/Stop/Logs (the arbiter-bracket-free lifecycle dispatches) -------------------------

// handleLifecycleSimple dispatches OpStart/OpStop/OpLogs to the substrate — NO arbiter bracket
// here (that stays core, wrapping THIS call; see the file header + charly/arbiter_bracket.go). The
// opts payload (req.OptsJSON, populated core-side from the still-core pod_lifecycle_dispatch.go
// plan-hook table) rides under a DIFFERENT extra key per op, matching each substrate's OWN
// lifecycleParams decode exactly (verified against candy/plugin-deploy-pod/lifecycle.go and
// candy/plugin-deploy-vm/lifecycle.go, NOT assumed): Start/Stop decode from the "plan" key
// (podStart/podStop → json.Unmarshal(p.Plan, ...)), while Logs decodes from the "opts" key
// (podLogs → json.Unmarshal(p.Opts, ...)) — an existing asymmetry in the substrate wire contract
// this function preserves rather than papers over.
func handleLifecycleSimple(ctx context.Context, exec *sdk.Executor, req spec.DeployTargetDispatchRequest, op string) (spec.DeployTargetDispatchReply, error) {
	var reply spec.DeployTargetDispatchReply
	var extra map[string]any
	if len(req.OptsJSON) > 0 {
		key := "plan"
		if op == sdk.OpLogs {
			key = "opts"
		}
		extra = map[string]any{key: json.RawMessage(req.OptsJSON)}
	}
	var venueDesc *spec.VenueDescriptor
	if len(req.VenueJSON) > 0 {
		var d spec.VenueDescriptor
		if json.Unmarshal(req.VenueJSON, &d) == nil {
			venueDesc = &d
		}
	}

	// The Q1 resource-arbiter bracket (FLOOR-SLIM-proper Unit-8, the K4-exit the former
	// charly/arbiter_bracket.go tracked) — HasPlan + a non-nil Node is core's signal that this
	// substrate's Start/Stop needs bracketing (today only "pod"). Decision logic lives in the
	// pure, directly-testable runLifecycleBracket below; here we just wire the REAL HostBuild
	// calls + the REAL dispatch into it.
	err := runLifecycleBracket(op, req.HasPlan, req.Node,
		func() error { return arbiterBracketAcquire(ctx, exec, req.Name, *req.Node) },
		func() { _ = arbiterBracketRelease(ctx, exec, req.Name) },
		func() error {
			_, ierr := lifecycleInvoke(ctx, exec, req.Word, op, req.Name, "", req.Node, extra, venueDesc, req.HostEnvJSON)
			return ierr
		},
	)
	return reply, err
}

// runLifecycleBracket implements the Q1 resource-arbiter bracket's decision logic in isolation —
// no exec, no wire types, pure control flow — so it round-trips in a unit test the same way the
// deleted charly/arbiter_bracket_test.go used to test arbiterBracketedStart/Stop, just relocated
// here alongside the ownership move (FLOOR-SLIM-proper Unit-8). "bracketed" requires HasPlan AND a
// non-nil node (a nil node with HasPlan true is a caller bug, treated as "no claim" rather than
// failing — the SAME defensive check the deleted file made) AND op being Start or Stop (Logs
// shares this dispatch path but is never bracketed).
//
//   - Start: acquire BEFORE dispatch; on a dispatch failure, release ON THE FAILURE PATH (a failed
//     start must not leak the claim); an acquire failure itself aborts before dispatch ever runs.
//   - Stop: dispatch, then release AFTER unconditionally (success or failure) — the deliberate
//     simplification the deleted file documented (a Stop-path error's safe default is to release
//     rather than leak the lease).
func runLifecycleBracket(op string, hasPlan bool, node *spec.Deploy, acquire func() error, release func(), dispatch func() error) error {
	bracketed := hasPlan && node != nil && (op == sdk.OpStart || op == sdk.OpStop)
	if !bracketed {
		return dispatch()
	}
	if op == sdk.OpStart {
		if err := acquire(); err != nil {
			return err
		}
		if err := dispatch(); err != nil {
			release()
			return err
		}
		return nil
	}
	// op == sdk.OpStop
	err := dispatch()
	release()
	return err
}

// arbiterBracketAcquire routes a Start dispatch's arbiter claim through the "arbiter-bracket-acquire"
// HostBuild seam — see host_build_arbiter_bracket.go (core) for why the actual claim/env mutation
// stays host-side.
func arbiterBracketAcquire(ctx context.Context, exec *sdk.Executor, name string, node spec.Deploy) error {
	reqJSON, err := json.Marshal(spec.ArbiterBracketAcquireRequest{Name: name, Node: node})
	if err != nil {
		return err
	}
	_, err = exec.HostBuild(ctx, "arbiter-bracket-acquire", reqJSON)
	return err
}

// arbiterBracketRelease routes a Start/Stop dispatch's arbiter release through the
// "arbiter-bracket-release" HostBuild seam.
func arbiterBracketRelease(ctx context.Context, exec *sdk.Executor, name string) error {
	reqJSON, err := json.Marshal(spec.ArbiterBracketReleaseRequest{Name: name})
	if err != nil {
		return err
	}
	_, err = exec.HostBuild(ctx, "arbiter-bracket-release", reqJSON)
	return err
}

// --- Status --------------------------------------------------------------------------------

func handleDeployStatus(ctx context.Context, exec *sdk.Executor, req spec.DeployTargetDispatchRequest) (spec.DeployTargetDispatchReply, error) {
	var reply spec.DeployTargetDispatchReply
	if req.HasLifecycle {
		var venueDesc *spec.VenueDescriptor
		if len(req.VenueJSON) > 0 {
			var d spec.VenueDescriptor
			if json.Unmarshal(req.VenueJSON, &d) == nil {
				venueDesc = &d
			}
		}
		resJSON, err := lifecycleInvoke(ctx, exec, req.Word, sdk.OpStatus, req.Name, "", req.Node, nil, venueDesc, req.HostEnvJSON)
		if err != nil {
			return reply, err
		}
		if len(resJSON) > 0 {
			var si spec.DeployTargetStatus
			if err := json.Unmarshal(resJSON, &si); err != nil {
				return reply, fmt.Errorf("deploy-dispatch status: decode: %w", err)
			}
			reply.Status = si
		}
		return reply, nil
	}
	paths, err := ledgerPathsFor(req.LedgerRoot)
	if err != nil {
		return reply, err
	}
	rec, err := kit.ReadDeployRecord(paths, deploykit.ComputeDeployID(req.Name, nil, nil))
	if err != nil || rec == nil {
		reply.Status = spec.DeployTargetStatus{State: "stopped", Healthy: false}
		return reply, nil
	}
	reply.Status = spec.DeployTargetStatus{
		State:   "running",
		Healthy: true,
		Details: map[string]string{"target": rec.Target, "candies": fmt.Sprintf("%d", len(rec.Candy))},
	}
	return reply, nil
}

// --- Shell/Attach (the F12 interactive/live-stdio legs) --------------------------------------

// handleDeployExec serves OpShell (charly cmd — the K4 `charly service` non-interactive capture
// leg) and OpAttach (charly shell/cmd's interactive live-stdio leg), which DIFFER in venue choice
// exactly like the former core-resident substrate lifecycle proxy did: Shell ALWAYS dispatches on a host-local
// venue (nil venueDesc — the substrate's own ARGV already encodes the remote-exec mechanics, e.g.
// `podman exec <ctr> …` / `virsh …`, so running it via the live guest venue would double-remote
// it); Attach dispatches WITH the live venue (podAttach/vm's attach resolver run the interactive
// session ON that venue). OptsJSON (present only for Attach — Shell carries none) rides under
// "plan", matching podAttach's own p.Plan decode.
func handleDeployExec(ctx context.Context, exec *sdk.Executor, req spec.DeployTargetDispatchRequest, op string) (spec.DeployTargetDispatchReply, error) {
	var reply spec.DeployTargetDispatchReply
	var venueDesc *spec.VenueDescriptor
	if op == sdk.OpAttach && len(req.VenueJSON) > 0 {
		var d spec.VenueDescriptor
		if json.Unmarshal(req.VenueJSON, &d) == nil {
			venueDesc = &d
		}
	}
	extra := map[string]any{}
	if len(req.OptsJSON) > 0 {
		extra["plan"] = json.RawMessage(req.OptsJSON)
	}
	if op == sdk.OpShell {
		extra["cmd"] = req.Cmd
	}
	resJSON, err := lifecycleInvoke(ctx, exec, req.Word, op, req.Name, "", req.Node, extra, venueDesc, req.HostEnvJSON)
	if err != nil {
		return reply, err
	}
	var execReply spec.PodExecReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &execReply); err != nil {
			return reply, fmt.Errorf("deploy-dispatch %s: decode exec reply: %w", req.Op, err)
		}
	}
	reply.Output = execReply.Output
	reply.ExitCode = int64(execReply.ExitCode)
	return reply, nil
}

// --- Rebuild ---------------------------------------------------------------------------------

func handleDeployRebuild(ctx context.Context, exec *sdk.Executor, req spec.DeployTargetDispatchRequest) (spec.DeployTargetDispatchReply, error) {
	var reply spec.DeployTargetDispatchReply
	if req.HasLifecycle {
		var venueDesc *spec.VenueDescriptor
		if len(req.VenueJSON) > 0 {
			var d spec.VenueDescriptor
			if json.Unmarshal(req.VenueJSON, &d) == nil {
				venueDesc = &d
			}
		}
		_, err := lifecycleInvoke(ctx, exec, req.Word, sdk.OpRebuild, req.Name, "", req.Node,
			map[string]any{"opts": json.RawMessage(req.OptsJSON)}, venueDesc, req.HostEnvJSON)
		return reply, err
	}
	// A hookless substrate (local/android/k8s) has no charly-owned runtime to rebuild in place —
	// the refresh path is an idempotent re-apply, driven by the SAME add path (mirrors the
	// pre-move core's `runCharlySubcommand("bundle", "add", t.name)` shell-out, done here as an
	// in-process re-dispatch instead of a subprocess).
	var opts spec.DeployTargetRebuildOpts
	if len(req.OptsJSON) > 0 {
		_ = json.Unmarshal(req.OptsJSON, &opts)
	}
	if opts.DryRun {
		fmt.Printf("dry-run: charly bundle add %s\n", req.Name)
		return reply, nil
	}
	addReq := req
	addReq.Op = "add"
	addReq.OptsJSON = nil
	_, err := handleDeployApply(ctx, exec, addReq, false)
	return reply, err
}
