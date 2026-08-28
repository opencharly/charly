package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/opencharly/spec/spec"

	"cuelang.org/go/cue"

	"github.com/opencharly/spec/fleet"
	"github.com/opencharly/spec/lock"
	sdkschema "github.com/opencharly/spec/schema"
	"github.com/opencharly/spec/schemaconcat"
)

// compileBasePlusServed compiles charly's BASE schema concatenated with served
// plugin schema source (base ++ served) — the unified value a plugin's authored
// plugin_input validates against. servedCUE is the package-less, SELF-CONTAINED
// .cue body a plugin shipped over the Describe channel (for a builtin, its
// embedded schema; for an external, the gRPC schema_cue) — NEVER read from a candy
// schema/ dir. Same concatenation contract as the runtime sharedCueSchema (R3).
func compileBasePlusServed(servedCUE string) (cue.Value, error) {
	baseBody, _, err := schemaconcat.ConcatSchema(sdkschema.FS, ".", nil)
	if err != nil {
		return cue.Value{}, fmt.Errorf("base schema: %w", err)
	}
	v := cueSchemaCtx().CompileString(baseBody + "\n" + servedCUE)
	return v, v.Err()
}

// pluginSchemaSet is the process-wide compiled plugin schema: base ++ Σ(every
// loaded unit's self-contained schema). Each plugin (builtin at process start,
// external at connect) adds its served schema through registerPluginUnitSchema,
// which recompiles the unified value. validateAuthoredPluginInput reads from it.
type pluginSchemaSet struct {
	mu        sync.Mutex
	sources   []string
	inputDefs map[string]string // provKey → def
	unified   cue.Value
}

var pluginSchemas = &pluginSchemaSet{inputDefs: map[string]string{}}

// registerPluginUnitSchema is THE plugin schema load gate — byte-identical for a
// builtin (in-proc) and an external (out-of-proc) unit (the zero-distinction
// requirement). It rejects an empty schema, a schema that will not splice onto the
// base (base ++ plugin), and a declared input def the schema does not define. On
// success it commits the unit's schema into the process-wide set and recompiles
// base ++ Σ. (directive: a proper schema is evaluated every time a plugin loads.)
func registerPluginUnitSchema(name string, s PluginSchema) error {
	if strings.TrimSpace(s.CueSource) == "" {
		// Stub-gate relaxation (schema-compaction cutover): a plugin declaring NO
		// authored input (no InputDef on any capability) needs no schema — the
		// former requirement forced ~46 near-identical stub files. A plugin that
		// DOES declare an input def must still serve the schema defining it.
		if len(s.InputDefs) > 0 {
			return fmt.Errorf("plugin %q declares input defs but served an EMPTY CUE schema", name)
		}
		return nil
	}
	pluginSchemas.mu.Lock()
	defer pluginSchemas.mu.Unlock()
	merged := append(append([]string(nil), pluginSchemas.sources...), s.CueSource)
	v, err := compileBasePlusServed(strings.Join(merged, "\n"))
	if err != nil {
		return fmt.Errorf("plugin %q: schema does not splice onto the base (base ++ plugin): %w", name, err)
	}
	for key, def := range s.InputDefs {
		d := v.LookupPath(cue.ParsePath(def))
		if d.Err() != nil {
			return fmt.Errorf("plugin %q: provides %s but its schema defines no %s: %w", name, key, def, d.Err())
		}
		// primary cross-check: a verb declaring a scalar-sugar primary must
		// define that field in its served input def, or the shorthand would
		// desugar to a key the closed def rejects.
		if word, ok := strings.CutPrefix(key, string(ClassVerb)+":"); ok {
			if prim, has := pluginPrimaryFor(word); has {
				if !cueDefHasField(d, prim) {
					return fmt.Errorf("plugin %q: verb %q declares primary %q but %s defines no such field", name, word, prim, def)
				}
			}
		}
	}
	pluginSchemas.sources = merged
	for key, def := range s.InputDefs {
		pluginSchemas.inputDefs[key] = def
	}
	pluginSchemas.unified = v
	return nil
}

// validateAuthoredPluginInput is THE only plugin_input validator — schema-source
// agnostic (the def comes from the process-wide set the load gate fills, so a
// builtin and an external are validated identically). A missing def, an
// uncompilable input, or a failed constraint (e.g. the externalprobe marker's
// `& !=""`) is a hard error, never a silent runtime surprise.
//
//nolint:unparam // class is the provider-key dimension (InputDefs are keyed by provKey(class,word)); the verb runtime seam (runPluginVerb) is the only caller today — kind/deploy/step/builder plugin_inputs validate through this SAME function when their seams wire.
func validateAuthoredPluginInput(class ProviderClass, word string, inputJSON []byte) error {
	pluginSchemas.mu.Lock()
	def, ok := pluginSchemas.inputDefs[provKey(class, word)]
	unified := pluginSchemas.unified
	pluginSchemas.mu.Unlock()
	if !ok {
		return fmt.Errorf("plugin %s:%s: no input def registered (schema not loaded)", class, word)
	}
	d := unified.LookupPath(cue.ParsePath(def))
	if d.Err() != nil {
		return fmt.Errorf("plugin %s:%s: schema missing %s: %w", class, word, def, d.Err())
	}
	in := cueSchemaCtx().CompileBytes(inputJSON)
	if in.Err() != nil {
		return fmt.Errorf("plugin %s:%s: input: %w", class, word, in.Err())
	}
	if err := d.Unify(in).Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("plugin %s:%s: plugin_input fails %s: %w", class, word, def, err)
	}
	return nil
}

// builtinGateOnce + loadBuiltinPluginUnits gate EVERY in-tree builtin plugin unit's
// schema at process start (directive: a schema is evaluated every time a plugin is
// loaded — a builtin is "loaded" at process start). It obtains each unit through
// InProcTransport — the builtin's Describe channel — so the schema reaches the
// SAME gate (registerPluginUnitSchema) an external's does. Builtin PROVIDERS are
// already registered at init() (RegisterBuiltinPluginUnit); this gates only their
// schemas. Idempotent (sync.Once); a broken builtin schema fails loudly here.
var (
	builtinGateOnce sync.Once
	builtinGateErr  error
)

func loadBuiltinPluginUnits() error {
	builtinGateOnce.Do(func() {
		for i := range builtinPluginUnits {
			unit, _, err := (&InProcTransport{Unit: &builtinPluginUnits[i]}).Connect(context.Background())
			if err != nil {
				builtinGateErr = err
				return
			}
			if err := registerPluginUnitSchema(builtinUnitName(unit), unit.Schema); err != nil {
				builtinGateErr = err
				return
			}
		}
	})
	return builtinGateErr
}

// builtinUnitName derives a stable error-message name for a builtin unit from its
// first provider capability (a unit has no separate name field — its candy does).
func builtinUnitName(u *PluginUnit) string {
	if len(u.Providers) > 0 {
		return provKey(u.Providers[0].Class(), u.Providers[0].Reserved())
	}
	return "<builtin>"
}

// safePluginBinName flattens a candy key (which may be an @github ref with
// slashes/colons) to a single filesystem-safe filename for the built binary.
func safePluginBinName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		default:
			return '_'
		}
	}, name)
}

// pluginBuildCacheDir is where built out-of-tree plugin binaries land.
func pluginBuildCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "charly", "plugins")
}

