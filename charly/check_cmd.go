package main

import (
	"context"
	"fmt"
	"github.com/opencharly/sdk/spec"
	"maps"
	"os"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

// The `charly check` exit-code contract (2 = checks failed, 3 = prereq skip) lives in
// the sdk (sdk.CheckFailExitCode / sdk.CheckSkippedExitCode); the plugin/main signal it
// across the module boundary via *sdk.ExitCodeError. The `charly check` CLI + its
// exit-code plumbing live in command:check (candy/plugin-check).

// CheckLiveCmd is the ENGINE CARRIER for a live check-run gather — no longer a CLI
// command (the `charly check live` CLI moved to command:check, candy/plugin-check).
// The check-run "live" seam (host_build_check_run.go hostCheckLive) constructs one
// from the wire request (Box/Instance/Section/Filter; Format defaults empty → text)
// and calls checkLiveGather. It:
//
//   - Extracts the image's three-section LabelDescriptionSet from OCI labels.
//   - Applies the local charly.yml tests overlay (merge by id:).
//   - Resolves ${…} variables using meta + deploy + podman-inspect of the
//     running container.
//   - Executes the merged spec (container-internal verbs via exec; host-side
//     verbs directly).
type CheckLiveCmd struct {
	Box      string
	Instance string
	Format   string
	Filter   []string
	Section  string
}

// liveResult is the host-internal outcome of a live check-run gather. hostCheckLive
// maps it onto the wire reply (kit.CheckRunReply) — Steps/Header/Passthrough verbatim,
// NoSteps ← NoPlan. Host-internal only (never crosses the plugin boundary); the plugin
// owns the reporter, so this carries only what the seam reads.
type liveResult struct {
	Steps       []StepResult  // per-step verdicts (nil for a passthrough or no-plan result)
	Header      string        // kind-specific banner, no trailing newline
	NoPlan      bool          // no plan steps → the reply sets NoSteps
	Passthrough *kit.StepPass // nested-pod-in-VM guest delegation (Steps unused)
}

// checkLiveGather classifies c.Box (vm / local / group / pod) and runs the matching gather
// engine, returning the internal liveResult. It uses the shared checkVmTarget/checkLocalTarget
// classifiers (R3, the same order resolveCheckVenue uses) so the check-run "live" seam
// (hostCheckLive) routes a live run exactly as the CLI did.
func (c *CheckLiveCmd) checkLiveGather() (liveResult, error) {
	if dir, derr := os.Getwd(); derr == nil {
		if uf, ok, lerr := LoadUnified(dir); lerr == nil && ok && uf != nil {
			if _, isVM := checkVmTarget(uf, c.Box); isVM {
				return c.checkLiveVM()
			}
			if _, isLocal := checkLocalTarget(uf, c.Box); isLocal {
				return c.checkLiveLocal()
			}
			if entry, present := uf.Bundle[c.Box]; present && entry.IsGroup() {
				return c.checkLiveGroup()
			}
		}
	}
	return c.checkLivePod()
}

// checkLivePod gathers the pod (running-container) live check. It resolves the running
// container + declared image, merges the baked plan with the project-bundle + per-host
// overlay, resolves runtime vars, and runs the plan. The check-run "live" seam
// (hostCheckLive) consumes it via checkLiveGather.
func (c *CheckLiveCmd) checkLivePod() (liveResult, error) {
	engine, containerName, err := resolveContainer(c.Box, c.Instance)
	if err != nil {
		return liveResult{}, err
	}

	// Load deploy overlay (local tests) AND project-level tests up front so the deploy
	// entry's `image:` field can drive metadata extraction. The check runner inspects what
	// the operator declared (the hard-required `image:` field), not what the container
	// happens to be running.
	dir, _ := os.Getwd()
	var localPlan, projectPlan []spec.Step
	var deployOverlay *BundleNode
	var projectCfg *Config
	if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
		projectCfg = uf.ProjectConfig()
		// The bed's OWN bundle node carries authored plan steps; merge them like the VM
		// check path (loadVmCheckPlans) does so a bundle-node `check:` runs under check live.
		if pc := uf.ProjectBundleConfig(); pc != nil && pc.Bundle != nil {
			if node := resolveNestedNode(pc.Bundle, c.Box); node != nil {
				projectPlan = node.Plan
			} else if entry, ok := pc.Bundle[c.Box]; ok {
				projectPlan = entry.Plan
			}
		}
	}
	dc := deploykit.LoadDeployConfigForRead("charly check live")
	if dc != nil {
		if entry, ok := dc.Bundle[deployKey(c.Box, c.Instance)]; ok {
			localPlan = entry.Plan
			deployOverlay = &entry
		} else if entry, ok := dc.Bundle[c.Box]; ok {
			localPlan = entry.Plan
			deployOverlay = &entry
		}
	}
	// Project bundle plan + per-host overlay (local replaces project by id via
	// MergeDeployDescriptions' merge rules), mirroring loadVmCheckPlans.
	overlayPlan := append(append([]spec.Step(nil), projectPlan...), localPlan...)

	// Resolve the deploy key → declared image short-name via THE shared resolver
	// (resolveDeployBoxName), then to a fully-qualified registry ref before ExtractMetadata
	// reads OCI labels.
	imageRef := resolveDeployBoxName(c.Box, c.Instance)
	resolvedRef, err := resolveImageRefForEnsure(imageRef, projectCfg, dir)
	if err != nil {
		return liveResult{}, fmt.Errorf("resolving deploy box %q: %w", imageRef, err)
	}
	meta, err := ExtractMetadata(engine, resolvedRef)
	if err != nil {
		return liveResult{}, err
	}
	set := MergeDeployDescriptions(meta.Description, overlayPlan, c.Box)
	if set == nil || set.IsEmpty() {
		return liveResult{NoPlan: true}, nil
	}
	resolver, _ := ResolveCheckVarsRuntime(meta, deployOverlay, engine, c.Box, containerName, c.Instance)

	rctx := resolveCheckRunnerContext(c.Box, dir, projectCfg)
	env, hasRuntime := resolverEnv(resolver)
	// ${HOST} teardown (design §6 leak fix): accumulate every section's ssh -L cleanups and
	// defer-close them AFTER the plan run — the pre-P12 pod path discarded them. A pod SUBJECT
	// resolves ${HOST:<member>} via container DNS with no forward, but a pod-path bed
	// referencing ${HOST:<member>:<port>} for a VM/host subject opens a real forward that
	// would otherwise leak. Mirrors the correct checkLiveVM/checkLiveGroup defers.
	hostVars := map[string]string{}
	var hostCleanups []func()
	for _, sec := range [][]LabeledDescription{set.Candy, set.Box, set.Deploy} {
		for _, ld := range sec {
			v, cl := resolveHostVarsForSteps(ld.Plan, c.Instance)
			maps.Copy(hostVars, v)
			hostCleanups = append(hostCleanups, cl...)
		}
	}
	defer closeHostCleanups(hostCleanups)
	runner := newCheckRunner(kit.RunnerConfig{
		Exec:           ContainerChain(engine, containerName),
		Mode:           RunModeLive,
		Env:            env,
		HasRuntime:     hasRuntime,
		Distros:        meta.Distro,
		Box:            c.Box,
		Instance:       c.Instance,
		VerifyOnly:     true, // charly check live: idempotent check:/agent-check: only
		CandyDirs:      rctx.CandyDirs,
		CandyScanErr:   rctx.CandyScanErr,
		HostVars:       hostVars,
		TargetResolver: venueResolver(c.Instance),
	})

	results := RunPlan(context.Background(), runner, set, nil, false)
	return liveResult{Steps: results, Header: fmt.Sprintf("Image: %s (container: %s)", meta.Box, containerName)}, nil
}

