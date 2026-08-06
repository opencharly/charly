package kube

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/vmshared"
)

// k3s_post.go — the k3s POST-PROVISION finalization (S3, FINAL/K5 unit 6, Cutover-B
// S3): relocated from charly/k3s_post.go. Runs after the deploy's artifact retrieval
// pulled a k3s-server candy's kubeconfig to
// ~/.cache/charly/clusters/<artifact-key>/kubeconfig.yaml (the artifact retrieval
// itself STAYS core — it walks the deploy's declared candy artifacts over the SAME
// executor the deploy used, unrelated to k3s specifically).
//
// Two things happen here that the generic artifact-retrieve pipeline cannot:
//  1. rewrite the retrieved kubeconfig's GUEST-local server URL (127.0.0.1:6443) to
//     the HOST-forwarded port (the VM's network.port_forwards), so `kubectl`/`kube:`
//     checks reach the API from the host — the port-forward allocation is
//     LoadUnified-coupled (the deploy's persisted VmState + the kind:vm entity's
//     declared forwards). The kind:vm entity spec self-loads PLUGIN-SIDE (K-wave
//     W3a A3-phase-2: loaderkit.ResolveVmEntityViaExecutor, the former
//     "deploy-entity-resolve" HostBuild seam is DELETED); the persisted VmState
//     read is PLUGIN-SIDE too (hostConfigResolveVmState → loaderkit.ResolveVmStateViaExecutor,
//     the config-resolve seam is DELETED, K-wave 2 cone R2 bank D) — see
//     deployVMForwards' own doc comment for why that read specifically CANNOT go
//     through a direct deploykit.LoadDeployConfigForRead call from this
//     out-of-process plugin (an R10 bed regression this file once had).
//  2. merge the (rewritten) kubeconfig into ~/.kube/config under a context named
//     after the deploy — the clientcmd merge (mergeKubeconfig, merge.go) called
//     directly, no separate host round-trip.
//
// Dispatched from candy/plugin-fleet/secrets_artifacts.go's k3sPostProvision (the
// register-hint handler run after the deploy dispatch) via exec.InvokeProvider("verb","kube") —
// a broker-carrying Invoke, so this Invoke has a reverse-channel broker for the
// HostBuild("config-resolve") leg above plus the kind:vm entity's own self-load leg.

// k3sPostProvisionParams is the {method: "k3s-post-provision", artifact_key,
// deploy_name, vm_entity} plugin_input this method decodes (params.KubeInput's
// ArtifactKey / DeployName / VmEntity fields — CUE-sourced, schema/kube.cue).
// ArtifactKey is now the per-deploy DOMAIN-scoped identity (task #18); VmEntity
// carries the SHARED kind:vm entity name separately (a different identity space
// deployVMForwards needs to resolve the entity's DECLARED port-forward template).
type k3sPostProvisionParams struct {
	ArtifactKey string
	DeployName  string
	VmEntity    string
}

// k3sPostProvision runs the post-provision steps for a k3s-server deploy. No-op
// (pass, no message) when the retrieved kubeconfig path does not exist (e.g. because
// the candy did not actually include k3s-server, or the artifact retrieve was
// skipped by --dry-run).
func k3sPostProvision(ctx context.Context, exec *sdk.Executor, p k3sPostProvisionParams) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home: %w", err)
	}
	safe := kit.SanitizeDeployName(p.ArtifactKey)
	retrieved := filepath.Join(home, ".cache", "charly", "clusters", safe, "kubeconfig.yaml")
	if _, err := os.Stat(retrieved); err != nil {
		// Not a k3s-server deploy, or retrieve was skipped. Nothing to do.
		return "", nil
	}

	// The retrieved kubeconfig carries k3s's GUEST-local server URL (127.0.0.1:6443);
	// the host reaches the in-guest API only through the VM's host:guest
	// port-forward. Rewrite the server to the host-forwarded port so `kubectl`/
	// `kube:` checks work host-side (without this, kubectl dials 127.0.0.1:6443 →
	// connection refused). The port-forward allocation is keyed by the DEPLOY
	// identity; the SHARED entity (VmEntity, task #18 — no longer derivable from
	// the now-domain-scoped ArtifactKey) resolves the VM spec.
	if err := rewriteK3sServerToForward(ctx, exec, retrieved, p.VmEntity, p.DeployName); err != nil {
		return "", fmt.Errorf("rewriting k3s kubeconfig server to the forwarded port: %w", err)
	}

	contextName := safe
	msg, err := mergeKubeconfig(retrieved, contextName)
	if err != nil {
		return "", fmt.Errorf("merging kubeconfig into ~/.kube/config: %w", err)
	}
	return fmt.Sprintf("k3s cluster %q registered — kubectl --context=%s get nodes (%s)", p.ArtifactKey, contextName, msg), nil
}