// pluginSourceTag returns a short, filesystem-safe digest of the plugin candy's resolved source
// directory, so the built binary's cache path is SCOPED BY SOURCE — the #76 root fix. The plugin
// build cache (~/.cache/charly/plugins/) was word-keyed shared-mutable state: two worktrees (or a
// bed/dev pair) building the SAME plugin word from DIFFERENT source raced the one `go build -o <bin>`
// output file (the #75 shared-image-tag collision class). Keying the path by source dir makes each
// worktree's build land its OWN file — same source reuses the path (the per-binary lock serializes
// the build), different source (a different worktree, or a remote @github ref fetched to a different
// cache dir) lands a distinct path, so the cross-worktree overwrite race is gone. The digest is of the
// ABSOLUTE srcDir (worktree-distinct by path), not the source CONTENT — go-build correctness depends
// on the full dependency graph (local replace targets, the proxy-resolved contract modules at their
// pinned require versions), so a content hash would
// cache-hit a STALE binary after a dependency bump; always-rebuild (no cache-hit skip) keeps it fresh.
func pluginSourceTag(srcDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(srcDir)))
	return hex.EncodeToString(sum[:8]) // 16 hex chars — collision-safe for a cache filename
}

// buildPluginBinary go-builds an out-of-tree plugin's provider binary on the HOST
// (never in a venue — the host owns the toolchain; the built binary is delivered
// into a venue by the in-venue transport). srcDir is the plugin candy's resolved
// dir, which is its own Go module (go.mod + a main serving via plugin/sdk).
func buildPluginBinary(ctx context.Context, srcDir, name string) (string, error) {
	cacheDir := pluginBuildCacheDir()
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("plugin %q: build cache: %w", name, err)
	}
	// The candy key may be an @github ref ("github.com/org/repo/candy/<name>") with slashes; flatten
	// it to ONE safe filename so `go build -o` lands a regular file in cacheDir (a slash would imply
	// non-existent nested dirs). SUFFIX it with the source-dir digest (#76) so the cache path is
	// scoped by source — two worktrees building the same plugin word land distinct files, never
	// racing the one shared output path.
	bin := filepath.Join(cacheDir, safePluginBinName(name)+"-"+pluginSourceTag(srcDir))
	// Serialize concurrent builds of the SAME plugin binary (same source → same path). Multiple check
	// beds (or a roster fan-out) can call buildPluginBinary for one plugin at once, racing the shared
	// `go build -o <bin>` output file — the observed failure mode was a plugin whose provider "did not
	// connect" mid-fan-out because its binary was momentarily half-written. A blocking per-binary file
	// lock makes the second builder wait for the first, never collide (R4: a synchronization primitive,
	// not a retry).
	release, lockErr := lock.AcquireFileLock(bin+".lock", true)
	if lockErr != nil {
		return "", fmt.Errorf("plugin %q: acquire build lock: %w", name, lockErr)
	}
	defer func() { _ = release() }()
	// Publish ATOMICALLY: build to a sibling temp file, then os.Rename onto `bin` (a same-directory
	// rename is atomic on POSIX). A reader (the LocalTransport.Connect that spawns the built binary)
	// then sees either the OLD complete binary or the NEW one — never a half-written file. This
	// closes the post-build read/write race: without it, a second worktree's rebuild could overwrite
	// `bin` in the window between this build's completion and the consumer's exec, handing the reader
	// a truncated binary (#76). The temp path lives under the per-binary lock, so two same-source
	// builders never race the temp file either.
	binTmp := bin + ".build"
	// An OUT-OF-PROCESS plugin binary builds STANDALONE in the candy's own module
	// (its go.mod + `replace …/charly => ../../charly`), NEVER in the repo
	// workspace: set GOWORK=off so a repo-root go.work — which lists only the
	// COMPILED-IN plugin candies (the `compiled_plugins:` selection) — cannot
	// reject a non-member candy dir ("current directory is contained in a module
	// that is not one of the workspace modules listed in go.work"). The dual
	// placement (compiled-in vs out-of-process) is exactly why the out-of-process
	// build must ignore the workspace.
	//
	// The serve shim lives conventionally at ./cmd/serve (the importable provider
	// package sits at the candy root for the in-proc placement; the shim wraps it
	// for the out-of-process one). Fall back to the candy root for a candy that has
	// not yet adopted the shim layout.
	target := "."
	if st, statErr := os.Stat(filepath.Join(srcDir, "cmd", "serve")); statErr == nil && st.IsDir() {
		target = "./cmd/serve"
	}
	buildEnv := pluginBuildEnv(os.Environ(), srcDir)
	buildVCS := pluginBuildVCSFlag(srcDir, buildEnv)
	// CONTENT-STAMPED REUSE. The cache path above is keyed by the source PATH (#76), which cannot
	// answer "is this binary still current"; the stamp answers that by digesting the candy's source
	// tree plus every module it reaches through a local `replace` (transitively: sdk, and sdk's
	// spec) together with the toolchain and build-relevant env. A match means a rebuild would
	// produce the same bytes, so it is skipped — the fix for the relink storm the roster measured
	// (36 relinks of ~30MB binaries in one window; 96GB / 6,791 files in the plugin cache).
	//
	// This does NOT weaken the #76 staleness rule it was written to protect: that rule forbids
	// keying freshness on the PATH, because a submodule bump leaves the path unchanged. A content
	// stamp changes on any bump — and on any uncommitted edit inside those trees, which a VCS-state
	// stamp would miss. The check runs INSIDE the per-binary lock, so a concurrent builder of the
	// same source cannot observe a half-published binary/stamp pair.
	//
	// A stamping error is never fatal: stamp stays empty, pluginBinaryIsFresh answers false, and
	// the build proceeds exactly as it did before — correct, just not reused.
	stamp, stampErr := pluginBuildStamp(srcDir, target, buildVCS, buildEnv)
	if stampErr != nil {
		stamp = ""
	}
	if pluginBinaryIsFresh(bin, stamp) {
		return bin, nil
	}
	cmd := exec.CommandContext(ctx, "go", "build", buildVCS, "-o", binTmp, target)
	cmd.Dir = srcDir
	// Multiple source builds may run concurrently; their read-only Git status
	// probes (when buildVCS selects -buildvcs=auto) never refresh or write a
	// shared index.
	cmd.Env = buildEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(binTmp) // never leave a half-written temp binary behind
		return "", fmt.Errorf("plugin %q: go build in %s: %w\n%s", name, srcDir, err, out)
	}
	if err := os.Rename(binTmp, bin); err != nil {
		_ = os.Remove(binTmp)
		return "", fmt.Errorf("plugin %q: publish build (rename %s -> %s): %w", name, binTmp, bin, err)
	}
	// Stamp AFTER the binary is published, so a crash between the two leaves a binary with no (or
	// an older) stamp — which reads as stale and rebuilds. The reverse order could claim a binary
	// that was never published.
	writePluginStamp(bin, stamp)
	return bin, nil
}

