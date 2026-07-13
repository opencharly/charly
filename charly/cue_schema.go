package main

// CUE-validation Core. One compiled schema instance (every schema *.cue file
// from the SDK's schema package unified — shared #Step lives once in
// _common.cue, R3), a kind registry populated by each cue_kind_<name>.go via
// init(), and a per-entity validator. Per-entity validation extracts an entity
// (the `candy:` value of a legacy kind-keyed file, or each value of a
// `pod:`/`k8s:`/… collection map) and unifies it with #<Kind>; a unified
// node-form document is validated whole against #NodeDoc — the sole load gate.
// The legacy shape-routing + hand-written validators are deleted; CUE is the
// single schema source, and it travels WITH the SDK (github.com/opencharly/sdk
// owns schema + the generated spec types in one module).

import (
	"fmt"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	cueyaml "cuelang.org/go/encoding/yaml"
	"gopkg.in/yaml.v3"

	sdkschema "github.com/opencharly/sdk/schema"
	"github.com/opencharly/sdk/schemaconcat"
)

// schemaFS is the CUE schema source, exported by the SDK module (the contract
// repo) — files sit at the FS root, so consumers concatenate with dir ".".
var schemaFS = sdkschema.FS

// cueSchemaCtx is the process-wide CUE context (schemas compile once, reuse).
var cueSchemaCtx = cuecontext.New()

// sharedCueSchema is every schema/*.cue file unified into one value (no package
// clauses → one shared scope, so kind defs reference the shared #Step/#Context).
// The concatenation is the SINGLE contract shared with the dev-time generator
// (schemaconcat.ConcatSchema — R3), so the compiled schema can never drift from the
// generated Go types.
var sharedCueSchema = func() cue.Value {
	body, _, err := schemaconcat.ConcatSchema(schemaFS, ".", nil)
	if err != nil {
		panic(fmt.Sprintf("read embedded schema: %v", err))
	}
	v := cueSchemaCtx.CompileString(body)
	if v.Err() != nil {
		panic(fmt.Sprintf("CUE schema failed to compile: %v", errors.Details(v.Err(), nil)))
	}
	return v
}()

// cueKindDefs maps a kind name to its entity definition path (e.g. "#Candy").
var cueKindDefs = map[string]string{}

// registerCueKind records that `kind` is validated by the CUE def at defPath.
// Panics on a duplicate name or a def absent from the compiled schema —
// fail-fast at process start (mirrors mustCalVer).
func registerCueKind(kind, defPath string) {
	if _, dup := cueKindDefs[kind]; dup {
		panic(fmt.Sprintf("duplicate CUE kind registration: %q", kind))
	}
	if d := sharedCueSchema.LookupPath(cue.ParsePath(defPath)); d.Err() != nil {
		panic(fmt.Sprintf("CUE kind %q: definition %s not found: %v", kind, defPath, d.Err()))
	}
	cueKindDefs[kind] = defPath
}

// cueKindDef returns the compiled entity definition for a kind.
func cueKindDef(kind string) (cue.Value, bool) {
	dp, ok := cueKindDefs[kind]
	if !ok {
		return cue.Value{}, false
	}
	return sharedCueSchema.LookupPath(cue.ParsePath(dp)), true
}

// validateEntityCUE unifies a single already-parsed entity value with #<Kind>
// and validates it concretely. label identifies the entity in errors.
func validateEntityCUE(kind, label string, entity cue.Value) error {
	def, ok := cueKindDef(kind)
	if !ok {
		return fmt.Errorf("%s: no CUE schema registered for kind %q", label, kind)
	}
	if err := entity.Unify(def).Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("%s: %s", label, errors.Details(err, nil))
	}
	return nil
}

