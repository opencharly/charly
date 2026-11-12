package main

// cue_schema.go — the KERNEL's composed document schema, kept for exactly two in-core mechanisms:
// the plugin-schema SPLICE (plugin_loader.go unifies each plugin's served `schema_cue` onto this
// base to gate its authored input) and the structural-kind VALUE gate (provider_kind_invoke.go's
// validateKindValueCUE, which types a substrate/candy node's rich authored value against the
// #<Kind>Value def its provider declares, read from the COMPOSED base & units value, before
// threading it to the plugin). Both are clause-M kernel mechanisms — plugin loading and
// prescan-dispatch — so the schema they consume stays here with them.
//
// Everything ELSE this file used to hold left in K-wave 2, cone R1 (ruling 1): the kind→def table
// (registerCueKind / cueKindDefs / cueKindDef, fed by nine per-kind cue_kind_<name>.go init()
// files), the coreCueSchema() handle constructor, and the six same-named wrappers that threaded
// that handle through the ProjectLoader seam. The loader owns its own compiled schema now
// (sdk/loaderkit/cue_schema.go) and its CUE-validate entry points take no schema parameter, so
// core neither builds a handle nor forwards one — the remaining call sites reach the loader
// through requireProjectLoader() directly.
//
// The two copies never interoperate: a cue.Value is valid only within the cue.Context that built
// it, and no call path mixes a value from one with a definition from the other.
//
// THE BASE IS COMPOSED BY IMPORT (the concat → package cutover: TODO 2.5, D-SCH-2, DESIGN §10.3).
// `github.com/opencharly/spec/schema` is ONE CUE package in the `github.com/opencharly/spec` CUE
// module, so the base is loaded as a CUE MODULE INSTANCE — that package, in that module, under the
// language version the module declares — instead of every `schema/*.cue` body being concatenated
// into one package-less compilation unit. Both halves travel with the spec Go module: the package
// source (`schema.FS`) and the module that owns it (`specmodule.FS`, i.e. `cue.mod/module.cue`).
// The instance is composed over an in-memory OVERLAY, so no checkout, no module-cache entry and
// no host filesystem path is consulted at runtime.

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"

	specmodule "github.com/opencharly/spec"
	"github.com/opencharly/spec/schema"
)

// specModuleRoot is the VIRTUAL module root the embedded spec module is composed at. Nothing is
// read from the host filesystem: every file below it arrives through load.Config.Overlay (whose
// entries imply their parent directories), and a FIXED path — never a temp dir — keeps every CUE
// diagnostic deterministic and free of machine-specific paths.
const specModuleRoot = "/charly/spec-module"

// cueSchemaCtx is the kernel's CUE context — the one every splice/ingest/Unify call on
// sharedCueSchema must use. LAZY: composing the module instance costs ~39ms (measured on this
// tree: 0.2ms to read the embedded overlay + 39ms to load and build the package), and a command
// that touches neither the plugin splice nor a structural-kind value (`charly version`) must not
// pay it.
var cueSchemaCtx = sync.OnceValue(func() *cue.Context { return cuecontext.New() })

// sharedCueSchema is the base document schema composed by import: the `schema` package of the
// `github.com/opencharly/spec` CUE module, loaded as a module instance and compiled in
// cueSchemaCtx. A compose failure is a process invariant violation (panic): a command that reached
// validation without a base schema would have nothing to validate against.
var sharedCueSchema = sync.OnceValue(func() cue.Value {
	v, err := composeBaseSchema()
	if err != nil {
		panic(fmt.Sprintf("composing the base document schema: %v", errors.Details(err, nil)))
	}
	return v
})

// composeBaseSchema loads `github.com/opencharly/spec/schema` as a CUE module instance: one
// package of one module, evaluated under the language version that module declares. It fails loudly
// when the source does not arrive as a module — a package loaded without its module would silently
// fall back to the toolchain's default language version, which is the drift the module's pinned
// `language.version` exists to prevent.
func composeBaseSchema() (cue.Value, error) {
	overlay, err := specModuleOverlay()
	if err != nil {
		return cue.Value{}, err
	}
	return buildSpecModuleInstance(overlay)
}

// buildSpecModuleInstance loads ./schema out of an assembled module overlay. It is the ONE load
// path — production passes the real overlay, and the parity test passes variants of it to prove a
// change in the source is visible in the composed value.
func buildSpecModuleInstance(overlay map[string]load.Source) (cue.Value, error) {
	instances := load.Instances([]string{"./schema"}, &load.Config{
		Dir:     specModuleRoot,
		Overlay: overlay,
	})
	if len(instances) != 1 {
		return cue.Value{}, fmt.Errorf("loading ./schema from the spec module produced %d instances, want 1", len(instances))
	}
	inst := instances[0]
	if err := inst.Err; err != nil {
		return cue.Value{}, fmt.Errorf("loading github.com/opencharly/spec/schema: %w", err)
	}
	if inst.Module == "" || inst.ModuleFile == nil {
		return cue.Value{}, fmt.Errorf("github.com/opencharly/spec/schema loaded OUTSIDE its CUE module (module %q): the module's language.version must govern evaluation", inst.Module)
	}
	if inst.ModuleFile.Language == nil || inst.ModuleFile.Language.Version == "" {
		return cue.Value{}, fmt.Errorf("the spec CUE module declares no language.version, so the schema would silently evaluate under the toolchain's default semantics")
	}
	v := cueSchemaCtx().BuildInstance(inst)
	return v, v.Err()
}

// specModuleOverlay materializes the spec CUE module — the `schema` package source and the
// `cue.mod/module.cue` declaration that makes it a module — under specModuleRoot, from the two
// embedded trees the spec Go module ships. It WALKS both rather than naming files, so a file added
// to either side is carried without an edit here.
func specModuleOverlay() (map[string]load.Source, error) {
	overlay := map[string]load.Source{}
	if err := overlayFSTree(overlay, schema.FS, "schema"); err != nil {
		return nil, fmt.Errorf("reading the embedded spec schema: %w", err)
	}
	if err := overlayFSTree(overlay, specmodule.FS, "."); err != nil {
		return nil, fmt.Errorf("reading the embedded spec CUE module declaration: %w", err)
	}
	return overlay, nil
}

// overlayFSTree copies every regular file of fsys into the overlay under
// specModuleRoot/<dir>/<path>. CUE's overlay implies parent directories, so this creates no
// directory entry and touches no disk.
func overlayFSTree(overlay map[string]load.Source, fsys fs.FS, dir string) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		overlay[filepath.Join(specModuleRoot, dir, filepath.FromSlash(p))] = load.FromBytes(data)
		return nil
	})
}
