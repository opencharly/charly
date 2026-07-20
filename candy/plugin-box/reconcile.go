package box

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// reconcile.go — `charly box reconcile`, moved from charly/reconcile.go (Cutover B unit 3+4). It
// aligns cross-repo `@github` git-tag pins so every reference of a repo fetches ONE commit —
// clearing any per-entity-version warning from the resolver (which compares each candy's own
// `version:`, read after fetch). For each distinct repo referenced by the project's versioned
// YAML files, every pin of that repo is rewritten to ONE target version: the newest
// already-referenced version (default) or the newest tag on the remote (`--remote`). Edits are
// comment-preserving (yaml.v3 node API) and idempotent. Operates on the current project (cwd; the
// host's -C / --dir / CHARLY_PROJECT_DIR already resolved os.Getwd before this dispatches). For a
// multi-repo tree, run it per repo (e.g. `charly -C box/<name> box reconcile`).
//
// This word needs NO host reverse-channel coupling at all — it operates purely over sdk/kit
// (FileExists/IsGitSubmoduleDir/SortStrings/GitLatestTag/RepoGitURL/CompareSemver) +
// sdk/deploykit (IsRemoteCandyRef/StripVersion) + sdk/spec (ParseRemoteRef) + stdlib
// filesystem/YAML, exactly like the `new`/`labels` words above — zero HostBuild, zero
// InvokeProvider, zero core reentry.

// reconcileGrammar is the `charly box reconcile [--dry-run] [--remote]` CLI surface.
type reconcileGrammar struct {
	DryRun bool `name:"dry-run" help:"Print the pin rewrites without modifying any file."`
	Remote bool `help:"Align each repo's pins to its newest REMOTE tag (git ls-remote) instead of the newest already-referenced version."`
}

// dispatchReconcile kong-parses the reconcile grammar and runs the two-pass rewrite: collect the
// per-repo referenced version set, compute each repo's target version, then rewrite every pin
// that disagrees with its repo's target.
func dispatchReconcile(args []string) error {
	var g reconcileGrammar
	if done, err := parseLeaf("reconcile", &g, args); err != nil || done {
		return err
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	files := reconcileCandidateFiles(dir)
	if len(files) == 0 {
		return fmt.Errorf("no opencharly project files found in %s (run from a project directory or use -C)", dir)
	}

	// Pass 1: collect, per repo, the set of pinned versions referenced anywhere.
	refVersions := make(map[string]map[string]bool) // repoPath -> set of versions
	roots := make(map[string]*yaml.Node)            // file -> document root (reused in pass 2)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("reading %s: %w", f, err)
		}
		var root yaml.Node
		if err := yaml.Unmarshal(data, &root); err != nil {
			return fmt.Errorf("parsing %s: %w", f, err)
		}
		roots[f] = &root
		walkScalars(&root, func(s *yaml.Node) {
			if !deploykit.IsRemoteCandyRef(s.Value) {
				return
			}
			p := spec.ParseRemoteRef(s.Value)
			if p.Version == "" {
				return // unpinned ref — nothing to align
			}
			if refVersions[p.RepoPath] == nil {
				refVersions[p.RepoPath] = make(map[string]bool)
			}
			refVersions[p.RepoPath][p.Version] = true
		})
	}
	if len(refVersions) == 0 {
		fmt.Println("no @github pins found — nothing to reconcile")
		return nil
	}

	// Compute the target version per repo.
	target := make(map[string]string)
	for repo, vers := range refVersions {
		t, err := reconcileTargetVersion(g.Remote, repo, vers)
		if err != nil {
			return err
		}
		target[repo] = t
	}

	// Pass 2: rewrite every pin whose version != its repo's target.
	type rewrite struct{ file, from, to string }
	var rewrites []rewrite
	for _, f := range files {
		root := roots[f]
		fileChanged := false
		walkScalars(root, func(s *yaml.Node) {
			if !deploykit.IsRemoteCandyRef(s.Value) {
				return
			}
			p := spec.ParseRemoteRef(s.Value)
			if p.Version == "" {
				return
			}
			want := target[p.RepoPath]
			if p.Version == want {
				return
			}
			stripped, _ := deploykit.StripVersion(s.Value)
			newRef := stripped + ":" + want
			rewrites = append(rewrites, rewrite{filepath.Base(f), s.Value, newRef})
			if !g.DryRun {
				s.Value = newRef
			}
			fileChanged = true
		})
		if fileChanged && !g.DryRun {
			out, err := yaml.Marshal(root)
			if err != nil {
				return fmt.Errorf("serializing %s: %w", f, err)
			}
			if err := os.WriteFile(f, out, 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", f, err)
			}
		}
	}

	// Report per-repo targets (only the ones that were at >1 version).
	repos := make([]string, 0, len(target))
	for r := range target {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, r := range repos {
		if len(refVersions[r]) > 1 {
			fmt.Printf("%s -> %s (was at %d versions)\n", r, target[r], len(refVersions[r]))
		}
	}
	if len(rewrites) == 0 {
		fmt.Println("already reconciled — every repo's pins are at one version")
		return nil
	}
	if g.DryRun {
		fmt.Printf("would rewrite %d pin(s):\n", len(rewrites))
	} else {
		fmt.Printf("rewrote %d pin(s):\n", len(rewrites))
	}
	for _, rw := range rewrites {
		fmt.Printf("  %s: %s -> %s\n", rw.file, rw.from, rw.to)
	}
	return nil
}

