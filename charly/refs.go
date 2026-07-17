package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/loaderkit"
	"github.com/opencharly/sdk/spec"
)

// ParsedRef, IsRemoteImageRef, ParseRemoteRef, SplitRepoAndSubPath — pure remote-ref string
// parsing, relocated to sdk/spec (K1/W9) as generic vocab: their consumer set spans deploy/
// provider/command files with zero loader-cone character, so a MECHANISM kit (loaderkit) would be
// the wrong home — spec is the shared, always-legal-to-import vocabulary layer. charly/*.go call
// sites now reference spec.ParsedRef / spec.ParseRemoteRef / spec.IsRemoteImageRef directly
// (ZERO-ALIASES — no alias reintroduced here).

// bareRefs returns the bare map-key form of each ref — for the consumers that
// resolve a candy list against the candy map.
func bareRefs(refs []deploykit.CandyRef) []string {
	if len(refs) == 0 {
		return nil
	}
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.Bare()
	}
	return out
}

// Candy-version resolution is per-entity, not per-git-tag: the `@github…:vTAG`
// suffix is ONLY the FETCH coordinate (which commit to clone). The authority is
// the candy's own `version:` field, read AFTER fetch and arbitrated by
// pickCandyVersion in ScanAllCandyWithConfigOpts (layers.go). So a repo re-tag
// that doesn't change a candy emits no warning. CollectRemoteRefsOpts below
// therefore collects EVERY distinct (repo, git-tag) a ref is referenced at;
// the per-entity dedup + warn happens once, after fetch.

// RepoOverrideEnv configures RDD local-overrides: it points a remote `@github`
// repo ref at a LOCAL working tree (Go-`replace`-style), so an UNCOMMITTED
// candy / charly.yml change can be built and `charly check`'d by ANY
// consumer — across submodule boundaries — BEFORE it is committed and pushed.
// This is the supported "verify before you push to main" mechanism (no cache
// hacks, no producer-first tag churn).
//
// Value: a comma-separated list of `repoPath=localDir` pairs. repoPath matches
// the repo-root form every `@github` candy/namespace/image ref resolves through
// (`github.com/<org>/<repo>`); a bare `<org>/<repo>` is accepted too (auto
// `github.com/` prefix, same rule as `--repo`). Example:
//
//	CHARLY_REPO_OVERRIDE=opencharly/charly=/home/me/oc-charly \
//	    charly -C box/ubuntu box build ubuntu-coder
//
// The matched directory resolves verbatim (leading `~/` expanded); the ref's
// `:vTAG` is IGNORED — an override ALWAYS resolves to the dev's current tree.
const RepoOverrideEnv = "CHARLY_REPO_OVERRIDE"

// normalizeOverrideRepoPath canonicalizes the LHS of a CHARLY_REPO_OVERRIDE pair to
// the repo-root form ParseRemoteRef yields, so `opencharly/charly` and
// `github.com/opencharly/charly` both match (same auto-prefix rule as
// loaderkit.NormalizeRepoSpec, since W9's main_repo.go relocation).
func normalizeOverrideRepoPath(rp string) string {
	rp = strings.TrimSpace(strings.TrimSuffix(rp, "/"))
	if i := strings.Index(rp, "/"); i > 0 && !strings.Contains(rp[:i], ".") {
		return "github.com/" + rp
	}
	return rp
}

