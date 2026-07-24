package check

// live_gather.go — K1-unblock wave, arm 1 (the "live" check-run mode): pluginCheckRunLive, the
// plugin-resident port of charly/host_build_check_run.go's hostCheckRunLive (charly/check_cmd.go's
// checkLiveGather + checkLivePod/VM/Local/Group). Mirrors the DELETE-AS-YOU-WIRE contract: this
// file's wiring commit (command.go's hostCheckRun dispatching Mode:"live" here) deletes the core
// originals in the SAME commit.
//
// Every core-only dependency the original arm had is now either (a) already ported by Unit A
// (venue.go's resolveCheckVenue/checkVmTarget/checkLocalTarget/nodeTraits/resolveLeafVenue,
// members.go's resolveHostVarsForSteps/liveTargetResolver/resolveDeployBoxName/
// resolveImageRefForEnsure/stampCharlyBin, cmd_helpers.go's resolveNestedNode/candyAddSteps/
// candyDirsFromEnvelope), (b) already sdk-portable (deploykit.*/kit.*/vmshared.* — confirmed by
// grep before writing a line here), or (c) resolvable straight off the resolved-project envelope's
// existing fields (rp.Templates.VM/.Local carry the SAME raw JSON body a fresh core decode would
// produce — vmshared.VmSpec IS spec.ResolvedVm, spec.Local's shape is what findLocalSpec returned —
// so no NEW envelope field was needed for either). The ONE genuine gap — connecting an
// out-of-process plugin candy a live plan's verb words reference, the M-mechanism the boundary law
// keeps core-side — got the new thin "check-load-plugins" HostBuild seam (checkLoadPlugins,
// command.go), mirroring exactly what resolveCheckRunnerContext already did in-core: VM and GROUP
// mode call it (matching the ORIGINAL arms, which called resolveCheckRunnerContext); POD mode does
// NOT (the original checkLivePod never called it either — preserved verbatim, not "fixed", per R5
// hard-cutover discipline: this port changes NO behavior beyond relocation).

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strconv"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// pluginCheckRunLive classifies req.Name via the SAME shared checkVmTarget/checkLocalTarget
// classifiers Unit A's resolveCheckVenue uses (R3) and runs the matching per-kind gather.
func pluginCheckRunLive(ex *sdk.Executor, ctx context.Context, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	dir, _ := os.Getwd()
	rp, err := resolvedProject(ex, ctx, dir)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	tree := derefDeployTree(rp.Deploy)
	if _, isVM := checkVmTarget(tree, req.Name); isVM {
		return pluginCheckLiveVM(ex, ctx, rp, tree, dir, req)
	}
	if _, isLocal := checkLocalTarget(tree, req.Name); isLocal {
		return pluginCheckLiveLocal(ex, ctx, rp, tree, dir, req)
	}
	if entry, ok := tree[req.Name]; ok && entry.IsGroup() {
		return pluginCheckLiveGroup(ex, ctx, rp, tree, dir, req)
	}
	return pluginCheckLivePod(ex, ctx, rp, tree, dir, req)
}

// pluginVenueResolver adapts members.go's liveTargetResolver into the kit.VenueResolver shape a
// live RunnerConfig.TargetResolver needs — the plugin-side counterpart of
// charly/planrun_adapter.go's venueResolver.
func pluginVenueResolver(ex *sdk.Executor, ctx context.Context, dir, instance string) kit.VenueResolver {
	resolve := liveTargetResolver(ex, ctx, dir, instance)
	return func(venue string) (kit.Executor, map[string]string, bool, error) {
		res, exec, err := resolve(venue)
		if err != nil {
			return nil, nil, false, err
		}
		env := map[string]string{}
		hasRuntime := false
		if res != nil {
			if res.Env != nil {
				env = res.Env
			}
			hasRuntime = res.HasRuntime
		}
		return exec, env, hasRuntime, nil
	}
}

