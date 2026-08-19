package substratekind

// status_kubernetes.go — the Kubernetes substrate's OpStatus (K5: relocated verbatim from
// charly core). A `target: kubernetes` deploy does not run a
// container on this host — it emits a Kustomize manifest tree that
// `charly fleet sync` / `kubectl apply -k` applies to a remote cluster, so
// this collector reports GENERATION state (tree-present | not-generated) and
// the referenced cluster/context, never live pod health (that is a `kube:`
// check, candy/plugin-kube). Every input this needs (the folded project
// deploy tree, the kind:kubernetes template bodies) is fetched from the host via the
// established InvokeProvider("build","project") seam (already proven in
// production by candy/plugin-fleet's OpCompile) — resolving the referenced
// kubernetes template itself needs no cross-plugin hop at all: this SAME provider
// already implements the kubernetes substrate-template resolve (resolve.go), so it's
// an in-package call, not an Invoke.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// collectKubernetesStatus serves the kubernetes substrate's OpStatusCollect. It re-hydrates
// the resolved-project envelope over the reverse channel, enumerates every
// declared target:kubernetes deploy node, and emits one row per entry.
func collectKubernetesStatus(ctx context.Context, req spec.SubstrateStatusRequest) (spec.SubstrateStatusReply, error) {
	rp, err := fetchResolvedProject(ctx)
	if err != nil {
		return spec.SubstrateStatusReply{}, fmt.Errorf("kubernetes status-collect: %w", err)
	}

	entries := kubernetesDeployEntries(rp.Deploy)
	if len(entries) == 0 {
		return spec.SubstrateStatusReply{}, nil
	}
	treeRoot, rootErr := kubernetesTreeRoot()

	rows := make([]spec.DeploymentStatus, 0, len(entries))
	for _, name := range entries {
		node := rp.Deploy[name]
		row := spec.DeploymentStatus{
			Kind:      spec.SubstrateKubernetes,
			Source:    "tree",
			Image:     kubernetesImageRef(name, node),
			Container: name,
			RunMode:   req.RunMode,
		}

		treePresent := false
		if rootErr == nil {
			if _, statErr := os.Stat(filepath.Join(treeRoot, name)); statErr == nil {
				treePresent = true
			}
		}
		if treePresent {
			row.Status = "tree-present"
		} else {
			row.Status = "not-generated"
		}

		if ks := kubernetesSpecFor(rp.Templates, node); ks != nil && ks.KubeconfigContext != "" {
			row.Network = ks.KubeconfigContext
		} else if node != nil && node.From != "" {
			row.Network = node.From
		}

		rows = append(rows, row)
	}
	return spec.SubstrateStatusReply{Rows: rows}, nil
}

// fetchResolvedProject re-hydrates the resolved-project envelope over the
// established InvokeProvider("build","project") seam (candy/plugin-fleet's
// OpCompile proves this composition in production). Dir is left empty — a
// compiled-in substrate plugin shares the host process's cwd, so the host-side
// "resolved-project" handler's own os.Getwd() already resolves the right
// project without this plugin naming a directory.
// When ctx carries a fan-out memo (status_project_memo.go) the resolution is shared with every
// other collector in the same `charly status`; without one it resolves directly.
func fetchResolvedProject(ctx context.Context) (*spec.ResolvedProject, error) {
	if m := memoFromContext(ctx); m != nil {
		m.once.Do(func() { m.rp, m.err = resolveProjectDirect(ctx) })
		return m.rp, m.err
	}
	return resolveProjectDirect(ctx)
}

// resolveProjectDirect performs the un-memoised resolution — the body fetchResolvedProject used
// to be, kept whole so the memo is a pure addition rather than a rewrite of the seam.
func resolveProjectDirect(ctx context.Context) (*spec.ResolvedProject, error) {
	exec, err := sdk.ExecutorForInvoke(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("reach host reverse channel: %w", err)
	}
	envReq, err := json.Marshal(spec.ResolvedProjectRequest{})
	if err != nil {
		return nil, fmt.Errorf("marshal resolved-project request: %w", err)
	}
	envJSON, err := exec.InvokeProvider(ctx, "build", "project", sdk.OpResolve, envReq, nil, sdk.InvokeProviderOpts{})
	if err != nil {
		return nil, fmt.Errorf("fetch resolved-project envelope: %w", err)
	}
	var rp spec.ResolvedProject
	if err := json.Unmarshal(envJSON, &rp); err != nil {
		return nil, fmt.Errorf("decode resolved-project envelope: %w", err)
	}
	return &rp, nil
}

// kubernetesTreeRoot returns <cwd>/.opencharly/k8s — the canonical root the
// Kustomize generator emits trees under. A separate implementation, not a
// shared call: the kernel/plugin boundary law forbids this plugin from
// importing charly core, so the two share only the convention, not the code.
// The compiled-in plugin shares the host's cwd.
func kubernetesTreeRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".opencharly", "k8s"), nil
}

// kubernetesDeployEntries returns the names of every target:kubernetes deploy in the
// resolved-project's folded Deploy map, in deterministic (sorted) order.
func kubernetesDeployEntries(deploy map[string]*spec.Deploy) []string {
	if len(deploy) == 0 {
		return nil
	}
	var names []string
	for name, node := range deploy {
		if deploykit.ClassifyTarget(node) == "kubernetes" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// kubernetesImageRef resolves the image a kubernetes deploy runs, mirroring the kubernetes deploy
// preresolver: the node's explicit Box (carried on the wire as Image), falling
// back to the deploy name.
func kubernetesImageRef(name string, node *spec.Deploy) string {
	if node != nil && node.Image != "" {
		return node.Image
	}
	return name
}

// kubernetesSpecFor resolves the kind:kubernetes template referenced by node.From against
// the resolved-project's kubernetes template bodies. Nil when unreferenced or
// absent. Uses this SAME provider's own template-resolve leg (resolve.go) —
// an in-package call, never a cross-plugin Invoke.
func kubernetesSpecFor(templates *spec.ProjectTemplates, node *spec.Deploy) *spec.ResolvedKubernetes {
	if templates == nil || node == nil || node.From == "" {
		return nil
	}
	body, ok := templates.Kubernetes[node.From]
	if !ok {
		return nil
	}
	out, err := resolveSubstrateTemplate(spec.SubstrateTemplateResolveRequest{Kubernetes: &spec.KubernetesResolveInput{Kubernetes: body}})
	if err != nil {
		return nil
	}
	var reply spec.KubernetesResolveReply
	if err := json.Unmarshal(out, &reply); err != nil {
		return nil
	}
	return reply.Resolved
}
