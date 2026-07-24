package main

// k8s_generate.go — the in-core DISPATCH SHIM for Kustomize generation (K5-A item 6). The
// manifest GENERATOR (generateWorkload / generatePodSpec / generateService / generatePVCs /
// generateIngress / checkToProbe / …) lives in the COMPILED-IN candy/plugin-k8sgen; the WRITE +
// egress-VALIDATE sequence that used to run here too (disk I/O, the ValidateEgressValue gate)
// relocated into candy/plugin-kube's materializeKustomize (candy/plugin-kube/materialize.go) —
// reached peer-to-peer via InvokeProvider, no host round trip needed (the generator is pure, and
// plugin-kube is a same-host subprocess with direct disk access). This file's ONLY remaining job
// is the core→plugin DISPATCH for the ONE caller that has no venue of its own: the source-less
// `charly bundle from-box --target k8s` path (k8s_deploy_from_box.go). It threads a throwaway
// kit.ShellExecutor{} purely to stand up the InvokeWithExecutor broker candy/plugin-kube needs to
// reach verb:k8sgen/verb:egress — the "cold-start" pattern (a caller with no real venue still
// needs SOME broker for its callee to reach peer providers). The deploy:k8s preresolve leg
// (candy/plugin-kube/preresolve.go) calls materializeKustomize DIRECTLY (same package, no
// dispatch needed — it already holds a live venue executor from its own Invoke). The former
// "k8s-generate-kustomize" HostBuild seam (host_build_k8s_generate.go) is DELETED.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// K8sGenerateOpts carries the inputs a Kustomize emit needs.
type K8sGenerateOpts struct {
	DeploymentName string // map key from charly.yml:deployments.images (base image name)
	Instance       string // "" for the bare overlay; non-empty for image/instance
	ImageRef       string // fully qualified image ref (registry/name:tag)
	Deploy         spec.BundleNode
	Capabilities   *spec.BoxMetadata
	Cluster        *ResolvedK8s
	OutputDir      string // usually <projectDir>/.opencharly/k8s
}

// GenerateK8sKustomize dispatches to candy/plugin-kube's deploy:k8s OpEmit (materializeKustomize)
// for the source-less from-box path — the ONE remaining core caller with no venue of its own.
// Returns the absolute path to the overlay that `kubectl apply -k` should target.
func GenerateK8sKustomize(opts K8sGenerateOpts) (string, error) {
	if opts.DeploymentName == "" {
		return "", fmt.Errorf("deployment name is required")
	}
	if opts.Capabilities == nil {
		return "", fmt.Errorf("capabilities are required (read from OCI labels of %q)", opts.ImageRef)
	}
	if opts.Cluster == nil {
		return "", fmt.Errorf("cluster profile is required (kubernetes.cluster: not set?)")
	}

	capsJSON, err := json.Marshal(opts.Capabilities)
	if err != nil {
		return "", fmt.Errorf("marshal capabilities: %w", err)
	}
	req := spec.K8sGenerateKustomizeRequest{
		Name:        opts.DeploymentName,
		ImageRef:    opts.ImageRef,
		Node:        &opts.Deploy,
		CapsJSON:    capsJSON,
		ClusterJSON: opts.Cluster.Raw,
		OutputDir:   opts.OutputDir,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal k8s materialize request: %w", err)
	}

	prov, ok := providerRegistry.ResolveDeploy("k8s")
	if !ok {
		return "", fmt.Errorf("k8s deploy provider (deploy:k8s) not registered — charly built without candy/plugin-kube")
	}
	inv, ok := prov.(executorInvoker)
	if !ok {
		return "", fmt.Errorf("k8s deploy provider is in-proc; deploy:k8s OpEmit requires an out-of-process broker")
	}
	// No real venue is involved in generating a Kustomize tree — kit.ShellExecutor{} only stands
	// up the broker candy/plugin-kube needs internally to reach verb:k8sgen/verb:egress.
	res, err := inv.InvokeWithExecutor(context.Background(),
		&Operation{Reserved: "k8s", Op: sdk.OpEmit, Params: reqJSON}, kit.ShellExecutor{}, buildEngineContext{}, false, nil)
	if err != nil {
		return "", fmt.Errorf("k8s materialize invoke: %w", err)
	}
	var reply spec.K8sGenerateKustomizeReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return "", fmt.Errorf("k8s materialize decode reply: %w", err)
		}
	}
	return reply.OverlayPath, nil
}

// defaultK8sOutputDir resolves the canonical output directory for emitted kustomize trees, for
// the ONE remaining core caller (k8s_deploy_from_box.go) that hasn't already resolved a project
// dir. candy/plugin-kube carries its OWN copy (materialize.go) for its own callers — this one
// cannot be shared across the process boundary.
func defaultK8sOutputDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}