// pluginBuildVCSFlag picks the `go build -buildvcs=…` value for an out-of-process
// plugin binary build. It defers to pluginBuildVCSFlagForContext with the REAL
// test-binary state (testing.Testing(), Go stdlib — true only inside a "go
// test"-built binary, never in a real `charly` build); both contexts now resolve
// to the same safe, non-racing value. See pluginBuildVCSFlagForContext for the
// full rationale (charly#178 — the production `-buildvcs=auto` Go worktree bug
// was removed: all plugin builds use `-buildvcs=false`).
func pluginBuildVCSFlag(srcDir string, env []string) string {
	return pluginBuildVCSFlagForContext(srcDir, env, testing.Testing())
}

// pluginBuildVCSFlagForContext is the pure decision pluginBuildVCSFlag wraps,
// parameterized on whether the caller is a test binary so both branches are
// independently unit-testable (testing.Testing() is always true inside `go
// test`, so a test cannot otherwise observe the production branch). Both
// branches now resolve to the same value.
//
// ALL plugin builds (test AND production) use `-buildvcs=false`. Go's
// `-buildvcs=auto` walks `git status` on srcDir's repository to embed a VCS
// revision stamp; for a subdirectory target (./cmd/serve — the layout charly's
// out-of-process plugin builds use) this is a DETERMINISTIC Go bug, NOT a
// concurrency race: a SINGLE `go build -buildvcs=auto ./cmd/serve` fails with
// "error obtaining VCS status: exit status 128" (FATAL in Go 1.26, exit 1) in
// a linked worktree OUTSIDE the main repo path (e.g. /tmp/...), while it
// succeeds in the main checkout and in linked worktrees UNDER the main repo
// path. The plugin fails to load → "no provider registered for builder:…" →
// the check-pod canary FAILS at [image-build]. (The prior charly#178 comment
// framed this as a concurrent-race; the single-invocation reproduction refutes
// that — a race framing cannot explain one build failing alone.) An explicit
// `-buildvcs=false` argv flag always wins over GOFLAGS, so an env-var override
// could never have worked; skipping the git-status walk entirely removes the
// bug at its source (R4 — root-cause, NOT a worktree-location workaround: every
// teammate using a /tmp worktree re-hits it otherwise; a mutex in charly cannot
// serialize Go's internal walk).
//
// The stamp is NEVER read back out of a plugin binary — grep for
// debug.ReadBuildInfo / vcs.revision / vcs.modified / vcs.time / ReadBuildInfo
// across . + spec + sdk + plugins finds ZERO consumers (only this comment).
// Plugin binaries are transient provider processes (forked per Invoke, reaped
// on disconnect), not distributed artifacts; their VCS stamp is unobservable,
// so dropping it is safe. The previous charly#178 cutover fixed the test-binary
// path but left production as `-buildvcs=auto` "for a future consumer" — a
// consumer that does not and may never exist (YAGNI), and whose hypothetical
// need cannot outweigh the real, reproduced, bed-blocking Go worktree bug.
// Standing rule forward: all plugin builds use `-buildvcs=false`; no "future
// consumer" carve-out.
func pluginBuildVCSFlagForContext(srcDir string, env []string, isTestBinary bool) string {
	_ = srcDir
	_ = env
	_ = isTestBinary // historically branched; both contexts now use the safe value (charly#178).
	return "-buildvcs=false"
}

func pluginBuildEnv(base []string, srcDir string) []string {
	env := make([]string, 0, len(base)+4)
	for _, entry := range base {
		if strings.HasPrefix(entry, "GOWORK=") || strings.HasPrefix(entry, "GIT_") || strings.HasPrefix(entry, "PWD=") {
			continue
		}
		env = append(env, entry)
	}
	if absolute, err := filepath.Abs(srcDir); err == nil {
		srcDir = absolute
	}
	env = append(env, "GOWORK=off", "GIT_OPTIONAL_LOCKS=0", "PWD="+srcDir)
	return env
}

// bakedPluginDir is the FHS system path a candy's `bake_plugin:` step copies a
// pre-built provider binary to at image-build time, so a DEPLOYED container (which has
// neither the candy source nor a go toolchain) can run an external plugin its
// in-container charly needs at runtime — e.g. the charly-mcp service's `charly mcp
// serve`. `CHARLY_PLUGIN_DIR` PREPENDS a directory ahead of it (tests, non-FHS
// layouts) — it does NOT replace it, so setting it alone cannot HIDE a plugin baked
// here. `CHARLY_PLUGIN_ONLY=1` is what drops this path from the search; see
// bakedPluginDirs.
const bakedPluginDir = "/usr/lib/charly/plugins"

// bakedPluginFileName is the filename a baked plugin binary takes under bakedPluginDir.
// It keys by the plugin candy's LEAF name (the last path segment) — STABLE across how the
// candy is referenced: bare `plugin-mcp` in a local composition vs the qualified
// `github.com/org/repo/candy/plugin-mcp` scanned-set key under an @github composition. The
// build-side bake and the in-container loader resolve the candy under different keys
// (the build may see the @github ref while the in-container project sees it bare), so they
// agree ONLY on the leaf. Shared by emitBakedPlugins (bake) + bakedPluginBinary (load), R3.
func bakedPluginFileName(name string) string {
	return safePluginBinName(filepath.Base(name))
}

// bakedPluginDirs returns the SEARCH PATH baked plugin binaries (+ their .providers word
// manifests) live on: $CHARLY_PLUGIN_DIR first when set, then the FHS bakedPluginDir.
// The env var reorders precedence; it does not remove the FHS path from the search, because a
// deployed image's in-container charly must still find what the package baked there.
//
// $CHARLY_PLUGIN_ONLY=1 DROPS the FHS path, so the search is exactly $CHARLY_PLUGIN_DIR (or
// empty when that is unset). That is the supported way to resolve AS-IF-UNPACKAGED: on a host
// with the charly package installed, a baked word short-circuits the project scan
// (resolveCommandPluginBinary returns on the first baked hit), so a project's own declaration
// of that word is otherwise unreachable and untestable. Without this, the only way to observe
// the project path was to mask /usr/lib/charly/plugins with a mount namespace.
func bakedPluginDirs() []string {
	dirs := []string{}
	if d := os.Getenv("CHARLY_PLUGIN_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	if os.Getenv("CHARLY_PLUGIN_ONLY") == "1" {
		return dirs
	}
	return append(dirs, bakedPluginDir)
}

