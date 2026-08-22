// Package climodel (github.com/opencharly/spec/climodel, #55 import-purity) holds the
// ValidateGenerated CUE-validation helper that checks a generated SDK value against its
// authoritative CUE definition, and re-exports the CLISubcommand authoring struct (the
// SDK-facing form of the proto CLISubcommand wire type). It is a fabric slice of the spec
// contract module — its only heavy dep is cuelang.org/go + the spec/schema +
// spec/schemaconcat slices (#55 Rule 2) — relocated from the github.com/opencharly/sdk root
// package. charly core imports this slice INSTEAD of the sdk root; the sdk root keeps a
// thin re-export during cutover then is deleted.
//
// CLISubcommand itself is NOT defined here anymore — it was RELOCATED to spec/capability
// (#55 import-purity, Rule 2: the plain authoring struct has no cuelang dependency, so it
// lives in the cuelang-free spec/capability slice rather than this cuelang-bearing slice,
// keeping cuelang confined to ValidateGenerated). This slice re-exports it
// (`type CLISubcommand = capability.CLISubcommand`) so its existing consumers (sdk/schema.go's
// CLISubcommand alias, sdk/kong_reflect.go's KongSubcommands, and this slice's own
// TestCLISubcommandFields) compile UNCHANGED.
//
// CLIModel itself is NOT defined here — it is REPOINTed: the generated #CLIModel already
// lives in spec/spec/cue_types_gen.go, so charly references spec.CLIModel directly (R3 — no
// duplicate). This slice carries only the SDK-facing authoring helpers that surround it.
package climodel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/opencharly/spec/capability"
	"github.com/opencharly/spec/schema"
	"github.com/opencharly/spec/schemaconcat"
)

// CLISubcommand is one DECLARED child of a class="command" capability's own CLI word — the
// SDK-facing authoring form (a Name+Help pair). The proto wire form is pb.CLISubcommand
// (in spec/proto); this struct is the authoring shape a plugin constructs in its
// ProvidedCapability.Subcommands list, which BuildCapabilities marshals into the proto.
//
// Relocated to spec/capability (#55 import-purity, Rule 2: the plain authoring struct has no
// cuelang dependency, so it lives in the cuelang-free spec/capability slice, keeping cuelang
// confined to this slice's ValidateGenerated). Re-exported here so existing consumers (sdk/schema.go's
// CLISubcommand alias, sdk/kong_reflect.go's KongSubcommands, and this slice's own
// TestCLISubcommandFields) compile UNCHANGED.
type CLISubcommand = capability.CLISubcommand

var generatedSchema struct {
	sync.Once
	ctx   *cue.Context
	value cue.Value
	err   error
}

// ValidateGenerated validates a generated SDK value against its authoritative
// CUE definition. Command plugins use the same embedded schema as core, so
// moving command ownership never creates a hand-maintained validation copy.
func ValidateGenerated(definition string, value any) error {
	if err := loadGeneratedSchema(); err != nil {
		return err
	}
	return ValidateCUEValue(generatedSchema.ctx, generatedSchema.value, definition, value)
}

// DecodeGeneratedJSON strictly decodes one persisted or received JSON value
// into its generated Go type, then validates that typed value against the
// authoritative CUE definition. Typed decoding is required for fields such as
// []byte, whose standard JSON representation is base64 text but whose CUE value
// is bytes. Unknown fields and trailing JSON values are rejected before CUE
// validation so decoding cannot silently discard persisted input.
func DecodeGeneratedJSON(definition string, payload []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decode JSON for %s: %w", definition, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode JSON for %s: trailing JSON value", definition)
		}
		return fmt.Errorf("decode JSON for %s: trailing data: %w", definition, err)
	}
	return ValidateGenerated(definition, dst)
}

func loadGeneratedSchema() error {
	generatedSchema.Do(func() {
		generatedSchema.ctx = cuecontext.New()
		body, _, err := schemaconcat.ConcatSchema(schema.FS, ".", nil)
		if err != nil {
			generatedSchema.err = err
			return
		}
		generatedSchema.value = generatedSchema.ctx.CompileString(body)
		generatedSchema.err = generatedSchema.value.Err()
	})
	if generatedSchema.err != nil {
		return fmt.Errorf("compile SDK CUE schema: %w", generatedSchema.err)
	}
	return nil
}

// ValidateCUEValue encodes a Go value into CUE and validates it against a named
// definition in the compiled schema. Exported so the sdk root's SchemaValidator
// shim (and any other consumer) shares ONE validation path (R3 — no duplicate).
func ValidateCUEValue(ctx *cue.Context, schemaValue cue.Value, definition string, value any) error {
	input := ctx.Encode(value)
	if input.Err() != nil {
		return input.Err()
	}
	return ValidateCUEInput(schemaValue, definition, input)
}

// ValidateCUEInput validates an already-encoded CUE input against a named definition
// in the compiled schema. Exported so the sdk root's SchemaValidator shim shares ONE
// validation path (R3 — no duplicate).
func ValidateCUEInput(schemaValue cue.Value, definition string, input cue.Value) error {
	def := schemaValue.LookupPath(cue.ParsePath(definition))
	if !def.Exists() {
		return fmt.Errorf("CUE definition %s does not exist", definition)
	}
	if err := input.Unify(def).Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("%s: %w", definition, err)
	}
	return nil
}
