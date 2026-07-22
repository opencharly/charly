package substratekind

// command_reap_orphans.go — the externalized `charly reap-orphans` command (K5: relocated from
// charly/status_reap.go). Finds ephemeral deployments whose charly.yml ledger says "active" but
// whose underlying engine resource (libvirt domain, podman container, k8s namespace) is gone, and
// tears them down via `charly bundle del --assume-yes`.
//
// Every dependency this needed turned out reachable without a new seam, mirroring the android
// status collector's own precedent:
//   - the per-host deploy overlay reads through loadBundleConfig (status_flat.go) — the
//     "pod-config-load-bundle" HostBuild seam, NOT deploykit.LoadBundleConfig() directly (bed-
//     robustness batch item 5: a direct call silently degrades to an empty config out-of-process,
//     since deploykit.DeployStateHost is only ever registered by charly core's own init()).
//   - the pod/k8s liveness probes are plain podman/kubectl exec calls — no core coupling at all.
//   - the vm liveness probe used charly-core's invokeVmPlugin (a private registry accessor); the
//     portable equivalent is Executor.InvokeProvider("verb", "libvirt", sdk.OpRun, ...) — the SAME
//     verb:libvirt provider, reached the way ANY plugin reaches a peer (F10), not a core-only path.
//   - the actual reap shells out `charly bundle del <name> --assume-yes` via os.Executable() +
//     exec.Command — valid because this command is COMPILED-IN (os.Executable() resolves to the
//     charly binary itself, exactly as it did when this code ran in core).
//
// COMPILED-IN ONLY: like candy/plugin-status, the os.Executable()-based re-entry assumes it is
// running INSIDE the charly binary; served out-of-process (cmd/serve) it degrades with a clear
// error (mirrors runStatusCLI's nil-executor path).

import (
	"context"
	"encoding/json"
	"fmt"
	osexec "os/exec"

	"os"

	"github.com/alecthomas/kong"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// ReapOrphansCmd is the `charly reap-orphans` Kong grammar — moved verbatim from
// charly/status_reap.go (it carries no flags).
type ReapOrphansCmd struct{}

// deployDelArgv returns the argv (everything after the charly binary) for a non-interactive
// `charly bundle del <name>` — a local copy of charly/bundle_add_cmd.go's helper of the same name
// (a one-line literal; duplicating it here is cheaper and safer than importing charly core for it,
// mirrors candy/plugin-substrate's own androidDevice/containerRunning precedent, R3).
func deployDelArgv(name string) []string {
	return []string{"bundle", "del", name, "--assume-yes"}
}

// runReapOrphansCLI parses the pass-through args (none expected), then runs the reap.
func runReapOrphansCLI(ctx context.Context, exec *sdk.Executor, args []string) error {
	var cmd ReapOrphansCmd
	if done, err := sdk.ParseInProcCLI("reap-orphans", &cmd, args,
		kong.Description("Find ephemeral deployments whose underlying resource is gone and clean them up")); err != nil || done {
		return err
	}
	if exec == nil {
		return fmt.Errorf("charly reap-orphans requires compiled-in placement (the libvirt liveness probe needs the reverse-channel executor)")
	}
	return runReapOrphans(ctx, exec)
}

// runReapOrphans finds and cleans up orphaned ephemeral deployments — entries whose charly.yml
// ledger says "active" but whose underlying engine resource is gone. Pure orphan detection — no
// race resolution: if a teardown is concurrently in progress, the second `charly bundle del
// --assume-yes` no-ops on the already-removed pieces.
func runReapOrphans(ctx context.Context, exec *sdk.Executor) error {
	// Routed through the loadBundleConfig seam helper (status_flat.go, bed-robustness batch
	// item 5) instead of deploykit.LoadBundleConfig() directly — the exact "unvetted grep hit"
	// this audit closes: reap-orphans exists specifically to clean up orphaned EPHEMERAL
	// deploys (item 1's own subject), so a silently-empty read here would make `charly
	// reap-orphans` find nothing to reap on EVERY invocation, out-of-process.
	dc, err := loadBundleConfig(ctx)
	if err != nil {
		return fmt.Errorf("loading charly.yml: %w", err)
	}
	if dc == nil {
		fmt.Println("no charly.yml; nothing to reap")
		return nil
	}
	var orphans []string
	for name, node := range dc.Bundle {
		if node.VmState == nil || node.VmState.Ephemeral == nil {
			continue
		}
		if node.VmState.Ephemeral.Status != "active" {
			continue
		}
		if !ephemeralUnderlyingResourceAlive(ctx, exec, name, node) {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) == 0 {
		fmt.Println("no orphaned ephemerals")
		return nil
	}
	for _, name := range orphans {
		fmt.Printf("reaping orphan %q ...\n", name)
		exe, _ := os.Executable()
		delCmd := osexec.Command(exe, deployDelArgv(name)...)
		delCmd.Stderr = os.Stderr
		delCmd.Stdout = os.Stdout
		if rerr := delCmd.Run(); rerr != nil {
			fmt.Fprintf(os.Stderr, "warning: charly bundle del %q: %v\n", name, rerr)
		}
	}
	return nil
}

// ephemeralUnderlyingResourceAlive returns true when the named ephemeral's underlying resource is
// still alive. Best-effort across targets — false negatives are OK (we just skip reaping that
// entry); false positives are bad (we'd nuke a still-running resource), so the checks lean
// conservative.
func ephemeralUnderlyingResourceAlive(ctx context.Context, exec *sdk.Executor, name string, node spec.Deploy) bool {
	switch node.Target {
	case "vm":
		domName := "charly-" + node.From
		if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.InstanceName != "" {
			domName = "charly-" + node.VmState.Ephemeral.InstanceName
		}
		// Probe the domain via the verb:libvirt peer provider (F10 plugin-to-plugin dispatch) —
		// the portable equivalent of charly-core's private invokeVmPlugin.
		envJSON, merr := json.Marshal(map[string]string{"vm_op": "domain-state", "vm_name": domName})
		if merr != nil {
			return true // can't build the request → conservative: assume alive
		}
		raw, ierr := exec.InvokeProvider(ctx, "verb", "libvirt", sdk.OpRun, nil, envJSON)
		if ierr != nil {
			return true // can't probe → conservative: assume alive
		}
		var st struct {
			Exists bool `json:"exists"`
		}
		if json.Unmarshal(raw, &st) != nil {
			return true // decode failed → conservative
		}
		return st.Exists
	case "pod", "container":
		check := osexec.Command("podman", "container", "exists", "charly-"+name)
		return check.Run() == nil
	case "k8s", "kubernetes":
		ns := name
		if node.VmState != nil && node.VmState.Ephemeral != nil && node.VmState.Ephemeral.InstanceName != "" {
			ns = node.VmState.Ephemeral.InstanceName
		}
		check := osexec.Command("kubectl", "get", "namespace", ns)
		check.Stderr = nil
		check.Stdout = nil
		return check.Run() == nil
	}
	return true // unknown target — conservative
}