// bakedPluginBinary returns a pre-built provider binary for `name` if one was baked into
// the image (bake_plugin:), else "".
func bakedPluginBinary(name string) string {
	for _, d := range bakedPluginDirs() {
		p := filepath.Join(d, bakedPluginFileName(name))
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// bakedPluginBinaries maps a baked plugin's provider KEY (class:word, e.g. "command:mcp" or
// "verb:credential") → its binary path, populated by discoverBakedPluginWords from the
// `.providers` manifests baked beside each binary. It lets connectBakedPlugin connect a baked
// COMMAND plugin (the charly-mcp service's `charly mcp serve`) OR a baked VERB plugin (the
// credential store's verb:credential, served by candy/plugin-secrets) in a deployed container
// — or on a host where the plugin is installed alongside the charly binary
// (/usr/lib/charly/plugins) — that has NO candy source to scan. Keyed by class:word (not by
// bare word) because a word may exist in two classes; the lazy-connect resolves on (class, word).
var bakedPluginBinaries = map[string]string{}

// discoverBakedPluginWords reads the `.providers` word manifests baked beside each plugin
// binary (bake_plugin:, or a host install into /usr/lib/charly/plugins) and registers their
// declared external COMMAND words into the kong grammar (registerDeclaredExternalCommand) AND
// their external VERB words (registerDeclaredExternalVerb) — CHEAPLY, WITHOUT connecting any
// plugin (the connect is lazy, on the first dispatch / ResolveVerb miss, via connectBakedPlugin).
// It records class:word → baked-binary in bakedPluginBinaries, so a deployed container (or a
// project-less host where the plugin is installed beside charly) recognizes `charly <word>` for
// a baked command AND resolves verb:<word> for a baked verb (the credential store). A NO-OP when
// no plugins are baked (the dev-host / from-source case): the dirs are absent or hold no
// `.providers` files, and every existing charly invocation is byte-for-byte unchanged.
func discoverBakedPluginWords() {
	for _, dir := range bakedPluginDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".providers") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			binPath := filepath.Join(dir, strings.TrimSuffix(e.Name(), ".providers"))
			for _, line := range strings.Split(string(data), "\n") {
				class, word, ok := splitCapability(strings.TrimSpace(line))
				if !ok {
					continue
				}
				switch class {
				case ClassCommand:
					registerDeclaredExternalCommand(word)
				case ClassVerb:
					registerDeclaredExternalVerb(word)
				default:
					continue // only command + verb words are dispatched lazily by word today
				}
				// FIRST dir wins (CHARLY_PLUGIN_DIR ahead of the FHS path) — consistent with
				// bakedPluginBinary's first-hit lookup. Precedence only: a word baked under the
				// FHS path is still discovered when $CHARLY_PLUGIN_DIR lacks it.
				if _, seen := bakedPluginBinaries[provKey(class, word)]; !seen {
					bakedPluginBinaries[provKey(class, word)] = binPath
				}
			}
		}
	}
}

// loadBakedPluginBinary connects a baked plugin binary DIRECTLY (no source build) over
// LocalTransport, gates its served schema, and registers its providers — the lazy connect
// connectBakedPlugin pays when a baked command/verb is actually invoked. Returns true on success.
func loadBakedPluginBinary(ctx context.Context, bin string) bool {
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: baked plugin %s: connect: %v\n", bin, err)
		return false
	}
	if err := registerPluginUnitSchema(bin, unit.Schema); err != nil {
		_ = closer.Close()
		fmt.Fprintf(os.Stderr, "warning: baked plugin %s: schema gate: %v\n", bin, err)
		return false
	}
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, "local:"+bin, closer); err != nil {
		_ = closer.Close()
		fmt.Fprintf(os.Stderr, "warning: baked plugin %s: register: %v\n", bin, err)
		return false
	}
	return true
}

// connectBakedPlugin resolves the Provider for (class, word), lazily connecting a BAKED binary
// on a registry MISS — the verb/command-class generalization of the baked path the command
// dispatch formerly inlined. (1) already-registered (the eager path) → returned directly;
// (2) a baked binary (discoverBakedPluginWords mapped class:word → bin) → connect it DIRECTLY
// (no source build) and re-resolve. Returns (nil,false) when neither holds — the caller decides
// whether to fall through to a project-source build (connectPluginByWord) or fail. Shared by the
// `plugin:` verb runtime (runPluginVerb) AND the credential store's verb:credential resolve, so a
// baked plugin resolves with NO project scan (R3).
func connectBakedPlugin(class ProviderClass, word string) (Provider, bool) {
	if p, ok := providerRegistry.resolve(class, word); ok {
		return p, true
	}
	if bin, ok := bakedPluginBinaries[provKey(class, word)]; ok {
		if loadBakedPluginBinary(context.Background(), bin) {
			if p, ok := providerRegistry.resolve(class, word); ok {
				return p, true
			}
		}
	}
	return nil, false
}

// connectPluginByWord resolves the Provider for (class, word), lazily connecting it by any
// available means: (1) already-registered or a BAKED binary (connectBakedPlugin), then
// (2) BUILT from the project's candy source on a dev host with the repo checked out (the
// LoadConfig → ScanAllCandyWithConfigOpts → loadProjectPlugins scan, scoped to this one word so
// only the referenced plugin is built). The project dir is the post-chdir cwd (main resolved
// -C/--dir/--repo before dispatch). Returns (nil,false) on any failure — surfaced loudly by the
// caller (the credential store adapter). The ONE on-demand plugin-connect entry point for a word
// that appears in NO plan step (the credential/kube/tunnel/oci VERB the core adapter drives directly).
//
// `class` is a DELIBERATE generic-seam parameter, not dead weight: the F11 uniform-API gate
// (uniform_api_gate_test.go) documents this as the generic host-subsystem connect entry point a host
// adapter MAY call with any ProviderClass. Its callers now span classes — the credential/vm/kube/tunnel
// VERB out-calls (ClassVerb) AND dispatchLifecycleTarget's on-demand DEPLOY-substrate connect
// (ClassDeployTarget) for a `charly shell`/`cmd`/`logs` on an unconfigured image — so `class` genuinely
// varies and needs no unparam suppression.
func connectPluginByWord(class ProviderClass, word string) (Provider, bool) {
	return connectPluginByWordRef(class, word, "")
}

// connectPluginByWordRef is connectPluginByWord with an optional CANONICAL candy ref appended to
// the source scan (spec.ResolveOpts.ExtraCandyRefs) — for a host out-call to a plugin whose candy the
// project's closure references NOWHERE (e.g. a box/<distro> project that RPCs verb:libvirt but
// vendors no candy requiring candy/plugin-vm). connectBakedPlugin's registry-resolve-first check
// makes it idempotent: after the first connect, every subsequent call returns the registered
// provider without re-scanning (so it replaces the bespoke per-client sync.Once). extraRef "" is
// the plain connectPluginByWord. The ONE on-demand plugin-connect entry point for a word that
// appears in NO plan step (the credential/vm/kube host out-calls + any future host adapter).
func connectPluginByWordRef(class ProviderClass, word, extraRef string) (Provider, bool) {
	if p, ok := connectBakedPlugin(class, word); ok {
		return p, true
	}
	dir, err := os.Getwd()
	if err != nil {
		return nil, false
	}
	cfg, cerr := LoadConfig(dir)
	if cerr != nil {
		return nil, false
	}
	// Pass 1: the project's OWN candy closure (local candy/ dir — network-free). Pass 2 (ONLY when
	// a canonical ref is given AND pass 1 did not connect): pull the plugin candy in by its ref for
	// a project whose closure references it nowhere (a box/<distro> VM bed). This local-first order
	// keeps the common case network-free — adding the remote ref unconditionally would make a local
	// op (e.g. `charly vm list` in the main repo) attempt a github fetch even when candy/plugin-vm
	// is local. Mirrors the deleted ensureVmPluginConnected two-pass.
	passes := []spec.ResolveOpts{{}}
	if extraRef != "" {
		passes = append(passes, spec.ResolveOpts{ExtraCandyRefs: []string{extraRef}})
	}
	for _, opts := range passes {
		candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, opts)
		if scanErr != nil || candyMap == nil {
			continue
		}
		if perr := loadProjectPlugins(context.Background(), candyMap, map[string]struct{}{word: {}}); perr != nil {
			fmt.Fprintf(os.Stderr, "warning: plugin load (%s:%s): %v\n", class, word, perr)
		}
		if p, ok := providerRegistry.resolve(class, word); ok {
			return p, true
		}
	}
	return providerRegistry.resolve(class, word)
}

