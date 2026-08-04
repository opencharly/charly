package bundle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/spec/spec"
)

// deploy_from_box.go — Cone A shape 3: the source-less K8s deploy path (`charly bundle from-box
// --cluster <name>`), relocated WHOLESALE from the deleted charly/k8s_deploy_from_box.go. The two
// registry-coupled calls it made are now the established plugin↔host idioms every OTHER shape in
// this cutover uses:
//
//   - the former charly-core direct LoadUnified-coupled k8s-spec lookup → PLUGIN-SIDE self-load
//     (loaderkit.ResolveK8sEntityViaExecutor, K-wave W3a A3-phase-2) — the former
//     "deploy-entity-resolve" HostBuild seam this used to round-trip through is DELETED, unblocked
//     by W1's LoadUnifiedViaExecutor letting a plugin load the project itself.
//   - providerRegistry.ResolveDeploy("k8s") + InvokeWithExecutor(...) (the core-registry dance) →
//     exec.InvokeProvider(ctx, "deploy", "k8s", sdk.OpEmit, reqJSON, nil, opts) — the exact
//     peer-dispatch idiom deploy_target.go's lifecycleInvoke/preresolveSubstrate already use.
//
// deploykit.CapabilitiesFromLabels is already 100% sdk-portable (package deploykit), unchanged.
//
// candy/plugin-bundle/bundle_cmd.go's BundleFromBoxCmd.Run() branches BEFORE forwarding to the
// host: --cluster set → calls DeployFromBox here directly; --cluster empty → unchanged
// hostDeploySeam("deploy-from-box", ...) for the pod path (charly/bundle_from_box_cmd.go, which
// lost its now-dead Cluster/Namespace branch — R5).

// DeployFromBoxOpts carries the source-less-deploy inputs.
type DeployFromBoxOpts struct {
	Engine         string // "podman" | "docker" (auto-detected if empty)
	ImageRef       string // fully-qualified registry/name:tag
	DeploymentName string // optional override; defaults to the basename of ImageRef without tag
	Instance       string // optional "image/instance" suffix
	ClusterName    string // cluster profile name
	Namespace      string // optional override of the cluster's default namespace
	DeployOverlay  *spec.Deploy
	OutputDir      string // defaults to <cwd>/.opencharly/k8s
	ProjectDir     string // for the self-loaded kind:k8s cluster lookup (resolveK8sSpec)
}

// DeployFromBox performs the source-less deploy. Returns the absolute path
// to the Kustomize overlay directory produced (the argument to
// `kubectl apply -k`).
func DeployFromBox(ctx context.Context, exec *sdk.Executor, opts DeployFromBoxOpts) (string, error) {
	if opts.ImageRef == "" {
		return "", fmt.Errorf("image ref is required")
	}
	if opts.ClusterName == "" {
		return "", fmt.Errorf("--cluster is required")
	}

	// 1. Pull capabilities from OCI labels.
	engine := opts.Engine
	if engine == "" {
		engine = "podman"
	}
	caps, err := deploykit.CapabilitiesFromLabels(engine, opts.ImageRef)
	if err != nil {
		return "", fmt.Errorf("reading capabilities from %q: %w", opts.ImageRef, err)
	}

	// 2. Look up the kind:k8s cluster template — self-loaded PLUGIN-SIDE (resolveK8sSpec).
	projectDir := opts.ProjectDir
	if projectDir == "" {
		projectDir = "."
	}
	cluster := resolveK8sSpec(ctx, exec, projectDir, opts.ClusterName)
	// cluster may be nil — downstream Kustomize emission handles that
	// (defaults fall back to kubectl current-context + "default" namespace).

	// 3. Derive deployment name if not provided (use image basename without tag).
	deployName := opts.DeploymentName
	if deployName == "" {
		deployName = spec.DeriveDeploymentName(opts.ImageRef)
	}

	// 4. Build the deployment spec from the per-machine overlay if any.
	dc := spec.Deploy{
		Target: "k8s",
	}
	if opts.DeployOverlay != nil {
		dc = *opts.DeployOverlay
		dc.Target = "k8s"
	}
	if dc.Kubernetes == nil {
		dc.Kubernetes = &spec.K8sDeploy{}
	}
	dc.From = opts.ClusterName
	if opts.Namespace != "" {
		dc.Kubernetes.Namespace = opts.Namespace
	}

	// 5. Resolve output dir — defaultK8sOutputDir mirrors the deploy:k8s preresolver's own copy
	// (R3): the sole caller (BundleFromBoxCmd.Run()) always passes ProjectDir as os.Getwd(), so
	// this is behavior-preserving.
	outDir := opts.OutputDir
	if outDir == "" {
		var err error
		outDir, err = defaultK8sOutputDir()
		if err != nil {
			return "", fmt.Errorf("resolving default k8s output dir: %w", err)
		}
	}

	// 6. Generate.
	return GenerateK8sKustomize(ctx, exec, K8sGenerateOpts{
		DeploymentName: deployName,
		Instance:       opts.Instance,
		ImageRef:       opts.ImageRef,
		Deploy:         dc,
		Capabilities:   caps,
		Cluster:        cluster,
		OutputDir:      outDir,
	})
}

