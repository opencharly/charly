package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"gopkg.in/yaml.v3"
)

// CheckFailExitCode is the process exit code `charly check` returns when an
// check RAN to completion but one or more checks FAILED — deliberately
// distinct from 0 (all checks passed) and 1 (command / usage / infra error:
// couldn't build, deploy, or even run the check). This lets automation (and
// `charly check run <bed>`) tell "the thing under test is broken" apart from "the
// check itself couldn't run". Mirrors the goss / pytest 0/1/2 convention;
// main() maps CheckFailedError to this code.
const CheckFailExitCode = 2

// CheckFailedError marks an check that ran but had failing checks. main()
// detects it via errors.As and exits CheckFailExitCode. Wrap with %w to
// preserve the chain through callers.
type CheckFailedError struct {
	Failed int    // number of failed checks (0 = aggregate/unknown)
	Msg    string // optional message override (e.g. a bed-level aggregate)
}

func (e *CheckFailedError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("%d check(s) failed", e.Failed)
}

// CheckCmd is the unified `charly check` command tree — declarative checkuation,
// AI-driven iteration, and live-container probe verbs all under one
// prefix. Three primary verbs (image / live / run) replace the old
// `charly check box` / `charly check live <image>` / `charly check run <score>` split:
//
//   - `charly check box <image>` — pure-image artifact check (disposable
//     container, build-scope checks only, no host port mapping, no
//     volumes attached).
//   - `charly check live <name>` — full-stack check against a running
//     deployment (pod / vm / host / k8s); runtime variables resolved.
//   - `charly check run <name>` — overloaded by the resolved kind: a
//     kind:check bed runs the full R10 sequence (build → check box →
//     deploy → check live → fresh update → tear down); a kind:score
//     drives an AI through iteration cycles. ONE bed per invocation —
//     run a whole roster concurrently via the /verify-beds workflow
//     (one agent per bed). (Replaces the retired `charly check kind <subkind>`,
//     whose hardcoded per-kind bed table moved into check.yml as
//     kind:check entities.)
//
// The mode is explicit; there is no autodetect or implicit fallback.
//
// EVERY live-container probe verb (cdp/wl/vnc/dbus/mcp/record + kube/adb/appium/spice/
// libvirt) is now a DECLARATIVE check verb served by an out-of-process plugin — none has an
// in-core sub-Cmd here; they all dispatch via the provider registry (invokeVerbProvider).
// `wl` was the LAST live-container verb compiled into charly; after it, ZERO check verbs are
// in-core.
//
// Check-run management subcommands (list-ai, list, sync-credential,
// report, scope, last-tag, note, run-local, self-evaluate) are the
// renamed `charly check *` surface.
type CheckCmd struct {
	// Three primary modes
	Box     CheckBoxCmd     `cmd:"" name:"box" help:"Pure-box check (disposable container, build-scope checks)"`
	Live    CheckLiveCmd    `cmd:"" help:"Full-stack check against a running deployment"`
	Run     CheckRunCmd     `cmd:"" help:"Run a kind:check R10 bed (full sequence) or drive an AI through an iterate: entity's iteration cycles"`
	Feature CheckFeatureCmd `cmd:"" help:"Run a running deployment's baked plan as acceptance tests; agent steps are agent-graded (Agent Driven Evaluation)"`

	// Live-container probe verbs — ALL out-of-process now (no in-core sub-Cmd here)
	// `wl` is NOT a CLI subcommand here — the Wayland/sway desktop driver (input, windows,
	// screenshots, sway IPC, overlay, atspi, clipboard — ~40 methods) was relocated to the
	// out-of-tree candy/plugin-wl module (EXEC-based, driving the venue's compositor via
	// wlrctl/grim/wtype/swaymsg over the executor reverse channel; the screenshot PNG pulls
	// via GetFile). The `wl:` DECLARATIVE check verb dispatches to that external plugin via
	// the provider registry (invokeVerbProvider); there is no host `charly check wl`. wl was
	// the LAST live-container verb compiled into charly — after it, ZERO check verbs are
	// in-core. The CLI-only `--from-cdp`/`--from-sway`/`--from-x11` coordinate translation was
	// DROPPED with the move (the declarative `wl: click` uses X/Y directly), shedding the core's
	// minimal CDP WebSocket client + golang.org/x/net/websocket from charly's core.
	// `dbus` is NOT a CLI subcommand here — the D-Bus driver (list/call/introspect/notify)
	// was relocated to the out-of-tree candy/plugin-dbus module (EXEC-based, driving the
	// venue's session bus with gdbus over the executor reverse channel — no godbus). The
	// `dbus:` DECLARATIVE check verb dispatches to that external plugin via the provider
	// registry (invokeVerbProvider); there is no host `charly check dbus`. STRUCTURAL
	// externalization, not a dep-shed: dbus drives the venue bus with gdbus, never godbus (the
	// in-core best-effort notify in notify.go does the same). charly's core links no godbus at
	// all — the Secret Service keyring (godbus) lives out-of-process in candy/plugin-secrets.
	// `libvirt` is NOT a CLI subcommand here — the VM/libvirt-API probe verb (`charly check
	// libvirt`) is served by the out-of-process candy/plugin-vm verb plugin, nested under
	// `charly check` at runtime via attachNestedCheckPlugins exactly like `kube`/`adb`/`appium`.
	// This shed go-libvirt + kata-containers/govmm + libvirt.org/go/libvirtxml from charly's core.
	// `kube` is NOT a CLI subcommand here — the Kubernetes cluster-probe implementation (+ the
	// client-go + apimachinery dependency) was dep-shed into the out-of-tree
	// candy/plugin-kube module. `kube` is now a DECLARATIVE check VERB that dispatches to that
	// external plugin via the provider registry (invokeVerbProvider, after the host pre-resolves
	// any --cluster profile to a kubeconfig context); there is no host `charly check kube`.
	// `adb` is NOT a CLI subcommand here — the Android Debug Bridge implementation (+ the
	// goadb ADB-wire dependency) was dep-shed into the out-of-tree
	// candy/plugin-adb module. `adb` is now a DECLARATIVE check VERB that dispatches to that
	// external plugin via the provider registry (invokeVerbProvider); there is no host
	// `charly check adb`.
	// `appium` is NOT a CLI subcommand here — the Appium WebDriver implementation (+ the
	// tebeka/selenium dependency) was dep-shed into the out-of-tree candy/plugin-appium
	// module. The `appium:` DECLARATIVE check verb dispatches to that external plugin via
	// the provider registry (invokeVerbProvider); there is no host `charly check appium`.
	// `spice` is NOT a CLI subcommand here — the SPICE-wire implementation (+ the upstream
	// SPICE wire client library and its cgo opus/portaudio audio transitives) was dep-shed
	// into the out-of-tree candy/plugin-spice module. The `spice:` DECLARATIVE check verb
	// dispatches to that external plugin via the provider registry (invokeVerbProvider,
	// after the host pre-resolves the VM's live SPICE endpoint to a dialable address);
	// there is no host `charly check spice`.
	// `mcp` is NOT a CLI subcommand here — the MCP-protocol client implementation (the
	// github.com/modelcontextprotocol/go-sdk client + the dial/dispatch/format layer) was
	// dep-shed into the out-of-tree candy/plugin-mcp module. The `mcp:` DECLARATIVE check verb
	// dispatches to that external plugin via the provider registry (invokeVerbProvider, after
	// the host pre-resolves the deployment's declared mcp_provides + the picked, host-routable
	// dial endpoint, via the cc.ResolveImageLabel + cc.ResolveEndpoint reverse-legs); there is no host `charly check mcp`.
	// `cdp` is NOT a CLI subcommand here — the Chrome DevTools Protocol client (the
	// golang.org/x/net/websocket CDP WebSocket client + the open/list/text/eval/screenshot/
	// click/SPA dial+dispatch layer) was dep-shed into the out-of-tree candy/plugin-cdp module.
	// The `cdp:` DECLARATIVE check verb dispatches to that external plugin via the provider
	// registry (invokeVerbProvider; the plugin resolves the deployment's CDP port 9222 to a
	// host-reachable DevTools base URL itself via the cc.ResolveEndpoint reverse-leg); there is no host
	// `charly check cdp`. (charly's core no longer keeps any CDP WebSocket client: the last
	// in-core consumer — the `wl … --from-cdp` coordinate translation — externalized into
	// candy/plugin-wl, so the core's minimal CDP WebSocket client was deleted and
	// golang.org/x/net/websocket left the core.)
	// `record` is NOT a CLI subcommand here — the recording driver (asciinema/wf-recorder/
	// pixelflux session management) was dep-shed into the out-of-tree candy/plugin-record
	// module. The `record:` DECLARATIVE check verb dispatches to that external plugin via the
	// provider registry (invokeVerbProvider) — the FIRST EXEC-based external verb: the host
	// attaches its live DeployExecutor over the E3b reverse channel and the plugin drives the
	// venue with RunCapture/GetFile. There is no host `charly check record`.
	// `vnc` is NOT a CLI subcommand here — the RFB/VNC client (the stdlib-only RFC 6143 VNC
	// client + the status/screenshot/click/mouse/type/key/rfb dispatch layer) was dep-shed
	// into the out-of-tree candy/plugin-vnc module. The `vnc:` DECLARATIVE check verb
	// dispatches to that external plugin via the provider registry (invokeVerbProvider, after
	// the host pre-resolves the deployment's VNC endpoint — a container's published port 5900
	// OR a kind:vm deployment's libvirt <graphics type='vnc'> listener bridged/tunneled to a
	// host-reachable RFB address, via the cc.ResolveGraphicsEndpoint reverse-leg); there is no host `charly check vnc`
	// (the former `charly check vnc vm` VM-VNC CLI is subsumed into `vnc:` against a vm target).

	// Check-run management (was `charly check *`)
	ListAgent CheckListAgentCmd `cmd:"" name:"list-agent" help:"List configured agents from check.yml"`
	RunLocal  CheckRunLocalCmd  `cmd:"" name:"run-local" hidden:"" help:"Pod/VM-side iteration driver (not invoked directly)"`
	SyncCred  CheckSyncCredCmd  `cmd:"" name:"sync-credential" help:"Copy AI credentials into the score's target"`
	Scope     CheckScopeCmd     `cmd:"" name:"scope" help:"AI-facing: print current iteration scope"`
	LastTag   CheckLastTagCmd   `cmd:"" name:"last-tag" help:"AI-facing: print prior iteration's image tag"`
	SelfCheck CheckSelfCheckCmd `cmd:"" name:"self-evaluate" help:"AI-facing: rebuild current clone + re-run live check"`
	List      CheckListRunsCmd  `cmd:"" name:"list" help:"List past check runs under .check/<score>/"`
	Report    CheckReportCmd    `cmd:"" name:"report" help:"Render a past result-<calver>.yml"`
	Note      CheckNoteCmd      `cmd:"" name:"note" help:"Read/append the persistent NOTES.md memory for a score"`

	// Out-of-process CHECK subcommand plugins (e.g. `charly check kube`/`adb`/`appium`
	// extracted to shed client-go/selenium) attach here as dynamic kong commands. Populated
	// in main from collectExternalCommandPlugins' nestedByParent["check"] and dispatched
	// manually post-parse (they carry no Run()). Empty until such a plugin registers.
	kong.Plugins
}