// resolvePluginBinary returns a plugin's provider binary: a BAKED binary (pre-built,
// baked into the image for a source/toolchain-less deployed container) if present, else
// built from the candy source on the host. The baked path is the enabler for running an
// external plugin INSIDE a deployed container.
func resolvePluginBinary(ctx context.Context, srcDir, name string) (string, error) {
	if baked := bakedPluginBinary(name); baked != "" {
		return baked, nil
	}
	if srcDir == "" {
		return "", fmt.Errorf("no baked binary (%s) and no source dir to build from", filepath.Join(bakedPluginDir, safePluginBinName(name)))
	}
	return buildPluginBinary(ctx, srcDir, name)
}

// loadPluginUnit loads ONE out-of-tree plugin: resolve its provider binary (baked-in or
// host-built), connect over LocalTransport, run the SAME schema gate a builtin runs, then
// register its providers. The schema travels over the Describe channel (gRPC
// schema_cue) — the host never reads the candy's schema/ dir.
func loadPluginUnit(ctx context.Context, name string, source string, srcDir string) error {
	bin, err := resolvePluginBinary(ctx, srcDir, name)
	if err != nil {
		return fmt.Errorf("plugin %q (source %s): %w", name, source, err)
	}
	unit, closer, err := (&LocalTransport{BinPath: bin}).Connect(ctx)
	if err != nil {
		return fmt.Errorf("plugin %q: connect: %w", name, err)
	}
	if err := registerPluginUnitSchema(name, unit.Schema); err != nil {
		_ = closer.Close()
		return err
	}
	if err := providerRegistry.RegisterPluginProviders(unit.Providers, source, closer); err != nil {
		_ = closer.Close()
		return fmt.Errorf("plugin %q: register: %w", name, err)
	}
	return nil
}

// collectReferencedPluginWords returns the COMPLETE set of plugin words the work at
// hand can reference, so loadProjectPlugins host-builds + connects ONLY the plugins
// it actually needs (perf-scoping). It unions every reference SITE:
//   - every candy's `external_builder:` selection (the BUILDER leg);
//   - every Op.Plugin across every candy PLAN step (the verb/step legs — all steps,
//     not just run:, so a build-emit run verb AND a deploy/runtime check verb count);
//   - every Op.Plugin across every box PLAN step (a box may author a plugin check
//     verb directly, baked into ai.opencharly.description and run at check live);
//   - the caller-supplied `extra` words (a deploy's substrate kind + the inline
//     Op.Plugin words in its FLATTENED bed plan — see deployNodePluginContext).
//
// The EXTERNALIZED detection-builders (cargo/npm/pixi/aur) are NOT collected here: their
// build-time multi-stage ops.OpResolve leg (C10) AND their deploy-time ops.OpCollectContext/
// ops.OpReverse legs are connected PRECISELY + on-demand at the moment of first Invoke, via
// InvokeProvider's own connectPluginByWordRef fallback carrying the builder's canonical ref
// (ops.InvokeProviderOpts.ExtraRef; K-wave 2 cone R1 deleted the separate host-side
// ensureBuildersConnected pre-pass, which was a second copy of that same fallback). Either way the
// connect is scoped to the builders actually detected + distro-gated — NOT surfaced across an
// entire box scan, which over-built unrelated builder plugins (e.g. aur on a fedora deploy).
//
// Word-keyed + class-AGNOSTIC by design: a plugin candy loads iff ANY of its provided
// words is in this set (pluginProvidesReferencedWord), regardless of class. Over-load
// (a matched-but-unused word) is harmless — the idempotency guard + a connect for an
// undispatched word — while under-load (a MISSED reference) breaks the verb/builder/
// substrate at dispatch, so collection errs toward INCLUDE: every enumerated site is
// unioned, and when in doubt a word is added rather than filtered.
func collectReferencedPluginWords(candies map[string]spec.CandyReader, boxes spec.BoxMap, extra []string) map[string]struct{} {
	refs := make(map[string]struct{})
	add := func(w string) {
		if w != "" {
			refs[w] = struct{}{}
		}
	}
	for _, w := range extra {
		add(w)
	}
	// addStep references a step's explicit plugin: word AND its closed-#Op verb discriminator. A
	// closed-#Op EXTERNAL check verb (libvirt/spice/kube/adb/appium) authored in a candy/box PLAN
	// is NOT a plugin: word, so without op.Kind() the perf-scoping never connects the candy serving
	// it — e.g. an android bed's `adb:`/`appium:` assertions live in the android-emulator candy
	// plan, and their plugins must load at BOTH the device deploy and check-live. This MIRRORS the
	// op.Kind() surfacing deployNodePluginContext already does for the deploy NODE's plan (R3).
	// Over-load safe: a builtin verb's candy is already registered; a non-plugin verb has no candy.
	addStep := func(op *spec.Op) {
		add(op.Plugin)
		if v, err := op.Kind(); err == nil {
			add(v)
		}
	}
	for _, candy := range candies {
		if candy == nil {
			continue
		}
		add(candy.GetExternalBuilder())
		plan := candy.PlanSteps()
		for i := range plan {
			addStep(&plan[i].Op)
		}
	}
	for _, raw := range boxes {
		box, ok := spec.DecodeBox(raw)
		if !ok {
			continue
		}
		for i := range box.Plan {
			addStep(&box.Plan[i].Op)
		}
	}
	return refs
}

// pluginProvidesReferencedWord reports whether ANY of a plugin candy's declared
// providers' words is in the referenced set — the perf-scoping predicate. Class is
// IGNORED (a word match in any class loads the unit): collection is the complete,
// over-load-safe side, so matching on the word alone can never UNDER-load on a class
// mismatch. A malformed capability string is skipped (validate flags it elsewhere).
func pluginProvidesReferencedWord(providers []string, refs map[string]struct{}) bool {
	for _, capability := range providers {
		if _, word, ok := splitCapability(capability); ok {
			if _, hit := refs[word]; hit {
				return true
			}
		}
	}
	return false
}