// resolveNestedNode walks a dotted path through the Nested tree rooted at
// the top-level deployment, returning the leaf BundleNode.
func resolveNestedNode(roots map[string]BundleNode, path string) *BundleNode {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil
	}
	entry, ok := roots[parts[0]]
	if !ok {
		return nil
	}
	current := &entry
	for _, p := range parts[1:] {
		if current.Children == nil {
			return nil
		}
		next, ok := current.Children[p]
		if !ok || next == nil {
			return nil
		}
		current = next
	}
	return current
}

// guestNestedCheckCmd builds the `charly check live <pod>` command that checkLiveVM
// runs IN the guest (over SSH) to evaluate a nested-in-VM pod as a direct pod. The
// host's --format/--section/--filter/-i selectors pass through unchanged so the
// guest produces the same report shape the host would. Args are single-quoted
// (shellSingleQuote) since they cross an `ssh ... bash -c` boundary.
func guestNestedCheckCmd(guestPod, format, section string, filter []string, instance string) string {
	if format == "" {
		format = "text"
	}
	var cmd strings.Builder
	cmd.WriteString("charly check live " + shellSingleQuote(guestPod) + " --format " + shellSingleQuote(format))
	if section != "" {
		cmd.WriteString(" --section " + shellSingleQuote(section))
	}
	for _, f := range filter {
		cmd.WriteString(" --filter " + shellSingleQuote(f))
	}
	if instance != "" {
		cmd.WriteString(" -i " + shellSingleQuote(instance))
	}
	return cmd.String()
}