// pluginCheckLivePod gathers the pod (running-container) live check — the port of
// charly/check_cmd.go's checkLivePod.
func pluginCheckLivePod(ex *sdk.Executor, ctx context.Context, rp *spec.ResolvedProject, tree map[string]spec.BundleNode, dir string, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	engine, containerName, err := deploykit.ResolveContainer(req.Name, req.Instance)
	if err != nil {
		return kit.CheckRunReply{}, err
	}

	var localPlan, projectPlan []spec.Step
	var deployOverlay *spec.BundleNode
	if node := resolveNestedNode(tree, req.Name); node != nil {
		projectPlan = node.Plan
	} else if entry, ok := tree[req.Name]; ok {
		projectPlan = entry.Plan
	}
	if dc := deploykit.LoadDeployConfigForRead("charly check live"); dc != nil {
		if entry, ok := dc.Bundle[deploykit.DeployKey(req.Name, req.Instance)]; ok {
			localPlan = entry.Plan
			deployOverlay = &entry
		} else if entry, ok := dc.Bundle[req.Name]; ok {
			localPlan = entry.Plan
			deployOverlay = &entry
		}
	}
	overlayPlan := append(append([]spec.Step(nil), projectPlan...), localPlan...)

	imageRef := resolveDeployBoxName(rp, req.Name, req.Instance)
	resolvedRef, err := resolveImageRefForEnsure(rp, imageRef)
	if err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("resolving deploy box %q: %w", imageRef, err)
	}
	meta, err := deploykit.ExtractMetadata(engine, resolvedRef)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	set := kit.MergeDeployDescriptions(meta.Description, overlayPlan, req.Name)
	if set == nil || set.IsEmpty() {
		return kit.CheckRunReply{NoSteps: true}, nil
	}
	resolver, _ := kit.ResolveCheckVarsRuntime(meta, deployOverlay, engine, req.Name, containerName, req.Instance)
	resolver = stampCharlyBin(resolver)

	env, hasRuntime := pluginResolverEnv(resolver)
	hostVars := map[string]string{}
	var hostCleanups []func()
	for _, sec := range [][]kit.LabeledDescription{set.Candy, set.Box, set.Deploy} {
		for _, ld := range sec {
			v, cl := resolveHostVarsForSteps(ex, ctx, dir, ld.Plan, req.Instance)
			maps.Copy(hostVars, v)
			hostCleanups = append(hostCleanups, cl...)
		}
	}
	defer kit.CloseHostCleanups(hostCleanups)

	execChain := deploykit.ContainerChain(engine, containerName)
	var venueDesc *spec.VenueDescriptor
	if d := kit.DescriptorFromExecutor(execChain); d.Kind != "" {
		venueDesc = &d
	}
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "live",
		Box:       req.Name,
		Instance:  req.Instance,
		Distros:   meta.Distro,
		VenueKind: execChain.Kind(),
	}, venueDesc, kit.RunnerConfig{
		Exec:           execChain,
		Mode:           kit.ModeLive,
		Env:            env,
		HasRuntime:     hasRuntime,
		Distros:        meta.Distro,
		Box:            req.Name,
		Instance:       req.Instance,
		VerifyOnly:     true,
		CandyDirs:      candyDirsFromEnvelope(rp),
		HostVars:       hostVars,
		TargetResolver: pluginVenueResolver(ex, ctx, dir, req.Instance),
	})
	results := kit.RunPlan(ctx, runner, set, false)
	return kit.CheckRunReply{Steps: results, Header: fmt.Sprintf("Image: %s (container: %s)", meta.Box, containerName)}, nil
}

