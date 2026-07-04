package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/opencharly/sdk/spec"
)

// Auto-exported variable names reserved for the generator.
// `vars:` entries may not shadow these. Every task-field `${VAR}` reference
// resolves against (auto-exports ∪ candy.Vars).
var taskAutoExports = map[string]bool{
	"USER":       true,
	"UID":        true,
	"GID":        true,
	"HOME":       true,
	"ARCH":       true, // BuildKit-style: amd64/arm64 — emitted as ENV ARCH=${TARGETARCH}
	"BUILD_ARCH": true, // uname-style: x86_64/aarch64 — only valid inside shell (cmd/download)
}

// taskVarRefPattern matches ${NAME} references in task fields.
var taskVarRefPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// taskKnownNames returns the set of ${NAME} references that resolve cleanly
// in task fields for this candy: auto-exports ∪ candy.Vars keys.
func taskKnownNames(vars map[string]string) map[string]bool {
	known := make(map[string]bool, len(taskAutoExports)+len(vars))
	for k := range taskAutoExports {
		known[k] = true
	}
	for k := range vars {
		known[k] = true
	}
	return known
}

// taskUnresolvedRefs returns the names of ${VAR} references in s that are
// not in known. Used by the validator to flag typos at config time.
func taskUnresolvedRefs(s string, known map[string]bool) []string {
	matches := taskVarRefPattern.FindAllStringSubmatch(s, -1)
	var out []string
	seen := make(map[string]bool)
	for _, m := range matches {
		name := m[1]
		if !known[name] && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// resolveUserSpec converts a task's `user:` field to (userDirective, chownPair).
//   - userDirective: the value to emit in `USER <value>` (numeric or name).
//   - chownPair: the numeric "<uid>:<gid>" for COPY --chown (empty if not needed).
//
// For root (empty or "root" or "0"): directive="0", chown="" (root:root is the
// default so COPY --chown= is omitted).
// For ${USER}: directive=<img.User>, chown=<img.UID>:<img.GID>.
// For numeric "uid:gid": directive as-is, chown as-is.
// For literal names: directive=<name>, chown="" (generator doesn't know the
// uid/gid, and COPY --chown supports literal names too so we fall back to
// --chown=<name>:<name> in that case).
func resolveUserSpec(userField string, img *ResolvedBox) (directive, chown string) {
	u := strings.TrimSpace(userField)

	// Special-case ${USER} to emit numeric UID directive (matches the
	// existing USER <UID> form at generate.go:1113 in the legacy path).
	// This also avoids a /etc/passwd dependency at the instant USER is
	// switched — base images create the user, but numeric always works.
	if u == "${USER}" {
		return strconv.Itoa(img.UID), fmt.Sprintf("%d:%d", img.UID, img.GID)
	}

	u = taskSubstAutoExports(u, img) // resolve ${USER}, ${UID}, ${GID}, ${HOME}

	if u == "" || u == "root" || u == "0" {
		return "0", ""
	}
	// Numeric uid:gid
	if strings.Contains(u, ":") {
		return u, u
	}
	// All-numeric uid
	if _, err := strconv.Atoi(u); err == nil {
		return u, u + ":" + u
	}
	// Literal name — emit as-is; COPY --chown supports <name>:<name>
	return u, u + ":" + u
}

// taskSubstAutoExports performs string substitution of the image-level
// auto-exports (USER, UID, GID, HOME) in s. ARCH and BUILD_ARCH are NOT
// substituted here — ARCH is delivered via ENV at candy-section top so
// Docker's substitution handles it; BUILD_ARCH is shell-only (cmd/download).
func taskSubstAutoExports(s string, img *ResolvedBox) string {
	if s == "" || img == nil {
		return s
	}
	repl := map[string]string{
		"USER": img.User,
		"UID":  strconv.Itoa(img.UID),
		"GID":  strconv.Itoa(img.GID),
		"HOME": img.Home,
	}
	return taskVarRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		if v, ok := repl[name]; ok {
			return v
		}
		return match
	})
}

// taskSubstPath resolves a destination/path field: runs auto-export
// substitution and expands leading ~/ to ${HOME}'s resolved value.
func taskSubstPath(p string, img *ResolvedBox) string {
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~/") && img != nil && img.Home != "" {
		p = img.Home + p[1:]
	}
	return taskSubstAutoExports(p, img)
}

