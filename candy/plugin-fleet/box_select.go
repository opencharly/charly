package fleet

import (
	"fmt"

	"github.com/opencharly/sdk/buildkit"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// box_select.go — the K4 unit B box-half: the PRIMARY pod/kubernetes image selection
// (the former host-side compileBoxSelection, since-deleted from charly/ core) now runs plugin-side,
// mirroring candy_select.go's candy-half exactly. The BOX-VIEW shape
// (compileCandyOnBoxSelection, add_candy on an ALREADY-RESOLVED base image) is UNCHANGED and out
// of scope — its base image comes from a separate host-side ResolveBox call in
// compileNodePlans, so there is no envelope-only path to it yet (see
// #DeployCompileRequest's doc comment, sdk/schema/seam.cue).

// resolveBoxSelection computes the resolved *buildkit.ResolvedBox and topo-sorted candy order
// for a primary pod/kubernetes image deploy (req.BoxRef) from the already-fetched envelope: rp.Boxes[box_ref]
// is the SAME spec.ResolvedBoxView candy/plugin-build's `build:project` word (resolveProjectEnvelope,
// #55 step3 unit 3b) already computed to build the envelope in the first place (no re-derivation —
// R3), re-hydrated via deploykit.NewSpecResolvedBox
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

// resolveAddCandyOnBoxSelection is the ADD-CANDY-ON-BOX shape (K4 box-half completion): the
// add_candy overlay (req.CandyRef) compiled against the primary pod/kubernetes base image
// (req.BaseBoxRef). The base image comes from rp.Boxes[base_box_ref] as the COMPILE CONTEXT (the
// SAME ResolvedBoxView the BOX-REF shape reads via NewSpecResolvedBox, R3 — never re-derived), and
// the overlay's OWN topo order is resolved from the envelope over {BareRef(candy_ref)} widened by
// extra_candy_refs (mirrors the CANDY shape's resolveCandySelection exactly). Replaces the former
// host-side buildkit.ResolveBox(baseImg) + scanCandiesForRef path (the former host-side
// compileCandyOnBoxSelection): the base-box read reuses the primary BOX-REF shape's already-proven
// envelope parity, and the overlay-order read reuses the CANDY shape's — so this is a COMPOSITION of
// two already-parity-proven resolutions, not a new resolver.
func resolveAddCandyOnBoxSelection(rp *spec.ResolvedProject, req spec.DeployCompileRequest) ([]string, *buildkit.ResolvedBox, error) {
	view, ok := rp.Boxes[req.BaseBoxRef]
	if !ok {
		return nil, nil, fmt.Errorf("base box %q not in resolved-project envelope", req.BaseBoxRef)
	}
	img := deploykit.NewSpecResolvedBox(view, rp.Distro, rp.Builder)

	candyKey := deploykit.BareRef(req.CandyRef)
	candyModels := envelopeCandyModels(rp)
	if _, ok := candyModels[candyKey]; !ok {
		return nil, nil, fmt.Errorf("add_candy %q not in resolved-project envelope", req.CandyRef)
	}
	order, err := deploykit.ResolveCandyOrder([]string{candyKey}, candyModels, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving deps for add_candy %s: %w", req.CandyRef, err)
	}
	return order, img, nil
}