// repoOverrideDir returns the configured local override directory for repoPath,
// or ("", false, nil) when none applies. A malformed entry, a missing/empty
// directory, or a non-directory target is a hard error — the override was set
// deliberately, so a typo must fail loud rather than silently fall through to a
// remote fetch.
func repoOverrideDir(repoPath string) (string, bool, error) {
	spec := strings.TrimSpace(os.Getenv(RepoOverrideEnv))
	if spec == "" {
		return "", false, nil
	}
	for pair := range strings.SplitSeq(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.LastIndex(pair, "=")
		if eq < 0 {
			return "", false, fmt.Errorf("%s: malformed entry %q (want repoPath=localDir)", RepoOverrideEnv, pair)
		}
		if normalizeOverrideRepoPath(pair[:eq]) != repoPath {
			continue
		}
		dir := strings.TrimSpace(pair[eq+1:])
		if dir == "" {
			return "", false, fmt.Errorf("%s: empty directory for repo %q", RepoOverrideEnv, repoPath)
		}
		if strings.HasPrefix(dir, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				dir = filepath.Join(home, dir[2:])
			}
		}
		info, err := os.Stat(dir)
		if err != nil {
			return "", false, fmt.Errorf("%s: override dir for %q not accessible: %w", RepoOverrideEnv, repoPath, err)
		}
		if !info.IsDir() {
			return "", false, fmt.Errorf("%s: override for %q is not a directory: %s", RepoOverrideEnv, repoPath, dir)
		}
		return dir, true, nil
	}
	return "", false, nil
}

// selfSuperprojectOverridePair returns a CHARLY_REPO_OVERRIDE pair
// (`<repo-identity>=<superproject-dir>`) that points a bed project's OWN
// superproject `@github` refs at the local working tree, or "" when projectDir
// is not a git submodule of a charly superproject. A check bed (a `disposable: true` bundle) living in
// a `box/<distro>` submodule references its parent repo's shared candies via
// `@github.com/<org>/<parent>/candy/<name>:<tag>`; without this override the bed
// would build the PINNED REMOTE candy and so test STALE code — the candy-ref
// analogue of why the bed runner builds the toolchain with `--dev-local-pkg`. The
// override IGNORES the ref's `:vTAG`, so the bed always tests the dev's current
// tree. Returns "" when projectDir is its own root (its candies already resolve
// from the local tree) or when git / the superproject identity is unavailable.
func selfSuperprojectOverridePair(projectDir string) string {
	out, err := exec.Command("git", "-C", projectDir, "rev-parse", "--show-superproject-working-tree").Output()
	if err != nil {
		return ""
	}
	superDir := strings.TrimSpace(string(out))
	if superDir == "" {
		return "" // not a submodule — its candies already resolve from the local tree
	}
	identity := loaderkit.RootRepoIdentity(superDir)
	if identity == "" {
		return ""
	}
	return identity + "=" + superDir
}

// mergeRepoOverrides combines an existing CHARLY_REPO_OVERRIDE value with an
// auto-added pair. The existing (operator-set) entries are placed FIRST so an
// explicit operator override for a repo WINS over the auto pair — repoOverrideDir
// returns the FIRST matching entry. Either argument may be empty.
func mergeRepoOverrides(existing, add string) string {
	existing = strings.TrimSpace(existing)
	add = strings.TrimSpace(add)
	switch {
	case existing == "":
		return add
	case add == "":
		return existing
	default:
		return existing + "," + add
	}
}

// autoMigratedRepos guards the remote-cache auto-migration against unbounded
// re-entry. A migration that re-enters LoadUnified would resolve @github refs and
// re-enter EnsureRepoDownloaded → the command:migrate Invoke. With a self- or mutual
// import (the main ↔ cachyos cycle), and especially right after a LatestSchemaVersion
// bump (when EVERY cache reads as behind-head), that could recurse without bound.
// markRepoAutoMigrating returns true exactly once per cache path per process, so each
// cache is auto-migrated at most once and the cycle terminates — safe because the
// migration engine is idempotent, so a single pass per process is sufficient.
var (
	autoMigratedRepos   = map[string]bool{}
	autoMigratedReposMu sync.Mutex
)

func markRepoAutoMigrating(path string) bool {
	autoMigratedReposMu.Lock()
	defer autoMigratedReposMu.Unlock()
	if autoMigratedRepos[path] {
		return false
	}
	autoMigratedRepos[path] = true
	return true
}