// stageInlineContent writes write-task content to
// <buildDir>/_inline/<candy>/<sha256> on disk and returns the
// build-context-relative path suitable for COPY (e.g.
// ".build/<image>/_inline/<candy>/<sha256>"). Writes are idempotent —
// repeated calls with identical content are no-ops via content-addressed
// filenames.
//
// buildDir is the absolute path to .build/<boxName>/.
// contextRelPrefix is the build-context-relative prefix (e.g. ".build/<boxName>").
func stageInlineContent(buildDir, contextRelPrefix, candyName, content string) (string, error) {
	sum := sha256.Sum256([]byte(content))
	hexSum := hex.EncodeToString(sum[:])
	relToBuildDir := filepath.ToSlash(filepath.Join("_inline", candyName, hexSum))
	abs := filepath.Join(buildDir, relToBuildDir)
	contextRel := filepath.ToSlash(filepath.Join(contextRelPrefix, relToBuildDir))

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("staging inline content dir: %w", err)
	}
	// Idempotent: skip write if file already exists with identical content.
	if existing, err := os.ReadFile(abs); err == nil && string(existing) == content {
		return contextRel, nil
	}
	// Atomic: content-addressed path, so a concurrent same-dir build writing the
	// SAME sha sees no partial file (temp + rename); different content → different
	// sha → different path, so there is never a stale-content hazard.
	if err := atomicWriteFile(abs, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("staging inline content: %w", err)
	}
	return contextRel, nil
}

// buildArchExports returns a multi-line shell snippet that exports both
// BUILD_ARCH (uname-style: x86_64/aarch64) and ARCH (BuildKit-style:
// amd64/arm64/arm) so candy download URLs templating ${ARCH} match the
// build-time behavior. The build-time path gets ARCH from BuildKit's
// TARGETARCH (set via ENV in emitVarsEnv); the host/vm-deploy paths
// have no such mechanism, so this helper translates uname → BuildKit
// inline. Used by renderDownloadScript and renderTaskCommand on
// the local deploy target. Lines end in \n; suitable for direct
// strings.Builder.WriteString.
func buildArchExports() string {
	return "BUILD_ARCH=$(uname -m)\n" +
		"case \"$BUILD_ARCH\" in\n" +
		"  x86_64) ARCH=amd64 ;;\n" +
		"  aarch64) ARCH=arm64 ;;\n" +
		"  armv7l|armv7|armhf) ARCH=arm ;;\n" +
		"  *) ARCH=$BUILD_ARCH ;;\n" +
		"esac\n" +
		"export BUILD_ARCH ARCH\n"
}

// emitVarsEnv writes ENV directives for candy.Vars and the build-arg-sourced
// ARCH auto-export. Sorts vars by key for deterministic output.
// Emitted once per candy, before the candy's tasks block.
func emitVarsEnv(b *strings.Builder, vars map[string]string) {
	// ARCH comes from BuildKit's TARGETARCH automatic arg.
	// ARG TARGETARCH makes it visible in this stage; ENV propagates it
	// to subsequent directives and to RUN shells.
	b.WriteString("ARG TARGETARCH\n")
	b.WriteString("ENV ARCH=${TARGETARCH}\n")

	if len(vars) == 0 {
		return
	}
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := vars[k]
		// Quote the value if it contains spaces or shell-sensitive chars.
		// Simple strategy: always double-quote with basic escaping.
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		fmt.Fprintf(b, "ENV %s=\"%s\"\n", k, escaped)
	}
}

// --- Per-verb emitters ---
//
// Each emitter receives an already-verified task (Kind() has been called
// and returned OK, user has been resolved via resolveUserSpec). Coalescing
// functions receive a slice of same-verb, same-user tasks.

// emitMkdirBatch emits a single RUN mkdir -p for a batch of adjacent
// mkdir tasks that share the same user. When modes differ within the batch,
// splits per-mode chmod at the tail of the single RUN.
func emitMkdirBatch(b *strings.Builder, tasks []Op, img *ResolvedBox) {
	if len(tasks) == 0 {
		return
	}
	// Collect paths grouped by mode (default mode: empty = skip chmod).
	byMode := make(map[string][]string)
	var modeOrder []string
	var allPaths []string
	for _, t := range tasks {
		pth := taskSubstPath(t.Mkdir, img)
		allPaths = append(allPaths, pth)
		mode := t.Mode
		if _, ok := byMode[mode]; !ok {
			modeOrder = append(modeOrder, mode)
		}
		byMode[mode] = append(byMode[mode], pth)
	}
	parts := []string{"mkdir -p " + strings.Join(allPaths, " ")}
	for _, m := range modeOrder {
		if m == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("chmod %s %s", m, strings.Join(byMode[m], " ")))
	}
	b.WriteString("RUN " + strings.Join(parts, " && ") + "\n")
}

