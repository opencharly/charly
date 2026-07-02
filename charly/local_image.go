package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// LocalImageInfo describes an image present in the engine's local storage.
// Populated by ListLocalImages from `{podman,docker} images --format json`.
type LocalImageInfo struct {
	ID     string            // image ID (sha256:...) — used by `charly clean` to skip in-use images
	Names  []string          // Full refs: ["ghcr.io/opencharly/jupyter:latest", ...]
	Labels map[string]string // OCI labels from the image config
}

// ListLocalImages returns all images in the engine's local storage.
// Package-level var for testability (same pattern as LocalImageExists, DetectGPU).
var ListLocalImages = defaultListLocalImages

func defaultListLocalImages(engine string) ([]LocalImageInfo, error) {
	binary := EngineBinary(engine)
	cmd := exec.Command(binary, "images", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing local images via %s: %w", binary, err)
	}
	return parseLocalImagesJSON(out)
}

// parseLocalImagesJSON parses `{podman,docker} images --format json` output
// into ONE LocalImageInfo per distinct image ID, with that id's tag refs
// merged into Names.
//
// This dedup is load-bearing: podman emits ONE ROW PER TAG, and each row's
// Names array already lists EVERY tag on that id. A naive row-by-row mapping
// therefore produces N near-identical entries for an id with N tags — which
// made `charly clean`'s keep-N retention over-count entries and, worse, remove an
// id's whole Names array per "extra" entry (deleting tags it meant to keep).
// Collapsing to one-id-with-a-tag-list matches the struct's shape and what
// every consumer (retention prune, short-name resolver) expects.
//
// Empty-id rows (dangling/untagged) are kept separate via a per-row sentinel
// key so they never merge into one another.
func parseLocalImagesJSON(out []byte) ([]LocalImageInfo, error) {
	var rawImages []map[string]any
	if err := json.Unmarshal(out, &rawImages); err != nil {
		return nil, fmt.Errorf("parsing images output: %w", err)
	}
	byKey := make(map[string]*LocalImageInfo)
	order := make([]string, 0, len(rawImages))
	for i, raw := range rawImages {
		// Image ID: podman uses "Id", docker uses "ID".
		id := ""
		if s, ok := raw["Id"].(string); ok {
			id = s
		} else if s, ok := raw["ID"].(string); ok {
			id = s
		}
		key := id
		if key == "" {
			key = fmt.Sprintf("\x00row%d", i) // never merge distinct untagged images
		}
		info, ok := byKey[key]
		if !ok {
			info = &LocalImageInfo{ID: id, Labels: make(map[string]string)}
			byKey[key] = info
			order = append(order, key)
		}
		// Tag refs: podman uses "Names", docker uses "RepoTags". Merge + dedup.
		var refs []string
		if names, ok := raw["Names"].([]any); ok {
			for _, n := range names {
				if s, ok := n.(string); ok {
					refs = append(refs, s)
				}
			}
		}
		if len(refs) == 0 {
			if tags, ok := raw["RepoTags"].([]any); ok {
				for _, t := range tags {
					if s, ok := t.(string); ok {
						refs = append(refs, s)
					}
				}
			}
		}
		seen := make(map[string]bool, len(info.Names))
		for _, n := range info.Names {
			seen[n] = true
		}
		for _, n := range refs {
			if !seen[n] {
				info.Names = append(info.Names, n)
				seen[n] = true
			}
		}
		// Labels are identical across rows for one id; first-writer wins.
		if labels, ok := raw["Labels"].(map[string]any); ok {
			for k, v := range labels {
				if s, ok := v.(string); ok {
					if _, exists := info.Labels[k]; !exists {
						info.Labels[k] = s
					}
				}
			}
		}
	}
	result := make([]LocalImageInfo, 0, len(order))
	for _, key := range order {
		result = append(result, *byKey[key])
	}
	return result, nil
}

