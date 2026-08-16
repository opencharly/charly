package clean

// retention.go — the SHARED retention ENGINE (image-tag / build-candy / check-run pruning +
// the --deep store-wide dangling-image purge + the charly-labeled image-tag inventory).
// Relocated from charly/retention.go (K1-alpha core-minimization): this candy is the ONE
// owner now. `charly clean`'s own CLI (command.go) calls runRetention directly — no wire
// hop, same package. The remaining callers — candy/plugin-box's post-build prune
// (pruneAfterBuild) and `box list tags` (listImageTags), and the check harness's post-run
// prune — are all PEER PLUGINS reaching it via verb:retention over InvokeProvider, the same
// peer-dispatch pattern verb:credential/verb:gpu/verb:tunnel use; NO core adapter remains
// (charly/retention_plugin.go, the former core-side caller, is DELETED — #118).
//
// Retention fallback: when defaults.keep_images / keep_check_runs are absent from config,
// the caller resolves 0 ("disabled") so third-party configs get no surprise pruning. The
// repo's charly.yml opts in (keep_images: 3, keep_check_runs: 3). See /charly-core:clean.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/spec/spec"
)

// runRetention dispatches one verb:retention request to the requested category(ies) and
// returns the reply. Mirrors the former charly/host_build_retention.go dispatch, plus the
// new `list` (charly box list tags) and `build_prune` (the narrow post-build-only scope)
// actions this relocation adds. req.KeepImages/req.KeepCheckRuns arrive PRE-RESOLVED (the
// caller's defaults.keep_images/keep_check_runs, 0 = disabled) — this engine never reads
// charly.yml itself, it only ever runs the podman/filesystem side of retention (R3: config
// resolution stays with whoever can load the project — core call sites resolve in-process,
// the plugin's own CLI + plugin-check's post-run hook + plugin-box's post-build prune resolve
// it PLUGIN-SIDE via the shared sdk/loaderkit.ResolveRetentionDefaultsViaExecutor, K-wave 2
// cone R6, the former "retention-defaults" HostBuild seam DELETED).
func runRetention(req spec.RetentionRequest) spec.RetentionReply {
	engineBin, err := resolveEngineBinary()
	if err != nil {
		return spec.RetentionReply{Error: err.Error()}
	}

	// --invalidate: targeted image-tag invalidation ONLY (matches the CLI's early return).
	if req.Invalidate != "" {
		refs, ierr := invalidateImageTags(engineBin, req.Invalidate, req.DryRun)
		if ierr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("invalidating image tags: %v", ierr)}
		}
		return spec.RetentionReply{ImageRefs: refs}
	}

	// list: the read-only tag inventory (`charly box list tags`) — nothing removed.
	if req.List {
		groups, lerr := charlyImageTags(engineBin)
		if lerr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("listing image tags: %v", lerr)}
		}
		return spec.RetentionReply{TagGroups: flattenTagGroups(groups)}
	}

	keepImages, keepCheck := req.KeepImages, req.KeepCheckRuns
	if req.Keep > 0 {
		keepImages, keepCheck = req.Keep, req.Keep
	}

	// build_prune: the narrow post-`charly box build` scope — tag retention + stale
	// .build/_candy staging dirs ONLY, matching the historic pruneAfterBuild behavior
	// exactly (never the fuller dangling-image/staging sweep the `images` category runs).
	if req.BuildPrune {
		reply := spec.RetentionReply{KeepImages: keepImages}
		refs, perr := pruneImagesByRetention(engineBin, keepImages, req.DryRun)
		if perr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("pruning images: %v", perr)}
		}
		reply.ImageRefs = refs
		reply.BuildDirs = pruneBuildCandyDirs(filepath.Join(req.Dir, ".build"), keepImages, req.DryRun)
		return reply
	}

	reply := spec.RetentionReply{KeepImages: keepImages, KeepCheckRuns: keepCheck}

	if req.Images {
		refs, perr := pruneImagesByRetention(engineBin, keepImages, req.DryRun)
		if perr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("pruning images: %v", perr)}
		}
		reply.ImageRefs = refs
		dangling, derr := pruneDanglingCharlyImages(engineBin, req.DryRun)
		if derr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("pruning dangling images: %v", derr)}
		}
		reply.DanglingIDs = dangling
		reply.StagingDirs = pruneBuildahStaging(req.DryRun)
		reply.BuildDirs = pruneBuildCandyDirs(filepath.Join(req.Dir, ".build"), keepImages, req.DryRun)
	}
	if req.Check {
		paths, perr := pruneCheckRuns(filepath.Join(req.Dir, ".check"), keepCheck, req.DryRun)
		if perr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("pruning check runs: %v", perr)}
		}
		reply.CheckPaths = paths
	}
	if req.Deep {
		ids, bytes, derr := pruneDeepDanglingImages(engineBin, req.DryRun)
		if derr != nil {
			return spec.RetentionReply{Error: fmt.Sprintf("deep-purging dangling images: %v", derr)}
		}
		reply.DeepIDs = ids
		reply.DeepBytes = bytes
	}
	return reply
}

