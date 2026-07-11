package main

// deploykit_tree_aliases.go — package-main bindings onto the deploy-tree walk +
// executor chain-builder (deploy_tree.go + deploy_chain.go), moved to sdk/deploykit
// in P4. These are the "tree walk + executor pick" orchestration; the loader glue
// (resolveTreeRoot / stampBundleDescents, which call LoadUnified) STAYS in charly's
// deploy_tree.go and passes the resolved root into these walk functions.

import "github.com/opencharly/sdk/deploykit"

type (
	DeployTreePhase   = deploykit.DeployTreePhase
	DeployTreeVisitor = deploykit.DeployTreeVisitor
)

const (
	DeployTreePhaseAdd = deploykit.DeployTreePhaseAdd
	DeployTreePhaseDel = deploykit.DeployTreePhaseDel
)

var (
	WalkDeploymentTree        = deploykit.WalkDeploymentTree
	vmChildExecutor           = deploykit.VmChildExecutor
	classifyTarget            = deploykit.ClassifyTarget
	sshParamsForVm            = deploykit.SSHParamsForVm
	sortedNestedKeys          = deploykit.SortedNestedKeys
	rootExecutorForDeployNode = deploykit.RootExecutorForDeployNode
	ContainerChain            = deploykit.ContainerChain
	ImageChain                = deploykit.ImageChain
	ResolveDeployChain        = deploykit.ResolveDeployChain
)
