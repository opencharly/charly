package bundle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// secrets_artifacts.go — Cone A shape 3: the ORCHESTRATION that used to live core-side in
// charly/deploy_add_shared.go (prepareCandySecrets / retrieveArtifactsAndK3s / K3sPostProvision),
// wrapping handleDeployApply's substrate dispatch (deploy_target.go). The genuine floor-M pieces
// (the project candy scan, the credential-store touch, the live artifact fetch) stay host-side
// behind two thin HostBuild seams ("deploy-candy-secrets", "deploy-artifacts-retrieve"); the
// DECISION of what to do with their results — inject secrets into the in-proc plans BEFORE
// dispatch, and dispatch the register-hint-driven k3s-post-provision AFTER artifact retrieval —
// runs HERE, plugin-side, reaching candy/plugin-kube directly via exec.InvokeProvider (F10) instead
// of the former core resolveKubePlugin/connectPluginByWord/InvokeWithExecutor registry dance.

// injectCandySecrets resolves the candies backing plans (project scan + credential-store lookup,
// via the "deploy-candy-secrets" HostBuild seam) and injects the resolved env into plans' TaskSteps
// IN-PROC via the already-portable deploykit.InjectSecretsIntoPlans — no plan mutation crosses the
// wire, only the resolved secret_env does. Returns the resolved secret_env (the caller folds it
// into the artifact-retrieval env via deploykit.BuildArtifactEnv, matching the former core Add()'s
// own reuse of prepareCandySecrets's secretEnv) and the register hints (e.g. "kubeconfig") the
// caller consults AFTER the substrate dispatch + artifact retrieval to decide whether to dispatch
// k3sPostProvision. A no-op (nil, nil, no error) when dir is empty, mirroring the former core
// Add()'s own `if dir != "" { ... }` guard.
func injectCandySecrets(ctx context.Context, exec *sdk.Executor, dir string, plans []*deploykit.InstallPlan) (map[string]string, []string, error) {
	if dir == "" {
		return nil, nil, nil
	}
	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		if p != nil {
			views = append(views, deploykit.WireView(p))
		}
	}
	plansJSON, err := json.Marshal(views)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal plans for secret resolution: %w", err)
	}
	reqJSON, err := json.Marshal(spec.DeployCandySecretsRequest{Dir: dir, PlansJSON: plansJSON})
	if err != nil {
		return nil, nil, err
	}
	resJSON, err := exec.HostBuild(ctx, "deploy-candy-secrets", reqJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("loading candies for secret resolution: %w", err)
	}
	var reply spec.DeployCandySecretsReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return nil, nil, fmt.Errorf("decode deploy-candy-secrets reply: %w", err)
		}
	}
	deploykit.InjectSecretsIntoPlans(plans, reply.SecretEnv)
	return reply.SecretEnv, reply.RegisterHints, nil
}

// retrieveArtifactsAndDispatchRegisters pulls back the deploy's candy artifacts (via the
// "deploy-artifacts-retrieve" HostBuild seam, over the ALREADY-established venue) and then
// dispatches whichever register hint handlers apply (dispatchRegisterHints below). The caller
// (handleDeployApply) only reaches this on the non-dry-run, substrate-dispatch-succeeded path —
// dryRun is handled entirely by that earlier early-return, so this function is never itself
// called under dry-run (matching the former retrieveArtifactsAndK3s's own DryRun early-return,
// one level up).
func retrieveArtifactsAndDispatchRegisters(ctx context.Context, exec *sdk.Executor, dir string, plans []*deploykit.InstallPlan, artifactKey, deployName string, artifactEnv map[string]string, registerHints []string) error {
	views := make([]spec.InstallPlanView, 0, len(plans))
	for _, p := range plans {
		if p != nil {
			views = append(views, deploykit.WireView(p))
		}
	}
	plansJSON, err := json.Marshal(views)
	if err != nil {
		return fmt.Errorf("marshal plans for artifact retrieval: %w", err)
	}
	reqJSON, err := json.Marshal(spec.DeployArtifactsRetrieveRequest{
		Dir: dir, PlansJSON: plansJSON, ArtifactKey: artifactKey, DeployName: deployName, ArtifactEnv: artifactEnv,
	})
	if err != nil {
		return err
	}
	if _, err := exec.HostBuild(ctx, "deploy-artifacts-retrieve", reqJSON); err != nil {
		return fmt.Errorf("retrieving candy artifacts: %w", err)
	}
	return dispatchRegisterHints(ctx, exec, artifactKey, deployName, registerHints)
}