// resolveEngineBinary resolves the container engine binary via kit.ResolveRuntime — the
// same resolver every other engine-shelling site uses.
func resolveEngineBinary() (string, error) {
	rt, err := kit.ResolveRuntime()
	if err != nil {
		return "", err
	}
	return kit.EngineBinary(rt.BuildEngine), nil
}

// --- image-tag inventory (charlyImageTags + support) -------------------------------------

// listContainerImageRefs returns the set of image IDs and image refs currently
// referenced by ANY container (running or stopped, incl. quadlet-managed
// deploys). Package-level var for testability (same pattern as kit.ListLocalImages).
var listContainerImageRefs = defaultContainerImageRefs

func defaultContainerImageRefs(engine string) (ids map[string]bool, refs map[string]bool, err error) {
	ids = map[string]bool{}
	refs = map[string]bool{}
	// Parse JSON, not a Go-template `--format`: podman's `{{.ImageID}}` template
	// panics (slice bounds [:12] length 0) when any container has an empty image
	// ID. The raw JSON field handles that gracefully.
	out, e := exec.Command(kit.EngineBinary(engine), "ps", "-a", "--format", "json").Output()
	if e != nil {
		return ids, refs, fmt.Errorf("listing containers via %s: %w", kit.EngineBinary(engine), e)
	}
	var rows []map[string]any
	if e := json.Unmarshal(out, &rows); e != nil {
		return ids, refs, fmt.Errorf("parsing %s ps output: %w", kit.EngineBinary(engine), e)
	}
	for _, r := range rows {
		if v, ok := r["ImageID"].(string); ok {
			if id := normImageID(v); id != "" {
				ids[id] = true
			}
		}
		if v, ok := r["Image"].(string); ok && v != "" {
			refs[v] = true
		}
	}
	return ids, refs, nil
}

// normImageID strips the "sha256:" prefix so short (12-char) and full (64-char)
// IDs compare by prefix.
func normImageID(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "sha256:") }

// imageInUse reports whether the candidate image is referenced by any container,
// by ID (prefix-tolerant: 12-char vs 64-char) or by any of its tags.
func imageInUse(im kit.LocalImageInfo, ids, refs map[string]bool) bool {
	cid := normImageID(im.ID)
	for id := range ids {
		if cid != "" && id != "" && (strings.HasPrefix(cid, id) || strings.HasPrefix(id, cid)) {
			return true
		}
	}
	for _, n := range im.Names {
		if refs[n] {
			return true
		}
	}
	return false
}

// imageLabelCalVer parses the image's ai.opencharly.version label (the
// content-derived EffectiveVersion) — the PRIMARY retention ordering key.
func imageLabelCalVer(im kit.LocalImageInfo) (kit.CalVer, bool) {
	return kit.ParseCalVer(im.Labels[spec.LabelVersion])
}

// imageTagInfo is one locally stored tag of a charly-labeled image — the shared
// inventory row behind retention pruning, `charly box list tags`, and
// `charly clean --invalidate`.
type imageTagInfo struct {
	Ref         string
	ID          string
	LabelCalVer kit.CalVer
	OkLabel     bool
	TagCalVer   kit.CalVer
	OkTag       bool
	InUse       bool
	// Created is the image's creation time (unix seconds) — the build-recency key, total over
	// every tag charly mints. The CalVer keys above are NOT: `charly box build --tag` REPLACES
	// the CalVer tag, so a bed build carries `check-<bed>-<calver>`, which parses as no CalVer at
	// all. A group of bed-tagged images therefore had OkTag false for EVERY member, the sort's
	// final comparator was false for every pair, and the surviving order was whatever
	// `podman images` happened to emit — so keep_images: N kept an ARBITRARY N and could delete
	// the newest build while keeping older ones. Unlike the resolver's wrong ANSWER, this one
	// destroys an artifact.
	Created int64
}

