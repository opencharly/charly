package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Retention fallbacks — used ONLY when defaults.keep_images / keep_check_runs are
// absent from config. Zero means "disabled" so third-party configs that never
// declare the keys get NO surprise pruning. The repo's charly.yml opts in
// (keep_images: 5, keep_check_runs: 10). See /charly-core:clean.
const (
	keepImagesFallback    = 0
	keepCheckRunsFallback = 0
)

// listContainerImageRefs returns the set of image IDs and image refs currently
// referenced by ANY container (running or stopped, incl. quadlet-managed
// deploys). Package-level var for testability (same pattern as ListLocalImages).
var listContainerImageRefs = defaultContainerImageRefs

func defaultContainerImageRefs(engine string) (ids map[string]bool, refs map[string]bool, err error) {
	ids = map[string]bool{}
	refs = map[string]bool{}
	// Parse JSON, not a Go-template `--format`: podman's `{{.ImageID}}` template
	// panics (slice bounds [:12] length 0) when any container has an empty image
	// ID. The raw JSON field handles that gracefully.
	out, e := exec.Command(EngineBinary(engine), "ps", "-a", "--format", "json").Output()
	if e != nil {
		return ids, refs, fmt.Errorf("listing containers via %s: %w", EngineBinary(engine), e)
	}
	var rows []map[string]any
	if e := json.Unmarshal(out, &rows); e != nil {
		return ids, refs, fmt.Errorf("parsing %s ps output: %w", EngineBinary(engine), e)
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
func imageInUse(im LocalImageInfo, ids, refs map[string]bool) bool {
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
func imageLabelCalVer(im LocalImageInfo) (CalVer, bool) {
	return ParseCalVer(im.Labels[LabelVersion])
}

// pruneImagesByRetention keeps the newest keepN build TAGS per
// `ai.opencharly.box` group and removes the older ones. Tags are ordered by
// the `ai.opencharly.version` CalVer label (PRIMARY) then the `:YYYY.DDD.HHMM`
// build TAG (TIEBREAKER); because the label is content-stable, the tag is what
// distinguishes the newest builds.
//
// Retention is per TAG, not per image entry. Older tags are `rmi`'d
// INDIVIDUALLY — so when several tags share one image id, the kept tags hold
// the id alive and the just-built (newest) tag is always retained. (The earlier
// per-entry form removed an entry's whole Names array, which deleted kept tags
// and could wipe the just-built image when content-stable rebuilds piled many
// tags onto one id — see CHANGELOG/.) Tags whose image is referenced by a
// container are skipped, and `rmi` runs WITHOUT `-f` as a backstop. keepN <= 0
// disables (no-op). Returns the refs removed (or that would be, when dryRun).
// imageTagInfo is one locally stored tag of a charly-labeled image —
// the shared inventory row behind retention pruning, `charly box list tags`,
// and `charly clean --invalidate`.
type imageTagInfo struct {
	Ref         string
	LabelCalVer CalVer
	OkLabel     bool
	TagCalVer   CalVer
	OkTag       bool
	InUse       bool
}

// charlyImageTags inventories local storage: one row PER TAG (deduped by
// ref), grouped by the ai.opencharly.box label and sorted newest-first
// (label-CalVer primary, build-tag CalVer tiebreaker; undatable tags last).
// Non-charly images (no label) never appear.
func charlyImageTags(engine string) (map[string][]imageTagInfo, error) {
	imgs, err := ListLocalImages(engine)
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
		short := im.Labels[LabelBox]
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
			tcv, okT := ParseCalVer(extractCalVerTag(ref))
			groups[short] = append(groups[short], imageTagInfo{
				Ref: ref, LabelCalVer: lcv, OkLabel: okL,
				TagCalVer: tcv, OkTag: okT, InUse: inUse,
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
			if group[i].OkTag && group[j].OkTag && group[i].TagCalVer != group[j].TagCalVer {
				return group[j].TagCalVer.Less(group[i].TagCalVer) // newer build first
			}
			return group[i].OkTag && !group[j].OkTag // dateable sorts before undateable
		})
	}
	return groups, nil
}

func pruneImagesByRetention(engine string, keepN int, dryRun bool) ([]string, error) {
	if keepN <= 0 {
		return nil, nil
	}
	groups, err := charlyImageTags(engine)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, group := range groups {
		for idx, c := range group {
			if idx < keepN {
				continue // keep the newest keepN tags
			}
			if !c.OkLabel && !c.OkTag {
				continue // never remove a tag we can't date (no label/tag CalVer)
			}
			if c.InUse {
				continue // image referenced by a container/deploy
			}
			if dryRun {
				removed = append(removed, c.Ref)
				continue
			}
			// rmi WITHOUT -f untags this ref while other tags of a shared id
			// survive; it also refuses an image still held by a build /
			// "external" container our InUse pre-check can't see — the
			// safety backstop. Silent skip — in-use retention is expected.
			if err := exec.Command(EngineBinary(engine), "rmi", c.Ref).Run(); err != nil {
				continue
			}
			removed = append(removed, c.Ref)
		}
	}
	return removed, nil
}

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
			if _, ok := ParseCalVer(name); ok {
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
		cv   CalVer
	}
	var items []dated
	for _, n := range names {
		core := strings.TrimSuffix(strings.TrimPrefix(n, prefix), suffix)
		if cv, ok := ParseCalVer(core); ok {
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

// cleanMakepkgArtifacts removes the one-time makepkg build leftovers under
// pkg/arch (src/, pkg/, *.pkg.tar.zst, *.log). These are pure transient waste:
// the package is already installed via pacman. Returns the paths removed.
func cleanMakepkgArtifacts(projectDir string, dryRun bool) []string {
	base := filepath.Join(projectDir, "pkg", "arch")
	var targets []string
	for _, sub := range []string{"src", "pkg"} {
		p := filepath.Join(base, sub)
		if _, err := os.Stat(p); err == nil {
			targets = append(targets, p)
		}
	}
	for _, pat := range []string{"*.pkg.tar.zst", "*.log"} {
		matches, _ := filepath.Glob(filepath.Join(base, pat))
		targets = append(targets, matches...)
	}
	var removed []string
	for _, p := range targets {
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

// CleanCmd applies the configured retention now (the on-demand counterpart to
// the auto-prune that runs after `charly box build` / `charly check run`), and also
// sweeps the one-time makepkg backlog.
type CleanCmd struct {
	DryRun     bool   `long:"dry-run" help:"Print everything that would be removed; touch nothing"`
	Images     bool   `long:"images" help:"Only image-tag retention"`
	Check      bool   `long:"check" help:"Only check-run retention"`
	Keep       int    `long:"keep" help:"Override the retention count for this run (0 = use defaults:)"`
	Invalidate string `long:"invalidate" help:"Remove every charly-labeled image tag matching this glob (full ref or last path segment) — targeted cache invalidation for stale intermediates; in-use images are skipped. Runs ONLY the invalidation"`
}

func (c *CleanCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	if c.Invalidate != "" {
		rt, err := ResolveRuntime()
		if err != nil {
			return err
		}
		refs, err := invalidateImageTags(EngineBinary(rt.BuildEngine), c.Invalidate, c.DryRun)
		if err != nil {
			return fmt.Errorf("invalidating image tags: %w", err)
		}
		verb := "removed"
		if c.DryRun {
			verb = "would remove"
		}
		fmt.Printf("invalidate: %s %d tag(s) matching %q\n", verb, len(refs), c.Invalidate)
		for _, r := range refs {
			fmt.Printf("  %s\n", r)
		}
		return nil
	}
	keepImages := resolveIntPtr(nil, nil, keepImagesFallback)
	keepCheck := resolveIntPtr(nil, nil, keepCheckRunsFallback)
	if cfg, err := LoadConfig(dir); err == nil {
		keepImages = resolveIntPtr(cfg.Defaults.KeepImages, nil, keepImagesFallback)
		keepCheck = resolveIntPtr(cfg.Defaults.KeepCheckRuns, nil, keepCheckRunsFallback)
	}
	if c.Keep > 0 {
		keepImages, keepCheck = c.Keep, c.Keep
	}

	// Default (neither flag) = all categories.
	doImages := c.Images || (!c.Images && !c.Check)
	doCheck := c.Check || (!c.Images && !c.Check)
	doMakepkg := !c.Images && !c.Check

	tag := "removed"
	if c.DryRun {
		tag = "would remove"
	}

	if doImages {
		rt, err := ResolveRuntime()
		if err != nil {
			return err
		}
		refs, err := pruneImagesByRetention(EngineBinary(rt.BuildEngine), keepImages, c.DryRun)
		if err != nil {
			return fmt.Errorf("pruning images: %w", err)
		}
		fmt.Printf("images: %s %d tag(s) (keep_images=%d)\n", tag, len(refs), keepImages)
		for _, r := range refs {
			fmt.Printf("  %s\n", r)
		}
		// Versioned build-staging retention: prune outdated .build/_candy/
		// <candy>.<version>/ dirs (+ remove the legacy _layers dir), paired with
		// image-tag retention.
		bpaths := pruneBuildCandyDirs(filepath.Join(dir, ".build"), keepImages, c.DryRun)
		fmt.Printf("build: %s %d staging dir(s) under .build/_candy (keep_images=%d)\n", tag, len(bpaths), keepImages)
		for _, p := range bpaths {
			fmt.Printf("  %s\n", p)
		}
	}
	if doCheck {
		paths, err := pruneCheckRuns(filepath.Join(dir, ".check"), keepCheck, c.DryRun)
		if err != nil {
			return fmt.Errorf("pruning check runs: %w", err)
		}
		fmt.Printf("check: %s %d run artifact(s) (keep_check_runs=%d, NOTES.md preserved)\n", tag, len(paths), keepCheck)
		for _, p := range paths {
			fmt.Printf("  %s\n", p)
		}
	}
	if doMakepkg {
		paths := cleanMakepkgArtifacts(dir, c.DryRun)
		fmt.Printf("makepkg: %s %d leftover(s) under pkg/arch\n", tag, len(paths))
		for _, p := range paths {
			fmt.Printf("  %s\n", p)
		}
	}
	return nil
}