// validateEntityClosedCUE unifies a single entity with #<Kind> and validates it
// WITHOUT requiring concreteness — it catches closedness violations (unknown
// keys) and type/enum/regex conflicts, but not missing-required fields. This is
// the LOAD-time check (restores the deleted unmarshalers' typo-detection); full
// concrete validation stays in `charly box validate` via validateEntityCUE.
func validateEntityClosedCUE(kind, label string, entity cue.Value) error {
	def, ok := cueKindDef(kind)
	if !ok {
		return fmt.Errorf("%s: no CUE schema registered for kind %q", label, kind)
	}
	if err := entity.Unify(def).Validate(); err != nil {
		return fmt.Errorf("%s: %s", label, errors.Details(err, nil))
	}
	return nil
}

// assembleAndValidateEntitySteps folds an entity node's step children into a
// plan: sequence and types EACH step against the closed #Step (which embeds the
// closed #Op). This is the ONLY validation that sees plan-STEP Op fields: node-form
// steps are sibling nodes, so the #NodeDoc whole-document gate accepts them as `_`,
// and the post-decode struct has already dropped unknown keys. So an unknown Op
// field or a bad enum on a step is a hard error here. We validate the STEPS, not the
// whole entity against its #Kind: a deploy entity (a `vm:`/`pod:` block carrying
// disposable/lifecycle/from/install_opts) mixes deploy-envelope fields the workload
// #Kind does not model — those are gated by #NodeDoc's deploy arm, not here.
// plugin_input: stays open (a plugin step's params are validated by the
// plugin's own spliced schema, not base #Op).
func assembleAndValidateEntitySteps(gn *genericNode, label string) error {
	body, err := assembleEntityBody(gn)
	if err != nil {
		return fmt.Errorf("%s: assemble: %w", label, err)
	}
	b, err := yaml.Marshal(body)
	if err != nil {
		return fmt.Errorf("%s: marshal: %w", label, err)
	}
	v, err := cueDocFromYAML(label, b)
	if err != nil {
		return err
	}
	plan := v.LookupPath(cue.ParsePath("plan"))
	if !plan.Exists() {
		return nil // no steps to type
	}
	stepDef := sharedCueSchema.LookupPath(cue.ParsePath("#Step"))
	if stepDef.Err() != nil {
		return fmt.Errorf("%s: #Step schema not found: %w", label, stepDef.Err())
	}
	iter, lerr := plan.List()
	if lerr != nil {
		return nil // plan not a sequence — structure is gated by #NodeDoc
	}
	for i := 0; iter.Next(); i++ {
		if verr := iter.Value().Unify(stepDef).Validate(); verr != nil {
			return fmt.Errorf("%s: plan step %d: %s", label, i, errors.Details(verr, nil))
		}
	}
	return nil
}

// validateCandyManifestCUE validates a candy manifest. A legacy kind-keyed
// manifest validates the WHOLE document against #NodeDoc (the structural gate),
// then walks the parsed + DESUGARED node tree: each candy node's assembled body
// validates against #CandyValue concretely and every entity's plan steps type
// against the closed #Step (validateNodeFormSteps → validateEntityNodeRec) —
// the desugared tree is the validation subject, never the raw sugar bytes.
func validateCandyManifestCUE(path string, data []byte) error {
	doc, err := cueDocFromYAML(path, data)
	if err != nil {
		return err
	}
	def := sharedCueSchema.LookupPath(cue.ParsePath("#NodeDoc"))
	if def.Err() != nil {
		return fmt.Errorf("%s: #NodeDoc schema not found: %w", path, def.Err())
	}
	if verr := doc.Unify(def).Validate(cue.Concrete(true)); verr != nil {
		return fmt.Errorf("%s: %s", path, errors.Details(verr, nil))
	}
	// #NodeDoc gates the node-form STRUCTURE but accepts each entity's body as
	// `_`; validateNodeFormSteps parses (and thereby DESUGARS) the tree, types
	// every entity's plan steps against the closed #Step/#Op, and concretely
	// validates each candy node's body against #CandyValue.
	return validateNodeFormSteps(path, data)
}