// resolveK8sSpec resolves a kind:k8s cluster template by name — PLUGIN-SIDE, self-loading the
// project (K-wave W3a A3-phase-2: loaderkit.ResolveK8sEntityViaExecutor, unblocked by W1's
// LoadUnifiedViaExecutor). Replaces the former "deploy-entity-resolve" HostBuild seam round-trip.
// A resolve miss (no charly.yml, no declared cluster, a decode failure) degrades to nil, matching
// the former function's own swallow-to-nil contract (downstream Kustomize emission handles a nil
// cluster).
func resolveK8sSpec(ctx context.Context, exec *sdk.Executor, dir, name string) *spec.ResolvedK8s {
	if exec == nil || dir == "" || name == "" {
		return nil
	}
	spc, err := loaderkit.ResolveK8sEntityViaExecutor(ctx, exec, dir, name)
	if err != nil {
		return nil
	}
	return spc
}

// K8sGenerateOpts carries the inputs a Kustomize emit needs.
type K8sGenerateOpts struct {
	DeploymentName string // the deployment's base name
	Instance       string // "" for the bare overlay; non-empty for image/instance
	ImageRef       string // fully qualified image ref (registry/name:tag)
	Deploy         spec.Deploy
	Capabilities   *spec.BoxMetadata
	Cluster        *spec.ResolvedK8s
	OutputDir      string // usually <projectDir>/.opencharly/k8s
}

// GenerateK8sKustomize dispatches to candy/plugin-kube's deploy:k8s OpEmit (materializeKustomize)
// via exec.InvokeProvider — the plugin-side replacement for the former core
// providerRegistry.ResolveDeploy("k8s") + InvokeWithExecutor registry dance. Returns the absolute
// path to the overlay that `kubectl apply -k` should target.
func GenerateK8sKustomize(ctx context.Context, exec *sdk.Executor, opts K8sGenerateOpts) (string, error) {
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

	resJSON, err := exec.InvokeProvider(ctx, "deploy", "k8s", sdk.OpEmit, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return "", fmt.Errorf("k8s materialize invoke: %w", err)
	}
	var reply spec.K8sGenerateKustomizeReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return "", fmt.Errorf("k8s materialize decode reply: %w", err)
		}
	}
	return reply.OverlayPath, nil
}

// defaultK8sOutputDir resolves the canonical output directory for emitted kustomize trees.
// candy/plugin-kube carries its OWN copy (materialize.go) for its own callers — this one is the
// plugin-side twin of the former core defaultK8sOutputDir for THIS package's sole caller
// (DeployFromBox above).
func defaultK8sOutputDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}
