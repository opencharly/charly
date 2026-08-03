package loader

// Shared test fixture helper for this package's relocated loader tests
// (mirrors charly's own candy_test_helpers_test.go testCandy — R3, one copy
// per module since each plugin candy is its own Go module).

import (
	"slices"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

func testCandy(name string, m spec.CandyModel, v spec.CandyView) spec.CandyReader {
	m.Name = name
	v.Name = name
	return deploykit.NewSpecCandyModel(m, v)
}

// init registers the spec.OpInContext DI hook this test package's relocated
// loaderkit tests need (loaderkit.CompleteCandyRunOps calls it) — mirrors
// charly's own registration (layers.go: spec.OpInContext = opInContext), a
// pure function of spec.Op.Context / spec.VerbCatalog with zero charly
// coupling, so the same logic lives here.
func init() {
	spec.OpInContext = opInContextForTest
}

func opEffectiveContextsForTest(c *spec.Op) []spec.ExecContext {
	if len(c.Context) > 0 {
		out := make([]spec.ExecContext, 0, len(c.Context))
		for _, s := range c.Context {
			out = append(out, spec.ExecContext(s))
		}
		return out
	}
	if verb, err := c.Kind(); err == nil {
		if vs, ok := spec.VerbCatalog[verb]; ok {
			return vs.Contexts
		}
	}
	return nil
}

func opInContextForTest(c *spec.Op, ctx spec.ExecContext) bool {
	return slices.Contains(opEffectiveContextsForTest(c), ctx)
}