// checkLiveVM gathers the VM live check over SSH. A nested-in-VM pod leaf is delegated
// to the guest `charly check live <pod>` and its verbatim stdout/stderr + exit ride
// back in liveResult.Passthrough; every other case runs the plan on the guest SSH
// venue and returns the per-step Steps + banner. The check-run "live" seam
// (hostCheckLive) consumes it via checkLiveGather.
func (c *CheckLiveCmd) checkLiveVM() (liveResult, error) {
	dir, _ := os.Getwd()
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return liveResult{}, err
	}
	if !ok || uf == nil {
		return liveResult{}, fmt.Errorf("check live: no charly.yml found in %s (vm targets need the project config)", dir)
	}
	vmName, domainID, nestedLeaf, spec := c.resolveVmTarget(uf)

	user := resolveVmSshUser(spec)
	// Port + ssh alias key off the per-deploy DOMAIN IDENTITY (the live domain is charly-<domainID>);
	// the spec + DEPLOY_NAME (k8s cluster context) stay keyed by the ENTITY.
	port, err := resolveVmSshPort(spec, domainID)
	if err != nil {
		return liveResult{}, err
	}

	plan, user, port := c.loadVmCheckPlans(uf, dir, vmName, nestedLeaf, user, port)

	// SSH connection details (User/Port/IdentityFile) live in the
	// managed ssh-config Host stanza (charly-<domainID>) written at deploy
	// time. We point the executor at the alias and let ssh(1) resolve
	// the rest from ~/.ssh/config + agent.
	host := "127.0.0.1"
	var executor DeployExecutor = &SSHExecutor{Host: VmSshAlias(domainID), ConnectTimeout: 10}

	// 2026-04 cutover: when c.Box is dotted ("vm.inner-pod"), walk
	// the deploy tree and construct the full chain via ResolveDeployChain
	// so leaf tests run inside the leaf's actual venue. Pre-cutover this
	// path was silently single-hop SSH — `command: id` for a pod-in-vm
	// leaf returned the VM's user, not the inner pod's.
	if strings.Contains(c.Box, ".") {
		if roots, _ := resolveTreeRoot(dir); roots != nil {
			if _, chain, chainErr := ResolveDeployChain(roots, c.Box, ShellExecutor{}); chainErr == nil && chain != nil {
				executor = chain
			}
		}
	}

	// Readiness gate (runs as the first step of the VM check sequence): confirm
	// the VM is up + SSH-reachable AND cloud-init has settled BEFORE running any
	// checks. Without it, a guest that is down, mid-cloud-init, or mid-restart
	// surfaces as a confusing wall of "Connection refused" on EVERY check
	// instead of one clear "VM not ready" signal — and a cloud-init that
	// triggers a reboot would otherwise be tested mid-restart. WaitForSSH (poll
	// until sshd answers) and WaitForCloudInit (retry until an ssh connection
	// survives a `cloud-init status` poll) are real synchronization primitives,
	// not fixed sleeps — the same SSHExecutor preflight the external vm deploy walk runs
	// at deploy time. Fast no-op on an already-settled guest (zero added
	// latency); the VM analog of waitForContainerReady for the bed runner.
	gate := &SSHExecutor{Host: VmSshAlias(domainID), ConnectTimeout: 5}
	gctx := context.Background()
	if gerr := gate.WaitForSSH(gctx); gerr != nil {
		return liveResult{}, fmt.Errorf("vm %q is not up / SSH-reachable — is the domain running? %w", domainID, gerr)
	}
	if gerr := gate.WaitForCloudInit(gctx); gerr != nil {
		return liveResult{}, fmt.Errorf("vm %q cloud-init did not settle (still running or restarting?): %w", domainID, gerr)
	}

	env := map[string]string{
		"IMAGE":          c.Box,
		"INSTANCE":       c.Instance,
		"HOST_PORT:22":   strconv.Itoa(port),
		"CONTAINER_IP":   host,
		"CONTAINER_NAME": "charly-" + domainID,
		"USER":           user,
		"HOME":           "/home/" + user,
		// VM_HOSTDEV_COUNT = how many <hostdev> passthrough devices THIS VM's
		// spec declares (the operator's INTENT). A guest-side GPU check uses it
		// to tell "no GPU configured for this VM" (legit N/A) apart from "a GPU
		// hostdev WAS configured but the guest cannot see it" (passthrough
		// silently failed → the check must HARD-FAIL, never N/A-pass). Sourced
		// from the VmSpec, NOT the running domain: a libvirt hostdev drop would
		// zero the running count and re-mask the exact failure this guards
		// against (the check-cachyos-gpu-vm false-green that motivated this var).
		"VM_HOSTDEV_COUNT": strconv.Itoa(vmHostdevCount(spec)),
		// DEPLOY_NAME — the sanitized VM deploy name (vm:<vmName> -> vm-<vmName>),
		// the SAME identifier `charly bundle add vm:<vmName>` feeds to K3sPostProvision
		// for the kubeconfig context + ClusterProfile. Lets a candy's deploy-scope
		// k8s checks address their own cluster generically via cluster:
		// "${DEPLOY_NAME}" instead of hard-coding the bed's cluster name.
		"DEPLOY_NAME": sanitizeDeployName("vm:" + vmName),
	}
	resolver := &CheckVarResolver{Env: env, HasRuntime: true}

	// Nested-in-VM POD leaf: delegate the pod's check to the guest `charly`. FROM
	// THE GUEST the nested pod is a DIRECT pod — guest-local podman, ports on
	// guest localhost, the guest `charly` binary (installed by kit.EnsureCharlyInGuest
	// at deploy time) — so the already-working direct-pod path runs the protocol
	// verbs (cdp/wl/dbus/vnc/mcp) AND resolves ${HOST_PORT} addr/http natively.
	// Those are exactly the checks the HOST chain cannot reach across the VM
	// boundary (they would SKIP). The guest reads the SAME baked checks from the
	// cp-box'd pod image, so the check set is identical; only the
	// previously-unreachable probes now actually execute. The readiness gate above
	// already confirmed the guest is up + cloud-init settled. Every other check
	// path (direct pods, the VM itself, host, on:-redirected cross-deployment
	// probes against a host driver) is unchanged — they never enter this branch.
	if nestedLeaf != nil && nodeTraits(nestedLeaf).Venue == "container" { // pod (container venue)
		parts := strings.Split(c.Box, ".")
		guestPod := parts[len(parts)-1]
		// c.Format is empty for the atom arm (the request carries no format field — the plugin
		// formats the returned Steps itself), so the guest defaults to text; the CLI shell has
		// c.Format and preserves it. Section/Filter ride the request and pass through either way.
		guestCmd := guestNestedCheckCmd(guestPod, c.Format, c.Section, c.Filter, c.Instance)
		vmSSH := &SSHExecutor{Host: VmSshAlias(domainID), ConnectTimeout: 10}
		header := fmt.Sprintf("VM: %s — nested pod %q evaluated IN the guest (%s)", VmSshAlias(domainID), guestPod, VmSshAlias(domainID))
		stdout, stderr, exit, rerr := vmSSH.RunCapture(context.Background(), guestCmd)
		pass := &kit.StepPass{Stdout: stdout, Stderr: stderr, ExitCode: exit}
		if rerr != nil {
			return liveResult{Header: header, Passthrough: pass}, fmt.Errorf("delegating nested-pod check to guest %q: %w", vmName, rerr)
		}
		return liveResult{Header: header, Passthrough: pass}, nil
	}

	if len(plan) == 0 {
		return liveResult{NoPlan: true}, nil
	}
	set := &LabelDescriptionSet{Deploy: []LabeledDescription{{Origin: "vm:" + vmName, Plan: plan}}}

	// Load the project's composed OUT-OF-TREE plugins so an externalized check
	// verb (e.g. `kube:`, served by candy/plugin-kube) RESOLVES in the VM check
	// path too — the SAME shared wiring the pod path uses (resolveCheckRunnerContext,
	// the ONE place every RunModeLive baked-plan runner loads plugins, R3). Without
	// it a VM bed's `kube:` steps SKIP as `unknown verb "kube"` (the kube dep-shed
	// regression: kube WAS a builtin, always registered, so the VM path never needed it).
	// A LoadConfig failure leaves CandyDirs empty (Box/Instance are set regardless).
	var rctx checkRunnerContext
	if cfg, cerr := LoadConfig(dir); cerr == nil {
		rctx = resolveCheckRunnerContext(c.Box, dir, cfg)
	}
	env, hasRuntime := resolverEnv(resolver)
	// Cross-deployment support for a VM SUBJECT (the `on:` driver dispatch +
	// ${HOST}/${HOST} resolution) — the SAME wiring the pod (checkLivePod)
	// and local (checkLiveLocal) paths already do (R3). Without
	// it, a VM bed whose check drives a peer (e.g. check-cross-vm-http: a local
	// host-driver curls the guest via ${HOST}'s ssh -L forward) leaves
	// ${HOST} unresolved → the check FAILS "peer unreachable". closeHostCleanups
	// tears down any ssh -L forwards at run end.
	hostVars, hostCleanups := resolveHostVarsForSteps(plan, c.Instance)
	defer closeHostCleanups(hostCleanups)
	// Box stays the deploy/bed name (container + DEPLOY_NAME identity); VmName is the
	// per-deploy DOMAIN IDENTITY (the deploy/bed key → vmDomainIdentity → charly-<domainID>,
	// the live libvirt domain — NOT the shared kind:vm entity). The operator-side
	// libvirt/spice verbs must address that domain, so they read VmTargetName() — the
	// out-of-process vm plugin cannot LoadUnified to compute it itself, so the host threads
	// the already-resolved domain identity through (post-P33 collision-free per-deploy domains).
	runner := newCheckRunner(kit.RunnerConfig{
		Exec:           executor,
		Mode:           RunModeLive,
		Env:            env,
		HasRuntime:     hasRuntime,
		Box:            c.Box,
		Instance:       c.Instance,
		VmName:         domainID,
		VerifyOnly:     true,
		CandyDirs:      rctx.CandyDirs,
		CandyScanErr:   rctx.CandyScanErr,
		HostVars:       hostVars,
		TargetResolver: venueResolver(c.Instance),
	})
	results := RunPlan(context.Background(), runner, set, nil, false)
	return liveResult{Steps: results, Header: fmt.Sprintf("VM: charly-%s (ssh %s@%s:%d)", c.Box, user, host, port)}, nil
}

