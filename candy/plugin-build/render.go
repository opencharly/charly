package build

import (
	"context"
	"fmt"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// render.go — the plugin-build RENDER DRIVE (#67 render-DRIVE move). plugin-build builds a
// deploykit.Generator from the resolved-project envelope (produced plugin-side by resolveBuildEngine,
// K3 U6) via the SHARED deploykit.NewRenderGeneratorFromProject helper (R3/DRY — the ONE construction
// source candy/plugin-build and candy/plugin-deploy-pod both call), then runs dg.Generate(order)
// to render Containerfiles. The render reads RESOLVED data (caches on ResolvedBox + CandyModel)
// WITHOUT the live *Candy/*Config graph — the plugin-side resolve filled the caches (render-prep) +
// projected the envelope. The Generator's seam fields are wired inside the shared helper; as of
// K-wave 2 cone R1 every one of them is InvokeProvider PEER-DISPATCH or a direct in-package call —
// the render makes NO HostBuild callback at all (the render-seam kind is deleted). Dispatch stays
// placement-invisible: compiled-in goes in-proc, out-of-process goes over gRPC.

// renderContainerfiles builds the deploykit.Generator from the envelope (via the shared helper)
// + runs Generate, returning the rendered Containerfile content per box name. Called by
// runBoxGenerate (generate-only) and runBoxBuild (build).
func renderContainerfiles(ctx context.Context, ex *sdk.Executor, reply spec.BuildResolveReply, dir string, devLocalPkg bool) (map[string]string, error) {
	dg, err := deploykit.NewRenderGeneratorFromProject(ctx, ex, reply.ResolvedProject, dir, devLocalPkg)
	if err != nil {
		return nil, err
	}

	// Determine the render order: filtered (reply.Order) or full (flattened levels).
	var order []string
	if len(reply.Order) > 0 {
		order = reply.Order
	} else {
		for _, level := range reply.Levels {
			order = append(order, level...)
		}
	}

	if err := dg.Generate(order); err != nil {
		return nil, fmt.Errorf("rendering Containerfiles: %w", err)
	}

	return dg.Containerfiles, nil
}