// CheckLiveCmd runs tests against a running service — the deploy-time entry point.
//
//   - Extracts the image's three-section LabelDescriptionSet from OCI labels.
//   - Applies the local charly.yml tests overlay (merge by id:).
//   - Resolves ${…} variables using meta + deploy + podman-inspect of the
//     running container.
//   - Executes the merged spec (container-internal verbs via exec; host-side
//     verbs directly).
//
// The command exits non-zero on any failed check. Skipped checks (missing
// runtime context, skip: true, id-override with skip) do not fail the run.
type CheckLiveCmd struct {
	Box      string   `arg:"" help:"Box name"`
	Instance string   `short:"i" long:"instance" help:"Instance name"`
	Format   string   `long:"format" default:"text" help:"Output format: text, json, tap"`
	Filter   []string `long:"filter" help:"Only run checks with these verbs (repeatable)"`
	Section  string   `long:"section" help:"Only run this section: candy, box, or deploy"`
}

func (c *CheckLiveCmd) Run() error {
	// VM dispatch: if the name matches a vm.yml entity, route the test run
	// through SSH instead of podman exec. VM deploys don't have an OCI image
	// to pull labels from, so tests come exclusively from the charly.yml
	// overlay. This keeps the same declarative `tests:` authoring surface
	// working for `charly bundle add vm:<name>` flows, and also works for bare VMs
	// created via `charly vm create` before `charly bundle add` has been run.
	if c.isVmTarget() {
		return c.runVm()
	}

	// Local dispatch: a `target: local` deploy is a host filesystem apply, not
	// a container, so its deploy-scope probes run on the host (or over SSH for
	// host: <remote>) via a ShellExecutor/SSHExecutor — the SAME target-dispatch
	// `charly bundle add` uses — instead of the podman-exec container path below.
	if c.isLocalTarget() {
		return c.runLocalCheck()
	}

	// Group dispatch: a GROUP check bed (no workload cross-ref + sibling
	// Members — the §3 group+siblings cross-deployment shape) has no root
	// container. Its flattened, venue-stamped plan dispatches each step to its
	// member, so it runs through runGroupCheck instead of the container path
	// below (which would fail at resolveContainer / ExtractMetadata).
	if c.isGroupTarget() {
		return c.runGroupCheck()
	}

	engine, containerName, err := resolveContainer(c.Box, c.Instance)
	if err != nil {
		return err
	}

	// Load deploy overlay (local tests) AND project-level tests up front
	// so the deploy entry's `image:` field can drive metadata extraction.
	// Pre-2026-05-12 the code read the running container's image ref via
	// `containerImageRef`, which silently returned a stale ref on
	// volume-pinned deploys and dropped any probes added after the seed
	// image. The cutover deletes that fallback: the check runner now
	// inspects what the operator declared, not what the container
	// happens to be running. The hard-required `image:` field
	// (validateDeployRequiresBox in unified.go / deploy.go) guarantees
	// this lookup always finds a non-empty value.
	dir, _ := os.Getwd()
	var localPlan, projectPlan []Step
	var deployOverlay *BundleNode
	var projectCfg *Config
	if uf, ok, _ := LoadUnified(dir); ok && uf != nil {
		projectCfg = uf.ProjectConfig()
		// The bed's OWN bundle node carries authored plan steps (status-shows-*,
		// etc.). Merge them like the VM check path (loadVmCheckPlans) does — without
		// this a bundle-node `check:` never runs under `charly check live`, only the
		// baked candy/box plan + the per-host overlay do.
		if pc := uf.ProjectBundleConfig(); pc != nil && pc.Bundle != nil {
			if node := resolveNestedNode(pc.Bundle, c.Box); node != nil {
				projectPlan = node.Plan
			} else if entry, ok := pc.Bundle[c.Box]; ok {
				projectPlan = entry.Plan
			}
		}
	}
	dc := loadDeployConfigForRead("charly check live")
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
	overlayPlan := append(append([]Step(nil), projectPlan...), localPlan...)

	// Resolve the deploy key → declared image short-name via THE shared
	// resolver (deploy.go resolveDeployBoxName) — the same one charly config /
	// start / shell use. This used to be an inline operator-then-project
	// copy, which is exactly how `charly check live` diverged from `charly config`
	// for kind:check beds where key != image (check-jupyter-pod → jupyter).
	// deployOverlay (loaded above) is still consulted for the tests overlay
	// + runtime var resolution. The hard-required `image:` field
	// (validateDeployRequiresBox) guarantees a real image for every pod
	// deploy, so the resolver returns the declared image, never the key.
	imageRef := resolveDeployBoxName(c.Box, c.Instance)
	// Short names (e.g. `versa`) need to be resolved to a fully-
	// qualified registry ref before ExtractMetadata can read OCI
	// labels. Full refs and remote refs pass through unchanged. The
	// canonical helper (also used by deploy preflight) lives in
	// ensure_image.go.
	resolvedRef, err := resolveImageRefForEnsure(imageRef, projectCfg, dir)
	if err != nil {
		return fmt.Errorf("resolving deploy box %q: %w", imageRef, err)
	}
	meta, err := ExtractMetadata(engine, resolvedRef)
	if err != nil {
		return err
	}
	set := MergeDeployDescriptions(meta.Description, overlayPlan, c.Box)
	if set == nil || set.IsEmpty() {
		fmt.Fprintln(os.Stderr, "No plan steps defined for this image.")
		return nil
	}
	resolver, _ := ResolveCheckVarsRuntime(meta, deployOverlay, engine, c.Box, containerName, c.Instance)

	runner := NewRunner(ContainerChain(engine, containerName), resolver, RunModeLive)
	runner.VerifyOnly = true // charly check live: idempotent check:/agent-check: only
	attachCheckRunnerContext(runner, c.Box, c.Instance, meta.Distro, dir, projectCfg)
	// Cross-deployment probing (a step with `on: <driver>` reaching a SEPARATE
	// subject via ${HOST:<member>}): wire the live target resolver + peer vars — the
	// ONE entry point every live path shares (R3).
	runner.TargetResolver = liveTargetResolver(c.Instance)
	for _, sec := range [][]LabeledDescription{set.Candy, set.Box, set.Deploy} {
		for _, ld := range sec {
			applyHostVarsSteps(runner, ld.Plan, c.Instance)
		}
	}

	results := RunPlan(context.Background(), runner, set, nil, false)
	fmt.Fprintf(os.Stderr, "Image: %s (container: %s)\n", meta.Box, containerName)
	fails := reportSteps(os.Stderr, results, c.Format)
	if fails > 0 {
		return &CheckFailedError{Failed: fails}
	}
	return nil
}