// charlyImageTags inventories local storage: one row PER TAG (deduped by
// ref), grouped by the ai.opencharly.box label and sorted newest-first
// (label-CalVer primary, CREATION TIME tiebreaker, build-tag CalVer tertiary).
// Non-charly images (no label) never appear.
func charlyImageTags(engine string) (map[string][]imageTagInfo, error) {
	imgs, err := kit.ListLocalImages(engine)
	if err != nil {
		return nil, err
	}
	inUseIDs, inUseRefs, err := listContainerImageRefs(engine)
	if err != nil {
		return nil, err
	}
	groups := map[string][]imageTagInfo{}
	seenRef := map[string]bool{}
	for _, im := range imgs {
		short := im.Labels[spec.LabelBox]
		if short == "" {
			continue
		}
		lcv, okL := imageLabelCalVer(im)
		inUse := imageInUse(im, inUseIDs, inUseRefs)
		for _, ref := range im.Names {
			if seenRef[ref] {
				continue
			}
			seenRef[ref] = true
			tcv, okT := kit.ParseCalVer(kit.ExtractCalVerTag(ref))
			groups[short] = append(groups[short], imageTagInfo{
				Ref: ref, ID: normImageID(im.ID), LabelCalVer: lcv, OkLabel: okL,
				TagCalVer: tcv, OkTag: okT, InUse: inUse, Created: im.Created,
			})
		}
	}
	for _, group := range groups {
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].OkLabel && group[j].OkLabel && group[i].LabelCalVer != group[j].LabelCalVer {
				return group[j].LabelCalVer.Less(group[i].LabelCalVer) // newer label first
			}
			if group[i].OkLabel != group[j].OkLabel {
				return group[i].OkLabel // labelled sorts before unlabelled
			}
			// Creation time before the build tag: it is the only recency key TOTAL over the tags
			// charly mints, so a group of bed-tagged images orders correctly instead of collapsing
			// to "whatever podman emitted" (see imageTagInfo.Created).
			if group[i].Created != group[j].Created && group[i].Created != 0 && group[j].Created != 0 {
				return group[i].Created > group[j].Created // newer build first
			}
			if group[i].OkTag && group[j].OkTag && group[i].TagCalVer != group[j].TagCalVer {
				return group[j].TagCalVer.Less(group[i].TagCalVer) // newer build first
			}
			return group[i].OkTag && !group[j].OkTag // dateable sorts before undateable
		})
	}
	return groups, nil
}

// flattenTagGroups presents charlyImageTags' grouped inventory as the verb:retention
// `list` reply payload: boxes sorted alphabetically, tags within each box in the
// group's existing newest-first order.
func flattenTagGroups(groups map[string][]imageTagInfo) []spec.TagInfo {
	boxes := make([]string, 0, len(groups))
	for b := range groups {
		boxes = append(boxes, b)
	}
	sort.Strings(boxes)
	var out []spec.TagInfo
	for _, b := range boxes {
		for _, t := range groups[b] {
			version := "-"
			if t.OkLabel {
				version = t.LabelCalVer.String()
			}
			out = append(out, spec.TagInfo{Box: b, Ref: t.Ref, Version: version, InUse: t.InUse})
		}
	}
	return out
}

// --- live-build-floor + image-tag retention ------------------------------------------------

// liveBuildFloor is a package-level var for testability — the same seam
// kit.ListLocalImages / listContainerImageRefs use. It reads the HOST-GLOBAL
// build-activity lock dir (~/.cache/charly/locks/builds, shared across every
// worktree on the host, written by charly core's acquireBuildActivityLock), so a
// retention test that does NOT stub it is non-deterministic: a concurrent build in
// ANOTHER process holds a lock, the live-build protection engages, and the test's
// expected pruning silently does not happen. A test stubs this to a fixed
// (no-live-build) result so the retention decision under test is deterministic
// regardless of host build activity.
var liveBuildFloor = defaultLiveBuildFloor

