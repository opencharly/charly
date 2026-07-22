package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
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
//     declared forwards), so it reaches the host ONLY through TWO generic
//     HostBuild seams (F10) over a broker-carrying Invoke: "deploy-entity-resolve"
//     for the kind:vm entity spec, and "config-resolve" for the persisted
//     VmDeployState (port-forward allocation) — see deployVMForwards' own doc
//     comment for why the persisted-state read specifically CANNOT go through a
//     direct deploykit.LoadDeployConfigForRead call from this out-of-process
//     plugin (an R10 bed regression this file once had).
//  2. merge the (rewritten) kubeconfig into ~/.kube/config under a context named
//     after the deploy — the clientcmd merge (mergeKubeconfig, merge.go) called
//     directly, no separate host round-trip.
//
// Dispatched from charly/k8s_plugin.go's invokeKubePluginWithBroker — an
// InvokeWithExecutor call, so this Invoke has a reverse-channel broker for the
// HostBuild("deploy-entity-resolve")/HostBuild("config-resolve") legs above.

// k3sPostProvisionParams is the {method: "k3s-post-provision", artifact_key,
// deploy_name} plugin_input this method decodes (params.KubeInput's ArtifactKey /
// DeployName fields — CUE-sourced, schema/kube.cue).
type k3sPostProvisionParams struct {
	ArtifactKey string
	DeployName  string
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
	// identity; the entity (ArtifactKey) resolves the VM spec.
	if err := rewriteK3sServerToForward(ctx, exec, retrieved, p.ArtifactKey, p.DeployName); err != nil {
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
func rewriteK3sServerToForward(ctx context.Context, exec *sdk.Executor, retrievedPath, entityRef, deployName string) error {
	forwards, err := deployVMForwards(ctx, exec, entityRef, deployName)
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
// #65 bug, preserved from the core original):
//   - entityRef (the ENTITY-scoped artifact key, e.g. "vm:k3s-vm") resolves the VM
//     SPEC — one shared k3s cluster per VM; reliable via the "vm:" prefix, no
//     foldMembers dependency.
//   - deployName (the real per-DEPLOY / domain identity, e.g.
//     "check-k8s-deploy-cluster") keys the VmState port-forward LEDGER:
//     "vm:"+VmDomainIdentity(deployName) is the EXACT key the orchestrator
//     persisted under.
//
// Both LoadUnified-coupled lookups (resolving the deploy tree node, then the
// kind:vm entity) route through the generic "deploy-entity-resolve" HostBuild seam
// (F10) — the SAME seam charly/host_build_deploy_entity_resolve.go serves for the
// preresolve leg (preresolve.go's k8sEntityResolve). The persisted VmState
// port-forward LEDGER read routes through the SIBLING "config-resolve" HostBuild
// seam (candy/plugin-vm's own hostConfigResolve calls the identical seam for its
// OWN VmState reuse) — NEVER a direct deploykit.LoadDeployConfigForRead call: that
// helper's LoadBundleConfig degrades to an EMPTY config whenever
// deploykit.DeployStateHost is nil, which it ALWAYS is inside this plugin's own
// out-of-process (candy/plugin-kube is served over go-plugin gRPC, never
// compiled-in) — DeployStateHost is wired ONLY by charly-core's own init(), so a
// direct call here silently found nothing (a `LookupKey` miss, ok=false) every
// single time regardless of what was actually persisted on disk, exactly the
// silent-out-of-process-degradation class of bug the "deploy-config-save-state"
// seam (Q2, S3b) was introduced to prevent for the WRITE half — this closes the
// matching gap on the READ half. R10 bed regression: check-k8s-deploy's
// bring-up-members failed with "auto port_forward \"auto:6443\" has no persisted
// host-port allocation" even though `charly vm create`'s own persist (verified via
// a live isolated CHARLY_DEPLOY_CONFIG repro, RDD) landed correctly and stayed
// stable on disk throughout the run — the read, not the write, was broken.
func deployVMForwards(ctx context.Context, exec *sdk.Executor, entityRef, deployName string) ([]string, error) {
	vmEntity := ""
	if e, cut := strings.CutPrefix(entityRef, "vm:"); cut {
		vmEntity = e
	} else {
		var reply spec.DeployEntityResolveReply
		if err := k8sEntityResolve(ctx, exec, spec.DeployEntityResolveRequest{Kind: "bundle", Name: entityRef}, &reply); err != nil || reply.Node == nil {
			return nil, nil //nolint:nilerr // best-effort: a resolve miss means "no forward", not a hard failure
		} else {
			vmEntity = reply.Node.From
		}
	}
	if vmEntity == "" {
		return nil, nil
	}
	var vmReply spec.DeployEntityResolveReply
	if err := k8sEntityResolve(ctx, exec, spec.DeployEntityResolveRequest{Kind: "vm", Name: vmEntity}, &vmReply); err != nil || len(vmReply.EntityJSON) == 0 {
		return nil, nil //nolint:nilerr // best-effort: see above
	}
	var vm spec.ResolvedVm
	if err := json.Unmarshal(vmReply.EntityJSON, &vm); err != nil {
		return nil, fmt.Errorf("deploy-entity-resolve: decode vm %q: %w", vmEntity, err)
	}
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

// hostConfigResolveVmState fetches the persisted VmDeployState for the given "vm:<domainID>"
// ledger key via the "config-resolve" HostBuild seam (the SAME seam candy/plugin-vm's own
// hostConfigResolve uses for its OWN VmState reuse, R3 — one host-resident reader of the
// per-host deploy overlay, never a direct deploykit.LoadDeployConfigForRead from an
// out-of-process plugin, which cannot see the core-only deploykit.DeployStateHost wiring). The
// heavier fields hostBuildConfigResolve also computes (backend probe, kind:vm entity resolution
// keyed by req.Entity) are irrelevant here — domainID is never a real kind:vm entity name, so
// that lookup harmlessly misses — only VmState is read.
func hostConfigResolveVmState(ctx context.Context, exec *sdk.Executor, domainID string) (*spec.VmDeployState, error) {
	reqJSON, err := json.Marshal(spec.ConfigResolveRequest{Entity: domainID})
	if err != nil {
		return nil, err
	}
	resJSON, err := exec.HostBuild(ctx, "config-resolve", reqJSON)
	if err != nil {
		return nil, err
	}
	var reply spec.ConfigResolveReply
	if err := json.Unmarshal(resJSON, &reply); err != nil {
		return nil, fmt.Errorf("config-resolve: decode reply: %w", err)
	}
	return reply.VmState, nil
}