// loadProjectPlugins gates every builtin plugin unit's schema (process-start pass)
// and connects the out-of-tree plugin candies the work at hand REFERENCES (the words
// in refs) before checks/deploys/builds dispatch to their providers. It takes the
// scanned set (ScanAllCandyWithConfig) — which, unlike LoadUnified's project-local
// Candy map, includes @github-fetched candies and carries each candy's own .SourceDir
// + .Plugin (so a box that vendors all its candies via @github, like box/<distro>,
// still loads its plugins). refs (from collectReferencedPluginWords) SCOPES the load:
// a plugin candy NONE of whose providers is referenced is SKIPPED — a host `go build`
// + connect avoided for a word nothing dispatches (a box/<distro> set vendors many
// plugin candies — adb/appium/kube/spice/example-* — most unused by any one build or
// deploy). Errors are returned (not swallowed) so a bed asserting a plugin verb fails
// loudly if its REFERENCED plugin won't load.
// resolveMergedDeployTree returns the top-level Fleet (deploy-node) map — the merged project
// charly.yml + per-host operator overlay, ready for dotted-path traversal — the host-side
// merged-tree read the two remaining check host seams need (deployNodePluginContext below +
// check_venue_resolve.go's checkVenueExecFromReply). It replaces the DELETED deploy_tree.go
// host merged-tree read (#55 LOADER cone): instead of a host-resident sdk/deploykit projection+merge
// (the incomplete seam a floor-M read must not carry), it drives the LOADER CAPABILITY — the
// spec.ProjectLoader.ResolveMergedDeployTree seam (#55 coneA Q2(1)), which runs the
// loaderkit.ResolveMergedTreeViaExecutor project+overlay merge INSIDE the loader plugin over the
// in-proc host reverse channel (the SAME executorReverseServer path command:validate /
// command:fleet drive, threaded on ctx via specexec.ContextWithExecutor) — so the deploykit
// projection/overlay/merge lives INSIDE loaderkit, off charly core, and this read routes through
// the loader broker exactly like every Cone A Unit 3 dispatch reader. The in-proc executor reaches
// only the compiled-in loader-* host legs (it never runs the
// deploy-plugins-connect seam), so a PRE-CONNECT caller (deployNodePluginContext feeding
// loadDeployPlugins BEFORE any out-of-process plugin connects) never recurses.
//
// This file imports NO loaderkit (#55 coneA Q2(1) shed): the per-host operator-overlay merge
// (loaderkit.LoadHostFleetConfigViaExecutor + MergeDeployConfigs) that spec.ProjectLoader.LoadUnified
// does NOT expose (LoadUnified returns the PROJECT-only tree, loadmodel.go Fleet has no overlay
// field, so repointing to LoadUnified would DROP operator overrides — verified not byte-equivalent)
// is now reached through the ResolveMergedDeployTree seam method, not a direct loaderkit call.
// NOT the boundary-law "host-boundary-object" trap: the merge IS a loader mechanism the plugin
// drives; the shed landed once the seam exposed it.
//
// Relocated from check_cmd.go (#55 W3 B3): deployNodePluginContext's real significance is as
// loadDeployPlugins' (below) direct input — plugin-LOADER infrastructure, not a check-only
// concern despite the former file's name. check_cmd.go's resolveCheckRunnerContext still calls
// deployNodePluginContext directly (same package, different file).
func resolveMergedDeployTree(dir string) (map[string]spec.FleetNode, error) {
	ctx := hostInProcCtx()
	return requireProjectLoader().ResolveMergedDeployTree(ctx, dir)
}

// deployNodePluginContext resolves the deploy/bed node named `name` in the project at
// `dir` ONCE (the SAME project-fleet loader the deploy walker uses) and returns the
// two plugin-loading inputs the check runner (resolveCheckRunnerContext) and the deploy
// path (loadDeployPlugins) both need (R3 — one helper, both paths):
//
//   - addCandy: the deploy's `add_candy:` refs. The project candy scan
//     (ScanAllCandyWithConfig) collects only IMAGE-closure candies (CollectRemoteRefs
//     walks base/builder/require edges); add_candy candies are NOT in that set, so both
//     callers feed these to ScanAllCandyWithConfigOpts' ExtraCandyRefs to fetch them.
//   - refWords: the plugin WORDS the node references DIRECTLY — its substrate kind (an
//     external deploy-substrate plugin word, e.g. `exampledeploy`) + every inline
//     Op.Plugin in its FLATTENED plan. flattenFleetVenues hoists member/nested steps
//     into the root node.Plan, so this ONE walk covers the whole bed including members
//     (e.g. a `spice:` check verb authored inline). These scope loadProjectPlugins to
//     the plugins the deploy actually dispatches — caught here because they appear in
//     NEITHER a candy plan NOR a box plan (over-load safe, never under-load).
//
// Best-effort: (nil, nil) on any load failure or unknown name (the caller still
// collects candy + box references; a genuinely missing reference fails loudly at
// dispatch, never silently mis-deploys).
func deployNodePluginContext(dir, name string) (addCandy []string, refWords []string) {
	tree, err := resolveMergedDeployTree(dir)
	if err != nil || tree == nil {
		return nil, nil
	}
	// Resolve the named node, walking a DOTTED path into nested children (the bed runner
	// deploys a nested child via `charly fleet add <root>.<child>` — its name is dotted and
	// is NOT a top-level tree key). Without dotted resolution a nested-child deploy surfaces
	// NO plugin words and its substrate word never loads its provider (ResolveTarget →
	// "unknown target"). The single source for "given a (possibly dotted) deploy name, which
	// node?".
	node, ok := resolveDeployNodeByPath(tree, name)
	if !ok {
		return nil, nil
	}
	// The node's plugin words are collected below (no inSubmodule gate — the Phase-4 cutover
	// moved every substrate plugin candy out of the main repo, so BOTH main and submodule
	// contexts need the standalone-repo auto-inject; see the visit loop).
	// Collect the node's plugin words AND recurse into its nested children: a deploy whose
	// OWN substrate OR whose nested children's substrates are externalized must load each
	// serving plugin. Two cases this covers, GENERALLY (never substrate-special-cased):
	//   - a dotted child deploy (check-arch-vm.arch-host) — node IS the nested child, so its
	//     OWN target (e.g. `local`) is surfaced + its plugin auto-injected;
	//   - a single-process tree deploy (a pod root walked in one process, its nested children
	//     of a DIFFERENT substrate) — the recursion surfaces every child's substrate word.
	var visit func(n *spec.FleetNode)
	visit = func(n *spec.FleetNode) {
		if n == nil {
			return
		}
		addCandy = append(addCandy, n.AddCandy...)
		if n.Target != "" {
			refWords = append(refWords, n.Target)
			// An EXTERNALIZED deploy substrate (vm/local/android/kubernetes) is served by an
			// out-of-process plugin candy in its STANDALONE repo (the Phase-4 cutover moved every
			// candy/plugin-deploy-* out of the main repo into opencharly/plugin-deploy-*). Neither
			// the main repo nor a box/<distro> submodule scans that repo in its own closure — the
			// main repo's candy/ no longer holds plugin-deploy-* (deleted at the cutover) and a
			// submodule scans only its own + imported candies — so the substrate word would never
			// resolve to its provider without the auto-inject. Inject the canonical ref via
			// ExtraCandyRefs UNCONDITIONALLY (both contexts). In a check bed CHARLY_REPO_OVERRIDE
			// redirects the ref to the local superproject under development. The SAME
			// host-side-plugin pattern as vmPluginCandyRef (verb:libvirt), generalized to every
			// external substrate (R3).
			if ref, ok := externalDeploySubstratePluginRef(n.Target); ok {
				addCandy = append(addCandy, ref)
			}
		}
		for i := range n.Plan {
			op := &n.Plan[i].Op
			if w := op.Plugin; w != "" {
				refWords = append(refWords, w)
			}
			// Also surface each step's VERB discriminator. A closed-#Op EXTERNAL check verb
			// (libvirt/spice/kube/adb/appium) is NOT a `plugin:` word, so without this the
			// loader never build-connects the out-of-process plugin candy serving it — e.g. a
			// bed's `libvirt: list` step would SKIP with "unknown verb". Over-load safe: a
			// compiled-in verb's candy is already registered, and a non-plugin verb has none.
			if v, err := op.Kind(); err == nil && v != "" {
				refWords = append(refWords, v)
			}
		}
		for _, ck := range fleet.SortedNestedKeys(n.Children) {
			visit(n.Children[ck])
		}
	}
	visit(node)
	// NOTE: the externalized DETECTION-builder plugins (cargo/npm/pixi/aur) are NOT injected here.
	// A builder is triggered by the DEPLOY's resolved image closure (a pixi.toml / aur: section), not
	// by the deploy NODE this walk sees — and surfacing all four across a whole-box scan over-built
	// unrelated builder plugins (aur on a fedora deploy). The deploy-time pre-pass
	// (candy/plugin-fleet's preresolveBuilderContexts) instead detects EXACTLY the builders the
	// deploy triggers (distro-gated) and connects only those on-demand, by their canonical ref
	// (ops.InvokeProviderOpts.ExtraRef → connectPluginByWordRef), where it has the resolved closure.
	return addCandy, refWords
}