// defaultLiveBuildFloor scans the build-activity locks (kit.BuildActivityDir): a
// lock file whose flock is ACQUIRABLE is stale (its build died) and is reaped;
// a HELD one is a LIVE build whose recorded generate CalVer floors every FROM
// pin it may still resolve. Returns the minimum live CalVer, whether that floor
// is usable, and the live-build count — a live lock with an unreadable CalVer
// forces floorOK=false, so the caller protects everything.
func defaultLiveBuildFloor() (floor kit.CalVer, floorOK bool, live int) {
	dir, err := kit.BuildActivityDir()
	if err != nil {
		return kit.CalVer{}, false, 0
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return kit.CalVer{}, false, 0
	}
	haveFloor := false
	floorOK = true
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if rel, lerr := kit.AcquireFileLock(p, false); lerr == nil {
			_ = rel()
			_ = os.Remove(p) // stale — its build died without releasing
			continue
		}
		live++
		var cv kit.CalVer
		ok := false
		if b, rerr := os.ReadFile(p); rerr == nil {
			cv, ok = kit.ParseCalVer(strings.TrimSpace(string(b)))
		}
		if !ok {
			floorOK = false
			continue
		}
		if !haveFloor || cv.Less(floor) {
			floor, haveFloor = cv, true
		}
	}
	if live == 0 {
		return kit.CalVer{}, false, 0
	}
	if !haveFloor {
		floorOK = false
	}
	return floor, floorOK, live
}

// retentionRemovable is the pure retention decision for one inventoried tag:
// the standing rules (keep every tag of the newest keepN DISTINCT images, never
// remove an undatable tag, never an in-use one) plus the build-activity
// protections. `rank` is the tag's DISTINCT-IMAGE rank within its box group —
// not its index among tags: keep_images budgets IMAGES, and a tag is kept when
// the image it names is inside the budget, however many tags that image wears
// (see imageRanksByDistinctID) — while ANY build
// is live, (a) a tag at or above the oldest live build's generate CalVer may
// still be FROM-resolved and is kept (an unknown floor keeps everything), and
// (b) an image's LAST local tag is never removed (an outright mid-build image
// deletion corrupts buildah's layer store — the layer-not-known/SIGSEGV
// variant the fan-out surfaced).
func retentionRemovable(c imageTagInfo, rank, keepN int, floor kit.CalVer, floorOK bool, live int, lastTag bool) bool {
	if rank < keepN {
		return false // keep every tag of the newest keepN DISTINCT images
	}
	// ORDER IS LOAD-BEARING — do not reorder these guards. The undatable check sits AFTER the
	// rank check, so a tag that falls outside either retention ordinal still reaches this guard
	// and is protected when it qualifies. Hoisting the rank check below this one would change
	// which rows are even considered.
	//
	// The guard is an AND, and that bounds it much more tightly than "non-CalVer tags are safe":
	// a row is undatable only when it has NEITHER a datable ai.opencharly.version label NOR a
	// datable :YYYY.DDD.HHMM tag. A `:latest` on an image that carries a datable label has
	// OkLabel == true, so it does NOT qualify and IS reclaimable by tag ordinal. Since the label
	// is a DECLARED version (deploykit.ComputeEffectiveVersions: the box's version:, else the
	// highest candy version:, else the base's) rather than a content hash, most charly-built
	// images carry one — so this exemption protects far fewer rows than its name suggests, and
	// `latest` on a managed image is not among them. An earlier version of this comment claimed
	// such a tag "can never be elected for removal however many tags its image wears", which is
	// the OR reading of an AND guard; the docs prose generated from candy/charly-core stated the
	// AND correctly while this comment did not.
	if !c.OkLabel && !c.OkTag {
		return false // never remove a tag we can't date by EITHER key
	}
	if c.InUse {
		return false // image referenced by a container/deploy
	}
	if live > 0 {
		if !floorOK {
			return false
		}
		if c.OkTag && !c.TagCalVer.Less(floor) {
			return false
		}
		if lastTag {
			return false
		}
	}
	return true
}

