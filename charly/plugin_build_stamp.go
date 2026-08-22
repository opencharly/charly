package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// plugin_build_stamp.go — the CONTENT stamp that lets buildPluginBinary reuse a cached
// out-of-process plugin binary instead of relinking it on every charly subprocess.
//
// WHY. buildPluginBinary was always-rebuild by design: pluginSourceTag keys the cache path by the
// ABSOLUTE srcDir so two worktrees never race one output file (#76), and its header explains that a
// path key cannot also serve as a freshness key — go-build correctness depends on the whole
// dependency graph (the sdk submodule reached through the candy's local `replace`, and the
// spec contract module resolved from the module proxy at the require version pinned in the
// candy's own go.mod — a file the source hash covers), so skipping a rebuild on a path match
// would hand back a STALE binary after a sdk bump or a spec require bump. That
// reasoning is right, and this stamp does not weaken it: it keys freshness on CONTENT, so a
// submodule bump or a go.mod require bump — or any uncommitted edit — changes the stamp and
// forces the rebuild.
//
// The cost of always-rebuilding is not theoretical: every charly subprocess relinked every plugin
// it needed (~30MB each), the roster window recorded 36 relinks, and the plugin cache reached 96GB
// / 6,791 files. That relink storm is the single largest self-inflicted load on a bed roster.
//
// WHY CONTENT AND NOT VCS STATE. Hashing the sdk submodule's git HEAD would be cheaper and would
// catch a sdk bump, but it is WRONG here: the sdk submodule carries uncommitted working-tree
// edits for most of a development session, and a HEAD-keyed stamp would happily reuse a binary
// built before those edits — reintroducing exactly the staleness #76 warned about, in the case that
// matters most day to day. Content hashing catches both (and the spec module is not in VCS state
// at all — it resolves from the proxy at a pinned require version).

// pluginBuildStampVersion prefixes every stamp so a change to the stamping RULES (a new input, a
// different traversal) invalidates every existing stamp rather than silently comparing
// incomparable digests.
const pluginBuildStampVersion = "charly-plugin-build-stamp-v1"

// pluginBuildStampEnvKeys are the environment variables that change the produced binary. They are
// folded into the stamp so a build under a different GOOS/GOARCH/CGO setting never reuses another's
// output. GOWORK is included because the out-of-process build forces it off — a future change to
// that decision must invalidate.
var pluginBuildStampEnvKeys = []string{"GOOS", "GOARCH", "GOARM", "GOAMD64", "CGO_ENABLED", "GOFLAGS", "GOEXPERIMENT", "GOWORK"}