// emitCopy emits a COPY --from=<layer-stage> directive for an existing file
// in the candy directory. No RUN required — BuildKit handles the file
// transfer directly from the layer's scratch stage.
func emitCopy(b *strings.Builder, t Op, layerStage string, img *ResolvedBox) {
	src := t.Copy // relative to candy dir; do not substitute (filesystem path at generate time)
	dest := taskSubstPath(t.To, img)
	mode := t.Mode
	if mode == "" {
		mode = "0755"
	}

	_, chown := resolveUserSpec(t.RunAs, img)
	flags := []string{fmt.Sprintf("--from=%s", layerStage), fmt.Sprintf("--chmod=%s", mode)}
	if chown != "" {
		flags = append(flags, fmt.Sprintf("--chown=%s", chown))
	}
	fmt.Fprintf(b, "COPY %s %s %s\n", strings.Join(flags, " "), src, dest)
}

// emitWrite emits a COPY from the staged inline-content directory to the
// destination. Caller has already called stageInlineContent to produce the
// relative path. layerStage here is the final build-stage name and srcPath
// is the _inline/<candy>/<hash> path inside the build context (NOT from a
// layer stage — inline content lives in the image's .build directory).
func emitWrite(b *strings.Builder, t Op, srcPath string, img *ResolvedBox) {
	dest := taskSubstPath(t.Write, img)
	mode := t.Mode
	if mode == "" {
		mode = "0644"
	}

	_, chown := resolveUserSpec(t.RunAs, img)
	flags := []string{fmt.Sprintf("--chmod=%s", mode)}
	if chown != "" {
		flags = append(flags, fmt.Sprintf("--chown=%s", chown))
	}
	// srcPath is relative to the build context root (the image's .build/<image>/ dir).
	fmt.Fprintf(b, "COPY %s %s %s\n", strings.Join(flags, " "), srcPath, dest)
}

// emitLinkBatch emits a single RUN with chained ln -sf for a batch of
// adjacent link tasks sharing the same user.
func emitLinkBatch(b *strings.Builder, tasks []Op, img *ResolvedBox) {
	if len(tasks) == 0 {
		return
	}
	parts := make([]string, 0, len(tasks))
	for _, t := range tasks {
		link := taskSubstPath(t.Link, img)
		target := taskSubstPath(t.Target, img)
		parts = append(parts, fmt.Sprintf("ln -sf %s %s", target, link))
	}
	b.WriteString("RUN " + strings.Join(parts, " && ") + "\n")
}

// emitSetcapBatch emits a single RUN setcap … for a batch of adjacent
// setcap tasks. strip (empty caps) and set (non-empty caps) are chained
// via &&.
func emitSetcapBatch(b *strings.Builder, tasks []Op, img *ResolvedBox) {
	if len(tasks) == 0 {
		return
	}
	parts := make([]string, 0, len(tasks))
	for _, t := range tasks {
		pth := taskSubstPath(t.Setcap, img)
		if strings.TrimSpace(t.Caps) == "" {
			parts = append(parts, fmt.Sprintf("setcap -r %s 2>/dev/null || true", pth))
		} else {
			parts = append(parts, fmt.Sprintf("setcap %s %s", t.Caps, pth))
		}
	}
	b.WriteString("RUN " + strings.Join(parts, " && ") + "\n")
}

// taskCacheMounts renders a task's candy-declared `cache:` paths as BuildKit
// cache-mount flags, so ANY cmd:/download: task can persist heavy downloads or
// build artifacts across builds the SAME way package caches do (surviving an
// upstream layer cache-miss instead of re-fetching). Ownership follows the
// task's user: root → shared (sharing=locked), non-root → uid/gid-owned. The
// cache-USE logic (sentinel guards, copy-into-place) lives in the task body;
// this only emits the mount. Generic + config-driven — nothing candy-specific.
func taskCacheMounts(t Op, img *ResolvedBox) []string {
	if len(t.Cache) == 0 {
		return nil
	}
	directive, _ := resolveUserSpec(t.RunAs, img)
	root := directive == "0"
	out := make([]string, 0, len(t.Cache))
	for _, p := range t.Cache {
		p = taskSubstPath(p, img)
		if root {
			out = append(out, SharedCacheMount(p, "").String())
		} else {
			out = append(out, OwnedCacheMount(p, img.UID, img.GID).String())
		}
	}
	return out
}