// retentionRanks maps each tag in a newest-first box group onto the TWO ordinals `keep_images: N`
// budgets, because one number alone cannot express what retention has to protect:
//
//   - imageRank — the rank of the DISTINCT IMAGE the tag names. Every tag of the newest image
//     ranks 0, every tag of the next distinct image ranks 1, and so on.
//   - tagOrd — the tag's ordinal WITHIN its own image, newest first.
//
// A tag survives when BOTH are inside the budget, i.e. `keep_images: N` keeps the newest N
// distinct images AND at most N tags of each. Both halves are load-bearing and each one alone
// regresses the other:
//
//   - Ranking by TAG INDEX alone (the form this replaces) let ONE image wearing three tags consume
//     the whole `keep_images: 3` budget, so the 2nd and 3rd DISTINCT images fell outside it and
//     were pruned — measured live on `fedora-nonfree` (id e2efeb1c). Direction matters: that made
//     retention keep FEWER distinct images, so it OVER-pruned within a managed family. It could
//     never cause under-reclaim, and it is not an explanation for any observed disk shortfall.
//   - Ranking by IMAGE alone reclaims no tags at all: a content-stable image rebuilt many times
//     wears every CalVer tag it ever had at imageRank 0, so its tag rows would grow without bound.
//     TestPruneImagesByRetention_SharedID is that invariant, and it caught this exact regression
//     when the first cut of this fix budgeted images only.
//
// An entry with no resolvable ID gets an imageRank of its own rather than joining a neighbour's:
// two unidentifiable rows are not evidence of one image, and merging them would silently widen
// what the budget protects. Such rows are usually also undatable, which retentionRemovable refuses
// to remove on a separate axis.
func retentionRanks(group []imageTagInfo) (imageRank, tagOrd []int) {
	imageRank = make([]int, len(group))
	tagOrd = make([]int, len(group))
	byID := map[string]int{}
	seenTags := map[string]int{}
	next := 0
	for i, c := range group {
		if c.ID == "" {
			imageRank[i], tagOrd[i] = next, 0
			next++
			continue
		}
		r, seen := byID[c.ID]
		if !seen {
			r = next
			byID[c.ID] = r
			next++
		}
		imageRank[i] = r
		tagOrd[i] = seenTags[c.ID]
		seenTags[c.ID]++
	}
	return imageRank, tagOrd
}

func pruneImagesByRetention(engine string, keepN int, dryRun bool) ([]string, error) {
	if keepN <= 0 {
		return nil, nil
	}
	groups, err := charlyImageTags(engine)
	if err != nil {
		return nil, err
	}
	floor, floorOK, live := liveBuildFloor()
	tagCount := map[string]int{}
	for _, group := range groups {
		for _, c := range group {
			if c.ID != "" {
				tagCount[c.ID]++
			}
		}
	}
	var removed []string
	for _, group := range groups {
		imageRank, tagOrd := retentionRanks(group)
		for idx, c := range group {
			lastTag := c.ID != "" && tagCount[c.ID] <= 1
			// BOTH ordinals must be inside the budget for a tag to survive — this is an AND, not
			// an OR. A tag whose IMAGE is inside the budget but which is itself a surplus tag of
			// that image (tagOrd >= keepN) is re-ranked to its tag ordinal and becomes removable;
			// that is the half that keeps a content-stable image's tag rows bounded. Reading this
			// as "either axis keeps it" invites deleting the reassignment below, which would
			// restore the unbounded tag growth TestPruneImagesByRetention_SharedID guards.
			// Verified by probe, not by reading: 1 image / 5 tags / keepN=3 removes 2.
			rank := imageRank[idx]
			if rank < keepN && tagOrd[idx] >= keepN {
				rank = tagOrd[idx] // surplus tag of a kept image — budget it as a tag
			}
			if !retentionRemovable(c, rank, keepN, floor, floorOK, live, lastTag) {
				continue
			}
			if dryRun {
				if c.ID != "" {
					tagCount[c.ID]--
				}
				removed = append(removed, c.Ref)
				continue
			}
			// rmi WITHOUT -f untags this ref while other tags of a shared id
			// survive; it also refuses an image still held by a build /
			// "external" container our InUse pre-check can't see — the
			// safety backstop. Silent skip — in-use retention is expected.
			if err := exec.Command(kit.EngineBinary(engine), "rmi", c.Ref).Run(); err != nil {
				continue
			}
			if c.ID != "" {
				tagCount[c.ID]--
			}
			removed = append(removed, c.Ref)
		}
	}
	return removed, nil
}