// resolveLocalImageRef resolves a user-supplied image reference against the
// engine's local storage — never reads charly.yml. Used by test-mode commands
// (charly check live, charly check box) so they stay within the test-mode input set.
//
// For full refs (registry prefix present) it validates the image exists
// locally and passes through unchanged. For short names it resolves via
// CalVer: collect every local image matching the short name (either by
// `ai.opencharly.image=<short>` label or by the tag-suffix short-name
// match) and pick the one whose tag has the highest CalVer (or the
// highest `ai.opencharly.version` label). charly is CalVer-only — no
// `:latest` fallback. See `/charly-build:build` "CalVer-only" for the contract.
//
// Returns `ErrImageNotLocal` when nothing matches. An ambiguous result
// across multiple repos with the same highest CalVer tag surfaces as an
// explicit error asking for a full ref.
func resolveLocalImageRef(engine, input string) (string, error) {
	if looksLikeFullRef(input) {
		if !LocalImageExists(engine, input) {
			return "", fmt.Errorf("%w: %s", ErrImageNotLocal, input)
		}
		return input, nil
	}

	images, err := ListLocalImages(engine)
	if err != nil {
		return "", err
	}

	var labelCands, nameCands []resolverCandidate
	for _, img := range images {
		labelCalVer := img.Labels[LabelVersion] // content-derived EffectiveVersion (primary key)
		// Label-preferred: ai.opencharly.image equals the short name.
		if img.Labels[LabelBox] == input && input != "" {
			for _, n := range img.Names {
				// label-CalVer is the PRIMARY ordering key; tag-CalVer (the
				// per-build timestamp) is the TIEBREAKER that picks the newest
				// BUILD among images sharing one content-stable label. No
				// label↔tag substitution — they are independent keys.
				labelCands = append(labelCands, resolverCandidate{
					ref:         n,
					labelCalVer: labelCalVer,
					tagCalVer:   extractCalVerTag(n),
				})
			}
			continue
		}
		// Name-fallback: any of the image's tags has the short name as
		// its trailing repo component. This catches `<deploy-name>:<calver>`
		// aliases (tagDeployAlias) on overlay images that inherited
		// the base image's label.
		for _, name := range img.Names {
			if shortNameMatchesRef(name, input) {
				nameCands = append(nameCands, resolverCandidate{
					ref:         name,
					labelCalVer: labelCalVer,
					tagCalVer:   extractCalVerTag(name),
				})
			}
		}
	}

	cands := labelCands
	if len(cands) == 0 {
		cands = nameCands
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("%w: %s", ErrImageNotLocal, input)
	}

	// Sort newest-first. The label-CalVer (the content-derived
	// ai.opencharly.version) is the PRIMARY key — it ALWAYS takes priority
	// over the tag-CalVer. The tag-CalVer (the per-build YYYY.DDD.HHMM
	// timestamp) is the TIEBREAKER: a content-stable label means many builds
	// share one label-CalVer, so the tag is what selects the newest BUILD.
	// YYYY.DDD.HHMM is NOT lexically sortable (DDD 1-366, HHMM 0-2359, both
	// variable-width) — compareCalVer parses each component numerically; an
	// empty CalVer sorts last (compareCalVerKey).
	// Final tiebreak: prefer the ref whose repo trailing segment exactly
	// matches `input` (the base over a per-deploy alias). Without it a
	// `<registry>/<base>/<instance>:<cv>` alias sorts BEFORE the base
	// `<registry>/<base>:<cv>` (ASCII `/` < `:`), silently picking the
	// instance alias. Pattern A deploys create these via `tagDeployAlias`
	// (deploy_target_pod.go), inheriting the base's
	// `ai.opencharly.image` label, so both land in `labelCands` with
	// identical label+tag CalVers.
	matchesShortName := func(ref, name string) bool {
		// Strip tag/digest, take repo's trailing segment, compare.
		repo := ref
		if i := strings.IndexAny(ref, ":@"); i >= 0 {
			repo = ref[:i]
		}
		if i := strings.LastIndex(repo, "/"); i >= 0 {
			repo = repo[i+1:]
		}
		return repo == name
	}
	sort.SliceStable(cands, func(i, j int) bool {
		// Primary: label-CalVer descending (label > tag, always).
		if c := compareCalVerKey(cands[i].labelCalVer, cands[j].labelCalVer); c != 0 {
			return c > 0
		}
		// Tiebreaker: tag-CalVer descending (newest build).
		if c := compareCalVerKey(cands[i].tagCalVer, cands[j].tagCalVer); c != 0 {
			return c > 0
		}
		iMatch := matchesShortName(cands[i].ref, input)
		jMatch := matchesShortName(cands[j].ref, input)
		if iMatch != jMatch {
			return iMatch
		}
		return cands[i].ref < cands[j].ref
	})

	// If the top candidate has NEITHER a label-CalVer NOR a tag-CalVer AND
	// there are multiple distinct repositories among the candidates, that's a
	// genuine cross-repo ambiguity (e.g. two third-party `:latest` tags).
	// Surface the full list so the user can disambiguate with a full ref.
	if cands[0].labelCalVer == "" && cands[0].tagCalVer == "" && !sameRepoAcross(cands) {
		refs := make([]string, len(cands))
		for i, c := range cands {
			refs[i] = c.ref
		}
		return "", fmt.Errorf("ambiguous short name %q in local storage; candidates: %s. Re-run with a full ref",
			input, strings.Join(refs, ", "))
	}

	return cands[0].ref, nil
}

// resolverCandidate pairs a full image ref with its two CalVer keys: the
// labelCalVer (ai.opencharly.version — the content-derived EffectiveVersion,
// the PRIMARY ordering key) and the tagCalVer (the `:<calver>` build-timestamp
// tag, the TIEBREAKER). Used internally by resolveLocalImageRef to sort
// candidates newest-first before picking one.
type resolverCandidate struct {
	ref         string
	labelCalVer string
	tagCalVer   string
}