// validateNodeFormSteps parses a node-form document and validates EVERY entity's
// (and nested sub-entity's) assembled body against its closed per-kind def — the
// step-typo gate for candies, boxes, pods, deploys, and check beds alike. Shared by
// validateCandyManifestCUE and validateProjectCUESchemas (R3).
func validateNodeFormSteps(path string, data []byte) error {
	var ydoc yaml.Node
	if err := yaml.Unmarshal(data, &ydoc); err != nil {
		return fmt.Errorf("%s: yaml: %w", path, err)
	}
	// The ONE node-form parse is the registered config front-end (P6, sdk/loaderkit); the
	// genericNode validateEntityNodeRec consumes is reconstructed from each ParsedNode.
	_, pp, err := requireLoaderParser().ParseDoc(&ydoc, loaderThreaded())
	if err != nil {
		return fmt.Errorf("%s: parse: %w", path, err)
	}
	for i := range pp.Nodes {
		gn, gerr := parsedNodeToGeneric(pp.Nodes[i])
		if gerr != nil {
			return fmt.Errorf("%s: %w", path, gerr)
		}
		if verr := validateEntityNodeRec(gn, path); verr != nil {
			return verr
		}
	}
	return nil
}

// validateEntityNodeRec assemble-validates one entity node (when its kind is
// CUE-registered) and recurses into its sub-entity children (bundle members,
// nested deploys), which carry their own steps. A candy node's DESUGARED body is
// additionally validated concretely against #CandyValue (version+description
// required, unknown inline fields rejected) — the box-validate counterpart of
// the load-time host-side validateKindValueCUE (which is closedness-only).
func validateEntityNodeRec(gn *genericNode, path string) error {
	if err := assembleAndValidateEntitySteps(gn, fmt.Sprintf("%s: %s", path, gn.name)); err != nil {
		return err
	}
	if gn.disc == "candy" {
		body, err := assembleEntityBody(gn)
		if err != nil {
			return fmt.Errorf("%s: %s: assemble: %w", path, gn.name, err)
		}
		// The concrete gate covers LAYER manifests only (the pre-cutover
		// validateCandyManifestCUE scope): an IMAGE entity (base:/from:) mixes
		// build fields that stay non-concrete until merge and is gated by the
		// #NodeDoc structural pass + decode validation instead.
		if m := mappingRoot(body); m != nil {
			for i := 0; i+1 < len(m.Content); i += 2 {
				if k := m.Content[i].Value; k == "base" || k == "from" {
					return nil
				}
			}
		}
		b, err := yaml.Marshal(body)
		if err != nil {
			return fmt.Errorf("%s: %s: marshal: %w", path, gn.name, err)
		}
		cv, err := cueDocFromYAML(fmt.Sprintf("%s: %s", path, gn.name), b)
		if err != nil {
			return err
		}
		cdef := sharedCueSchema.LookupPath(cue.ParsePath("#CandyValue"))
		if cdef.Err() != nil {
			return fmt.Errorf("%s: #CandyValue schema not found: %w", path, cdef.Err())
		}
		if verr := cv.Unify(cdef).Validate(cue.Concrete(true)); verr != nil {
			return fmt.Errorf("%s: candy %q: %s", path, gn.name, errors.Details(verr, nil))
		}
	}
	for _, ch := range gn.children {
		if ch.discClass == "entity" {
			if err := validateEntityNodeRec(ch, path); err != nil {
				return err
			}
		}
	}
	return nil
}

// cueDocFromYAML ingests one YAML document into a cue.Value (the whole doc).
func cueDocFromYAML(path string, data []byte) (cue.Value, error) {
	af, err := cueyaml.Extract(path, data)
	if err != nil {
		return cue.Value{}, fmt.Errorf("%s: yaml ingest: %w", path, err)
	}
	v := cueSchemaCtx.BuildFile(af)
	if v.Err() != nil {
		return cue.Value{}, fmt.Errorf("%s: build: %w", path, v.Err())
	}
	return v, nil
}
