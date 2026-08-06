package main

// cue_schema.go — the KERNEL's own compiled CUE schema, kept for exactly two in-core mechanisms:
// the plugin-schema SPLICE (plugin_loader.go unifies each plugin's served `schema_cue` onto this
// base to gate its authored input) and the structural-kind VALUE gate (provider_kind_invoke.go's
// validateKindValueCUE, which types a substrate/candy node's rich authored value against the kept
// #<Kind>Value def before threading it to the plugin). Both are clause-M kernel mechanisms —
// plugin loading and prescan-dispatch — so the schema they consume stays here with them.
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

import (
	"fmt"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"

	sdkschema "github.com/opencharly/spec/schema"
	"github.com/opencharly/spec/schemaconcat"
)

// cueSchemaCtx is the kernel's CUE context — the one every splice/ingest/Unify call on
// sharedCueSchema must use. LAZY: a compile costs ~11ms, and a command that touches neither the
// plugin splice nor a structural-kind value (`charly version`) must not pay it.
var cueSchemaCtx = sync.OnceValue(func() *cue.Context { return cuecontext.New() })

// sharedCueSchema is every schema/*.cue file unified into one value (the files carry no package
// clause, so they share one scope and the per-kind defs reference the shared #Step/#Context). The
// concatenation goes through the SINGLE contract shared with the dev-time generator
// (schemaconcat.ConcatSchema — R3), so the compiled schema can never drift from the generated Go
// types. sdkschema.FS is the CUE source exported by the spec module; its files sit at the FS root,
// so this concatenates with dir "." directly.
var sharedCueSchema = sync.OnceValue(func() cue.Value {
	body, _, err := schemaconcat.ConcatSchema(sdkschema.FS, ".", nil)
	if err != nil {
		panic(fmt.Sprintf("read embedded schema: %v", err))
	}
	v := cueSchemaCtx().CompileString(body)
	if v.Err() != nil {
		panic(fmt.Sprintf("CUE schema failed to compile: %v", errors.Details(v.Err(), nil)))
	}
	return v
})