// emitDownload emits one RUN per download task: fetch to a content-addressed
// /tmp/downloads cache, then extract. Honors candy-declared `cache:` mounts.
func emitDownload(b *strings.Builder, t Op, img *ResolvedBox) error {
	url := t.Download // no generate-time substitution — left for shell/ENV to handle
	dest := taskSubstPath(t.To, img)
	extract := strings.TrimSpace(t.Extract)

	// Exports are terminated with `;` so subsequent shell-level parameter
	// expansions (e.g. ${BUILD_ARCH} inside the URL) see the variable.
	// The bare `VAR=val cmd` prefix form sets VAR only in cmd's environment
	// *after* bash has already expanded any ${VAR} in cmd's arguments, which
	// leaves the URL with an empty arch string.
	var envPrefix strings.Builder
	envPrefix.WriteString("export BUILD_ARCH=$(uname -m);")
	var envForSh strings.Builder
	envForSh.WriteString("BUILD_ARCH=$(uname -m)")
	if len(t.Env) > 0 {
		keys := make([]string, 0, len(t.Env))
		for k := range t.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&envPrefix, " export %s=%s;", k, shellSingleQuote(t.Env[k]))
			fmt.Fprintf(&envForSh, " %s=%s", k, shellSingleQuote(t.Env[k]))
		}
	}

	// strip_components is valid for tar.* formats; append as --strip-components=N
	// so tarballs that nest everything under a version-or-arch top-level dir
	// (common for Go, most Rust/Go tools, Node.js binary releases) can drop
	// that wrapper and land their binaries directly at the dest path.
	stripFlag := ""
	if t.StripComponents > 0 {
		stripFlag = fmt.Sprintf(" --strip-components=%d", t.StripComponents)
	}

	// Resolve an empty extract: a tarball-looking URL → tar.gz, else none.
	if extract == "" {
		if strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz") {
			extract = "tar.gz"
		} else {
			extract = "none"
		}
	}
	if extract == "none" && dest == "" {
		return fmt.Errorf("download %q: extract=none requires `to:` destination", url)
	}

	// Content-addressed download cache in the /tmp/downloads mount: the file is
	// fetched ONCE (keyed by the URL's sha256) and reused across builds — the
	// SAME persistence package caches get — so an upstream layer cache-miss
	// never forces a re-download. Integrity-safe: curl writes <hash>.part and it
	// is atomically renamed to <hash> only on success, so a partial or corrupt
	// download (e.g. a flaky CDN) is never reused (next build re-fetches). The
	// previous implementation declared the /tmp/downloads mount but streamed
	// curl straight to tar / wrote to /tmp/dl.zip — the cache was never used.
	fetch := fmt.Sprintf(`%s mkdir -p /tmp/downloads; __u=%q; __c=/tmp/downloads/$(printf %%s "$__u" | sha256sum | cut -c1-64); [ -s "$__c" ] || { curl -fsSL "$__u" -o "$__c.part" && mv -f "$__c.part" "$__c"; }`, envPrefix.String(), url)

	var extractCmd string
	switch extract {
	case "tar.gz":
		extractCmd = fmt.Sprintf(`tar -xzf "$__c"%s -C %s %s`, stripFlag, dest, strings.Join(t.ExtractInclude, " "))
	case "tar.xz":
		extractCmd = fmt.Sprintf(`tar -xJf "$__c"%s -C %s %s`, stripFlag, dest, strings.Join(t.ExtractInclude, " "))
	case "tar.zst":
		extractCmd = fmt.Sprintf(`tar --zstd -xf "$__c"%s -C %s %s`, stripFlag, dest, strings.Join(t.ExtractInclude, " "))
	case "zip":
		extractCmd = fmt.Sprintf(`unzip -o "$__c" -d %s`, dest)
	case "sh":
		// Run the cached install script (exported env above is inherited; also
		// passed explicitly to mirror the prior `| ENV sh` form).
		extractCmd = fmt.Sprintf(`%s sh "$__c"`, envForSh.String())
	case "none":
		extractCmd = fmt.Sprintf(`cp -f "$__c" %s`, dest)
	default:
		return fmt.Errorf("download %q: unknown extract %q", url, extract)
	}

	cmd := fetch + " && " + extractCmd
	if t.Mode != "" {
		if extract == "none" {
			cmd += fmt.Sprintf(" && chmod %s %s", t.Mode, dest)
		} else if extract != "sh" {
			cmd += fmt.Sprintf(" && chmod -R %s %s", t.Mode, dest)
		}
	}

	cacheMounts := taskCacheMounts(t, img)
	mounts := make([]string, 0, 1+len(cacheMounts))
	mounts = append(mounts, SharedCacheMount("/tmp/downloads", "").String())
	mounts = append(mounts, cacheMounts...)
	b.WriteString("RUN " + strings.Join(mounts, " ") + " bash -c " + shellSingleQuote(cmd) + "\n")
	return nil
}

