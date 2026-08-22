package fleet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/fleet"
	"github.com/opencharly/spec/poll"
	"github.com/opencharly/spec/spec"
)

// secrets_artifacts.go — the deploy-add secret+artifact ORCHESTRATION that used to live core-side in
// charly/deploy_add_shared.go (prepareCandySecrets / retrieveArtifactsAndK3s / K3sPostProvision),
// wrapping handleDeployApply's substrate dispatch (deploy_target.go). #55 K4 collapsed the two host
// seams ("deploy-candy-secrets" / "deploy-artifacts-retrieve") entirely: the candy set comes from the
// resolved-project envelope command:fleet ALREADY fetched (candiesForPlans → envelopeCandyModels +
// deploykit.SelectCandiesForPlans, no host scan), secret resolution runs plugin-side via the shared
// deploykit.CredentialAccessViaExecutor (verb:credential), and artifact retrieval runs plugin-side via
// deploykit.RetrieveCandyArtifacts. The DECISION of what to do with the results — inject secrets into
// the in-proc plans BEFORE dispatch, and dispatch the register-hint-driven k3s-post-provision AFTER
// artifact retrieval — runs HERE, reaching candy/plugin-kube directly via exec.InvokeProvider (F10)
// instead of the former core resolveKubePlugin/connectPluginByWord/InvokeWithExecutor registry dance.

// candiesForPlans resolves the candies backing plans PLUGIN-SIDE from the resolved-project envelope
// (envelopeCandyModels — the SAME candy set command:fleet already fetched to compile the plans) +
// the shared deploykit.SelectCandiesForPlans pick. #55 K4: no host scan, no "deploy-candy-secrets"
// seam — the envelope already holds every candy the plan references (by construction).
func candiesForPlans(dir string, plans []*deploykit.InstallPlan) ([]spec.CandyReader, error) {
	rp, err := fetchResolvedProject(dir, nil, false)
	if err != nil {
		return nil, err
	}
	return deploykit.SelectCandiesForPlans(plans, envelopeCandyModels(rp)), nil
}

// injectCandySecrets resolves the candies backing plans + their secret_requires:/secret_accepts: env
// PLUGIN-SIDE (deploykit.ResolveSecretForCandy over the shared verb:credential CredentialAccess) and
// injects it into plans' TaskSteps via deploykit.InjectSecretsIntoPlans — #55 K4 config-write cone,
// no host "deploy-candy-secrets" seam. Returns the resolved secret_env (the caller folds it into the
// artifact-retrieval env) + the register hints (e.g. "kubeconfig") the caller consults AFTER the
// substrate dispatch + artifact retrieval to decide whether to dispatch k3sPostProvision. A no-op
// (nil, nil, no error) when dir is empty, mirroring the former core Add()'s `if dir != "" { ... }`.
func injectCandySecrets(ctx context.Context, exec *sdk.Executor, dir string, plans []*deploykit.InstallPlan) (map[string]string, []string, error) {
	if dir == "" {
		return nil, nil, nil
	}
	candyList, err := candiesForPlans(dir, plans)
	if err != nil {
		return nil, nil, fmt.Errorf("loading candies for secret resolution: %w", err)
	}
	secretEnv := deploykit.ResolveSecretForCandy(candyList, deploykit.CredentialAccessViaExecutor(ctx, exec))
	registers := fleet.CandyArtifactRegisters(candyList)
	hints := make([]string, 0, len(registers))
	for register := range registers {
		hints = append(hints, register)
	}
	deploykit.InjectSecretsIntoPlans(plans, secretEnv)
	return secretEnv, hints, nil
}

