package bundle

import (
	"fmt"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/spec"
)

// box_select.go — the K4 unit B box-half: the PRIMARY pod/k8s image selection
// (charly/bundle_compile_seam.go's former compileBoxSelection) now runs entirely plugin-side,
// mirroring candy_select.go's candy-half exactly. The BOX-VIEW shape
// (compileCandyOnBoxSelection, add_candy on an ALREADY-RESOLVED base image) is UNCHANGED and out
// of scope — its base image comes from a separate host-side ResolveBox call in
// compileNodePlans, so there is no envelope-only path to it yet (see
// #DeployCompileRequest's doc comment, sdk/schema/seam.cue).

// resolveBoxSelection computes the resolved *buildkit.ResolvedBox and topo-sorted candy order
// for a primary pod/k8s image deploy (req.BoxRef) from the already-fetched envelope: rp.Boxes[box_ref]
// is the SAME spec.ResolvedBoxView hostBuildResolvedProject already computed to build the
// envelope in the first place (no re-derivation — R3), re-hydrated via deploykit.NewSpecResolvedBox
// exactly like the BOX-VIEW shape already does host-side. The candy order comes from img.Candy
// over the SAME envelopeCandyModels the CANDY shape uses.
func resolveBoxSelection(rp *spec.ResolvedProject, req spec.DeployCompileRequest) (*buildkit.ResolvedBox, []string, error) {
	view, ok := rp.Boxes[req.BoxRef]
	if !ok {
		return nil, nil, fmt.Errorf("box %q not in resolved-project envelope", req.BoxRef)
	}
	img := deploykit.NewSpecResolvedBox(view, rp.Distro, rp.Builder)

	candyModels := envelopeCandyModels(rp)
	order, err := deploykit.ResolveCandyOrder(img.Candy, candyModels, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving deps for box %s: %w", req.BoxRef, err)
	}
	// Mirrors the OLD host compileBoxSelection's compileOrder filter (keep only candies actually
	// present in the map) — a no-op today (envelopeCandyModels is nil-free by construction, so
	// deploykit.ResolveCandyOrder either resolves every order entry or errors outright), kept as
	// an explicit defensive filter so a FUTURE envelope change that reintroduces a present-but-nil
	// entry fails the SAME way (drop, not silently compile a broken candy) rather than needing to
	// be rediscovered.
	compileOrder := make([]string, 0, len(order))
	for _, name := range order {
		if _, ok := candyModels[name]; ok {
			compileOrder = append(compileOrder, name)
		}
	}
	return img, compileOrder, nil
}