// dispatchRegisterHints dispatches whichever register hint handlers apply — data-driven,
// word-keyed (R3), mirroring the former core artifactRegisterHandlers dispatch exactly: today only
// "kubeconfig" has a handler (k3sPostProvision). Split out from
// retrieveArtifactsAndDispatchRegisters as its OWN function purely for testability (the seam call
// needs a live *sdk.Executor broker; this pure word-keyed loop does not).
func dispatchRegisterHints(ctx context.Context, exec *sdk.Executor, artifactKey, deployName string, registerHints []string) error {
	for _, register := range registerHints {
		handler, ok := artifactRegisterHandlers[register]
		if !ok {
			continue
		}
		if err := handler(ctx, exec, artifactKey, deployName); err != nil {
			return err
		}
	}
	return nil
}

// artifactRegisterHandlers maps a candy artifact's declared `register:` hint (the
// #vmshared.CandyArtifact.Register field, SDD-sourced in sdk/schema/candy.cue) to the post-retrieve
// processing it triggers — the plugin-side twin of the former core map, word-keyed and data-driven
// (R3): a candy declares the hint on its OWN artifact entry (k3s-server's kubeconfig artifact
// declares `register: kubeconfig`) — adding a new registration kind means adding ONE map entry
// here, never a hardcoded candy-name special case.
var artifactRegisterHandlers = map[string]func(ctx context.Context, exec *sdk.Executor, artifactKey, deployName string) error{
	"kubeconfig": k3sPostProvision,
}

// k3sPostProvision dispatches the k3s-post-provision method to candy/plugin-kube directly via
// exec.InvokeProvider (F10 peer-dispatch) — the plugin-side replacement for the former core
// resolveKubePlugin/connectPluginByWord/InvokeWithExecutor registry dance (`candy/plugin-adb`'s
// credential_shim.go and this file's own lifecycleInvoke are the established precedent). The
// explicit shellVenueDescriptor override reproduces the ORIGINAL "broker only, no live venue"
// idiom exactly (invokeKubePluginWithBroker's kit.ShellExecutor{}): the k3s-post-provision method
// runs entirely on the operator host's own filesystem (~/.cache/charly + ~/.kube/config), never
// inside the deploy's own venue (a podman container / VM guest), so it must NOT inherit whatever
// live venue this Invoke's own broker happens to carry.
func k3sPostProvision(ctx context.Context, exec *sdk.Executor, artifactKey, deployName string) error {
	shellDesc := kit.DescriptorFromExecutor(kit.ShellExecutor{})
	params, err := json.Marshal(spec.Op{Plugin: "kube", PluginInput: map[string]any{
		"method": "k3s-post-provision", "artifact_key": artifactKey, "deploy_name": deployName,
	}})
	if err != nil {
		return fmt.Errorf("k3s post-provision %q: marshal op: %w", artifactKey, err)
	}
	resJSON, err := exec.InvokeProvider(ctx, "verb", "kube", sdk.OpRun, params, nil, sdk.InvokeProviderOpts{VenueDescriptor: &shellDesc})
	if err != nil {
		return fmt.Errorf("k3s post-provision %q: %w", artifactKey, err)
	}
	var pr struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &pr); err != nil {
			return fmt.Errorf("k3s post-provision %q: decode reply: %w", artifactKey, err)
		}
	}
	if pr.Status == "fail" {
		return fmt.Errorf("k3s post-provision %q: %s", artifactKey, pr.Message)
	}
	return nil
}

// kubeAlreadyConnected reports whether verb:kube is ALREADY registered — a pure routing query
// (sdk.Executor.DescribeProvider, K5-A item 2) with NO connect attempt and NO side effect,
// mirroring the former core Update()'s own `providerRegistry.ResolveVerb("kube")` gate exactly:
// an Update dispatch has no candyList to consult artifactRegisterHandlers against, so it instead
// re-runs k3sPostProvision UNCONDITIONALLY whenever kube is already connected (a k3s-server
// deploy's own plugin-loading preamble already connected it if THIS deploy needs it).
func kubeAlreadyConnected(ctx context.Context, exec *sdk.Executor) bool {
	found, _, err := exec.DescribeProvider(ctx, "verb", "kube")
	return err == nil && found
}