// --- dangling-image sweeps (default charly-labeled + --deep store-wide) --------------------

// listDanglingImages lists every UNTAGGED (dangling) image in local storage — charly-built or
// not. Package-level var for testability (same pattern as kit.ListLocalImages /
// listContainerImageRefs): a test stubs this so the pure selection + dry-run logic in
// pruneDanglingImages is deterministic without touching the real engine.
var listDanglingImages = defaultListDanglingImages

func defaultListDanglingImages(engine string) ([]kit.LocalImageInfo, error) {
	out, err := exec.Command(kit.EngineBinary(engine), "images", "--all", "--filter", "dangling=true", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("listing dangling images: %w", err)
	}
	return kit.ParseLocalImagesJSON(out)
}

// selectDanglingImages is the PURE selection predicate behind both dangling-image sweeps: given
// every listed dangling image, decide which are candidates for removal. onlyCharly=true is the
// default `charly clean` sweep (only images carrying the ai.opencharly.box label — never a
// foreign image); onlyCharly=false is the `charly clean --deep` store-wide sweep (every untagged
// image, INCLUDING unlabeled multi-stage build intermediates — WriteLabels stamps
// ai.opencharly.* only on the FINAL build stage, so an intermediate stage image is never
// charly-labeled and is invisible to the default sweep; this is the R4-closing gap --deep
// exists to close). No InUse check here: a dangling (untagged) image is essentially never a
// running container's OWN image reference, and the real backstop is the unforced `rmi` at
// removal time (below), which refuses anything still referenced by a container or a kept tag.
func selectDanglingImages(imgs []kit.LocalImageInfo, onlyCharly bool) []kit.LocalImageInfo {
	var out []kit.LocalImageInfo
	for _, im := range imgs {
		if onlyCharly && im.Labels[spec.LabelBox] == "" {
			continue // not charly-built
		}
		out = append(out, im)
	}
	return out
}

// pruneDanglingImages is the shared engine behind the charly-labeled dangling sweep (default
// `charly clean`) and the store-wide `--deep` purge (`charly clean --deep`): list, select
// (selectDanglingImages), then `rmi` each candidate — WITHOUT -f, so an image still referenced
// by a container or held by an in-flight build is refused and silently skipped (the same
// backstop tag retention relies on). Guarded like every other image-removing sweep in this file:
// never while ANY build is live (an untagged intermediate may be a parent of an in-flight
// build). Returns the removed (or would-remove, under dryRun) image IDs and the sum of their
// reported Size in bytes. This byte total is an UPPER BOUND on actual reclaimed disk, NOT a
// prediction: podman's per-image Size counts every layer the image references, and dangling
// images routinely SHARE layers with images that stay (retained tags, other dangling images) —
// removing one image frees only the layers it held UNIQUELY.
func pruneDanglingImages(engine string, onlyCharly, dryRun bool) ([]string, int64, error) {
	if _, _, live := liveBuildFloor(); live > 0 {
		return nil, 0, nil // never delete images while any build is in flight
	}
	imgs, err := listDanglingImages(engine)
	if err != nil {
		return nil, 0, err
	}
	var removed []string
	var totalBytes int64
	for _, im := range selectDanglingImages(imgs, onlyCharly) {
		if dryRun {
			removed = append(removed, im.ID)
			totalBytes += im.Size
			continue
		}
		if err := exec.Command(kit.EngineBinary(engine), "rmi", im.ID).Run(); err != nil {
			continue // parent of a kept image / in use — expected, keep
		}
		removed = append(removed, im.ID)
		totalBytes += im.Size
	}
	return removed, totalBytes, nil
}

// pruneDanglingCharlyImages removes UNTAGGED (dangling) charly-built images — the residue
// tag-retention leaves behind (an untagged id) plus dead build intermediates that happen to
// carry the ai.opencharly.box label. The default `charly clean` category; see
// pruneDanglingImages for the shared engine.
func pruneDanglingCharlyImages(engine string, dryRun bool) ([]string, error) {
	ids, _, err := pruneDanglingImages(engine, true, dryRun)
	return ids, err
}