// resolveDeployNodeByPath resolves a (possibly DOTTED) deploy name to its FleetNode,
// descending node.Children for each dotted segment (the SAME nested-tree shape
// ResolveDeployChain walks). A bare name is the top-level entry; a dotted name
// (root.child[.grandchild…]) is the nested child the bed runner deploys via `charly fleet
// add <root>.<child>`. A leading "vm:" is stripped first via spec.SplitVmAddress (RCA #8/#9,
// FINAL/K5 unit 6a, live-probe-caught) — the SAME legacy-vm CLI-addressing convention
// resolveDelNode / spec.VmNameFromDeployName already honor elsewhere (`charly fleet del vm:<name>`
// / `vm:<parent.child>`): without stripping it, `tree["vm:"+parts[0]]` never matches (the tree
// is keyed by the plain name), so a "vm:"-prefixed dotted address silently resolved to
// nothing here — deployNodePluginContext (this function's one caller) then collected ZERO
// referenced plugin words for the deploy, and its substrate provider was never connected by
// loadDeployPlugins. resolveDelNode's OWN "vm:"-prefix shortcut masked the miss (it returns a
// synthetic Target-only placeholder without touching the tree at all), so the del RESOLVED
// fine while the CONNECT silently failed — the gap surfaced only later, when dispatch needed
// the never-connected provider. Returns false when any segment is absent.
func resolveDeployNodeByPath(tree map[string]spec.FleetNode, name string) (*spec.FleetNode, bool) {
	name, _ = spec.SplitVmAddress(name)
	parts := strings.Split(name, ".")
	root, ok := tree[parts[0]]
	if !ok {
		return nil, false
	}
	cur := &root
	for _, seg := range parts[1:] {
		child, ok := cur.Children[seg]
		if !ok || child == nil {
			return nil, false
		}
		cur = child
	}
	return cur, true
}

// loadDeployPlugins connects the project's OUT-OF-TREE plugin candies BEFORE a
// deploy verb resolves the target, so a deploy whose SUBSTRATE / step / verb is
// served by an external provider resolves out-of-process. It scans the WHOLE
// project (ScanAllCandyWithConfigOpts) but loads ONLY the plugin candies the
// deployment REFERENCES (perf-scoped): collectReferencedPluginWords unions the
// candy/box plans + candy external_builder selections, and deployNodePluginContext
// adds the deploy's OWN references — its substrate kind + the inline Op.Plugin words
// in its FLATTENED bed plan (members hoisted into the root node.Plan). A plugin candy
// none of whose providers is referenced is skipped (no wasted host build/connect); a
// REFERENCED one always loads (the reference set is collected COMPLETE — over-load
// safe, never under). The deployment's add_candy: candies + any caller-supplied extra
// refs are ADDED to the scan via ExtraCandyRefs (so a REMOTE composed plugin not in
// the local scan is fetched too, and its words are then collected from its plan). The
// SAME scan + loadProjectPlugins the check runner uses (resolveCheckRunnerContext) and
// the fleet-add path uses — so fleet add / fleet del / charly update all connect a
// deployment's plugins identically (R3). For an external deploy SUBSTRATE this is what
// turns the pre-scanned placeholder word into a connected grpcProvider that
// ResolveTarget can route to. Discovery and build/connect failures retain their original cause and
// abort dispatch; warning-and-continue used to mask a failed build as a downstream missing provider.
//
// FLOOR-M (Cone A shape 3 adjudication): this function directly drives loadProjectPlugins, which
// mutates providerRegistry — a clause-M kernel mechanism (plugin loading) that cannot live in a
// plugin. Relocated here (from the deleted charly/deploy_add_shared.go) to sit beside its callees
// collectReferencedPluginWords/loadProjectPlugins (R3) — already the body of two thin HostBuild
// seams (deploy-plugins-connect — the former deploy-del-resolve seam died with the del
// resolution moving to candy/plugin-fleet, K-wave 2 cone R2 bank C) and called directly by two
// more core files (pod_lifecycle_verb.go, update_deploy_dispatch.go).
func loadDeployPlugins(dir, deployName string, extraAddCandy []string) error {
	cfg, cerr := LoadConfig(dir)
	if cerr != nil {
		return fmt.Errorf("load plugin configuration: %w", cerr)
	}
	addCandy, refWords := deployNodePluginContext(dir, deployName)
	extra := append(append([]string(nil), extraAddCandy...), addCandy...)
	candyMap, scanErr := ScanAllCandyWithConfigOpts(dir, cfg, spec.ResolveOpts{ExtraCandyRefs: extra})
	if scanErr != nil {
		return fmt.Errorf("scan deploy plugins: %w", scanErr)
	}
	if candyMap == nil {
		return nil
	}
	refs := collectReferencedPluginWords(candyMap, cfg.Box, refWords)
	if perr := loadProjectPlugins(context.Background(), candyMap, refs); perr != nil {
		return fmt.Errorf("load deploy plugins: %w", perr)
	}
	return nil
}

func loadProjectPlugins(ctx context.Context, candies map[string]spec.CandyReader, refs map[string]struct{}) error {
	if err := loadBuiltinPluginUnits(); err != nil {
		return fmt.Errorf("builtin plugin schema gate: %w", err)
	}
	for name, candy := range candies {
		if candy == nil || !candy.IsPluginCandy() {
			continue
		}
		// Builtins are gated above (their schemas) and registered at init() (their
		// providers); only out-of-tree sources need build + connect + register.
		src := candy.GetPluginSource()
		if src == "" || src == "builtin" {
			continue
		}
		providers := candy.GetPluginProviders()
		// PERF-SCOPING: skip an out-of-tree plugin candy NONE of whose providers is
		// referenced by the work at hand — no wasted host build/connect for a word
		// nothing will dispatch. refs is collected COMPLETE (collectReferencedPluginWords
		// + deployNodePluginContext), so a skip here can never drop a referenced plugin
		// (over-load safe; under-load is a bug — see the HARD CONSTRAINT in those docs).
		if !pluginProvidesReferencedWord(providers, refs) {
			// Record WHAT was skipped and by which candy. A skip here is invisible at
			// the dispatch site — no warning is printed, because a skip is the normal,
			// correct case — so without this note an unresolved word later looks like a
			// broken plugin rather than one charly deliberately did not load.
			recordScopedOutPlugin(name, providers)
			continue
		}
		// Idempotent re-load: loadProjectPlugins runs on EVERY connect path (build,
		// deploy, check), and a single process that builds AND deploys connects twice
		// (e.g. `charly fleet add` → loadDeployPlugins, then candy/plugin-build's
		// resolveBuildEngine reaching hostBuildConnectPlugins for its own build-time
		// connect step — #55 step3 3-II deleted the former host-side NewGenerator that
		// used to run this). Skip a plugin already connected FROM
		// THE SAME SOURCE in this process — short-circuiting the whole build+connect+
		// schema-append+register before any of it runs a second time. A SAME word
		// already registered from a DIFFERENT origin is a genuine bijection collision
		// and errors here (preserving register's intent) before the wasteful re-build.
		connected, err := pluginAlreadyConnected(name, src, providers)
		if err != nil {
			return err
		}
		if connected {
			continue
		}
		if err := loadPluginUnit(ctx, name, src, candy.GetSourceDir()); err != nil {
			return err
		}
	}
	return nil
}