// pluginBuildStamp digests everything that determines the built binary: the Go toolchain, the
// build-relevant environment, the build target and vcs flag, and the CONTENT of the candy's own
// source tree plus every module it reaches through a local `replace` directive (transitively — the
// candy replaces sdk; the spec contract module resolves from the module proxy at the require
// version pinned in the candy's own go.mod, which the source-tree hash covers).
//
// An error is never fatal to the caller: buildPluginBinary degrades to its previous always-rebuild
// behavior, which is correct, just slower.
func pluginBuildStamp(srcDir, target, buildVCS string, env []string) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n", pluginBuildStampVersion)
	fmt.Fprintf(h, "toolchain=%s\n", runtime.Version())
	fmt.Fprintf(h, "target=%s\nbuildvcs=%s\n", target, buildVCS)
	for _, key := range pluginBuildStampEnvKeys {
		fmt.Fprintf(h, "env:%s=%s\n", key, envValue(env, key))
	}
	roots, err := pluginSourceRoots(srcDir)
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		// The root's own path is deliberately NOT hashed — only its label and content. Two
		// worktrees with byte-identical trees SHOULD stamp alike; they already land distinct
		// cache paths via pluginSourceTag, so path-distinctness is the path key's job, not the
		// freshness key's.
		fmt.Fprintf(h, "root:%s\n", filepath.Base(root))
		if err := hashDirContent(h, root); err != nil {
			return "", fmt.Errorf("hashing %s: %w", root, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pluginSourceRoots returns srcDir plus every local-path `replace` target reachable from it,
// transitively and deduplicated, in a stable order.
func pluginSourceRoots(srcDir string) ([]string, error) {
	abs, err := filepath.Abs(srcDir)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var order []string
	var walk func(dir string) error
	walk = func(dir string) error {
		dir = filepath.Clean(dir)
		if seen[dir] {
			return nil
		}
		seen[dir] = true
		order = append(order, dir)
		for _, rep := range localReplaceDirs(dir) {
			if err := walk(rep); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(abs); err != nil {
		return nil, err
	}
	sort.Strings(order)
	return order, nil
}

// localReplaceDirs parses dir/go.mod and returns the absolute directories of its LOCAL-path
// replace targets (`replace <mod> => ../../sdk`). A version-replace (`=> other/mod v1.2.3`) is not
// a local path and is already pinned by go.sum, which lives inside dir and is hashed with it.
//
// This is a deliberately small hand parser rather than a modfile dependency: the only construct it
// must understand is a replace arrow whose right-hand side starts with "./" or "../", in both the
// single-line and parenthesized-block forms.
func localReplaceDirs(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil
	}
	var out []string
	inBlock := false
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
		}
		switch {
		case strings.HasPrefix(line, "replace ("):
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "replace "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "replace "))
		case !inBlock:
			continue
		}
		_, rhs, ok := strings.Cut(line, "=>")
		if !ok {
			continue
		}
		rhs = strings.TrimSpace(rhs)
		// A local replace is a path; a module replace carries a version after the path.
		if !strings.HasPrefix(rhs, "./") && !strings.HasPrefix(rhs, "../") && !strings.HasPrefix(rhs, "/") {
			continue
		}
		target := strings.Fields(rhs)[0]
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		out = append(out, filepath.Clean(target))
	}
	return out
}

// hashDirContent feeds every file under root into h: its root-relative path, its executable bit,
// and its bytes. Traversal is lexical (filepath.WalkDir), so the digest is order-stable.
//
// .git is skipped — it is huge, it churns on every index refresh, and it says nothing about the
// bytes the compiler reads. A submodule bump still invalidates, because it rewrites the WORKING
// TREE this walk hashes.
func hashDirContent(h io.Writer, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		// A symlink's target bytes are not read here; hash its link text instead, so a retargeted
		// link invalidates.
		if info.Mode()&os.ModeSymlink != 0 {
			dest, lerr := os.Readlink(path)
			if lerr != nil {
				return lerr
			}
			fmt.Fprintf(h, "L %s %s\n", rel, dest)
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer func() { _ = f.Close() }()
		fh := sha256.New()
		if _, cerr := io.Copy(fh, f); cerr != nil {
			return cerr
		}
		fmt.Fprintf(h, "F %s %o %s\n", rel, info.Mode().Perm()&0o111, hex.EncodeToString(fh.Sum(nil)))
		return nil
	})
}

// envValue reads key out of an exec-style environment slice ("K=V"), last-wins like exec does.
func envValue(env []string, key string) string {
	prefix := key + "="
	val := ""
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			val = e[len(prefix):]
		}
	}
	return val
}

// pluginStampPath is the sidecar recording the stamp of the binary at bin.
func pluginStampPath(bin string) string { return bin + ".stamp" }

// pluginBinaryIsFresh reports whether bin exists and was built from exactly this stamp. Any doubt —
// a missing binary, a missing or unreadable stamp, a mismatch — answers false, so the fallback is
// always the correct-but-slower rebuild.
func pluginBinaryIsFresh(bin, stamp string) bool {
	if stamp == "" {
		return false
	}
	if st, err := os.Stat(bin); err != nil || st.IsDir() || st.Size() == 0 {
		return false
	}
	recorded, err := os.ReadFile(pluginStampPath(bin))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(recorded)) == stamp
}

// writePluginStamp records the stamp beside a freshly-published binary, atomically (temp + rename)
// so a concurrent reader sees either the old stamp or the new one, never a partial digest. A write
// failure is not fatal: the next build simply re-stamps.
func writePluginStamp(bin, stamp string) {
	if stamp == "" {
		return
	}
	path := pluginStampPath(bin)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(stamp+"\n"), 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}