// emitCmd emits a single RUN for a cmd task, with the layer-stage /ctx bind
// mount plus cache mounts appropriate to the user (distro format caches for
// root, npm cache for non-root). Shell is bash invoked via Dockerfile
// heredoc syntax so the command works regardless of whether /bin/sh is bash
// (Fedora, Arch) or dash (Debian, Ubuntu). Heredoc body is processed by
// bash directly — no ANSI-C $'...' quoting involved, which dash doesn't
// understand and the OCI image format doesn't honor SHELL directives for.
// BUILD_ARCH is injected as a shell var so tasks using ${BUILD_ARCH} work.
func emitCmd(b *strings.Builder, t Op, layerStage string, img *ResolvedBox, userIsRoot bool) {
	var mounts []string
	mounts = append(mounts, fmt.Sprintf("--mount=type=bind,from=%s,source=/,target=/ctx", layerStage))

	if userIsRoot && img != nil && img.DistroDef != nil {
		if formatDef, ok := img.DistroDef.Format[img.Pkg]; ok {
			if cm := RenderCacheMounts(formatDef.CacheMount, -1, 0, " ", false); cm != "" {
				mounts = append(mounts, cm)
			}
		}
	} else {
		// Non-root: npm cache (matches old writeUserYml)
		mounts = append(mounts, OwnedCacheMount("/tmp/npm-cache", img.UID, img.GID).String())
	}

	// Candy-declared `cache:` mounts (generic, config-driven) — let a task
	// persist heavy downloads/build artifacts the same way package caches do.
	mounts = append(mounts, taskCacheMounts(t, img)...)

	b.WriteString("RUN ")
	for _, m := range mounts {
		b.WriteString(m + " ")
	}
	// Heredoc with 'OVCMD' (single-quoted delimiter) — body passed verbatim to
	// bash; no $ / `backtick` substitution happens during heredoc parsing, so
	// authors' $(cmd) / ${VAR} remain intact for bash itself to evaluate.
	b.WriteString("bash <<'OVCMD'\n")
	b.WriteString(buildArchExports())
	// Export task-declared env vars (including secret_requires values
	// resolved by InjectSecretsIntoPlans) BEFORE the user's cmd body
	// runs. Without this, references like `${K3S_CLUSTER_TOKEN}` inside
	// the cmd body hit "unbound variable" under `set -u`.
	if len(t.Env) > 0 {
		keys := make([]string, 0, len(t.Env))
		for k := range t.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "export %s=%s\n", k, shellSingleQuote(t.Env[k]))
		}
	}
	b.WriteString("set -e\n")
	b.WriteString(t.Command)
	if !strings.HasSuffix(t.Command, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("OVCMD\n")
}

// --- Orchestrator ---

