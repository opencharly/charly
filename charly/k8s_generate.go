package main

// k8s_generate.go — the in-core SHIM for Kustomize generation (C8/M13). The
// manifest GENERATOR (generateWorkload / generatePodSpec / generateService /
// generatePVCs / generateIngress / checkToProbe / …) moved into the COMPILED-IN
// candy/plugin-k8sgen; this shim builds the pure-generation input from
// K8sGenerateOpts, Invokes the plugin's OpEmit, then does the host-side disk I/O +
// the egress gate (ValidateEgressValue, via the M16 egress shim) before the bytes
// hit disk. Both in-core callers (the deploy:k8s preresolver in
// k8s_deploy_preresolve.go and the source-less `charly bundle from-box --target k8s`
// in k8s_deploy_from_box.go) keep calling GenerateK8sKustomize unchanged.
//
// host→plugin dispatch mirrors egress.go (plain resolve+Invoke). Compiled-in
// placement keeps verb:k8sgen resolvable at deploy time with no connect step.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// K8sGenerateOpts carries the inputs a Kustomize emit needs.
type K8sGenerateOpts struct {
	DeploymentName string // map key from charly.yml:deployments.images (base image name)
	Instance       string // "" for the bare overlay; non-empty for image/instance
	ImageRef       string // fully qualified image ref (registry/name:tag)
	Deploy         spec.BundleNode
	Capabilities   *Capabilities
	Cluster        *ResolvedK8s
	OutputDir      string // usually <projectDir>/.opencharly/k8s
}

// GenerateK8sKustomize materializes the Kustomize tree on disk. Returns the
// absolute path to the overlay that `kubectl apply -k` should target. The pure
// generation runs in candy/plugin-k8sgen (verb:k8sgen / OpEmit); this shim owns the
// guards, the caps→ports/uid/gid lift, the disk I/O, the raw-manifest copy, and the
// egress gate.
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

	// Build the pure-generation input (caps lifted to ports/uid/gid host-side).
	input := spec.K8sGenInput{
		DeploymentName: opts.DeploymentName,
		Instance:       opts.Instance,
		ImageRef:       opts.ImageRef,
		Deploy:         opts.Deploy,
		// Ship the cluster body OPAQUELY (Cutover K) — candy/plugin-k8sgen decodes it.
		ClusterRaw: opts.Cluster.Raw,
		Ports:      opts.Capabilities.Port,
		UID:        opts.Capabilities.UID,
		GID:        opts.Capabilities.GID,
		OutputDir:  opts.OutputDir,
	}

	prov, ok := providerRegistry.resolve(ClassVerb, "k8sgen")
	if !ok {
		return "", fmt.Errorf("k8sgen plugin (verb:k8sgen) not registered — charly built without candy/plugin-k8sgen")
	}
	params, err := marshalJSON(input)
	if err != nil {
		return "", fmt.Errorf("k8sgen marshal input: %w", err)
	}
	res, err := prov.Invoke(context.Background(), &Operation{Reserved: "k8sgen", Op: OpEmit, Params: params})
	if err != nil {
		return "", fmt.Errorf("k8sgen invoke: %w", err)
	}
	var reply spec.K8sGenReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return "", fmt.Errorf("k8sgen decode reply: %w", err)
		}
	}

	root := filepath.Join(opts.OutputDir, opts.DeploymentName)
	// Always (re)emit base from scratch — it's computed from inputs every time to
	// avoid stale artifacts. Overlays are additive.
	if err := os.RemoveAll(filepath.Join(root, "base")); err != nil {
		return "", fmt.Errorf("cleaning base dir: %w", err)
	}

	// Materialize each generated manifest: egress-validate, then write.
	for _, f := range reply.Files {
		full := filepath.Join(root, f.RelPath)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return "", err
		}
		var doc any
		if err := json.Unmarshal(f.Doc, &doc); err != nil {
			return "", fmt.Errorf("decoding generated %q: %w", f.RelPath, err)
		}
		if err := ValidateEgressValue(f.EgressKind, f.RelPath, doc); err != nil {
			return "", err
		}
		if err := writeYAML(full, doc); err != nil {
			return "", err
		}
	}

	// Copy raw manifests from deployment.kubernetes.raw into base/raw/ verbatim
	// (the generator registered their kustomize resource paths; the host owns the
	// file copy since the generator does no disk I/O).
	if opts.Deploy.Kubernetes != nil && len(opts.Deploy.Kubernetes.Raw) > 0 {
		rawDir := filepath.Join(root, "base", "raw")
		if err := os.MkdirAll(rawDir, 0755); err != nil {
			return "", err
		}
		for _, src := range opts.Deploy.Kubernetes.Raw {
			data, err := os.ReadFile(src)
			if err != nil {
				return "", fmt.Errorf("reading raw manifest %q: %w", src, err)
			}
			if err := os.WriteFile(filepath.Join(rawDir, filepath.Base(src)), data, 0644); err != nil {
				return "", err
			}
		}
	}

	return filepath.Join(root, reply.OverlayRelPath), nil
}

func writeYAML(path string, doc any) error {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", path, err)
	}
	return os.WriteFile(path, out, 0644)
}

// defaultK8sOutputDir resolves the canonical output directory for emitted
// kustomize trees, next to its GenerateK8sKustomize caller (moved from the
// stray bundle_add_cmd_k8s.go): the deploy:k8s preresolver
// (k8s_deploy_preresolve.go) and the source-less
// `charly bundle from-box --target k8s` path (k8s_deploy_from_box.go) both
// default to it when their caller hasn't already resolved a project dir.
func defaultK8sOutputDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}