// rewriteK3sServerToForward rewrites the retrieved kubeconfig's server URL, mapping
// the guest-local k3s API port to the host-forwarded port declared on the deploy's
// VM (network.port_forwards "<host>:<guest>"). No-op when the deploy has no
// matching VM forward — a bare-metal / already-host-reachable k3s needs no rewrite.
func rewriteK3sServerToForward(ctx context.Context, exec *sdk.Executor, retrievedPath, vmEntity, deployName string) error {
	forwards, err := deployVMForwards(ctx, exec, vmEntity, deployName)
	if err != nil {
		return err
	}
	if len(forwards) == 0 {
		return nil
	}
	guestToHost := map[string]string{}
	for _, pf := range forwards {
		if host, guest, ok := strings.Cut(pf, ":"); ok {
			guestToHost[strings.TrimSpace(guest)] = strings.TrimSpace(host)
		}
	}
	data, err := os.ReadFile(retrievedPath)
	if err != nil {
		return err
	}
	out := deploykit.RewriteServerPorts(string(data), guestToHost)
	if out == string(data) {
		return nil
	}
	return os.WriteFile(retrievedPath, []byte(out), 0o600)
}

// deployVMForwards resolves the RESOLVED "<host>:<guest>" forwards for the VM a
// deploy runs on. The two identities are DISTINCT and must not be conflated (the
// #65 bug, preserved from the core original; re-separated onto the wire by task
// #18 once artifact_key stopped being entity-shaped):
//   - vmEntity (the SHARED kind:vm entity name, e.g. "k3s-vm") resolves the VM
//     SPEC — several beds may reach one entity via their own `from:` ref.
//   - deployName (the real per-DEPLOY / domain identity, e.g.
//     "check-k8s-deploy-cluster") keys the VmState port-forward LEDGER:
//     "vm:"+VmDomainIdentity(deployName) is the EXACT key the orchestrator
//     persisted under.
//
// The kind:vm entity self-loads the project PLUGIN-SIDE (K-wave W3a A3-phase-2:
// loaderkit.ResolveVmEntityViaExecutor) — the former "deploy-entity-resolve" HostBuild seam this
// round-tripped through is DELETED, unblocked by W1's LoadUnifiedViaExecutor. The persisted VmState
// port-forward LEDGER read routes through the SIBLING "config-resolve" HostBuild
// seam (candy/plugin-vm's own hostConfigResolve calls the identical seam for its
// OWN VmState reuse) — NEVER a direct deploykit.LoadDeployConfigForRead call: that
// helper's LoadFleetConfig degrades to an EMPTY config whenever
// deploykit.DeployStateHost is nil, which it ALWAYS is inside this plugin's own
// out-of-process (candy/plugin-kube is served over go-plugin gRPC, never
// compiled-in) — DeployStateHost is wired ONLY by charly-core's own init(), so a
// direct call here silently found nothing (a `LookupKey` miss, ok=false) every
// single time regardless of what was actually persisted on disk, exactly the
// silent-out-of-process-degradation class of bug the deploy-state WRITE path
// guards against (plugin-side via a loader-backed reader since #55 K4) for the
// WRITE half — this closes the matching gap on the READ half. R10 bed regression: check-k8s-deploy's
// bring-up-members failed with "auto port_forward \"auto:6443\" has no persisted
// host-port allocation" even though `charly vm create`'s own persist (verified via
// a live isolated CHARLY_DEPLOY_CONFIG repro, RDD) landed correctly and stayed
// stable on disk throughout the run — the read, not the write, was broken.
func deployVMForwards(ctx context.Context, exec *sdk.Executor, vmEntity, deployName string) ([]string, error) {
	if vmEntity == "" {
		// pod/local (non-VM) k3s-server deploys, or an old caller that never resolved an entity —
		// no VM spec to consult, so no forwards.
		return nil, nil
	}
	// Resolve the project dir via the "deploy-plugins-connect" seam (os.Getwd() host-side, the
	// SAME dir the host loader used) — needed below for the kind:vm entity self-load. A failure
	// degrades to "" (best-effort, matches this function's own no-forward-on-miss contract).
	dir, _ := hostProjectDir(ctx, exec, deployName)
	// K-wave W3a A3-phase-2: self-load the kind:vm entity plugin-side instead of the deleted
	// "deploy-entity-resolve" host seam — unblocked now that LoadUnifiedViaExecutor (W1) lets a
	// plugin load the project itself.
	vmPtr, verr := resolveVmEntityForForwards(ctx, exec, dir, vmEntity)
	if verr != nil || vmPtr == nil {
		return nil, nil //nolint:nilerr // best-effort: see above
	}
	vm := *vmPtr
	if vm.Network == nil {
		return nil, nil
	}
	domainID := vmshared.VmDomainIdentity(deployName)
	key := "vm:" + domainID
	var alloc map[string]int
	if vmState, err := hostConfigResolveVmState(ctx, exec, domainID); err != nil {
		return nil, fmt.Errorf("resolving persisted port-forward allocation for %q: %w", key, err)
	} else if vmState != nil {
		alloc = vmState.PortForwards
	}
	resolved, rerr := deploykit.ResolveDeployForwards(vm.Network.PortForwards, alloc)
	if rerr != nil {
		return nil, fmt.Errorf("deploy %q (vm_state key %q): %w", deployName, key, rerr)
	}
	return resolved, nil
}