// compareCalVerKey orders two CalVer strings with "" sorting LAST (lowest
// rank): returns >0 when a ranks higher (newer) than b, <0 when lower, 0 when
// equal. A non-empty CalVer always outranks an empty one.
func compareCalVerKey(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return -1
	}
	if b == "" {
		return 1
	}
	return compareCalVer(a, b)
}

// sameRepoAcross reports whether every candidate ref shares the same
// repository path (everything before the final `:<tag>`). Used to
// distinguish benign duplicate-tag cases (one image, multiple tags)
// from genuinely ambiguous matches (same short name across multiple
// unrelated repos).
func sameRepoAcross(cands []resolverCandidate) bool {
	if len(cands) <= 1 {
		return true
	}
	repoOf := func(ref string) string {
		if lastSlash := strings.LastIndex(ref, "/"); lastSlash >= 0 {
			if colon := strings.LastIndex(ref, ":"); colon > lastSlash {
				return ref[:colon]
			}
		} else if colon := strings.LastIndex(ref, ":"); colon >= 0 {
			return ref[:colon]
		}
		return ref
	}
	first := repoOf(cands[0].ref)
	for _, c := range cands[1:] {
		if repoOf(c.ref) != first {
			return false
		}
	}
	return true
}

// compareCalVer compares two CalVer strings numerically component-by-component.
// Returns >0 if a > b, <0 if a < b, 0 if equal. Handles the variable-width
// HHMM and DDD components (e.g. "9" vs "10" — lexical compare gives the
// wrong answer because "10" < "9" as strings but 10 > 9 numerically).
// Non-numeric components fall through to lexical compare as a defensive
// fallback, but extractCalVerTag only returns valid numeric CalVers.
func compareCalVer(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	n := len(aParts)
	if len(bParts) < n {
		n = len(bParts)
	}
	for i := 0; i < n; i++ {
		ai, aErr := strconv.Atoi(aParts[i])
		bi, bErr := strconv.Atoi(bParts[i])
		if aErr != nil || bErr != nil {
			// Fall back to lexical for this component.
			if aParts[i] < bParts[i] {
				return -1
			}
			if aParts[i] > bParts[i] {
				return 1
			}
			continue
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	if len(aParts) < len(bParts) {
		return -1
	}
	if len(aParts) > len(bParts) {
		return 1
	}
	return 0
}

// extractCalVerTag returns the CalVer portion of a ref's tag, or ""
// if the tag is not a recognisable CalVer (`YYYY.DDD.HHMM`). Lets the
// resolver distinguish CalVer tags from legacy floats like `:latest`
// (which should never be chosen as the newest candidate).
func extractCalVerTag(ref string) string {
	// Find the tag portion: last ':' after the last '/'.
	tagStart := -1
	if lastSlash := strings.LastIndex(ref, "/"); lastSlash >= 0 {
		if colon := strings.LastIndex(ref, ":"); colon > lastSlash {
			tagStart = colon + 1
		}
	} else if colon := strings.LastIndex(ref, ":"); colon >= 0 {
		tagStart = colon + 1
	}
	if tagStart < 0 || tagStart >= len(ref) {
		return ""
	}
	tag := ref[tagStart:]
	// CalVer shape: three dot-separated decimal parts. Legacy
	// `:latest` / `:stable` / `:dev` floats fall through.
	parts := strings.Split(tag, ".")
	if len(parts) != 3 {
		return ""
	}
	for _, p := range parts {
		if p == "" {
			return ""
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return ""
			}
		}
	}
	return tag
}

// ResolveNewestLocalCalVer is the canonical "find the newest local
// image for this short name" helper. Thin wrapper around
// resolveLocalImageRef — exposed so callers that start with an
// explicit short-name + empty-tag can resolve uniformly.
func ResolveNewestLocalCalVer(engine, short string) (string, error) {
	return resolveLocalImageRef(engine, short)
}

// shortNameMatchesRef reports whether a short name like "jupyter" matches a
// full ref like "ghcr.io/opencharly/jupyter:latest" by comparing the trailing
// repo component (after the last "/", before the tag).
func shortNameMatchesRef(fullRef, short string) bool {
	// Strip tag: find the last ":" that comes after the last "/".
	repo := fullRef
	if lastSlash := strings.LastIndex(repo, "/"); lastSlash >= 0 {
		if colon := strings.LastIndex(repo, ":"); colon > lastSlash {
			repo = repo[:colon]
		}
		return repo[lastSlash+1:] == short
	}
	// No slash — compare the whole thing minus any tag.
	if colon := strings.LastIndex(repo, ":"); colon >= 0 {
		repo = repo[:colon]
	}
	return repo == short
}