// resolveVmTarget resolves the VM check request (c.Box) to its kind:vm entity
// name, an optional nested-leaf node (for a dotted "parent.child" path), and the
// VmSpec.
func (c *CheckLiveCmd) resolveVmTarget(uf *UnifiedFile) (vmName, domainID string, nestedLeaf *BundleNode, spec *VmSpec) {
	// Schema v4: c.Box may be
	//   (a) a kind:vm entity name directly (e.g. "arch"),
	//   (b) a kind:deployment name with target:vm (e.g. "arch-vm") whose
	//       Vm field points at the actual kind:vm entity, OR
	//   (c) a dotted path "parent.child" where `parent` is a target:vm
	//       deployment and `child` is a nested node whose tests run in
	//       the parent's SSH substrate.
	//
	// vmName resolves to the kind:vm ENTITY (the disk/spec source); domainID is the per-deploy
	// DOMAIN IDENTITY (charly-<domainID> is the live libvirt domain + managed ssh alias + ssh-port
	// key). The domain is named after the DEPLOY (the key the operator typed — c.Box, or the parent
	// for a dotted leaf), NOT the entity, so distinct beds sharing one entity stay collision-free.
	vmName = c.Box
	domainKey := c.Box
	if uf.Bundle != nil {
		if entry, ok := uf.Bundle[c.Box]; ok && nodeTraits(&entry).Venue == "ssh" && entry.From != "" { // vm (ssh venue)
			vmName = entry.From
		} else if idx := strings.Index(c.Box, "."); idx > 0 {
			root := c.Box[:idx]
			if parent, present := uf.Bundle[root]; present && nodeTraits(&parent).Venue == "ssh" { // vm (ssh venue)
				if parent.From != "" {
					vmName = parent.From
				}
				domainKey = root // the parent vm deploy owns the live domain
				nestedLeaf = resolveNestedNode(uf.Bundle, c.Box)
			}
		}
	}
	domainID = vmDomainIdentity(domainKey)
	if uf.VM != nil {
		spec, _ = resolveVmViaPlugin(uf.VM[vmName])
	}
	return vmName, domainID, nestedLeaf, spec
}

