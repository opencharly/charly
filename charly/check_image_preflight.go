package main

// check_image_preflight.go — score-driven image preflight for
// `charly check run` host-target dispatches.
//
// The deploy applies host packages + configs only (CLAUDE.md "Deploy
// fetches NOTHING speculative"); container images that plan steps
// spawn need to be present BEFORE the runner walks them. This file
// owns the score → image-set discovery only — the actual ensure
// logic lives in `charly/ensure_image.go::EnsureImagePresent`, the
// canonical helper used by every command (R3).

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// ensureScoreImages collects every image identifier the iterate plan's
// scored steps may spawn (per-step Op.Venue values, loader-derived from tree
// position), deduplicates, and ensures each is present via the canonical
// EnsureImagePresent helper. Idempotent. A dotted venue (a nested child) names
// no directly-pullable image and is skipped; an agent-provisioned venue is an
// image the AGENT builds during the run, so it is not preflighted here either.
//
// Failures abort the check BEFORE any step runs.
func ensureScoreImages(ctx context.Context, plan []spec.Step, uf *UnifiedFile, projectDir string) error {
	if uf == nil {
		return nil
	}
	cfg := uf.ProjectConfig()

	want := map[string]struct{}{}
	for _, s := range plan {
		// Skip empty + dotted venues (nested children resolve through the
		// agent-deployed tree, not a pullable image).
		if s.Venue == "" || strings.Contains(s.Venue, ".") {
			continue
		}
		// Skip agent-provisioned venues — the AI builds those images in-run.
		if venueIsAgentProvisioned(uf, s.Venue) {
			continue
		}
		want[s.Venue] = struct{}{}
	}
	if len(want) == 0 {
		return nil
	}

	// Stable order so the preflight banner is deterministic.
	refs := make([]string, 0, len(want))
	for ref := range want {
		refs = append(refs, ref)
	}
	sort.Strings(refs)

	fmt.Fprintf(os.Stderr, "preflight: ensuring %d image(s) present in podman storage\n", len(refs))
	for _, ref := range refs {
		if err := EnsureImagePresent(ctx, ref, cfg, projectDir); err != nil {
			return fmt.Errorf("preflight: %w", err)
		}
	}
	return nil
}