// EnsureRepoDownloaded downloads the repo if not already cached.
// Returns the cache path. The cache is auto-migrated to the latest schema
// CalVer via the project-only command:migrate Invoke on EVERY access —
// cache HIT and fresh clone alike. Re-migrating a cache hit is required (and
// safe, the chain being idempotent): a cache populated by an OLDER binary — or
// relocated from a prior cache directory across a schema bump (an older-schema
// cache) — so the current binary would otherwise fail to find charly.yml. An
// already-current cache is a no-op.
func EnsureRepoDownloaded(repoPath, version string) (string, error) {
	// RDD local-override (CHARLY_REPO_OVERRIDE): resolve a remote repo ref to a local
	// working tree instead of fetching, so an uncommitted candy/charly.yml change
	// can be built + evaluated by any consumer before it is pushed. The override
	// is the dev's LIVE tree — it is used verbatim and NEVER migrated (migration
	// would mutate the working tree); the dev keeps it schema-current themselves.
	if dir, ok, err := repoOverrideDir(repoPath); err != nil {
		return "", err
	} else if ok {
		return dir, nil
	}
	cached, err := kit.IsRepoCached(repoPath, version)
	if err != nil {
		return "", err
	}
	var path string
	if cached {
		path, err = kit.RepoCachePath(repoPath, version)
	} else {
		// The cache-miss DOWNLOAD dispatches through the registered refs backend (P7):
		// the compiled-in candy/plugin-refs (git) by default, swappable for an OCI/S3 plugin.
		path, err = activeRefsDownloader.Download(repoPath, version)
	}
	if err != nil {
		return "", err
	}
	// Migrate a fresh clone ALWAYS; migrate a cache HIT only when it is actually
	// behind HEAD (an older-schema cache). The chain is idempotent,
	// but re-running it on every access of an already-current cache is costly
	// (re-parses every cached repo) and re-emits benign "unknown field" warnings
	// from very old transitive deps — so the already-current hit takes the fast,
	// silent path. Project-only subset (HostDeployPath empty) so a remote fetch
	// never mutates the user's per-host state — even the calver-schema stamp
	// touches only the cache's project files.
	if (!cached || cacheBehindHead(path)) && markRepoAutoMigrating(path) {
		if err := autoMigrateCacheProjectOnly(path); err != nil {
			return path, fmt.Errorf("auto-migrating remote cache %s: %w", path, err)
		}
	}
	return path, nil
}

// autoMigrateCacheProjectOnly brings a remote-repo cache's PROJECT files up to the head schema
// via an in-proc Invoke of the compiled-in command:migrate plugin — the migration ENGINE lives
// in candy/plugin-migrate now (M15), not core. The `--project-only` flag never touches the
// per-host overlay (a remote fetch must not mutate the user's deploy state); `--quiet` discards
// the progress output; `--dir <cache>` targets the cache tree. command:migrate is compiled-in,
// so it resolves at init() — available here, deep in config loading, before LoadUnified completes.
func autoMigrateCacheProjectOnly(path string) error {
	prov, ok := providerRegistry.resolve(ClassCommand, "migrate")
	if !ok {
		return fmt.Errorf("migrate plugin (command:migrate) not registered — charly built without candy/plugin-migrate")
	}
	params, err := marshalJSON(map[string]any{"args": []string{"--project-only", "--quiet", "--dir", path}})
	if err != nil {
		return err
	}
	_, err = prov.Invoke(context.Background(), &Operation{Reserved: "migrate", Op: OpRun, Params: params})
	return err
}

// cacheBehindHead reports whether a cached repo still needs migration: its
// root config (charly.yml) is absent or carries a schema version older than
// HEAD. A cache already at HEAD with charly.yml returns false — the fast,
// silent path.
func cacheBehindHead(path string) bool {
	data, err := os.ReadFile(filepath.Join(path, UnifiedFileName))
	if err != nil {
		return true // no charly.yml → never-migrated → migrate
	}
	cv, ok := ParseCalVer(kit.FirstYAMLVersionLine(data))
	if !ok {
		return true
	}
	return cv.Less(LatestSchemaVersion())
}

// RemoteDownload represents a unique (repo, version) pair to download,
// along with the specific bare refs needed from it.
type RemoteDownload struct {
	RepoPath string
	Version  string
	Refs     []string // bare refs to import (e.g. "github.com/org/repo/candy/name")
}

