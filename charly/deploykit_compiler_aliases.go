package main

// deploykit_compiler_aliases.go — bindings onto the deploy-plan compiler moved to
// sdk/deploykit in P4 (install_build.go: BuildDeployPlan + the IR helpers). charly
// callers (bundle_add_cmd, etc.) reach it through these.

import "github.com/opencharly/sdk/deploykit"

type HostContext = deploykit.HostContext

var (
	BuildDeployPlan = deploykit.BuildDeployPlan
	MergePlan       = deploykit.MergePlan
	DescribePlan    = deploykit.DescribePlan
	computeDeployID = deploykit.ComputeDeployID
)
