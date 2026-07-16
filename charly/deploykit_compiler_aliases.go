package main

// deploykit_compiler_aliases.go — bindings onto the deploy-plan compiler moved to
// sdk/deploykit in P4 (install_build.go: the IR helpers). The BuildDeployPlan compile
// LOOP moved out of core into candy/plugin-bundle (K4-B — the command:bundle plugin's
// OpCompile leg), so the BuildDeployPlan + MergePlan aliases are deleted (R5): the
// compile call sites no longer live in charly/. The surviving aliases cover the
// host-side helpers that STAY core: HostContext (detectHostContext/compileHostContext/
// preresolveBuildersInto), DescribePlan (printPlans), computeDeployID (compileNodePlans).

import "github.com/opencharly/sdk/deploykit"

type HostContext = deploykit.HostContext

var (
	DescribePlan    = deploykit.DescribePlan
	computeDeployID = deploykit.ComputeDeployID
)