// pluginResolveVmTarget resolves the VM check request (name) to its kind:vm entity name, an
// optional nested-leaf node (for a dotted "parent.child" path), and the per-deploy domain
// identity — the port of charly/check_cmd.go's CheckLiveCmd.resolveVmTarget, off the envelope
// tree instead of *UnifiedFile.
func pluginResolveVmTarget(tree map[string]spec.BundleNode, name string) (vmName, domainID string, nestedLeaf *spec.BundleNode) {
	vmName = name
	domainKey := name
	if entry, ok := tree[name]; ok && nodeTraits(&entry).Venue == "ssh" && entry.From != "" {
		vmName = entry.From
	} else if idx := strings.Index(name, "."); idx > 0 {
		if leaf, venue, ok := resolveLeafVenue(tree, name); ok && venue == "ssh" {
			if leaf.From != "" {
				vmName = leaf.From
			}
			leafCopy := leaf
			nestedLeaf = &leafCopy
		} else {
			root := name[:idx]
			if parent, present := tree[root]; present && nodeTraits(&parent).Venue == "ssh" {
				if parent.From != "" {
					vmName = parent.From
				}
				domainKey = root
				nestedLeaf = resolveNestedNode(tree, name)
			}
		}
	}
	domainID = vmshared.VmDomainIdentity(domainKey)
	return vmName, domainID, nestedLeaf
}

// pluginResolveVmSpec decodes the vmName's kind:vm template off the envelope's opaque
// rp.Templates.VM map — the plugin-side replacement for charly/check_cmd.go's
// resolveVmViaPlugin(uf.VM[vmName]): the raw JSON body is byte-identical to what a fresh core
// decode of the SAME template would produce, and vmshared.VmSpec IS spec.ResolvedVm (the wire
// type ResolveCloudInitSSHUser/vmHostdevCount/ResolveVmSshPort already consume), so no new
// mechanism is needed — a direct decode.
func pluginResolveVmSpec(rp *spec.ResolvedProject, vmName string) *vmshared.VmSpec {
	raw, ok := templateBody(rp, "vm", vmName)
	if !ok {
		return nil
	}
	var vm vmshared.VmSpec
	if err := json.Unmarshal(raw, &vm); err != nil {
		return nil
	}
	return &vm
}

// pluginVmHostdevCount returns how many <hostdev> passthrough devices the VM spec declares — the
// port of charly/check_cmd.go's vmHostdevCount (pure field reads, nil-safe throughout).
func pluginVmHostdevCount(sp *vmshared.VmSpec) int {
	if sp == nil || sp.Libvirt == nil || sp.Libvirt.Devices == nil {
		return 0
	}
	return len(sp.Libvirt.Devices.Hostdevs)
}

// pluginLoadVmCheckPlans aggregates the VM deployment's check plan from the project tree, the
// per-machine deploy overlay, and add_candy deploy-scope steps — the port of
// charly/check_cmd.go's CheckLiveCmd.loadVmCheckPlans.
func pluginLoadVmCheckPlans(rp *spec.ResolvedProject, tree map[string]spec.BundleNode, name, vmName string, nestedLeaf *spec.BundleNode, user string, port int) (plan []spec.Step, outUser string, outPort int, err error) {
	outUser, outPort = user, port
	var projectPlan, localPlan []spec.Step
	var addCandies []string
	if nestedLeaf != nil {
		projectPlan = nestedLeaf.Plan
		addCandies = nestedLeaf.AddCandy
	} else {
		entry, ok, ferr := deploykit.FindVmDeployNode(tree, name, vmName)
		if ferr != nil {
			return nil, "", 0, fmt.Errorf("resolving project vm deploy plan for %q: %w", name, ferr)
		}
		if ok {
			projectPlan = entry.Plan
			addCandies = entry.AddCandy
		}
	}
	if dc := deploykit.LoadDeployConfigForRead("charly check vm"); dc != nil {
		entry, ok, ferr := deploykit.FindVmDeployNode(dc.Bundle, name, vmName)
		if ferr != nil {
			return nil, "", 0, fmt.Errorf("resolving local vm deploy state for %q: %w", name, ferr)
		}
		if ok {
			localPlan = entry.Plan
			if entry.VmState != nil {
				if entry.VmState.SshUser != "" {
					outUser = entry.VmState.SshUser
				}
				if entry.VmState.SshPort > 0 {
					outPort = entry.VmState.SshPort
				}
			}
		}
	}
	plan = append(append([]spec.Step(nil), projectPlan...), localPlan...)
	plan = append(plan, candyAddSteps(rp, addCandies)...)
	return plan, outUser, outPort, nil
}