// loadVmCheckPlans aggregates the VM deployment's check plan from the project
// and per-machine deploy sources plus add_candy deploy-scope steps, returning
// the merged plan and the SSH user/port (possibly overridden by local VmState).
func (c *CheckLiveCmd) loadVmCheckPlans(uf *UnifiedFile, dir, vmName string, nestedLeaf *BundleNode, user string, port int) (plan []spec.Step, outUser string, outPort int) {
	outUser, outPort = user, port
	// Two deploy sources for VMs:
	//   - project-level: charly.yml / charly.yml `deployments.images["vm:<name>"]`
	//     → holds the authored `tests:` list (part of the repo).
	//   - per-machine:   ~/.config/charly/charly.yml `images["vm:<name>"]`
	//     → holds VmState written by `charly bundle add vm:<name>` and any local
	//       overrides/additions.
	//
	// Schema v3: also accept plain-identifier deployment entries whose
	// `target: vm` + `vm: <c.Box>` resolves to the same VM.
	// This is what makes `charly check live <deploy-name>` work for beds like
	// `arch-vm` that don't carry the legacy `vm:` prefix in the key.
	// Merge by id (local replaces project); same rules as MergeDeployDescriptions.
	// Resolve the VM's deploy entry via THE shared findVmDeployNode (deploy.go)
	// — the same lookup `charly bundle add` uses — by deploy NAME (c.Box) first,
	// then the vm entity (vmName). Keying by name first means a bed whose key
	// differs from its vm entity (check-k3s-vm -> vm: k3s-vm) resolves to its
	// own entry rather than being mis-matched via the vm entity name.
	var projectPlan, localPlan []spec.Step
	var addCandies []string
	// Nested dotted-path short-circuit: when the request is for a
	// child node, use its own plan directly instead of the parent's.
	if nestedLeaf != nil {
		projectPlan = nestedLeaf.Plan
		addCandies = nestedLeaf.AddCandy
	} else if pc := uf.ProjectBundleConfig(); pc != nil {
		if entry, ok := findVmDeployNode(pc.Bundle, c.Box, vmName); ok {
			projectPlan = entry.Plan
			addCandies = entry.AddCandy
		}
	}
	if dc := deploykit.LoadDeployConfigForRead("charly check vm"); dc != nil {
		if entry, ok := findVmDeployNode(dc.Bundle, c.Box, vmName); ok {
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

	// Collect deploy-scope steps from the candies this VM deployment applies,
	// so ANY VM deploy — disposable bed OR persistent operator VM — that adds a
	// candy automatically runs that candy's plan (R3).
	plan = append(plan, collectAddCandySteps(uf, dir, addCandies)...)
	return plan, outUser, outPort
}

// vmHostdevCount returns how many <hostdev> passthrough devices the VM spec
// declares — the operator's INTENT, sourced from the authored VmSpec rather
// than the running domain (a libvirt drop would zero the live count and re-mask
// a silent passthrough failure). nil-safe at every level: a spec with no
// libvirt block, no devices block, or no hostdevs all yield 0, which a GPU check
// check reads as "no GPU configured for this VM" (legit N/A).
func vmHostdevCount(spec *VmSpec) int {
	if spec == nil || spec.Libvirt == nil || spec.Libvirt.Devices == nil {
		return 0
	}
	return len(spec.Libvirt.Devices.Hostdevs)
}

// collectAddCandyDeployCheck collects the deploy-scope check checks from each
// candy a VM deployment applies via add_candy. ProjectCandies resolves the
// project's LOCAL candy map (the shared check-only candies live here); remote
// @github candies not materialized locally are skipped. This is the general
// mechanism that lets `charly check live <vm>` run a candy's checks against ANY
// deployment that applies it — the disposable bed or the persistent operator
// VM — so one shared check-only candy covers both (no per-deploy copy, R3).
func collectAddCandySteps(uf *UnifiedFile, dir string, addCandies []string) []spec.Step {
	if uf == nil || len(addCandies) == 0 {
		return nil
	}
	// ScanAllCandyWithConfig (not ProjectCandies) — it includes the FILESYSTEM
	// candies under candy/ discovered via `discover:`, where the shared
	// check-only candies live; ProjectCandies only sees inline `candy:` entries.
	var cfg *Config
	if uf != nil {
		cfg = uf.ProjectConfig()
	}
	candyMap, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil || candyMap == nil {
		return nil
	}
	var out []spec.Step
	for _, ref := range addCandies {
		// Only LOCAL (filesystem) candies contribute steps here — the shared
		// candies live in the project's candy/ dir. Remote @github candies are
		// SKIPPED: they carry their own context (and a re-scan can resolve a
		// different cached version than what was deployed).
		if IsRemoteCandyRef(ref) {
			continue
		}
		lyr, ok := candyMap[BareRef(ref)]
		if !ok || lyr == nil {
			continue
		}
		out = append(out, bakeableSteps(lyr.plan)...)
	}
	return out
}

// candySourceDirs builds a candy-name → source-dir map for anchoring relative
// committed-APK paths in adb/appium checks against the authoring candy's tree
// (local or @github-fetched). A scan error is RETURNED, never swallowed: the
// caller stores it on the Runner so resolveCheckApk can fail an apk check with
// the REAL cause ("candy source-dir scan failed: …") instead of a misleading
// "no such file" — and an apk-free check is unaffected (it never consults the
// map).
func candySourceDirs(dir string, cfg *Config) (map[string]string, error) {
	candyMap, err := ScanAllCandyWithConfig(dir, cfg)
	if err != nil {
		return nil, fmt.Errorf("scanning candy source dirs: %w", err)
	}
	return candyDirsFromScan(candyMap), nil
}

// candyDirsFromScan extracts the candy-name → SourceDir map from a scanned candy
// set. Keyed by the candy MAP KEY — the check's Origin form: a bare name for a
// local candy ("sshd"), the bare @github ref for a fetched one
// ("github.com/owner/repo/candy/<name>"). CollectDescriptions stamps
// Origin = "candy:" + this same key, so resolveCheckApk's CandyDirs[origin]
// lookup matches in BOTH cases. The SAME scanned map drives the plugin loader
// (R3 — one scan, both consumers).
func candyDirsFromScan(candyMap map[string]*Candy) map[string]string {
	if len(candyMap) == 0 {
		return nil
	}
	out := make(map[string]string, len(candyMap))
	for key, lyr := range candyMap {
		if lyr != nil && lyr.SourceDir != "" {
			out[key] = lyr.SourceDir
		}
	}
	return out
}

// checkRunnerContext carries the committed-APK anchoring (CandyDirs / CandyScanErr) a live
// baked-plan runner folds into its RunnerConfig. resolveCheckRunnerContext computes it (and
// performs the plugin-load side effect); the caller wires the fields into kit.RunnerConfig.
type checkRunnerContext struct {
	CandyDirs    map[string]string
	CandyScanErr error
}

// resolveCheckRunnerContext computes the committed-APK anchoring + loads the OUT-OF-TREE plugin
// candies a live baked-plan runner needs, so `charly check live` and `charly check feature run`
// resolve adb/appium `apk:` checks IDENTICALLY (R3). They previously diverged — only check live
// populated CandyDirs, so a committed-APK check passed under check live yet failed to anchor
// ("0 candies scanned") under feature run. Any RunModeLive runner that executes a baked plan
// MUST fold its result into the RunnerConfig (CandyDirs + CandyScanErr).
func resolveCheckRunnerContext(box, dir string, cfg *Config) checkRunnerContext {
	// Scan the RESOLVED candy set ONCE (local + @github-fetched): it carries each
	// candy's SourceDir (committed-APK anchoring) AND its `plugin:` block, so one
	// scan feeds BOTH consumers (R3). A box that vendors all its candies via @github
	// (every box/<distro>) has no project-local Candy map, so the plugin set MUST
	// come from this scan — never from LoadUnified.
	//
	// ExtraCandyRefs adds the BED's own `add_candy:` candies to the collection: the
	// image-closure walk never reaches them, so a bed that add_candy's a host-side
	// PLUGIN candy (e.g. plugin-spice for the `spice:` check verb authored INLINE in
	// the bed plan, with no candy in the image closure requiring it) would otherwise
	// leave the plugin unloaded and the `spice:` step failing as an unknown verb.
	addCandy, refWords := deployNodePluginContext(dir, box)
	// The VM plugin candy (verb:libvirt) is external (out-of-process) and in no box's image
	// closure, so a bed whose plan dispatches `libvirt:` (e.g. check-fedora-vm's libvirt-verb-
	// dispatches step) needs it pulled in by its canonical ref — the same host-side-plugin pattern
	// as a bed add_candy'ing plugin-spice for `spice:`. Harmless for non-VM beds: loadProjectPlugins
	// build-connects it only if the plan references libvirt; in a bed CHARLY_REPO_OVERRIDE resolves
	// the ref to the local superproject under development.
	addCandy = append(addCandy, vmPluginCandyRef())
	candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, ResolveOpts{ExtraCandyRefs: addCandy})
	if scanErr != nil {
		return checkRunnerContext{CandyScanErr: fmt.Errorf("scanning candy source dirs: %w", scanErr)}
	}
	// Connect + register the OUT-OF-TREE plugin candies a `check: plugin: <verb>` step
	// REFERENCES, out-of-process (built-in plugins are already compiled in). Perf-scoped
	// via collectReferencedPluginWords: the candy/box plans + candy external_builder +
	// the bed's OWN refWords (its substrate kind + the inline plugin verbs in its
	// flattened plan — the `spice:` step above) name every plugin the bed dispatches, so
	// an UNREFERENCED plugin candy in the scan (the rest of a box/<distro> plugin set) is
	// not host-built while a referenced one always loads (over-load safe, never under). A
	// build/connect failure is surfaced as a warning; the bed's plugin check then fails
	// loudly via runPluginVerb's unresolved-verb path. The shared check-runner setup is
	// the ONE place every check path (box/live) loads plugins (R3).
	refs := collectReferencedPluginWords(candyMap, cfg.Box, refWords)
	if err := loadProjectPlugins(context.Background(), candyMap, refs); err != nil {
		fmt.Fprintf(os.Stderr, "warning: plugin load: %v\n", err)
	}
	return checkRunnerContext{CandyDirs: candyDirsFromScan(candyMap)}
}

// deployNodePluginContext resolves the deploy/bed node named `name` in the project at
// `dir` ONCE (the SAME project-bundle loader the deploy walker uses) and returns the
// two plugin-loading inputs the check runner (resolveCheckRunnerContext) and the deploy
// path (loadDeployPlugins) both need (R3 — one helper, both paths):
//
//   - addCandy: the deploy's `add_candy:` refs. The project candy scan
//     (ScanAllCandyWithConfig) collects only IMAGE-closure candies (CollectRemoteRefs
//     walks base/builder/require edges); add_candy candies are NOT in that set, so both
//     callers feed these to ScanAllCandyWithConfigOpts' ExtraCandyRefs to fetch them.
//   - refWords: the plugin WORDS the node references DIRECTLY — its substrate kind (an
//     external deploy-substrate plugin word, e.g. `exampledeploy`) + every inline
//     Op.Plugin in its FLATTENED plan. flattenBundleVenues hoists member/nested steps
//     into the root node.Plan, so this ONE walk covers the whole bed including members
//     (e.g. a `spice:` check verb authored inline). These scope loadProjectPlugins to
//     the plugins the deploy actually dispatches — caught here because they appear in
//     NEITHER a candy plan NOR a box plan (over-load safe, never under-load).
//
// Best-effort: (nil, nil) on any load failure or unknown name (the caller still
// collects candy + box references; a genuinely missing reference fails loudly at
// dispatch, never silently mis-deploys).
func deployNodePluginContext(dir, name string) (addCandy []string, refWords []string) {
	tree, err := resolveTreeRoot(dir)
	if err != nil || tree == nil {
		return nil, nil
	}
	// Resolve the named node, walking a DOTTED path into nested children (the bed runner
	// deploys a nested child via `charly bundle add <root>.<child>` — its name is dotted and
	// is NOT a top-level tree key). Without dotted resolution a nested-child deploy surfaces
	// NO plugin words and its substrate word never loads its provider (ResolveTarget →
	// "unknown target"). The single source for "given a (possibly dotted) deploy name, which
	// node?".
	node, ok := resolveDeployNodeByPath(tree, name)
	if !ok {
		return nil, nil
	}
	inSubmodule := selfSuperprojectOverridePair(dir) != ""
	// Collect the node's plugin words AND recurse into its nested children: a deploy whose
	// OWN substrate OR whose nested children's substrates are externalized must load each
	// serving plugin. Two cases this covers, GENERALLY (never substrate-special-cased):
	//   - a dotted child deploy (check-arch-vm.arch-host) — node IS the nested child, so its
	//     OWN target (e.g. `local`) is surfaced + its plugin auto-injected;
	//   - a single-process tree deploy (a pod root walked in one process, its nested children
	//     of a DIFFERENT substrate) — the recursion surfaces every child's substrate word.
	var visit func(n *BundleNode)
	visit = func(n *BundleNode) {
		if n == nil {
			return
		}
		addCandy = append(addCandy, n.AddCandy...)
		if n.Target != "" {
			refWords = append(refWords, n.Target)
			// An EXTERNALIZED deploy substrate (vm/local/android/k8s) is served by an
			// out-of-process plugin candy. A main-repo project discovers that candy from
			// candy/ directly (its `discover:` scans candy/*), but a box/<distro> SUBMODULE
			// scans only its own + imported candies — so the parent's
			// candy/plugin-deploy-<substrate> is absent from the submodule's scan and the
			// substrate word would never resolve to its provider. Auto-inject the canonical
			// ref via ExtraCandyRefs, but ONLY in a submodule context — the main repo already
			// has it locally, and injecting a remote ref there over the local candy is both
			// redundant and (for an as-yet-unpublished plugin) a fetch failure. In a submodule
			// bed CHARLY_REPO_OVERRIDE redirects the ref to the local superproject under
			// development. The SAME host-side-plugin pattern as vmPluginCandyRef (verb:libvirt),
			// generalized to every external substrate (R3).
			if inSubmodule {
				if ref, ok := externalDeploySubstratePluginRef(n.Target); ok {
					addCandy = append(addCandy, ref)
				}
			}
		}
		for i := range n.Plan {
			op := &n.Plan[i].Op
			if w := op.Plugin; w != "" {
				refWords = append(refWords, w)
			}
			// Also surface each step's VERB discriminator. A closed-#Op EXTERNAL check verb
			// (libvirt/spice/kube/adb/appium) is NOT a `plugin:` word, so without this the
			// loader never build-connects the out-of-process plugin candy serving it — e.g. a
			// bed's `libvirt: list` step would SKIP with "unknown verb". Over-load safe: a
			// compiled-in verb's candy is already registered, and a non-plugin verb has none.
			if v, err := op.Kind(); err == nil && v != "" {
				refWords = append(refWords, v)
			}
		}
		for _, ck := range sortedNestedKeys(n.Children) {
			visit(n.Children[ck])
		}
	}
	visit(node)
	// NOTE: the externalized DETECTION-builder plugins (cargo/npm/pixi/aur) are NOT injected here.
	// A builder is triggered by the DEPLOY's resolved image closure (a pixi.toml / aur: section), not
	// by the deploy NODE this walk sees — and surfacing all four across a whole-box scan over-built
	// unrelated builder plugins (aur on a fedora deploy). The build PRE-PASS (builder_preresolve.go)
	// instead detects EXACTLY the builders the deploy triggers (distro-gated) and connects only those
	// on-demand, by their canonical ref (ensureBuildersConnected), where it has the resolved closure.
	return addCandy, refWords
}

// resolveDeployNodeByPath resolves a (possibly DOTTED) deploy name to its BundleNode,
// descending node.Children for each dotted segment (the SAME nested-tree shape
// ResolveDeployChain walks). A bare name is the top-level entry; a dotted name
// (root.child[.grandchild…]) is the nested child the bed runner deploys via `charly bundle
// add <root>.<child>`. Returns false when any segment is absent.
func resolveDeployNodeByPath(tree map[string]BundleNode, name string) (*BundleNode, bool) {
	parts := strings.Split(name, ".")
	root, ok := tree[parts[0]]
	if !ok {
		return nil, false
	}
	cur := &root
	for _, seg := range parts[1:] {
		child, ok := cur.Children[seg]
		if !ok || child == nil {
			return nil, false
		}
		cur = child
	}
	return cur, true
}

// checkLiveLocal gathers a `target: local` deployment's deploy-scope check on its
// host venue. The venue is a ShellExecutor (host: local) or SSHExecutor
// (host: <remote>) selected by the shared rootExecutorForDeployNode, and dotted
// paths compose through ResolveDeployChain. It resolves the (possibly dotted) node +
// root venue, then runs the shared local plan core (runLocalDeployScopePlan).
//
// Local deploys carry no OCI image labels, so there is no candy/box test section —
// checks come from the resolved kind:local template's `check:` (base) merged with the
// deploy entry's `check:` and the per-host charly.yml overlay. Host-context vars only:
// no HOST_PORT:<N> / CONTAINER_IP. The check-run "live" seam (hostCheckLive) consumes
// it via checkLiveGather.
func (c *CheckLiveCmd) checkLiveLocal() (liveResult, error) {
	dir, _ := os.Getwd()
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return liveResult{}, err
	}

	// Resolve the target node (leaf for a dotted path; the entry otherwise)
	// and the root-segment node (whose host: selects the chain's root venue).
	dotted := strings.Contains(c.Box, ".")
	var node, rootNode *BundleNode
	if uf.Bundle != nil {
		if dotted {
			node = resolveNestedNode(uf.Bundle, c.Box)
			root, _, _ := strings.Cut(c.Box, ".")
			if entry, ok := uf.Bundle[root]; ok {
				rn := entry
				rootNode = &rn
			}
		} else if entry, ok := uf.Bundle[c.Box]; ok {
			n := entry
			node = &n
			rootNode = &n
		}
	}
	if node == nil {
		return liveResult{}, fmt.Errorf("check live: local deployment %q not found", c.Box)
	}

	// Select the root venue from the root node's host:, then compose nested
	// hops for a dotted path through the shared ResolveDeployChain.
	executor, err := rootExecutorForDeployNode(rootNode)
	if err != nil {
		return liveResult{}, fmt.Errorf("check live %q: %w", c.Box, err)
	}
	if dotted {
		if roots, _ := resolveTreeRoot(dir); roots != nil {
			if _, chain, chainErr := ResolveDeployChain(roots, c.Box, executor); chainErr == nil && chain != nil {
				executor = chain
			}
		}
	}

	venue := "host (local)"
	if _, isShell := executor.(ShellExecutor); !isShell {
		venue = executor.Venue()
	}
	header := fmt.Sprintf("Local deploy: %s [%s]", c.Box, venue)

	results, hadPlan, err := runLocalDeployScopePlan(dir, node, c.Box, c.Instance, executor)
	if err != nil {
		return liveResult{}, err
	}
	if !hadPlan {
		return liveResult{Header: header, NoPlan: true}, nil
	}
	return liveResult{Steps: results, Header: header}, nil
}

