package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opencharly/sdk/spec"

	"cuelang.org/go/cue"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/schemaconcat"
)

// validatePluginCandy validates a candy's `plugin:` block. The CUE schema already
// checks the capability + source FORMAT (#Plugin / #PluginCapability); this adds
// the Go runtime checks the schema cannot express:
//   - each capability is well-formed (<class>:<word> with a known class);
//   - for source:builtin, the provider is ACTUALLY registered (its init()
//     compiled it into charly) — a builtin plugin candy naming a provider charly
//     does not ship is a hard error, not a silent no-op.
//
// An out-of-tree source (github.com/…) is NOT connected here — that is the
// loader's job at deploy/check time; validate only confirms the declaration is
// well-formed (a load-TIMING property of out-of-process plugins, not a
// schema-handling distinction — see validateAuthoredPluginInput).
func validatePluginCandy(name string, p *spec.Plugin) []string {
	if p == nil {
		return nil
	}
	var issues []string
	source := p.Source
	if source == "" {
		source = "builtin"
	}
	if len(p.Providers) == 0 {
		issues = append(issues, fmt.Sprintf("candy %q: plugin block declares no providers", name))
	}
	for _, capStr := range p.Providers {
		class, word, ok := splitCapability(string(capStr))
		if !ok {
			issues = append(issues, fmt.Sprintf("candy %q: plugin capability %q is malformed (want <class>:<word>)", name, capStr))
			continue
		}
		if source == "builtin" {
			if _, ok := providerRegistry.resolve(class, word); !ok {
				issues = append(issues, fmt.Sprintf(
					"candy %q: plugin declares builtin %s:%s but no such provider is compiled into charly", name, class, word))
			}
		}
	}
	return issues
}

// compileBasePlusServed compiles charly's BASE schema concatenated with served
// plugin schema source (base ++ served) — the unified value a plugin's authored
// plugin_input validates against. servedCUE is the package-less, SELF-CONTAINED
// .cue body a plugin shipped over the Describe channel (for a builtin, its
// embedded schema; for an external, the gRPC schema_cue) — NEVER read from a candy
// schema/ dir. Same concatenation contract as the runtime sharedCueSchema (R3).
func compileBasePlusServed(servedCUE string) (cue.Value, error) {
	baseBody, _, err := schemaconcat.ConcatSchema(schemaFS, ".", nil)
	if err != nil {
		return cue.Value{}, fmt.Errorf("base schema: %w", err)
	}
	v := cueSchemaCtx.CompileString(baseBody + "\n" + servedCUE)
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
	in := cueSchemaCtx.CompileBytes(inputJSON)
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
// on the full dependency graph (the sdk submodule, replace directives), so a content hash would
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
	release, lockErr := kit.AcquireFileLock(bin+".lock", true)
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
	buildVCS := "-buildvcs=false"
	buildEnv := pluginBuildEnv(os.Environ(), srcDir)
	if pluginSourceHasGitRevision(srcDir, buildEnv) {
		buildVCS = "-buildvcs=auto"
	}
	cmd := exec.CommandContext(ctx, "go", "build", buildVCS, "-o", binTmp, target)
	cmd.Dir = srcDir
	// A source with a real Git revision is VCS-stamped. An archive/copy without
	// one cannot truthfully be stamped, so it is explicitly built without VCS
	// metadata instead of borrowing an unrelated ancestor repository. Multiple
	// source builds may run concurrently; their read-only Git status probes never
	// refresh or write a shared index.
	cmd.Env = buildEnv
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(binTmp) // never leave a half-written temp binary behind
		return "", fmt.Errorf("plugin %q: go build in %s: %w\n%s", name, srcDir, err, out)
	}
	if err := os.Rename(binTmp, bin); err != nil {
		_ = os.Remove(binTmp)
		return "", fmt.Errorf("plugin %q: publish build (rename %s -> %s): %w", name, binTmp, bin, err)
	}
	return bin, nil
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

// pluginSourceHasGitRevision establishes whether a source has provenance that
// may truthfully be embedded. The probes use exactly the sanitized child
// environment: inherited GIT_* state may never lend a source someone else's
// repository identity.
//
// Go discovers a physical .git ancestor before it invokes Git. If that marker
// is malformed or unrelated, auto VCS mode fails instead of omitting metadata.
// A source without a usable committed Git worktree is therefore built with the
// explicit no-stamp policy selected by buildPluginBinary.
func pluginSourceHasGitRevision(srcDir string, env []string) bool {
	inside, ok := pluginGitProbe(srcDir, env, "rev-parse", "--is-inside-work-tree")
	if !ok || inside != "true" {
		return false
	}
	if _, ok := pluginGitProbe(srcDir, env, "status", "--porcelain"); !ok {
		return false
	}
	_, ok = pluginGitProbe(srcDir, env, "rev-parse", "--verify", "HEAD")
	return ok
}

// pluginGitProbe runs a Git query with the exact environment the Go child will
// inherit. It does not fall back to the parent process environment.
func pluginGitProbe(srcDir string, env []string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = srcDir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// bakedPluginDir is the FHS system path a candy's `bake_plugin:` step copies a
// pre-built provider binary to at image-build time, so a DEPLOYED container (which has
// neither the candy source nor a go toolchain) can run an external plugin its
// in-container charly needs at runtime — e.g. the charly-mcp service's `charly mcp
// serve`. `CHARLY_PLUGIN_DIR` overrides it (tests, non-FHS layouts).
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

// bakedPluginDirs returns the directories baked plugin binaries (+ their .providers word
// manifests) live in: $CHARLY_PLUGIN_DIR (override) then the FHS bakedPluginDir.
func bakedPluginDirs() []string {
	dirs := []string{}
	if d := os.Getenv("CHARLY_PLUGIN_DIR"); d != "" {
		dirs = append(dirs, d)
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
				// bakedPluginBinary's override-wins lookup, so $CHARLY_PLUGIN_DIR is a true override.
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
// the source scan (ResolveOpts.ExtraCandyRefs) — for a host out-call to a plugin whose candy the
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
	passes := []ResolveOpts{{}}
	if extraRef != "" {
		passes = append(passes, ResolveOpts{ExtraCandyRefs: []string{extraRef}})
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
// build-time multi-stage OpResolve leg (C10) is connected on-demand by deploykit EmitBuilderStages
// (ensureBuildersConnected), and the deploy-time OpCollectContext/OpReverse legs are connected
// PRECISELY + on-demand by the build pre-pass (builder_preresolve.go's ensureBuildersConnected,
// scoped to the deploy's actually-detected + distro-gated builders) — NOT surfaced across an
// entire box scan, which over-built unrelated builder plugins (e.g. aur on a fedora deploy).
//
// Word-keyed + class-AGNOSTIC by design: a plugin candy loads iff ANY of its provided
// words is in this set (pluginProvidesReferencedWord), regardless of class. Over-load
// (a matched-but-unused word) is harmless — the idempotency guard + a connect for an
// undispatched word — while under-load (a MISSED reference) breaks the verb/builder/
// substrate at dispatch, so collection errs toward INCLUDE: every enumerated site is
// unioned, and when in doubt a word is added rather than filtered.
func collectReferencedPluginWords(candies map[string]spec.CandyReader, boxes boxMap, extra []string) map[string]struct{} {
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
			continue
		}
		// Idempotent re-load: loadProjectPlugins runs on EVERY connect path (build,
		// deploy, check), and a single process that builds AND deploys connects twice
		// (e.g. `charly bundle add` → loadDeployPlugins, then the pod-overlay
		// NewGenerator's build-time connect seam). Skip a plugin already connected FROM
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