// pluginAlreadyConnected reports whether an out-of-tree plugin candy's declared
// providers are ALREADY registered in this process from candy.Plugin.Source — making a
// re-load a no-op. It checks EVERY declared capability: any one already registered from
// the SAME source means the unit is connected (loadPluginUnit registers a unit's
// providers together), so it returns true (skip); any one registered from a DIFFERENT
// origin is a real word→two-providers collision and returns an error. Returns
// (false, nil) when none of the plugin's providers are registered yet.
func pluginAlreadyConnected(name string, source string, providers []string) (bool, error) {
	connected := false
	for _, capability := range providers {
		class, word, ok := splitCapability(capability)
		if !ok {
			continue
		}
		origin, found := providerRegistry.registeredOrigin(class, word)
		if !found {
			continue
		}
		// COEXIST SWITCH: a word already registered as a COMPILED-IN plugin (origin
		// "builtin", registered at init() by registerCompiledPlugin from the
		// charly.yml `compiled_plugins:` selection) means this candy is compiled INTO
		// the running charly — the out-of-process host build + connect is redundant,
		// so SKIP it rather than reporting a collision. This is THE placement-coexist
		// path: a plugin NOT in compiled_plugins loads out-of-process here; one that IS
		// compiled in is served in-proc and skipped. Placement is a per-charly-build
		// choice, invisible above the registry.
		if origin == originBuiltin {
			connected = true
			continue
		}
		if origin != source {
			return false, fmt.Errorf("plugin %q provider %s:%s collides with one already registered from %q", name, class, word, origin)
		}
		connected = true
	}
	return connected, nil
}

// cueDefHasField reports whether the def value declares a (possibly optional)
// field with the given label.
func cueDefHasField(d cue.Value, field string) bool {
	it, err := d.Fields(cue.Optional(true), cue.Definitions(false))
	if err != nil {
		return false
	}
	for it.Next() {
		if it.Selector().Unquoted() == field {
			return true
		}
	}
	return false
}

// pluginScopedOut records out-of-tree plugin candies that loadProjectPlugins SKIPPED because
// none of their providers was referenced by the resolved work (the perf-scoping skip). Keyed
// by provKey(class, word) → candy name.
//
// It exists so an unresolved dispatch can distinguish "charly decided not to load it" from
// "the plugin is broken". Those are opposite problems with opposite fixes, and without this
// record they produce the same message.
var pluginScopedOut = map[string]string{}

// recordScopedOutPlugin notes every provider word a perf-scoped-out candy would have served.
func recordScopedOutPlugin(candyName string, providers []string) {
	for _, p := range providers {
		class, word, ok := splitCapability(strings.TrimSpace(p))
		if !ok {
			continue
		}
		if _, seen := pluginScopedOut[provKey(class, word)]; !seen {
			pluginScopedOut[provKey(class, word)] = candyName
		}
	}
}

// explainUnresolvedPluginWord renders the failure for a plugin word that resolved to no
// provider, naming the CAUSE rather than only the symptom.
//
// Four different situations reach this point and used to be indistinguishable:
//
//   - a project candy declares the word but perf-scoping dropped it (charly never loaded it),
//   - the word is in a `.providers` manifest but its binary is missing,
//   - the word is in no manifest on the search path at all,
//   - the search path itself is empty ($CHARLY_PLUGIN_ONLY=1 with no $CHARLY_PLUGIN_DIR).
//
// The bare "no provider registered" message reads as "the plugin is broken", when the common
// cause is "charly never looked where the plugin is" or "charly chose not to load it".
func explainUnresolvedPluginWord(class ProviderClass, word string) string {
	key := provKey(class, word)
	var b strings.Builder
	fmt.Fprintf(&b, "no provider registered for plugin %s %q", class, word)

	// 1. A project candy declares it, but nothing referenced it, so it was never loaded.
	// This is the cause the old message hid most completely: the plugin is fine.
	if candy, ok := pluginScopedOut[key]; ok {
		fmt.Fprintf(&b, "\n  cause: the project candy %q declares %s, but it was not loaded —"+
			" nothing in the resolved work referenced that word (perf-scoping)."+
			"\n  fix: reference the word from the plan being run, or add the candy to the"+
			" bed's add_candy: so the check runner passes it as an extra candy ref", candy, key)
		return b.String()
	}

	// 2. Otherwise report the baked search path exactly as bakedPluginDirs() walked it.
	dirs := bakedPluginDirs()
	if len(dirs) == 0 {
		b.WriteString("\n  cause: the baked-plugin search path is EMPTY —" +
			" $CHARLY_PLUGIN_ONLY=1 drops the FHS path and $CHARLY_PLUGIN_DIR is unset")
		return b.String()
	}

	b.WriteString("\n  searched (in order):")
	foundWord := ""
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			fmt.Fprintf(&b, "\n    %s  (unreadable: %v)", d, err)
			continue
		}
		words := []string{}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".providers") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(d, e.Name()))
			if err != nil {
				continue
			}
			bin := filepath.Join(d, strings.TrimSuffix(e.Name(), ".providers"))
			for _, line := range strings.Split(string(data), "\n") {
				c, w, ok := splitCapability(strings.TrimSpace(line))
				if !ok {
					continue
				}
				words = append(words, provKey(c, w)+" -> "+filepath.Base(bin))
				if provKey(c, w) == key {
					if _, statErr := os.Stat(bin); statErr != nil {
						foundWord = fmt.Sprintf("declared by %s but its binary %s is missing",
							e.Name(), bin)
					} else {
						foundWord = fmt.Sprintf("declared by %s and %s exists — the connect"+
							" or schema gate failed; see the preceding warning", e.Name(), bin)
					}
				}
			}
		}
		sort.Strings(words)
		if len(words) == 0 {
			fmt.Fprintf(&b, "\n    %s  (no .providers manifests)", d)
			continue
		}
		fmt.Fprintf(&b, "\n    %s  %s", d, strings.Join(words, ", "))
	}

	if foundWord != "" {
		fmt.Fprintf(&b, "\n  cause: %s", foundWord)
		return b.String()
	}
	fmt.Fprintf(&b, "\n  cause: %s is declared by no .providers manifest on that path, and no"+
		" project candy declares it either."+
		"\n  fix: for a host-built verb plugin, set $CHARLY_PLUGIN_DIR to a directory holding"+
		" the provider binary beside a <binary>.providers manifest listing %s", key, key)
	return b.String()
}