// pluginCheckLiveVM gathers the VM live check over SSH — the port of charly/check_cmd.go's
// checkLiveVM (nested-in-VM pod delegation, readiness gate, plan run).
func pluginCheckLiveVM(ex *sdk.Executor, ctx context.Context, rp *spec.ResolvedProject, tree map[string]spec.BundleNode, dir string, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	vmName, domainID, nestedLeaf := pluginResolveVmTarget(tree, req.Name)
	sp := pluginResolveVmSpec(rp, vmName)

	user := vmshared.ResolveCloudInitSSHUser(sp)
	port, err := deploykit.ResolveVmSshPort(sp, domainID)
	if err != nil {
		return kit.CheckRunReply{}, err
	}

	plan, user, port, err := pluginLoadVmCheckPlans(rp, tree, req.Name, vmName, nestedLeaf, user, port)
	if err != nil {
		return kit.CheckRunReply{}, err
	}

	host := "127.0.0.1"
	var executor deploykit.DeployExecutor = &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}
	if strings.Contains(req.Name, ".") {
		if _, chain, chainErr := deploykit.ResolveDeployChain(tree, req.Name, kit.ShellExecutor{}); chainErr == nil && chain != nil {
			executor = chain
		}
	}

	gate := &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 5}
	if gerr := gate.WaitForSSH(ctx); gerr != nil {
		return kit.CheckRunReply{}, fmt.Errorf("vm %q is not up / SSH-reachable — is the domain running? %w", domainID, gerr)
	}
	if gerr := gate.WaitForCloudInit(ctx); gerr != nil {
		return kit.CheckRunReply{}, fmt.Errorf("vm %q cloud-init did not settle (still running or restarting?): %w", domainID, gerr)
	}

	env := map[string]string{
		"IMAGE":            req.Name,
		"INSTANCE":         req.Instance,
		"HOST_PORT:22":     strconv.Itoa(port),
		"CONTAINER_IP":     host,
		"CONTAINER_NAME":   "charly-" + domainID,
		"USER":             user,
		"HOME":             "/home/" + user,
		"VM_HOSTDEV_COUNT": strconv.Itoa(pluginVmHostdevCount(sp)),
		"DEPLOY_NAME":      kit.SanitizeDeployName("vm:" + vmName),
	}
	resolver := newPluginRuntimeCheckVarResolver(env)

	if nestedLeaf != nil && nodeTraits(nestedLeaf).Venue == "container" {
		parts := strings.Split(req.Name, ".")
		guestPod := parts[len(parts)-1]
		guestCmd := guestNestedCheckCmd(guestPod, "", req.Section, req.Filter, req.Instance)
		vmSSH := &kit.SSHExecutor{Host: kit.VmSshAlias(domainID), ConnectTimeout: 10}
		header := fmt.Sprintf("VM: %s — nested pod %q evaluated IN the guest (%s)", kit.VmSshAlias(domainID), guestPod, kit.VmSshAlias(domainID))
		stdout, stderr, exit, rerr := vmSSH.RunCapture(ctx, guestCmd)
		pass := &kit.StepPass{Stdout: stdout, Stderr: stderr, ExitCode: exit}
		if rerr != nil {
			return kit.CheckRunReply{Header: header, Passthrough: pass}, fmt.Errorf("delegating nested-pod check to guest %q: %w", vmName, rerr)
		}
		return kit.CheckRunReply{Header: header, Passthrough: pass}, nil
	}

	if len(plan) == 0 {
		return kit.CheckRunReply{NoSteps: true}, nil
	}
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "vm:" + vmName, Plan: plan}}}

	checkLoadPlugins(ex, ctx, req.Name, dir)

	envVars, hasRuntime := pluginResolverEnv(resolver)
	hostVars, hostCleanups := resolveHostVarsForSteps(ex, ctx, dir, plan, req.Instance)
	defer kit.CloseHostCleanups(hostCleanups)
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "live",
		Box:       req.Name,
		Instance:  req.Instance,
		Venue:     domainID,
		VenueKind: "vm",
	}, nil, kit.RunnerConfig{
		Exec:           executor,
		Mode:           kit.ModeLive,
		Env:            envVars,
		HasRuntime:     hasRuntime,
		Box:            req.Name,
		Instance:       req.Instance,
		VmName:         domainID,
		VerifyOnly:     true,
		CandyDirs:      candyDirsFromEnvelope(rp),
		HostVars:       hostVars,
		TargetResolver: pluginVenueResolver(ex, ctx, dir, req.Instance),
	})
	results := kit.RunPlan(ctx, runner, set, false)
	return kit.CheckRunReply{Steps: results, Header: fmt.Sprintf("VM: charly-%s (ssh %s@%s:%d)", req.Name, user, host, port)}, nil
}