// CollectRemoteRefs is the default-opts wrapper (enabled images only) around
// CollectRemoteRefsOpts. The overwhelming majority of call sites want
// enabled-only collection, so they keep this two-arg form.
func CollectRemoteRefs(cfg *Config, layers map[string]spec.CandyReader) ([]RemoteDownload, error) {
	return CollectRemoteRefsOpts(cfg, layers, ResolveOpts{})
}

// CollectRemoteRefsOpts collects all unique remote refs from charly.yml candy
// lists and candy manifest depends/candy fields. Different candies from the same repo
// can use different versions. Only the same bare ref at conflicting versions is
// an error. Returns a list of RemoteDownload grouped by (repoPath, version).
//
// opts gates the disabled-image walk: a disabled image's candy refs are
// collected when opts.shouldIncludeDisabled(name) is true (i.e. a
// `--include-disabled <name>` build). This keeps the remote-ref FETCH set in
// lockstep with the RESOLVE set walked by ResolveAllBox / GlobalCandyOrder —
// the same shouldIncludeDisabled predicate gates both. Without it, a disabled
// named image lands in the build working set but its remote candies are never
// fetched/registered, surfacing as "unknown layer" while computing global candy
// order.
//
//nolint:gocyclo // depth-first graph walker over base/candy/builder edges; nested loops are essential to the traversal
func CollectRemoteRefsOpts(cfg *Config, layers map[string]spec.CandyReader, opts ResolveOpts) ([]RemoteDownload, error) {
	// Collect EVERY distinct (repo, git-tag) a ref is referenced at. The git tag
	// is only the FETCH coordinate — per-entity-version arbitration (and any
	// warning) happens AFTER fetch in ScanAllCandyWithConfigOpts, so a re-tag of
	// an unchanged candy no longer warns here. `source` is unused now (kept for
	// call-site stability + future diagnostics).
	type repoVer struct{ repo, ver string }
	pairs := make(map[repoVer]map[string]bool) // (repo, git-tag) -> set of bare refs
	// Track resolved default branches per repo (to avoid duplicate git queries)
	defaultBranches := make(map[string]string)

	addRef := func(ref, source string) error {
		_ = source
		if !deploykit.IsRemoteCandyRef(ref) {
			return nil
		}
		parsed := spec.ParseRemoteRef(ref)
		bareRef := deploykit.BareRef(ref)
		version := parsed.Version
		if version == "" {
			// No version specified -- resolve to default branch
			if branch, ok := defaultBranches[parsed.RepoPath]; ok {
				version = branch
			} else {
				repoURL := kit.RepoGitURL(parsed.RepoPath)
				branch, err := kit.GitDefaultBranch(repoURL)
				if err != nil {
					return fmt.Errorf("%s: cannot resolve default branch for %s: %w", source, parsed.RepoPath, err)
				}
				version = branch
				defaultBranches[parsed.RepoPath] = branch
				fmt.Fprintf(os.Stderr, "Resolved @%s -> %s (default branch)\n", parsed.RepoPath, version)
			}
		}
		key := repoVer{parsed.RepoPath, version}
		if pairs[key] == nil {
			pairs[key] = make(map[string]bool)
		}
		pairs[key][bareRef] = true
		return nil
	}

	// format_config: has been removed. Remote build-config refs now live in
	// charly.yml's `includes:` mechanism (see unified.go).

	// Collect candy refs from the ROOT project's own build/deploy targets (every
	// enabled image + every kind:local template), then follow base/builder edges
	// into imported namespaces, collecting ONLY the namespaced images actually
	// reachable as a base or builder. A namespace is imported to provide
	// bases/builders; its UNREFERENCED images and its kind:local templates (which
	// can never be a base/builder of the importing project) are not build inputs
	// here and must not be collected. Over-collecting them pulled unrelated
	// candies pinned at a different ecosystem tag, which the one-candy-one-version
	// invariant (tracker) then correctly — but spuriously — rejected. The
	// per-(Config,name) `collected` set also breaks the main<->cachyos cycle.
	collected := map[*Config]map[string]bool{}
	var collectBox func(c *Config, name string) error
	collectBox = func(c *Config, name string) error {
		seen := collected[c]
		if seen == nil {
			seen = map[string]bool{}
			collected[c] = seen
		}
		if seen[name] {
			return nil
		}
		seen[name] = true
		img, ok := c.BoxConfig(name)
		if !ok {
			return nil // external OCI base or unknown name — no candies to collect
		}
		for _, candyRef := range img.Candy {
			if err := addRef(candyRef, fmt.Sprintf("image %s", name)); err != nil {
				return err
			}
		}
		// Follow the base edge, plus builder edges when this image actually builds
		// (a candyless base needs no builder). A namespaced builder (e.g.
		// charly.fedora-builder) is BUILT as an intermediate in the consumer's graph,
		// so its candies (rpmfusion, yay, …) must be fetched here — dropping the
		// builder edge under-collects them ("unknown layer"). The builder edge
		// follows the EFFECTIVE builder (effectiveBuilderForBox → the canonical
		// resolveEffectiveBuilder), NOT the raw per-image img.Builder: an image
		// whose builder comes from defaults.builder / the distro-keyed default
		// (e.g. bazzite/aurora -> charly.fedora-builder, with no per-image builder:
		// block) has an EMPTY raw img.Builder, so reading it skipped the builder
		// edge and under-collected its candies — the exact fetch/resolve lockstep
		// break this walk exists to prevent. Qualified refs descend into the
		// imported namespace; bare refs resolve within c; an external-URL/unknown
		// base resolves to ok=false and is skipped.
		edges := []string{}
		if img.Base != "" {
			edges = append(edges, img.Base)
		}
		if len(img.Candy) > 0 {
			edges = append(edges, c.effectiveBuilderForBox(name, img).AllBuilder()...)
		}
		for _, ref := range edges {
			if _, tc, ok := c.resolveBoxRef(ref); ok {
				if err := collectBox(tc, spec.LeafName(ref)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if cfg != nil {
		for _, imgName := range cfg.allBoxNames() {
			img, _ := cfg.BoxConfig(imgName)
			if !img.IsEnabled() && !opts.shouldIncludeDisabled(imgName) {
				continue
			}
			if err := collectBox(cfg, imgName); err != nil {
				return nil, err
			}
		}
		for tplName, body := range cfg.Local {
			r, rerr := resolveLocalViaPlugin(body)
			if rerr != nil || r == nil {
				continue
			}
			for _, candyRef := range r.Candy {
				if err := addRef(candyRef, fmt.Sprintf("kind:local %s", tplName)); err != nil {
					return nil, err
				}
			}
		}
	}

	// Scan the candy manifest require: and candy: fields
	for candyName, layer := range layers {
		for _, dep := range layer.GetRequire() {
			if err := addRef(dep.Raw, fmt.Sprintf("layer %s require", candyName)); err != nil {
				return nil, err
			}
		}
		for _, ref := range layer.GetIncludedCandy() {
			if err := addRef(ref.Raw, fmt.Sprintf("layer %s layer", candyName)); err != nil {
				return nil, err
			}
		}
	}

	// A deploy's add_candy: candies (opts.ExtraCandyRefs) are NOT reachable from the
	// image-closure walk above (add_candy is not a base/builder/require edge), so a
	// bed that add_candy's a host-side PLUGIN candy must collect them here — else the
	// plugin never enters the scan and loadProjectPlugins can't build it. A local ref
	// is a no-op (addRef gates on IsRemoteCandyRef; ScanCandy already has it); a
	// remote ref joins the same fetch + per-entity-version arbitration as any other.
	for _, ref := range opts.ExtraCandyRefs {
		if err := addRef(ref, "deploy add_candy"); err != nil {
			return nil, err
		}
	}

	// Emit one RemoteDownload per distinct (repo, git-tag). A bare ref pinned at
	// two git tags yields two downloads (both fetched); the post-fetch
	// arbitration keeps one materialization per bare ref.
	var result []RemoteDownload
	for key, refs := range pairs {
		refList := make([]string, 0, len(refs))
		for ref := range refs {
			refList = append(refList, ref)
		}
		result = append(result, RemoteDownload{
			RepoPath: key.repo,
			Version:  key.ver,
			Refs:     refList,
		})
	}
	return result, nil
}