// retrieveArtifactsAndDispatchRegisters pulls back the deploy's candy artifacts PLUGIN-SIDE
// (deploykit.RetrieveCandyArtifacts — #55 K4, no "deploy-artifacts-retrieve" host seam) and then
// dispatches whichever register hint handlers apply (dispatchRegisterHints below). The caller
// (handleDeployApply) only reaches this on the non-dry-run, substrate-dispatch-succeeded path —
// dryRun is handled entirely by that earlier early-return, so this function is never itself
// called under dry-run (matching the former retrieveArtifactsAndK3s's own DryRun early-return,
// one level up).
func retrieveArtifactsAndDispatchRegisters(ctx context.Context, exec *sdk.Executor, venueExec deploykit.DeployExecutor, dir string, plans []*deploykit.InstallPlan, artifactKey, deployName, vmEntity string, artifactEnv map[string]string, registerHints []string) error {
	candyList, err := candiesForPlans(dir, plans)
	if err != nil {
		return fmt.Errorf("loading candies for artifact retrieval: %w", err)
	}
	// Readiness: poll.ResolveReadiness(nil) reads CHARLY_READINESS_* env + built-in defaults —
	// byte-identical to the host TODAY (zero configs set defaults.readiness:). NAMED EXIT #87
	// (spec.Threaded CUE-sourcing): threading the project defaults.readiness: block plugin-side is
	// deferred there — spec.Threaded is a hand-written wire type today, so readiness must NOT ride it;
	// env+defaults is the SDD-compliant + currently-equivalent path. A nil config never errors.
	readiness, _ := poll.ResolveReadiness(nil)
	// The artifact READ must run on the deploy's VENUE executor — a VM deploy's artifact
	// (e.g. k3s-server's /etc/rancher/k3s/k3s.yaml) lives INSIDE the guest, reachable only
	// over the venue's SSH executor (SSHExecutor.GetFile's sudo-cat path exists for exactly
	// this file). The prior hardcoded kit.ShellExecutor{} polled the OPERATOR HOST's
	// filesystem for a guest path — "No such file or directory" for the full wait, then a
	// timeout against a perfectly healthy guest (the check-k3s-vm R10 catch; the old core
	// retrieveArtifactsAndK3s used the live venue executor, so the "always fell to a host
	// ShellExecutor" byte-equivalence claim was wrong for lifecycle substrates). For a pod
	// deploy the venue executor IS the host shell, so that path is unchanged.
	if venueExec == nil {
		venueExec = kit.ShellExecutor{}
	}
	if err := deploykit.RetrieveCandyArtifacts(ctx, venueExec, candyList, kit.SanitizeDeployName(artifactKey), artifactEnv, spec.EmitOpts{}, readiness); err != nil {
		return fmt.Errorf("retrieving candy artifacts: %w", err)
	}
	return dispatchRegisterHints(ctx, exec, artifactKey, deployName, vmEntity, registerHints)
}

// dispatchRegisterHints dispatches whichever register hint handlers apply — data-driven,
// word-keyed (R3), mirroring the former core artifactRegisterHandlers dispatch exactly: today only
// "kubeconfig" has a handler (k3sPostProvision). Split out from
// retrieveArtifactsAndDispatchRegisters as its OWN function purely for testability (the seam call
// needs a live *sdk.Executor broker; this pure word-keyed loop does not).
func dispatchRegisterHints(ctx context.Context, exec *sdk.Executor, artifactKey, deployName, vmEntity string, registerHints []string) error {
	for _, register := range registerHints {
		handler, ok := artifactRegisterHandlers[register]
		if !ok {
			continue
		}
		if err := handler(ctx, exec, artifactKey, deployName, vmEntity); err != nil {
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
var artifactRegisterHandlers = map[string]func(ctx context.Context, exec *sdk.Executor, artifactKey, deployName, vmEntity string) error{
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
func k3sPostProvision(ctx context.Context, exec *sdk.Executor, artifactKey, deployName, vmEntity string) error {
	shellDesc := kit.DescriptorFromExecutor(kit.ShellExecutor{})
	params, err := json.Marshal(spec.Op{Plugin: "kube", PluginInput: map[string]any{
		"method": "k3s-post-provision", "artifact_key": artifactKey, "deploy_name": deployName, "vm_entity": vmEntity,
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