// pruneDeepDanglingImages removes EVERY untagged (dangling) image in local storage — the
// store-wide `charly clean --deep` category. Unlike pruneDanglingCharlyImages, it is NOT
// restricted to the ai.opencharly.box label. Removing a dangling image via `rmi` also frees
// any layer blobs it alone referenced, so this is EFFECTIVELY a dangling-image-plus-unused-
// layer prune with a single engine call per image, not two.
func pruneDeepDanglingImages(engine string, dryRun bool) ([]string, int64, error) {
	return pruneDanglingImages(engine, false, dryRun)
}

// buildahStagingGlobs are the /var/tmp staging-dir patterns buildah/podman
// leave behind when a commit dies mid-write (ENOSPC, SIGKILL) — dead weight no
// engine command reclaims. Swept only when no build is live, and only dirs
// owned by the current user (rootless storage).
var buildahStagingGlobs = []string{
	"/var/tmp/container_images_storage*",
	"/var/tmp/buildah*",
}

// pruneBuildahStaging removes dead buildah/podman staging dirs (see
// buildahStagingGlobs). Live-build-guarded like the dangling reaper.
func pruneBuildahStaging(dryRun bool) []string {
	if _, _, live := liveBuildFloor(); live > 0 {
		return nil
	}
	uid := os.Getuid()
	var removed []string
	for _, g := range buildahStagingGlobs {
		matches, _ := filepath.Glob(g)
		for _, m := range matches {
			st, err := os.Stat(m)
			if err != nil || !st.IsDir() {
				continue
			}
			if sys, ok := st.Sys().(*syscall.Stat_t); !ok || int(sys.Uid) != uid {
				continue // not ours (rootless scope only)
			}
			if dryRun {
				removed = append(removed, m)
				continue
			}
			if err := os.RemoveAll(m); err != nil {
				continue
			}
			removed = append(removed, m)
		}
	}
	return removed
}

// --- check-run + build-candy staging retention ----------------------------------------------

// pruneCheckRuns trims each bed/score subdir of checkDir to the newest keepN run
// artifacts: CalVer-named run dirs (bed runs), `runs/<id>` dirs (score
// iterations), and `result-<calver>.yml` files. NOTES.md and any other file are
// always preserved. keepN <= 0 disables. Returns the paths removed.
func pruneCheckRuns(checkDir string, keepN int, dryRun bool) ([]string, error) {
	if keepN <= 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(checkDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		if !e.IsDir() {
			continue // top-level files (ISSUE-*.md, etc.) are not run output
		}
		rm, err := pruneOneCheckDir(filepath.Join(checkDir, e.Name()), keepN, dryRun)
		if err != nil {
			return removed, err
		}
		removed = append(removed, rm...)
	}
	return removed, nil
}

func pruneOneCheckDir(bedDir string, keepN int, dryRun bool) ([]string, error) {
	children, err := os.ReadDir(bedDir)
	if err != nil {
		return nil, err
	}
	var calverDirs, resultFiles []string
	hasRuns := false
	for _, c := range children {
		name := c.Name()
		if name == "NOTES.md" {
			continue // durable memory — never prune
		}
		if c.IsDir() {
			if _, ok := kit.ParseCalVer(name); ok {
				calverDirs = append(calverDirs, name)
			} else if name == "runs" {
				hasRuns = true
			}
		} else if strings.HasPrefix(name, "result-") && strings.HasSuffix(name, ".yml") {
			resultFiles = append(resultFiles, name)
		}
	}

	var removed []string
	// CalVer-named run dirs: keep newest keepN by CalVer.
	removed = append(removed, removeOldestByCalVer(bedDir, calverDirs, keepN, "result-", ".yml", dryRun)...)
	// result-<calver>.yml: keep newest keepN by embedded CalVer.
	removed = append(removed, removeOldestByCalVer(bedDir, resultFiles, keepN, "result-", ".yml", dryRun)...)
	// runs/<id>: keep newest keepN by mtime (runIDs aren't CalVer).
	if hasRuns {
		removed = append(removed, removeOldestByMtime(filepath.Join(bedDir, "runs"), keepN, dryRun)...)
	}
	return removed, nil
}