// reconcileTargetVersion picks the version every pin of repo should align to: the newest remote
// tag when remote is set, else the newest already-referenced version (CalVer/semver via
// kit.CompareSemver).
func reconcileTargetVersion(remote bool, repo string, vers map[string]bool) (string, error) {
	if remote {
		latest, err := kit.GitLatestTag(kit.RepoGitURL(repo))
		if err != nil {
			return "", fmt.Errorf("resolving newest remote tag for %s: %w", repo, err)
		}
		if latest == "" {
			return "", fmt.Errorf("no tags on remote %s", repo)
		}
		return latest, nil
	}
	newest := ""
	for v := range vers {
		if newest == "" || kit.CompareSemver(v, newest) > 0 {
			newest = v
		}
	}
	return newest, nil
}

// reconcileCandidateFiles returns the versioned YAML files in dir that may carry `@github` refs.
// charly.yml is the single entry point (it carries the namespaced @github imports and every
// inline kind); the per-box and per-candy charly.yml manifests under the discovered box/ and
// candy/ directories carry the rest.
func reconcileCandidateFiles(dir string) []string {
	seen := map[string]struct{}{}
	if p := filepath.Join(dir, kit.UnifiedFileName); kit.FileExists(p) {
		seen[filepath.Clean(p)] = struct{}{}
	}
	// Scan every YAML under the discovered box/ and candy/ directories. A per-box charly.yml
	// can pin a @github `base:`, and a per-candy charly.yml can pin @github deps in its
	// require:/candy: lists (e.g. the cachyos keepassxc-keyring candy); both must be aligned
	// too — otherwise reconciliation is not FULLY automatic and the resolver still warns about
	// a version it cannot reach from the entry point. filepath.Walk on a missing directory is a
	// clean no-op (the root err arm returns nil).
	for _, sub := range []string{kit.DefaultBoxDir, kit.DefaultCandyDir} {
		root := filepath.Join(dir, sub)
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil {
				return nil
			}
			if info.IsDir() {
				if kit.IsGitSubmoduleDir(p, root) {
					return filepath.SkipDir
				}
				return nil
			}
			if ext := filepath.Ext(p); ext == ".yml" || ext == ".yaml" {
				seen[filepath.Clean(p)] = struct{}{}
			}
			return nil
		})
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	kit.SortStrings(out)
	return out
}

// walkScalars visits every scalar node in a YAML node tree.
func walkScalars(n *yaml.Node, fn func(*yaml.Node)) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode {
		fn(n)
		return
	}
	for _, c := range n.Content {
		walkScalars(c, fn)
	}
}