// pluginCheckLiveLocal gathers a `target: local` deployment's deploy-scope check on its host
// venue — the port of charly/check_cmd.go's checkLiveLocal.
func pluginCheckLiveLocal(ex *sdk.Executor, ctx context.Context, rp *spec.ResolvedProject, tree map[string]spec.BundleNode, dir string, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	dotted := strings.Contains(req.Name, ".")
	var node, rootNode *spec.BundleNode
	if dotted {
		node = resolveNestedNode(tree, req.Name)
		root, _, _ := strings.Cut(req.Name, ".")
		if entry, ok := tree[root]; ok {
			rn := entry
			rootNode = &rn
		}
	} else if entry, ok := tree[req.Name]; ok {
		n := entry
		node = &n
		rootNode = &n
	}
	if node == nil {
		return kit.CheckRunReply{}, fmt.Errorf("check live: local deployment %q not found", req.Name)
	}

	executor, err := deploykit.RootExecutorForDeployNode(rootNode)
	if err != nil {
		return kit.CheckRunReply{}, fmt.Errorf("check live %q: %w", req.Name, err)
	}
	if dotted {
		if _, chain, chainErr := deploykit.ResolveDeployChain(tree, req.Name, executor); chainErr == nil && chain != nil {
			executor = chain
		}
	}

	venue := "host (local)"
	if _, isShell := executor.(kit.ShellExecutor); !isShell {
		venue = executor.Venue()
	}
	header := fmt.Sprintf("Local deploy: %s [%s]", req.Name, venue)

	results, hadPlan, err := pluginRunLocalDeployScopePlan(ex, ctx, rp, dir, node, req.Name, req.Instance, executor)
	if err != nil {
		return kit.CheckRunReply{}, err
	}
	if !hadPlan {
		return kit.CheckRunReply{Header: header, NoSteps: true}, nil
	}
	return kit.CheckRunReply{Steps: results, Header: header}, nil
}

// pluginRunLocalDeployScopePlan collects a local deployment's deploy-scope plan (the kind:local
// template's plan + the deploy node's plan + the per-host overlay) and runs it — the port of
// charly/check_cmd.go's runLocalDeployScopePlan.
func pluginRunLocalDeployScopePlan(ex *sdk.Executor, ctx context.Context, rp *spec.ResolvedProject, dir string, node *spec.BundleNode, image, instance string, exec deploykit.DeployExecutor) (results []kit.StepResult, hadPlan bool, err error) {
	var plan []spec.Step
	if node != nil && strings.TrimSpace(node.From) != "" {
		if raw, ok := templateBody(rp, "local", strings.TrimSpace(node.From)); ok {
			var lt spec.Local
			if uerr := json.Unmarshal(raw, &lt); uerr == nil {
				plan = append(plan, lt.Plan...)
			}
		}
	}
	if node != nil {
		plan = append(plan, node.Plan...)
	}
	if dc := deploykit.LoadDeployConfigForRead("charly check live"); dc != nil {
		if entry, ok := dc.Bundle[deploykit.DeployKey(image, instance)]; ok {
			plan = append(plan, entry.Plan...)
		} else if entry, ok := dc.Bundle[image]; ok {
			plan = append(plan, entry.Plan...)
		}
	}

	user := os.Getenv("USER")
	home, herr := exec.ResolveHome(ctx, user)
	if herr != nil || home == "" {
		home = os.Getenv("HOME")
	}
	resolver := newPluginRuntimeCheckVarResolver(map[string]string{
		"IMAGE":    image,
		"INSTANCE": instance,
		"USER":     user,
		"HOME":     home,
	})

	if len(plan) == 0 {
		return nil, false, nil
	}
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "local:" + image, Plan: plan}}}
	env, hasRuntime := pluginResolverEnv(resolver)
	hostVars, hostCleanups := resolveHostVarsForSteps(ex, ctx, dir, plan, instance)
	defer kit.CloseHostCleanups(hostCleanups)
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "live",
		Box:       image,
		Instance:  instance,
		VenueKind: exec.Kind(),
	}, nil, kit.RunnerConfig{
		Exec:           exec,
		Mode:           kit.ModeLive,
		Env:            env,
		HasRuntime:     hasRuntime,
		Box:            image,
		Instance:       instance,
		VerifyOnly:     true,
		HostVars:       hostVars,
		TargetResolver: pluginVenueResolver(ex, ctx, dir, instance),
	})
	return kit.RunPlan(ctx, runner, set, false), true, nil
}