// isVmTarget returns true when c.Box names a `kind: vm` entity OR a
// kind:deployment with target:vm OR a dotted-path child deployment nested
// inside a target:vm parent. Cheap check — a missing/unreadable
// charly.yml returns false and the caller falls through to the
// container dispatch path.
func (c *CheckLiveCmd) isVmTarget() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil || !ok {
		return false
	}
	// Shared classifier (check_venue.go) — also drives resolveCheckVenue for
	// the out-of-process verb pre-resolvers, so `charly check live <vm>` and the
	// VM-targeting verbs (vnc:/spice:/libvirt:) agree on what is a VM target (R3).
	_, isVM := checkVmTarget(uf, c.Box)
	return isVM
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

// runVm executes deploy-scope tests against a VM guest over SSH.
//
// Connection resolution order:
//  1. Start from VmSpec defaults (resolveVmSshUser / resolveVmSshPort / the
//     conventional key path under ~/.local/share/charly/vm/charly-<name>/).
//  2. Overlay any VmState-materialized fields from charly.yml (user, port,
//     key path) so VMs whose candies have been applied via `charly bundle add vm:`
//     honor the exact state the deploy wrote.
//
// VMs have no OCI image labels, so no candy/box test section exists —
// only the local deploy overlay's `tests:` list applies.

