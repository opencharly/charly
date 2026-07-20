package main

import (
	"fmt"

	"github.com/opencharly/sdk/spec"
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
	caps, err := CapabilitiesFromLabels(engine, opts.ImageRef)
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
		dc.Kubernetes = &K8sDeployConfig{}
	}
	dc.From = opts.ClusterName
	if opts.Namespace != "" {
		dc.Kubernetes.Namespace = opts.Namespace
	}

	// 5. Resolve output dir — shares defaultK8sOutputDir (k8s_generate.go) with
	// the deploy:k8s preresolver (R3): the sole caller (bundle_from_box_cmd.go)
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
