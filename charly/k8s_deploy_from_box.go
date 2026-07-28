package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"github.com/opencharly/sdk/vmshared"
)

// -----------------------------------------------------------------------------
// Source-less K8s deploy — Part F.10.
//
// `charly bundle from-box <registry-ref> [name]` can deploy to K8s without any
// access to the repo's charly.yml. Capabilities come from the pushed OCI
// image's ai.opencharly.* labels; runtime choices come from (per-machine
// ~/.config/charly/charly.yml, cluster profile). This proves the self-contained
// image invariant from Part G.
// -----------------------------------------------------------------------------

// DeployFromBoxOpts carries the source-less-deploy inputs.
type DeployFromBoxOpts struct {
	Engine         string           // "podman" | "docker" (auto-detected if empty)
	ImageRef       string           // fully-qualified registry/name:tag
	DeploymentName string           // optional override; defaults to the basename of ImageRef without tag
	Instance       string           // optional "image/instance" suffix
	ClusterName    string           // cluster profile name (ClusterProfile.Name)
	Namespace      string           // optional override of cluster profile's default namespace
	DeployOverlay  *spec.BundleNode // optional: merged from ~/.config/charly/charly.yml
	OutputDir      string           // defaults to <cwd>/.opencharly/k8s
	ProjectDir     string           // for looking up clusters/<name>.yaml
}

// DeployFromBox performs the source-less deploy. Returns the absolute path
// to the Kustomize overlay directory produced (the argument to
// `kubectl apply -k`).
func DeployFromBox(opts DeployFromBoxOpts) (string, error) {
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

	// 2. Look up kind:k8s template (schema v4 replacement for ClusterProfile).
	projectDir := opts.ProjectDir
	if projectDir == "" {
		projectDir = "."
	}
	cluster := findK8sSpec(projectDir, opts.ClusterName)
	// cluster may be nil — downstream Kustomize emission handles that
	// (defaults fall back to kubectl current-context + "default" namespace).

	// 3. Derive deployment name if not provided (use image basename without tag).
	deployName := opts.DeploymentName
	if deployName == "" {
		deployName = deriveDeploymentName(opts.ImageRef)
	}

	// 4. Build the deployment spec from the per-machine overlay if any.
	dc := spec.BundleNode{
		Target: "k8s",
	}
	if opts.DeployOverlay != nil {
		dc = *opts.DeployOverlay
		dc.Target = "k8s"
	}
	if dc.Kubernetes == nil {
		dc.Kubernetes = &vmshared.K8sDeployConfig{}
	}
	dc.From = opts.ClusterName
	if opts.Namespace != "" {
		dc.Kubernetes.Namespace = opts.Namespace
	}

	// 5. Resolve output dir — defaultK8sOutputDir (folded below) mirrors the deploy:k8s
	// preresolver's own copy (R3): the sole caller (bundle_from_box_cmd.go)
	// always passes ProjectDir as os.Getwd(), so this is behavior-preserving.
	outDir := opts.OutputDir
	if outDir == "" {
		var err error
		outDir, err = defaultK8sOutputDir()
		if err != nil {
			return "", fmt.Errorf("resolving default k8s output dir: %w", err)
		}
	}

	// 6. Generate.
	return GenerateK8sKustomize(K8sGenerateOpts{
		DeploymentName: deployName,
		Instance:       opts.Instance,
		ImageRef:       opts.ImageRef,
		Deploy:         dc,
		Capabilities:   caps,
		Cluster:        cluster,
		OutputDir:      outDir,
	})
}

// deriveDeploymentName turns "quay.io/myorg/openclaw:v1" → "openclaw" and
// "registry.example.com/path/foo" → "foo".
func deriveDeploymentName(imageRef string) string {
	// Strip tag.
	ref := imageRef
	if idx := lastIndexByteInRef(ref, ':'); idx >= 0 {
		ref = ref[:idx]
	}
	// Return last path component.
	if idx := lastIndexByteInRef(ref, '/'); idx >= 0 {
		return ref[idx+1:]
	}
	return ref
}

// lastIndexByteInRef returns the last index of c in s, ignoring any '/' that
// appears after a port number in a registry host (e.g., "localhost:5000/foo:v1"
// should not treat the ":5000" colon as a tag boundary). Simple heuristic:
// return last ':' only if it appears after the last '/'.
func lastIndexByteInRef(s string, c byte) int {
	lastSlash := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			lastSlash = i
		}
	}
	last := -1
	start := 0
	if c == ':' {
		start = lastSlash + 1 // only look after final path segment for tag
	}
	for i := start; i < len(s); i++ {
		if s[i] == c {
			last = i
		}
	}
	return last
}

// -----------------------------------------------------------------------------
// Kustomize generation dispatch (folded from the former k8s_generate.go, plugin-kube
// #2 seam-death): the in-core DISPATCH SHIM for Kustomize generation (K5-A item 6). The
// manifest GENERATOR (generateWorkload / generatePodSpec / …) lives in the COMPILED-IN
// candy/plugin-k8sgen; the WRITE + egress-VALIDATE sequence lives in candy/plugin-kube's
// materializeKustomize (reached peer-to-peer via InvokeProvider). GenerateK8sKustomize is
// the core→plugin DISPATCH for the ONE caller with no venue of its own: this source-less
// `charly bundle from-box --target k8s` path (DeployFromBox above). It threads a throwaway
// kit.ShellExecutor{} purely to stand up the InvokeWithExecutor broker candy/plugin-kube
// needs to reach verb:k8sgen/verb:egress — the "cold-start" pattern. The deploy:k8s
// preresolve leg (candy/plugin-kube/preresolve.go) calls materializeKustomize DIRECTLY
// (same package, no dispatch — it already holds a live venue executor). Co-located with its
// SOLE caller here so the standalone shim file drops (residue→0 progress).
// -----------------------------------------------------------------------------

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
// the ONE remaining core caller (DeployFromBox above) that hasn't already resolved a project
// dir. candy/plugin-kube carries its OWN copy (materialize.go) for its own callers — this one
// cannot be shared across the process boundary.
func defaultK8sOutputDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}