// removeOldestByCalVer keeps the newest keepN entries (sorted by the CalVer
// embedded in the name, after trimming the given prefix/suffix) and removes the
// rest. Entries without a parseable CalVer are left untouched.
func removeOldestByCalVer(parent string, names []string, keepN int, prefix, suffix string, dryRun bool) []string {
	type dated struct {
		name string
		cv   kit.CalVer
	}
	var items []dated
	for _, n := range names {
		core := strings.TrimSuffix(strings.TrimPrefix(n, prefix), suffix)
		if cv, ok := kit.ParseCalVer(core); ok {
			items = append(items, dated{n, cv})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[j].cv.Less(items[i].cv) }) // newest first
	var removed []string
	for idx, it := range items {
		if idx < keepN {
			continue
		}
		p := filepath.Join(parent, it.name)
		if dryRun {
			removed = append(removed, p)
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			removed = append(removed, p)
		}
	}
	return removed
}

// removeOldestByMtime keeps the newest keepN immediate subdirs of dir (by
// modification time) and removes the rest.
func removeOldestByMtime(dir string, keepN int, dryRun bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type timed struct {
		name string
		mod  int64
	}
	var items []timed
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, timed{e.Name(), info.ModTime().UnixNano()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod > items[j].mod }) // newest first
	var removed []string
	for idx, it := range items {
		if idx < keepN {
			continue
		}
		p := filepath.Join(dir, it.name)
		if dryRun {
			removed = append(removed, p)
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			removed = append(removed, p)
		}
	}
	return removed
}

// pruneBuildCandyDirs trims .build/_candy/<candy>.<version>/ to the newest keepN
// versions PER CANDY — the build-staging counterpart to image-tag retention, so
// outdated candy CalVer stagings don't accumulate (candy names are dot-free, so
// the version parses off the first dot). It also removes the LEGACY shared
// .build/_layers/ dir (fully superseded by the versioned _candy layout) — that
// cleanup is unconditional, like the makepkg sweep. keepN<=0 disables only the
// per-candy retention.
func pruneBuildCandyDirs(buildDir string, keepN int, dryRun bool) []string {
	var removed []string

	// Legacy: the pre-versioned shared staging dir is superseded; remove it.
	legacy := filepath.Join(buildDir, "_layers")
	if _, err := os.Stat(legacy); err == nil {
		if dryRun {
			removed = append(removed, legacy)
		} else if os.RemoveAll(legacy) == nil {
			removed = append(removed, legacy)
		}
	}

	if keepN <= 0 {
		return removed
	}
	candyRoot := filepath.Join(buildDir, "_candy")
	entries, err := os.ReadDir(candyRoot)
	if err != nil {
		return removed
	}
	byCandy := map[string][]string{}
	for _, e := range entries {
		// Skip transient .<name>.tmp.* staging dirs (in-flight installs).
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name, _, ok := strings.Cut(e.Name(), ".")
		if !ok {
			continue
		}
		byCandy[name] = append(byCandy[name], e.Name())
	}
	for name, dirs := range byCandy {
		removed = append(removed, removeOldestByCalVer(candyRoot, dirs, keepN, name+".", "", dryRun)...)
	}
	return removed
}

// --- targeted image-tag invalidation (`charly clean --invalidate`) -------------------------

// matchImageGlob matches a glob against a full image ref OR its last path
// segment (repo:tag), so 'charly-fedora-2*' matches
// 'ghcr.io/opencharly/charly-fedora-2…:tag' without the registry prefix.
func matchImageGlob(glob, ref string) bool {
	last := ref
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	full, _ := path.Match(glob, ref)
	short, _ := path.Match(glob, last)
	return full || short
}

// invalidateImageTags removes every charly-labeled image tag matching the
// glob (full ref or its last path segment) — targeted cache invalidation
// for stale intermediates, replacing ad-hoc `podman rmi '<glob>'`. The
// retention safety rules apply unchanged: in-use images are skipped and
// `rmi` runs without -f as the backstop.
func invalidateImageTags(engine, glob string, dryRun bool) ([]string, error) {
	groups, err := charlyImageTags(engine)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, tags := range groups {
		for _, t := range tags {
			if !matchImageGlob(glob, t.Ref) {
				continue
			}
			if t.InUse {
				continue
			}
			if dryRun {
				removed = append(removed, t.Ref)
				continue
			}
			if err := exec.Command(kit.EngineBinary(engine), "rmi", t.Ref).Run(); err != nil {
				continue // in-use backstop — engine refuses, same as retention
			}
			removed = append(removed, t.Ref)
		}
	}
	sort.Strings(removed)
	return removed, nil
}
