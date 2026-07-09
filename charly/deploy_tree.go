package main

// deploy_tree.go — the recursive tree walker for schema v2 deployments.
//
// Every deployment is a BundleNode that may carry `children:`.
// This file owns the walk-and-dispatch logic that turns the tree into
// a sequence of per-target Emit() calls with the correct ParentExec
// threaded through.
//
// Apply order is pre-order (parents first): the parent's environment
// must exist before its children can run inside it. Delete order is
// post-order (children first): children tear down while the parent
// venue is still alive to accept teardown commands.

import (
	"os"
	"path/filepath"

	"github.com/opencharly/sdk/kit"
)

func stampBundleDescents(uf *UnifiedFile) {
	if uf == nil {
		return
	}
	for name, node := range uf.Bundle {
		n := node
		kit.StampDescent(&n)
		uf.Bundle[name] = n
	}
}

// NestedContainerName computes the podman container name used when
// a container node is nested under a dotted path. Path segments are
// joined with underscores so the result is a legal podman name.
// Called by the walker when it knows the full dotted path.

// resolveTreeRoot returns the DeploymentsSection's Images map from
// the merged UnifiedFile + local overlay, ready for dotted-path
// traversal. Handles the project charly.yml + local overlay merge
// the same way BundleAddCmd.Run does today.
func resolveTreeRoot(dir string) (map[string]BundleNode, error) {
	var projectDC *BundleConfig
	if uf, ok, err := LoadUnified(dir); err != nil {
		return nil, err
	} else if ok && uf != nil {
		projectDC = uf.ProjectBundleConfig()
	}
	localDC, _ := LoadBundleConfig()
	merged := MergeDeployConfigs(projectDC, localDC)
	if merged == nil || merged.Bundle == nil {
		return nil, nil
	}
	return merged.Bundle, nil
}

// Suppressor for imports only used in doc comments / future
// expansion. Keeps `go vet` quiet and documents the intent.
var _ = filepath.Join
var _ = os.Getenv
