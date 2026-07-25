package check

import (
	"sort"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// preflight_images.go — the PURE half of the host-target image preflight (moved from
// charly/check_image_preflight.go's ensureScoreImages, CHECK-cone move). Discovers every
// distinct, directly-pullable image identifier the iterate plan's scored steps may spawn
// (per-step Op.Venue values, loader-derived from tree position), deduplicated and sorted
// for a deterministic preflight banner. A dotted venue (a nested child) names no directly
// pullable image; an agent-provisioned venue is an image the AI builds in-run — NEITHER is
// filterable here (both need the loaded project tree, which this plugin does not hold), so
// the host's "ensure-images" seam re-checks agent-provisioned status per candidate before
// calling dispatchBuildEnsure (R4: never silently over-trust a plugin-computed candidate).

// preflightImageCandidates walks plan's step venues and returns the distinct, sorted set of
// non-dotted venue names — the candidate images the host-side "ensure-images" seam should
// consider (after its own agent-provisioned filter) before a host-target harness run.
func preflightImageCandidates(plan []spec.Step) []string {
	want := map[string]struct{}{}
	for _, s := range plan {
		if s.Venue == "" || strings.Contains(s.Venue, ".") {
			continue
		}
		want[s.Venue] = struct{}{}
	}
	if len(want) == 0 {
		return nil
	}
	refs := make([]string, 0, len(want))
	for ref := range want {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	return refs
}
