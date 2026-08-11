package fleet

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

// deploy_from_box.go — Cone A shape 3: the source-less Kubernetes deploy path (`charly fleet
// from-box --cluster <name>`), served entirely plugin-side. The two registry-coupled calls it
// used to make are now the established plugin↔host idioms every OTHER shape in this cutover uses:
//
//   - the former charly-core direct LoadUnified-coupled kubernetes-spec lookup → PLUGIN-SIDE
//     self-load (loaderkit.ResolveKubernetesEntityViaExecutor, K-wave W3a A3-phase-2) — the
//     former "deploy-entity-resolve" HostBuild seam this used to round-trip through is DELETED,
//     unblocked by W1's LoadUnifiedViaExecutor letting a plugin load the project itself.
//   - the former core-registry dance → exec.InvokeProvider(ctx, "deploy", "kubernetes", sdk.OpEmit,
//     reqJSON, nil, opts) — the exact peer-dispatch idiom deploy_target.go's
//     lifecycleInvoke/preresolveSubstrate already use.
//
// deploykit.CapabilitiesFromLabels is already 100% sdk-portable (package deploykit), unchanged.
//
// candy/plugin-fleet/fleet_cmd.go's FleetFromBoxCmd.Run() branches between two fully
// plugin-side paths: --cluster set → calls DeployFromBox here directly; --cluster empty →
// runFromBoxPod (from_box_pod.go), which reaches deploy:pod's OpConfigSetup directly by
// InvokeProvider — the former "deploy-from-box" HostBuild seam + charly/fleet_from_box_cmd.go are
// DELETED (K-wave 2 cone R2 bank B).

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
	ProjectDir     string // for the self-loaded kind:kubernetes cluster lookup (resolveKubernetesSpec)
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

	// 2. Look up the kind:kubernetes cluster template — self-loaded PLUGIN-SIDE (resolveKubernetesSpec).
	projectDir := opts.ProjectDir
	if projectDir == "" {
		projectDir = "."
	}
	cluster := resolveKubernetesSpec(ctx, exec, projectDir, opts.ClusterName)
	// cluster may be nil — downstream Kustomize emission handles that
	// (defaults fall back to kubectl current-context + "default" namespace).

	// 3. Derive deployment name if not provided (use image basename without tag).
	deployName := opts.DeploymentName
	if deployName == "" {
		deployName = spec.DeriveDeploymentName(opts.ImageRef)
	}

	// 4. Build the deployment spec from the per-machine overlay if any.
	dc := spec.Deploy{
		Target: "kubernetes",
	}
	if opts.DeployOverlay != nil {
		dc = *opts.DeployOverlay
		dc.Target = "kubernetes"
	}
	if dc.Deploy == nil {
		dc.Deploy = &spec.KubernetesDeploy{}
	}
	dc.From = opts.ClusterName
	if opts.Namespace != "" {
		dc.Deploy.Namespace = opts.Namespace
	}

	// 5. Resolve output dir — defaultKubernetesOutputDir mirrors the deploy:kubernetes preresolver's own copy
	// (R3): the sole caller (FleetFromBoxCmd.Run()) always passes ProjectDir as os.Getwd(), so
	// this is behavior-preserving.
	outDir := opts.OutputDir
	if outDir == "" {
		var err error
		outDir, err = defaultKubernetesOutputDir()
		if err != nil {
			return "", fmt.Errorf("resolving default kubernetes output dir: %w", err)
		}
	}

	// 6. Generate.
	return GenerateKubernetesKustomize(ctx, exec, KubernetesGenerateOpts{
		DeploymentName: deployName,
		Instance:       opts.Instance,
		ImageRef:       opts.ImageRef,
		Deploy:         dc,
		Capabilities:   caps,
		Cluster:        cluster,
		OutputDir:      outDir,
	})
}

// resolveKubernetesSpec resolves a kind:kubernetes cluster template by name — PLUGIN-SIDE, self-loading the
// project (K-wave W3a A3-phase-2: loaderkit.ResolveKubernetesEntityViaExecutor, unblocked by W1's
// LoadUnifiedViaExecutor). Replaces the former "deploy-entity-resolve" HostBuild seam round-trip.
// A resolve miss (no charly.yml, no declared cluster, a decode failure) degrades to nil, matching
// the former function's own swallow-to-nil contract (downstream Kustomize emission handles a nil
// cluster).
func resolveKubernetesSpec(ctx context.Context, exec *sdk.Executor, dir, name string) *spec.ResolvedKubernetes {
	if exec == nil || dir == "" || name == "" {
		return nil
	}
	spc, err := loaderkit.ResolveKubernetesEntityViaExecutor(ctx, exec, dir, name)
	if err != nil {
		return nil
	}
	return spc
}

// KubernetesGenerateOpts carries the inputs a Kustomize emit needs.
type KubernetesGenerateOpts struct {
	DeploymentName string // the deployment's base name
	Instance       string // "" for the bare overlay; non-empty for image/instance
	ImageRef       string // fully qualified image ref (registry/name:tag)
	Deploy         spec.Deploy
	Capabilities   *spec.BoxMetadata
	Cluster        *spec.ResolvedKubernetes
	OutputDir      string // usually <projectDir>/.opencharly/k8s
}

// GenerateKubernetesKustomize dispatches to candy/plugin-kube's deploy:kubernetes OpEmit (materializeKustomize)
// via exec.InvokeProvider — the plugin-side replacement for the former core-registry
// ResolveDeploy + InvokeWithExecutor dance. Returns the absolute path to the overlay that
// `kubectl apply -k` should target.
func GenerateKubernetesKustomize(ctx context.Context, exec *sdk.Executor, opts KubernetesGenerateOpts) (string, error) {
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
	req := spec.KubernetesGenerateKustomizeRequest{
		Name:        opts.DeploymentName,
		ImageRef:    opts.ImageRef,
		Node:        &opts.Deploy,
		CapsJSON:    capsJSON,
		ClusterJSON: opts.Cluster.Raw,
		OutputDir:   opts.OutputDir,
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal kubernetes materialize request: %w", err)
	}

	resJSON, err := exec.InvokeProvider(ctx, "deploy", "kubernetes", sdk.OpEmit, reqJSON, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return "", fmt.Errorf("kubernetes materialize invoke: %w", err)
	}
	var reply spec.KubernetesGenerateKustomizeReply
	if len(resJSON) > 0 {
		if err := json.Unmarshal(resJSON, &reply); err != nil {
			return "", fmt.Errorf("kubernetes materialize decode reply: %w", err)
		}
	}
	return reply.OverlayPath, nil
}

// defaultKubernetesOutputDir resolves the canonical output directory for emitted kustomize trees.
// candy/plugin-kube carries its OWN copy (materialize.go) for its own callers — this one is the
// plugin-side twin of the former core defaultKubernetesOutputDir for THIS package's sole caller
// (DeployFromBox above).
func defaultKubernetesOutputDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}