// checkLocalDeployScope collects a local deployment's deploy-scope checks —
// kind:local template `check:` (base) merged with the deploy entry `check:`
// (extends/overrides) and the per-host charly.yml overlay — and runs them on
// `exec`. Shared by `charly check live <local>` (checkLiveLocal) and
// `charly bundle add <local> --verify` (the local deploy target) so the two surfaces
// source + run probes identically (R3). Host-context vars only (no
// HOST_PORT:<N> / CONTAINER_IP). Returns the failure count.
func checkLocalDeployScope(dir string, node *BundleNode, image, instance, _ string, _ []string, exec DeployExecutor, format string) (int, error) { //nolint:unparam // error return kept for symmetry with sibling deploy-scope checks
	results, hadPlan, err := runLocalDeployScopePlan(dir, node, image, instance, exec)
	if err != nil {
		return 0, err
	}
	if !hadPlan {
		fmt.Fprintln(os.Stderr, "No plan steps to run.")
		return 0, nil
	}
	return reportSteps(os.Stdout, results, format), nil
}

// runLocalDeployScopePlan collects a local deployment's deploy-scope plan — the kind:local
// template `check:` (base) + the deploy node `check:` + the per-host overlay — and runs it on
// exec, returning the per-step results. hadPlan is false when there were no plan steps (the
// caller prints its own "no plan" line). CLI-free core shared by checkLocalDeployScope (the
// external local deploy --verify + the check-live CLI shell, reporting to os.Stdout) and
// CheckLiveCmd.checkLiveLocal (returning Steps for the check-run reply) — R3. Host-context vars
// only (no HOST_PORT:<N> / CONTAINER_IP). Folds the ${HOST} CloseHosts teardown the pre-P12
// local path discarded (design §6): the ssh -L forwards a VM-peer subject opens are torn down
// after the plan run, exactly as checkLiveVM/checkLiveGroup already do.
func runLocalDeployScopePlan(dir string, node *BundleNode, image, instance string, exec DeployExecutor) (results []StepResult, hadPlan bool, err error) { //nolint:unparam // err kept for symmetry; RunPlan never errors here today
	var plan []spec.Step
	if node != nil && strings.TrimSpace(node.From) != "" {
		if spec, _ := findLocalSpec(dir, strings.TrimSpace(node.From)); spec != nil {
			plan = append(plan, spec.Plan...)
		}
	}
	if node != nil {
		plan = append(plan, node.Plan...)
	}
	if dc := deploykit.LoadDeployConfigForRead("charly check live"); dc != nil {
		if entry, ok := dc.Bundle[deployKey(image, instance)]; ok {
			plan = append(plan, entry.Plan...)
		} else if entry, ok := dc.Bundle[image]; ok {
			plan = append(plan, entry.Plan...)
		}
	}

	user := os.Getenv("USER")
	home, herr := exec.ResolveHome(context.Background(), user)
	if herr != nil || home == "" {
		home = os.Getenv("HOME")
	}
	resolver := &CheckVarResolver{Env: map[string]string{
		"IMAGE":    image,
		"INSTANCE": instance,
		"USER":     user,
		"HOME":     home,
	}, HasRuntime: true}

	if len(plan) == 0 {
		return nil, false, nil
	}
	set := &LabelDescriptionSet{Deploy: []LabeledDescription{{Origin: "local:" + image, Plan: plan}}}
	env, hasRuntime := resolverEnv(resolver)
	// Generic cross-deployment support (on: driver + ${HOST:<member>}) — a local SUBJECT bed
	// can drive a peer too (R3). Capture + defer-close the ssh -L cleanups (design §6 leak fix):
	// the pre-P12 local path discarded them, so a local subject driving a VM peer via
	// ${HOST:<member>:<port>} leaked the forward.
	hostVars, hostCleanups := resolveHostVarsForSteps(plan, instance)
	defer closeHostCleanups(hostCleanups)
	runner := newCheckRunner(kit.RunnerConfig{
		Exec:           exec,
		Mode:           RunModeLive,
		Env:            env,
		HasRuntime:     hasRuntime,
		Box:            image,
		Instance:       instance,
		VerifyOnly:     true,
		HostVars:       hostVars,
		TargetResolver: venueResolver(instance),
	})
	return RunPlan(context.Background(), runner, set, nil, false), true, nil
}

