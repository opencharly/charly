// CUE schema for the "retention" verb wire types behind the externalized `charly
// clean` command plugin (candy/plugin-clean), which now OWNS the shared retention
// ENGINE too (image-tag / build-candy / check-run / deep dangling-image pruning +
// the charly-labeled image-tag inventory) — not just the CLI. NOT authoring kinds
// (never in #Node/#Op) — pure generated wire structs, single-sourced here so `task
// cue:gen` produces the Go structs (spec.RetentionRequest / spec.RetentionReply)
// candy/plugin-clean and its peer plugins (candy/plugin-check, candy/plugin-box)
// reference directly. Both are plain structs — gengotypes generates them
// faithfully, no disjunction/inexpressibility escape needed (per the SDD "wire
// types are CUE-sourced without exception" mandate).
//
// The engine is reached two ways: `charly clean`'s own CLI dispatch calls it
// in-package (no wire hop — same Go process, same package); `charly box build` /
// `charly check run` / `charly box list tags` (still core) reach it via
// verb:retention — the same "core adapter resolves+Invokes a compiled-in plugin
// word" pattern verb:credential/verb:gpu/verb:tunnel already use.

// #RetentionRequest is the verb:retention request. dir is the project directory
// (os.Getwd()) — always present, never empty. keep=0 means "use the resolved
// defaults"; invalidate (non-empty) runs ONLY the targeted image-tag invalidation.
// deep runs the store-wide untagged/dangling-image purge category (`charly clean
// --deep`) — unlike images (which only ever touches charly-labeled tags/dangling
// ids), deep removes EVERY untagged image in local storage, including unlabeled
// multi-stage build intermediates the images category can never see. list runs
// the read-only tag inventory (`charly box list tags`) — no dir needed beyond
// resolving the engine, nothing is removed. build_prune is the narrow post-`charly
// box build` step: retention-prune image tags + stale .build/_candy staging dirs
// ONLY (the historic charly/retention.go pruneAfterBuild scope) — deliberately
// NOT the fuller images category (which also sweeps dangling images + buildah
// staging), so a build's own post-step behavior is unchanged by this relocation.
// keep_images/keep_check_runs are the caller's PRE-RESOLVED defaults.keep_images/
// keep_check_runs (0 = disabled) — this engine never reads charly.yml itself; a
// core caller (already running LoadConfig in-process) resolves these directly, a
// plugin caller resolves them PLUGIN-SIDE via the shared
// sdk/loaderkit.ResolveRetentionDefaultsViaExecutor (K-wave 2 cone R6 — the former
// "retention-defaults" HostBuild seam is DELETED).
// keep (the CLI --keep override) wins over both when > 0.
#RetentionRequest: {
	dir!:             string @go(Dir)
	dry_run?:         bool   @go(DryRun)
	images?:          bool   @go(Images)
	check?:           bool   @go(Check)
	deep?:            bool   @go(Deep)
	list?:            bool   @go(List)
	build_prune?:     bool   @go(BuildPrune)
	keep?:            int    @go(Keep, type=int)
	keep_images?:     int    @go(KeepImages, type=int)
	keep_check_runs?: int    @go(KeepCheckRuns, type=int)
	invalidate?:      string @go(Invalidate)
}

// #TagInfo is one locally stored image tag, as presented by `charly box list
// tags` (verb:retention list=true): version is "-" when the image carries no
// parseable ai.opencharly.version label.
#TagInfo: {
	box!:     string @go(Box)
	ref!:     string @go(Ref)
	version!: string @go(Version)
	in_use!:  bool   @go(InUse)
}

// #RetentionReply is the verb:retention reply: the removed (or would-remove,
// under dry_run) image refs, build-candy dirs, and check-run paths, plus the
// effective retention counts, for the caller to present.
//
// deep_ids/deep_bytes report the store-wide untagged-image purge (deep): the removed
// (or would-remove) image IDs and the sum of their reported storage Size in bytes.
// deep_bytes is an UPPER BOUND, not a prediction of actual freed disk: each image's
// reported Size counts every layer it references, and many of those layers are
// SHARED with images that remain (retained tags, or other still-referenced dangling
// images) — RDD-verified live on a real host, where a --deep purge removing 68
// untagged images (3,552 -> 3,484) reported ~92.6 GiB via this sum but only ~4.6 GiB
// of disk was actually freed (132.6 GB -> 128 GB), because most layer bytes stayed
// shared with the ~3,400 remaining (largely stale-tagged) images. `charly clean
// --deep --dry-run` presents deep_bytes as "up to" for exactly this reason; pairing
// --deep with --invalidate (removing stale TAGS too, so their exclusively-held
// layers also become unreferenced) gets closer to the reported figure.
//
// tag_groups is the `list` reply payload: every locally stored charly-labeled tag,
// newest-first per box.
//
// error is a human-facing message on a non-recoverable failure.
#RetentionReply: {
	image_refs?:      [...string]  @go(ImageRefs)
	dangling_ids?:    [...string]  @go(DanglingIDs)
	staging_dirs?:    [...string]  @go(StagingDirs)
	build_dirs?:      [...string]  @go(BuildDirs)
	check_paths?:     [...string]  @go(CheckPaths)
	deep_ids?:        [...string]  @go(DeepIDs)
	deep_bytes?:      int          @go(DeepBytes)
	tag_groups?:      [...#TagInfo] @go(TagGroups)
	keep_images?:     int          @go(KeepImages, type=int)
	keep_check_runs?: int          @go(KeepCheckRuns, type=int)
	error?:           string       @go(Error)
}