// parentDirsForDest returns the dirname of dest, or "" if dest has no parent
// worth creating (e.g., destination is at root). Used for auto-mkdir.
func parentDirForDest(dest string) string {
	clean := path.Clean(dest)
	dir := path.Dir(clean)
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// taskCoalescesWith returns true if next can be batched with current under
// the adjacent-coalescing rule: same verb, same user, both verbs support
// batching. mkdir additionally requires same mode to share one chmod.
func taskCoalescesWith(current, next Op, currentVerb string) bool {
	nextVerb, err := next.Kind()
	if err != nil || nextVerb != currentVerb {
		return false
	}
	if current.RunAs != next.RunAs {
		return false
	}
	switch currentVerb {
	case "mkdir", "link", "setcap":
		return true
	}
	return false
}

// emitTasks renders layer.tasks to b in strict YAML order, with
// adjacent-coalescing for mkdir/link/setcap batches, parent-dir
// auto-insertion for copy/write, USER switches on user change, and
// implicit build-task auto-append if the candy has builders and no
// explicit build: in tasks:.
//
// Returns the final USER after processing (so writeCandySteps knows
// whether to emit USER root for the candy boundary reset).
// Returns an error if emission fails (only for download/write I/O).
//
//nolint:gocyclo // task verb dispatcher (9 verbs) in one loop managing shared runningUser/declaredDirs/index state; cases coupled to state transitions
func (g *Generator) emitTasks(b *strings.Builder, layer *Candy, img *ResolvedBox, ops []Op, buildDir, contextRelPrefix string) (string, error) {
	initialUser := "0" // candy-boundary starting USER (root); every caller starts at root
	if len(ops) == 0 && !g.candyHasImplicitBuild(layer, img) {
		return initialUser, nil
	}

	// Clone ops and append implicit build if needed.
	tasks := make([]Op, 0, len(ops)+1)
	tasks = append(tasks, ops...)
	hasExplicitBuild := false
	for _, t := range ops {
		if t.Build != "" {
			hasExplicitBuild = true
			break
		}
	}
	if !hasExplicitBuild && g.candyHasImplicitBuild(layer, img) {
		tasks = append(tasks, Op{Build: "all", RunAs: "${USER}"})
	}

	// Track known mkdirs to suppress parent-dir auto-insertion for
	// paths the author already declared. mkdir -p implicitly creates all
	// ancestors, so register those too (declaring /etc/foo/bar makes
	// /etc and /etc/foo both "declared" for auto-insert purposes).
	declaredDirs := make(map[string]bool)
	for _, t := range tasks {
		if t.Mkdir != "" {
			for p := taskSubstPath(t.Mkdir, img); p != "" && p != "/" && p != "."; p = path.Dir(p) {
				declaredDirs[p] = true
			}
		}
	}

	runningUser := initialUser
	i := 0
	for i < len(tasks) {
		t := tasks[i]
		verb, err := t.Kind()
		if err != nil {
			// Validation should have caught this; emit a comment and skip.
			fmt.Fprintf(b, "# skipping task %d: %v\n", i, err)
			i++
			continue
		}

		// Resolve USER for this task. Build tasks default to ${USER}.
		userField := t.RunAs
		if verb == "build" && userField == "" {
			userField = "${USER}"
		}
		directive, _ := resolveUserSpec(userField, img)
		if directive != runningUser {
			fmt.Fprintf(b, "USER %s\n", directive)
			runningUser = directive
		}

		// Comment (optional)
		if t.Comment != "" {
			b.WriteString("# " + t.Comment + "\n")
		}

		// Verb dispatch
		switch verb {
		case "mkdir":
			batch := []Op{t}
			for i+1 < len(tasks) && taskCoalescesWith(t, tasks[i+1], verb) {
				batch = append(batch, tasks[i+1])
				i++
			}
			emitMkdirBatch(b, batch, img)

		case "copy":
			// Auto-insert parent mkdir if not declared by the author.
			parent := parentDirForDest(taskSubstPath(t.To, img))
			if parent != "" && !declaredDirs[parent] && parent != img.Home && parent != "/" {
				emitMkdirBatch(b, []Op{{Mkdir: parent, RunAs: t.RunAs}}, img)
				declaredDirs[parent] = true
			}
			emitCopy(b, t, layer.Name, img)

		case "write":
			parent := parentDirForDest(taskSubstPath(t.Write, img))
			if parent != "" && !declaredDirs[parent] && parent != img.Home && parent != "/" {
				emitMkdirBatch(b, []Op{{Mkdir: parent, RunAs: t.RunAs}}, img)
				declaredDirs[parent] = true
			}
			srcPath, err := stageInlineContent(buildDir, contextRelPrefix, layer.Name, t.Content)
			if err != nil {
				return runningUser, err
			}
			emitWrite(b, t, srcPath, img)

		case "link":
			batch := []Op{t}
			for i+1 < len(tasks) && taskCoalescesWith(t, tasks[i+1], verb) {
				batch = append(batch, tasks[i+1])
				i++
			}
			emitLinkBatch(b, batch, img)

		case "setcap":
			batch := []Op{t}
			for i+1 < len(tasks) && taskCoalescesWith(t, tasks[i+1], verb) {
				batch = append(batch, tasks[i+1])
				i++
			}
			emitSetcapBatch(b, batch, img)

		case "download":
			if err := emitDownload(b, t, img); err != nil {
				return runningUser, err
			}

		case "build":
			// Builders are emitted by the existing builder block in
			// writeCandySteps; this is a marker the orchestrator honours by
			// yielding back to the caller's builder logic. Because our
			// emitTasks returns after the whole loop, we don't physically
			// emit anything here — writeCandySteps handles build placement
			// by calling emitBuild at the position of the explicit task (or
			// at end for implicit). Phase 0 keeps existing builder behaviour
			// unchanged, so build tasks are a no-op in this emitter.
			b.WriteString("# build: " + t.Build + " (handled by builder stage)\n")

		case "plugin":
			// `plugin: command` is the ONE install-task plugin verb: its act IS the full
			// emitCmd path (an install-task RUN), NOT a RenderProvisionScript. Rehydrate the
			// command-EXCLUSIVE plugin_input.command back onto an Op (command stays an #Op
			// modifier field) alongside the step-level RunAs/Cache/Env (which never moved),
			// then emit via the SAME emitCmd the literal command verb used. Localized special
			// case (the others below are RenderProvisionScript or pure-check) — NOT
			// duplication. in_container/background/from_host are check/runtime-only and play
			// no part in a Containerfile RUN, so they are intentionally not rehydrated here.
			if t.Plugin == "command" {
				// The act needs only the command string; read it straight off the
				// schema-validated plugin_input map (no dependency on the command
				// candy's params package, which is compiled-in only when selected).
				cmdStr, _ := t.PluginInput["command"].(string)
				emitCmd(b, Op{Command: cmdStr, RunAs: t.RunAs, Cache: t.Cache, Env: t.Env},
					layer.Name, img, runningUser == "0" || runningUser == "root")
				break
			}
			// Every OTHER plugin: <verb> install step renders its BUILD-context output
			// PLACEMENT-AGNOSTICALLY above the registry (a plugin works identically whether it
			// is compiled into charly OR dynamically loaded as an external candy):
			//   - a BUILTIN ProvisionActor renders an act SHELL emitted as a RUN — the in-proc
			//     fast path (resolveProvisionScript: the ONE Op→act-shell seam shared with the
			//     runtime act path runProvisionAct AND the local/vm deploy emit renderOpCommand,
			//     R3), zero JSON;
			//   - ANY OTHER resolved provider — an EXTERNAL grpcProvider (host-built + connected
			//     by the build-time plugin connect seam in NewGenerator), or a builtin emitting a
			//     richer Containerfile fragment — renders its fragment via Invoke(OpEmit), in-proc
			//     for a builtin or over go-plugin gRPC for an external, written verbatim
			//     (egress-validated with the rest of the Containerfile). This is the build half of
			//     the operator-authorized build-time plugin execution.
			// writeCandySteps→emitTasks is the REAL box-build path (the IR/OCITarget path is
			// deploy-only), so this is the single seam every Containerfile plugin-step emit flows
			// through. An unresolved verb ⇒ a loud error, never a silently-dropped run: step (R4).
			prov, ok := providerRegistry.ResolveVerb(t.Plugin)
			if !ok {
				return runningUser, fmt.Errorf("run: plugin verb %q is not registered (an external plugin not connected at build time?)", t.Plugin)
			}
			if actor, isActor := prov.(ProvisionActor); isActor {
				script, sok := actor.RenderProvisionScript(&t, img.Tags)
				if !sok {
					return runningUser, fmt.Errorf("run: plugin verb %q is not act-capable (ProvisionActor declined)", t.Plugin)
				}
				emitCmd(b, Op{Command: script, RunAs: t.RunAs}, layer.Name, img, runningUser == "0" || runningUser == "root")
			} else {
				frag, ferr := emitPluginFragment(prov, &t, img)
				if ferr != nil {
					return runningUser, fmt.Errorf("run: plugin verb %q build-emit: %w", t.Plugin, ferr)
				}
				b.WriteString(frag)
				if !strings.HasSuffix(frag, "\n") {
					b.WriteString("\n")
				}
			}

		default:
			fmt.Fprintf(b, "# unknown verb %q — skipping\n", verb)
		}
		i++
	}
	return runningUser, nil
}

// emitPluginFragment renders a plugin verb's BUILD-context Containerfile fragment
// via the provider's OpEmit Invoke — placement-agnostic above the registry (in-proc
// for a builtin, over go-plugin gRPC for an external connected by the build-time
// plugin connect seam in NewGenerator). The plugin receives its plugin_input as
// op.Params and a spec.BuildEnv descriptor as op.Env, and returns a spec.EmitReply
// whose Fragment is spliced verbatim into the generated Containerfile. The build-time
// half of the operator-authorized build-time plugin execution.
func emitPluginFragment(prov Provider, op *Op, img *ResolvedBox) (string, error) {
	params, err := marshalJSON(op.PluginInput)
	if err != nil {
		return "", fmt.Errorf("marshal plugin_input: %w", err)
	}
	var distros []string
	if img != nil {
		distros = img.Tags
	}
	return invokeOpEmitFragment(context.Background(), prov, op.Plugin, params, distros)
}

// invokeOpEmitFragment is the ONE OpEmit → EmitReply → empty-guard → Fragment path (R3),
// shared by the build-context VERB emit (emitPluginFragment, via emitTasks) AND the
// build-context external-STEP emit (OCITarget.emitExternalStep, F-STEP-EMIT). It Invokes
// the provider's OpEmit with the already-marshalled params (a verb's plugin_input, or a
// step's opaque Payload) and a spec.BuildEnv descriptor, decodes the EmitReply, and returns
// the Containerfile fragment — failing LOUDLY on an empty fragment (a runtime-/deploy-only
// capability wrongly asked to build-emit; never bake nothing silently, R4). ctx MAY carry an
// in-proc reverse channel (sdk.ContextWithExecutor) so a HOST-COUPLED plugin can call back
// HostBuild during its OpEmit; a PURE plugin ignores it and returns the fragment directly.
func invokeOpEmitFragment(ctx context.Context, prov Provider, word string, params []byte, distros []string) (string, error) {
	return invokeOpEmitFragmentOpt(ctx, prov, word, params, distros, false)
}

// invokeOpEmitFragmentOpt is the OpEmit → EmitReply → Fragment core shared by the guarding
// invokeOpEmitFragment and the pod-overlay OCITarget's compiler-emitted-step build-emit (R3).
// allowEmpty controls the empty-fragment guard: false (the default) fails LOUDLY on an empty
// fragment — a runtime-/deploy-only capability wrongly asked to build-emit; true permits an empty
// fragment, used by OCITarget for a COMPILER-EMITTED typed step whose render is legitimately empty
// for a given instance (an empty shell snippet, a packaged service with no overrides + enable=false,
// a custom service with no unit text — exactly the cases the former OCITarget.emit* returned nothing).
func invokeOpEmitFragmentOpt(ctx context.Context, prov Provider, word string, params []byte, distros []string, allowEmpty bool) (string, error) {
	env, err := marshalJSON(spec.BuildEnv{Distros: distros})
	if err != nil {
		return "", fmt.Errorf("marshal build env: %w", err)
	}
	res, err := prov.Invoke(ctx, &Operation{Reserved: word, Op: OpEmit, Params: params, Env: env})
	if err != nil {
		return "", err
	}
	var reply spec.EmitReply
	if res != nil && len(res.JSON) > 0 {
		if err := json.Unmarshal(res.JSON, &reply); err != nil {
			return "", fmt.Errorf("decode OpEmit reply: %w", err)
		}
	}
	if !allowEmpty && strings.TrimSpace(reply.Fragment) == "" {
		return "", fmt.Errorf("plugin %q returned an empty OpEmit fragment — it has no build-context act (a runtime-only verb in a build run: step, or a deploy-only step declaring emits without an OpEmit fragment? use context: [runtime] / set emits=false)", word)
	}
	return reply.Fragment, nil
}

// candyHasImplicitBuild returns true if the candy has a detection file
// (pixi.toml, package.json, Cargo.toml, aur: config) that would trigger a
// builder auto-append. Phase 0: placeholder that returns false — builders
// continue to run via the existing writeCandySteps builder block. Phase 2
// migrations will switch on this once explicit build: tasks appear.
func (g *Generator) candyHasImplicitBuild(_ *Candy, _ *ResolvedBox) bool {
	return false
}