// subdeployments (subject + driver) brought up on the shared charly net, and every
// plan step carries its member venue (flattenBundleVenues). So there is nothing to
// exec into at the root: the base executor is a placeholder and every step
// venue-dispatches to its member (venueResolver), while ${HOST:<member>} addresses
// resolve via resolveHostVarsForSteps. The check-run "live" seam (hostCheckLive)
// consumes it via checkLiveGather.
func (c *CheckLiveCmd) checkLiveGroup() (liveResult, error) {
	dir, _ := os.Getwd()
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return liveResult{}, err
	}
	entry, ok := uf.Bundle[c.Box]
	if !ok {
		return liveResult{}, fmt.Errorf("check live: group bed %q not found", c.Box)
	}
	plan := entry.Plan
	if len(plan) == 0 {
		return liveResult{NoPlan: true}, nil
	}
	header := fmt.Sprintf("Group bed: %s [%d sibling member(s); venue-dispatched, no root container]", c.Box, len(entry.Members))

	resolver := &CheckVarResolver{Env: map[string]string{
		"IMAGE":    c.Box,
		"INSTANCE": c.Instance,
	}, HasRuntime: true}
	// Set the runner identity AND load the OUT-OF-PROCESS plugin candies the bed's
	// flattened plan REFERENCES — a cdp:/spice:/… verb authored under a member. A group
	// bed has no single image, so the load keys on the BED NAME (its flattened,
	// venue-stamped plan names every referenced verb) plus the project candy scan, the
	// SAME plugin-load path the container/vm/local venues use (R3). Without it an external
	// check verb under a member fails live as "unknown verb" — the cross-pod-cdp regression
	// once cdp left the compiled-in set (the group venue was the one path missing this).
	rctx := resolveCheckRunnerContext(c.Box, dir, uf.ProjectConfig())
	env, hasRuntime := resolverEnv(resolver)
	// Every step venue-dispatches to its member (its venue != the group root
	// name), so the placeholder base executor below is never used.
	// venueResolver performs the per-step swap; ${HOST:<member>} addresses
	// resolve through resolveHostVarsForSteps, whose ssh -L cleanups are deferred here.
	hostVars, hostCleanups := resolveHostVarsForSteps(plan, c.Instance)
	defer closeHostCleanups(hostCleanups)
	runner := newCheckRunner(kit.RunnerConfig{
		Exec:           ShellExecutor{},
		Mode:           RunModeLive,
		Env:            env,
		HasRuntime:     hasRuntime,
		Box:            c.Box,
		Instance:       c.Instance,
		CandyDirs:      rctx.CandyDirs,
		CandyScanErr:   rctx.CandyScanErr,
		HostVars:       hostVars,
		TargetResolver: venueResolver(c.Instance),
	})
	set := &LabelDescriptionSet{Deploy: []LabeledDescription{{Origin: "group:" + c.Box, Plan: plan}}}
	results := RunPlan(context.Background(), runner, set, nil, false)
	return liveResult{Steps: results, Header: header}, nil
}

// containerImageRef + containerImage (the live-container image-ref
// inspectors) live in commands.go — ONE inspect implementation shared by
// mcp / service / remove / start-direct and the check runner.