// resolveVmEntityForForwards is a package var (test seam) wrapping deployVMForwards' kind:vm
// plugin-side self-load call. A single HostBuild-kind stub cannot canned-reply a multi-leg loader
// path (loaderkit.LoadUnifiedViaExecutor dispatches loader-threaded/-bootstrap/-walk/-materialize,
// then InvokeProvider(kind,"local") — sdk/loaderkit/load_via_executor.go), mirroring
// candy/plugin-deploy-pod's loadProjectVolume/saveFleet stub pattern (R3) —
// k3s_post_forwards_test.go stubs this directly instead of faking the full loader chain.
var resolveVmEntityForForwards = loaderkit.ResolveVmEntityViaExecutor

// hostConfigResolveVmState fetches the persisted VmDeployState for the given "vm:<domainID>"
// ledger key PLUGIN-SIDE via loaderkit.ResolveVmStateViaExecutor (K-wave 2 cone R2 bank D — the
// "config-resolve" HostBuild seam is DELETED; the shared loaderkit reader is the ONE home
// candy/plugin-vm's hostConfigResolve + candy/plugin-deploy-vm's resolvePriorVmState also use,
// R3 — never a direct deploykit.LoadDeployConfigForRead from an out-of-process plugin, which
// cannot see the core-only deploykit.DeployStateHost wiring). A package var (test seam, same
// pattern as resolveVmEntityForForwards) so the forwards tests stub the read directly instead of
// faking the multi-leg loader path.
var hostConfigResolveVmState = loaderkit.ResolveVmStateViaExecutor