// guestNestedCheckCmd builds the `charly check live <pod>` command that runVm runs
// IN the guest (over SSH) to evaluate a nested-in-VM pod as a direct pod. The
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

func (c *CheckLiveCmd) runVm() error {
	dir, _ := os.Getwd()
	uf, ok, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	if !ok || uf == nil {
		return fmt.Errorf("check live: no charly.yml found in %s (vm targets need the project config)", dir)
	}
	vmName, nestedLeaf, spec := c.resolveVmTarget(uf)

	user := resolveVmSshUser(spec)
	port, err := resolveVmSshPort(spec, vmName)
	if err != nil {
		return err
	}

	plan, user, port := c.loadVmCheckPlans(uf, dir, vmName, nestedLeaf, user, port)

	// SSH connection details (User/Port/IdentityFile) live in the
	// managed ssh-config Host stanza (charly-<vmName>) written at deploy
	// time. We point the executor at the alias and let ssh(1) resolve
	// the rest from ~/.ssh/config + agent.
	host := "127.0.0.1"
	var executor DeployExecutor = &SSHExecutor{Host: VmSshAlias(vmName), ConnectTimeout: 10}

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
	gate := &SSHExecutor{Host: VmSshAlias(vmName), ConnectTimeout: 5}
	gctx := context.Background()
	if gerr := gate.WaitForSSH(gctx); gerr != nil {
		return fmt.Errorf("vm %q is not up / SSH-reachable — is the domain running? %w", vmName, gerr)
	}
	if gerr := gate.WaitForCloudInit(gctx); gerr != nil {
		return fmt.Errorf("vm %q cloud-init did not settle (still running or restarting?): %w", vmName, gerr)
	}

	env := map[string]string{
		"IMAGE":          c.Box,
		"INSTANCE":       c.Instance,
		"HOST_PORT:22":   strconv.Itoa(port),
		"CONTAINER_IP":   host,
		"CONTAINER_NAME": "charly-" + c.Box,
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
	if nestedLeaf != nil && nestedLeaf.Target == "pod" {
		parts := strings.Split(c.Box, ".")
		guestPod := parts[len(parts)-1]
		guestCmd := guestNestedCheckCmd(guestPod, c.Format, c.Section, c.Filter, c.Instance)
		vmSSH := &SSHExecutor{Host: VmSshAlias(vmName), ConnectTimeout: 10}
		fmt.Fprintf(os.Stderr, "VM: charly-%s — nested pod %q evaluated IN the guest (%s)\n", c.Box, guestPod, VmSshAlias(vmName))
		stdout, stderr, exit, rerr := vmSSH.RunCapture(context.Background(), guestCmd)
		if stdout != "" {
			fmt.Print(stdout)
		}
		if stderr != "" {
			fmt.Fprint(os.Stderr, stderr)
		}
		if rerr != nil {
			return fmt.Errorf("delegating nested-pod check to guest %q: %w", vmName, rerr)
		}
		if exit == CheckFailExitCode {
			return &CheckFailedError{Failed: 1}
		}
		if exit != 0 {
			return fmt.Errorf("nested-pod check in guest %q exited %d", vmName, exit)
		}
		return nil
	}

	if len(plan) == 0 {
		fmt.Fprintln(os.Stderr, "No plan steps to run.")
		return nil
	}
	set := &LabelDescriptionSet{Deploy: []LabeledDescription{{Origin: "vm:" + vmName, Plan: plan}}}

	runner := NewRunner(executor, resolver, RunModeLive)
	runner.VerifyOnly = true
	// Load the project's composed OUT-OF-TREE plugins so an externalized check
	// verb (e.g. `kube:`, served by candy/plugin-kube) RESOLVES in the VM check
	// path too — the SAME shared wiring the pod path uses (attachCheckRunnerContext,
	// the ONE place every RunModeLive baked-plan runner loads plugins, R3). Without
	// it a VM bed's `kube:` steps SKIP as `unknown verb "kube"` (the kube dep-shed
	// regression: kube WAS a builtin, always registered, so runVm never needed it).
	if cfg, cerr := LoadConfig(dir); cerr == nil {
		attachCheckRunnerContext(runner, c.Box, c.Instance, nil, dir, cfg)
	} else {
		runner.Box = c.Box
		runner.Instance = c.Instance
	}
	// Box stays the deploy/bed name (container + DEPLOY_NAME identity); VmName is the
	// resolved vm: ENTITY name (deploy name remapped via uf.Bundle[box].From). The
	// operator-side libvirt/spice verbs must address the live libvirt domain
	// charly-<VmName>, so they read vmTargetName() — the out-of-process vm plugin
	// cannot LoadUnified to remap the name itself, so the host threads the
	// already-resolved entity name through.
	runner.VmName = vmName
	// Cross-deployment support for a VM SUBJECT (the `on:` driver dispatch +
	// ${HOST}/${HOST} resolution) — the SAME wiring the pod
	// (CheckLiveCmd.Run) and local (runLocalCheck) paths already do (R3). Without
	// it, a VM bed whose check drives a peer (e.g. check-cross-vm-http: a local
	// host-driver curls the guest via ${HOST}'s ssh -L forward) leaves
	// ${HOST} unresolved → the check FAILS "peer unreachable". CloseHosts
	// tears down any ssh -L forwards at run end.
	runner.TargetResolver = liveTargetResolver(c.Instance)
	applyHostVarsSteps(runner, plan, c.Instance)
	defer runner.CloseHosts()
	results := RunPlan(context.Background(), runner, set, nil, false)

	fmt.Fprintf(os.Stderr, "VM: charly-%s (ssh %s@%s:%d)\n", c.Box, user, host, port)
	fails := reportSteps(os.Stderr, results, c.Format)
	if fails > 0 {
		return &CheckFailedError{Failed: fails}
	}
	return nil
}

// resolveVmTarget resolves the VM check request (c.Box) to its kind:vm entity
// name, an optional nested-leaf node (for a dotted "parent.child" path), and the
// VmSpec.
func (c *CheckLiveCmd) resolveVmTarget(uf *UnifiedFile) (vmName string, nestedLeaf *BundleNode, spec *VmSpec) {
	// Schema v4: c.Box may be
	//   (a) a kind:vm entity name directly (e.g. "arch"),
	//   (b) a kind:deployment name with target:vm (e.g. "arch-vm") whose
	//       Vm field points at the actual kind:vm entity, OR
	//   (c) a dotted path "parent.child" where `parent` is a target:vm
	//       deployment and `child` is a nested node whose tests run in
	//       the parent's SSH substrate.
	vmName = c.Box
	if uf.Bundle != nil {
		if entry, ok := uf.Bundle[c.Box]; ok && entry.Target == "vm" && entry.From != "" {
			vmName = entry.From
		} else if idx := strings.Index(c.Box, "."); idx > 0 {
			root := c.Box[:idx]
			if parent, present := uf.Bundle[root]; present && parent.Target == "vm" {
				if parent.From != "" {
					vmName = parent.From
				}
				nestedLeaf = resolveNestedNode(uf.Bundle, c.Box)
			}
		}
	}
	if uf.VM != nil {
		spec, _ = resolveVmViaPlugin(uf.VM[vmName])
	}
	return vmName, nestedLeaf, spec
}

// loadVmCheckPlans aggregates the VM deployment's check plan from the project
// and per-machine deploy sources plus add_candy deploy-scope steps, returning
// the merged plan and the SSH user/port (possibly overridden by local VmState).
func (c *CheckLiveCmd) loadVmCheckPlans(uf *UnifiedFile, dir, vmName string, nestedLeaf *BundleNode, user string, port int) (plan []Step, outUser string, outPort int) {
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
	var projectPlan, localPlan []Step
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
	if dc := loadDeployConfigForRead("charly check vm"); dc != nil {
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
	plan = append(append([]Step(nil), projectPlan...), localPlan...)

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
func collectAddCandySteps(uf *UnifiedFile, dir string, addCandies []string) []Step {
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
	var out []Step
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

// attachCheckRunnerContext wires the identity + committed-APK anchoring every
// live baked-plan runner needs, so `charly check live` and `charly check feature
// run` resolve adb/appium `apk:` checks IDENTICALLY (R3). They previously
// diverged — only check live populated CandyDirs, so a committed-APK check
// passed under check live yet failed to anchor ("0 candies scanned") under
// feature run. Any RunModeLive runner that executes a baked plan MUST call this.
func attachCheckRunnerContext(runner *Runner, box, instance string, distros []string, dir string, cfg *Config) {
	runner.Box = box
	runner.Instance = instance
	runner.Distros = distros
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
		runner.CandyScanErr = fmt.Errorf("scanning candy source dirs: %w", scanErr)
		return
	}
	runner.CandyDirs = candyDirsFromScan(candyMap)
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
}

// deployNodePluginContext resolves the deploy/bed node named `name` in the project at
// `dir` ONCE (the SAME project-bundle loader the deploy walker uses) and returns the
// two plugin-loading inputs the check runner (attachCheckRunnerContext) and the deploy
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

// isLocalTarget returns true when c.Box names a `target: local` deployment
// (a host filesystem apply) OR a dotted-path child whose root segment is a
// target:local deployment. Mirror of isVmTarget — a missing/unreadable
// charly.yml returns false and the caller falls through to the container
// dispatch path.
func (c *CheckLiveCmd) isLocalTarget() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil || !ok {
		return false
	}
	// Shared classifier (check_venue.go), same as isVmTarget (R3).
	_, isLocal := checkLocalTarget(uf, c.Box)
	return isLocal
}

// runLocalCheck executes deploy-scope checks against a `target: local`
// deployment on its host venue. Mirror of runVm, but the venue is a
// ShellExecutor (host: local) or SSHExecutor (host: <remote>) selected by the
// shared rootExecutorForDeployNode, and dotted paths compose through
// ResolveDeployChain exactly like runVm.
//
// Local deploys carry no OCI image labels, so there is no candy/box test
// section — checks come from the resolved kind:local template's `check:` (base)
// merged with the deploy entry's `check:` and the per-host charly.yml overlay
// (id-based replace/append, same as everywhere). Host-context vars only: no
// HOST_PORT:<N> / CONTAINER_IP (host services bind real ports; faking a port
// mapping would be wrong).
func (c *CheckLiveCmd) runLocalCheck() error {
	dir, _ := os.Getwd()
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
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
		return fmt.Errorf("check live: local deployment %q not found", c.Box)
	}

	// Select the root venue from the root node's host:, then compose nested
	// hops for a dotted path through the shared ResolveDeployChain.
	executor, err := rootExecutorForDeployNode(rootNode)
	if err != nil {
		return fmt.Errorf("check live %q: %w", c.Box, err)
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
	fmt.Fprintf(os.Stderr, "Local deploy: %s [%s]\n", c.Box, venue)

	fails, err := checkLocalDeployScope(dir, node, c.Box, c.Instance, c.Section, c.Filter, executor, c.Format)
	if err != nil {
		return err
	}
	if fails > 0 {
		return &CheckFailedError{Failed: fails}
	}
	return nil
}

// checkLocalDeployScope collects a local deployment's deploy-scope checks —
// kind:local template `check:` (base) merged with the deploy entry `check:`
// (extends/overrides) and the per-host charly.yml overlay — and runs them on
// `exec`. Shared by `charly check live <local>` (runLocalCheck) and
// `charly bundle add <local> --verify` (the local deploy target) so the two surfaces
// source + run probes identically (R3). Host-context vars only (no
// HOST_PORT:<N> / CONTAINER_IP). Returns the failure count.
func checkLocalDeployScope(dir string, node *BundleNode, image, instance, _ string, _ []string, exec DeployExecutor, format string) (int, error) { //nolint:unparam // error return kept for symmetry with sibling deploy-scope checks
	var plan []Step
	if node != nil && strings.TrimSpace(node.From) != "" {
		if spec, _ := findLocalSpec(dir, strings.TrimSpace(node.From)); spec != nil {
			plan = append(plan, spec.Plan...)
		}
	}
	if node != nil {
		plan = append(plan, node.Plan...)
	}
	if dc := loadDeployConfigForRead("charly check live"); dc != nil {
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
		fmt.Fprintln(os.Stderr, "No plan steps to run.")
		return 0, nil
	}
	set := &LabelDescriptionSet{Deploy: []LabeledDescription{{Origin: "local:" + image, Plan: plan}}}
	runner := NewRunner(exec, resolver, RunModeLive)
	runner.VerifyOnly = true
	runner.Box = image
	runner.Instance = instance
	// Generic cross-deployment support (on: driver + ${HOST:<member>}) — a local
	// SUBJECT bed can drive a peer too (R3).
	runner.TargetResolver = liveTargetResolver(instance)
	applyHostVarsSteps(runner, plan, instance)
	results := RunPlan(context.Background(), runner, set, nil, false)
	return reportSteps(os.Stdout, results, format), nil
}

// isGroupTarget reports whether c.Box is a GROUP check bed — a bundle with no
// workload cross-ref (Target == "") that carries sibling Members (the §3
// group+siblings cross-deployment shape: subject + driver as peers on the shared
// charly net). Such a bed has no root container; flattenBundleVenues stamped each
// plan step with its member venue. Shares the LoadUnified lookup style with
// isVmTarget/isLocalTarget (R3).
func (c *CheckLiveCmd) isGroupTarget() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	uf, ok, err := LoadUnified(dir)
	if err != nil || !ok || uf == nil {
		return false
	}
	entry, present := uf.Bundle[c.Box]
	return present && entry.IsGroup()
}

// runGroupCheck executes a GROUP check bed's flattened, venue-stamped plan.
// A group bed has no root container (no box/vm/local cross-ref) — its members
// are sibling subdeployments (subject + driver) brought up on the shared charly
// net, and every plan step carries its member venue (flattenBundleVenues). So
// there is nothing to exec into at the root: the runner's base executor is a
// placeholder and EVERY step dispatches to its member via the venue swap
// (liveTargetResolver), while cross-member ${HOST:<member>} addresses resolve via
// applyHostVarsSteps. Mirrors runLocalCheck's plan-run shape (R3).
func (c *CheckLiveCmd) runGroupCheck() error {
	dir, _ := os.Getwd()
	uf, _, err := LoadUnified(dir)
	if err != nil {
		return err
	}
	entry, ok := uf.Bundle[c.Box]
	if !ok {
		return fmt.Errorf("check live: group bed %q not found", c.Box)
	}
	plan := entry.Plan
	if len(plan) == 0 {
		fmt.Fprintln(os.Stderr, "No plan steps to run.")
		return nil
	}
	fmt.Fprintf(os.Stderr, "Group bed: %s [%d sibling member(s); venue-dispatched, no root container]\n", c.Box, len(entry.Members))

	resolver := &CheckVarResolver{Env: map[string]string{
		"IMAGE":    c.Box,
		"INSTANCE": c.Instance,
	}, HasRuntime: true}
	runner := NewRunner(ShellExecutor{}, resolver, RunModeLive)
	// Set the runner identity AND load the OUT-OF-PROCESS plugin candies the bed's
	// flattened plan REFERENCES — a cdp:/spice:/… verb authored under a member. A group
	// bed has no single image, so the load keys on the BED NAME (its flattened,
	// venue-stamped plan names every referenced verb) plus the project candy scan, the
	// SAME plugin-load path the container/vm/local venues use (R3). Without it an external
	// check verb under a member fails live as "unknown verb" — the cross-pod-cdp regression
	// once cdp left the compiled-in set (the group venue was the one path missing this).
	attachCheckRunnerContext(runner, c.Box, c.Instance, nil, dir, uf.ProjectConfig())
	// Every step venue-dispatches to its member (its venue != the group root
	// name), so the placeholder base executor above is never used.
	// liveTargetResolver performs the per-step swap; ${HOST:<member>} addresses
	// resolve through applyHostVarsSteps.
	runner.TargetResolver = liveTargetResolver(c.Instance)
	applyHostVarsSteps(runner, plan, c.Instance)
	defer runner.CloseHosts()
	set := &LabelDescriptionSet{Deploy: []LabeledDescription{{Origin: "group:" + c.Box, Plan: plan}}}
	results := RunPlan(context.Background(), runner, set, nil, false)
	if fails := reportSteps(os.Stdout, results, c.Format); fails > 0 {
		return &CheckFailedError{Failed: fails}
	}
	return nil
}

// CheckBoxCmd runs PURE-BOX check against a disposable container.
// Build-scope checks only (candy + box sections). Deploy-scope checks
// are skipped — they require a running deployment with port mappings,
// volumes, and resolved runtime variables. For full-stack check against
// a running deployment, use `charly check live <name>`.
//
// Image references resolve purely against local container storage via
// resolveLocalImageRef — never reads charly.yml. Run `charly box pull <name>`
// or `charly box build <name>` first if the image isn't in local storage yet.
type CheckBoxCmd struct {
	Image  string   `arg:"" help:"Image reference (full ref or short name resolved against local container storage; never reads charly.yml)"`
	Format string   `long:"format" default:"text" help:"Output format: text, json, tap, yaml"`
	Filter []string `long:"filter" help:"Only run checks with these verbs (repeatable)"`
}

func (c *CheckBoxCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	imageRef, err := resolveLocalImageRef(rt.RunEngine, c.Image)
	if err != nil {
		return err
	}

	meta, err := ExtractMetadata(rt.RunEngine, imageRef)
	if err != nil {
		return err
	}
	if meta == nil || meta.Description == nil || meta.Description.IsEmpty() {
		fmt.Fprintln(os.Stderr, "No plan steps defined for this image.")
		return nil
	}

	// PURE-BOX: always a disposable container, build-context steps only. The
	// mode is explicit; no autodetect, no fallback. Deploy/runtime-context
	// steps are skipped under RunModeBox.
	executor := ImageChain(rt.RunEngine, imageRef)
	resolver := ResolveCheckVarsBuild(meta)
	runner := NewRunner(executor, resolver, RunModeBox)
	runner.VerifyOnly = true
	runner.Distros = meta.Distro

	stepResults := RunPlan(context.Background(), runner, meta.Description, nil, false)

	fmt.Fprintf(os.Stderr, "Image: %s\n", imageRef)

	// YAML format emits the shape ParseCharlyTestOutput expects —
	// the benchmark scorer's input format.
	if c.Format == "yaml" {
		return emitImageTestYAML(os.Stdout, imageRef, "", stepResults, nil)
	}

	fails := reportSteps(os.Stderr, stepResults, c.Format)
	if fails > 0 {
		return &CheckFailedError{Failed: fails}
	}
	return nil
}

// emitImageTestYAML writes the `charly check box --format yaml` payload that
// ParseCharlyTestOutput (check_score.go) consumes. The shape is:
//
//	box: <ref>
//	mode: box | run
//	step:
//	  - id, origin, text, tag, keyword, verb, status
//	summary: { total, pass, fail, skip }
//
// Only check:/agent-check: steps are emitted (the scored success criteria).
func emitImageTestYAML(w io.Writer, imageRef, liveContainer string, steps []StepResult, _ []CheckResult) error {
	mode := "box"
	if liveContainer != "" {
		mode = "run"
	}
	out := CheckRunResults{Box: imageRef, Mode: mode}
	for _, sp := range steps {
		if sp.Keyword != string(KwCheck) && sp.Keyword != string(KwAgentCheck) {
			continue // only scored steps land in the --format yaml payload
		}
		ss := StepScore{
			ID:      sp.StepID,
			Origin:  sp.Origin,
			Text:    sp.Text,
			Keyword: sp.Keyword,
			Verb:    sp.Result.Verb,
			Status:  sp.Result.Status.String(),
		}
		out.Step = append(out.Step, ss)
		out.Summary.Total++
		switch ss.Status {
		case "pass":
			out.Summary.Pass++
		case "fail":
			out.Summary.Fail++
		case "skip":
			out.Summary.Skip++
		}
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// containerImageRef + containerImage (the live-container image-ref
// inspectors) live in commands.go — ONE inspect implementation shared by
// mcp / service / remove / start-direct and the check runner.