// pluginCheckLiveGroup runs a targetless GROUP bed's flattened, venue-stamped plan — the port of
// charly/check_cmd.go's checkLiveGroup. Every step venue-dispatches to its member (its own venue
// != the group root name), so the placeholder base executor below is never actually used.
func pluginCheckLiveGroup(ex *sdk.Executor, ctx context.Context, rp *spec.ResolvedProject, tree map[string]spec.BundleNode, dir string, req spec.CheckRunRequest) (kit.CheckRunReply, error) {
	entry, ok := tree[req.Name]
	if !ok {
		return kit.CheckRunReply{}, fmt.Errorf("check live: group bed %q not found", req.Name)
	}
	plan := entry.Plan
	if len(plan) == 0 {
		return kit.CheckRunReply{NoSteps: true}, nil
	}
	header := fmt.Sprintf("Group bed: %s [%d sibling member(s); venue-dispatched, no root container]", req.Name, len(entry.Members))

	resolver := newPluginRuntimeCheckVarResolver(map[string]string{
		"IMAGE":    req.Name,
		"INSTANCE": req.Instance,
	})

	checkLoadPlugins(ex, ctx, req.Name, dir)

	env, hasRuntime := pluginResolverEnv(resolver)
	hostVars, hostCleanups := resolveHostVarsForSteps(ex, ctx, dir, plan, req.Instance)
	defer kit.CloseHostCleanups(hostCleanups)
	runner := newPluginCheckRunner(ex, ctx, spec.CheckEnv{
		Mode:      "live",
		Box:       req.Name,
		Instance:  req.Instance,
		VenueKind: "shell",
	}, nil, kit.RunnerConfig{
		Exec:           kit.ShellExecutor{},
		Mode:           kit.ModeLive,
		Env:            env,
		HasRuntime:     hasRuntime,
		Box:            req.Name,
		Instance:       req.Instance,
		CandyDirs:      candyDirsFromEnvelope(rp),
		HostVars:       hostVars,
		TargetResolver: pluginVenueResolver(ex, ctx, dir, req.Instance),
	})
	set := &kit.LabelDescriptionSet{Deploy: []kit.LabeledDescription{{Origin: "group:" + req.Name, Plan: plan}}}
	results := kit.RunPlan(ctx, runner, set, false)
	return kit.CheckRunReply{Steps: results, Header: header}, nil
}

// newPluginRuntimeCheckVarResolver constructs a runtime check-var resolver (HasRuntime true) from
// an env map, stamping CHARLY_BIN via stampCharlyBin — the plugin-side port of
// charly/checkrun.go's newRuntimeCheckVarResolver.
func newPluginRuntimeCheckVarResolver(env map[string]string) *kit.CheckVarResolver {
	if env == nil {
		env = map[string]string{}
	}
	return stampCharlyBin(&kit.CheckVarResolver{Env: env, HasRuntime: true})
}
