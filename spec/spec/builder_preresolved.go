package spec

// builder_preresolved.go — the pre-resolved builder-context VALUE type, promoted from
// sdk/deploykit (#55 import-purity, value-type consolidation cone). A plain IN-PROCESS data
// carrier: an opaque builder-specific render Context (map[string]any) plus the teardown
// Reverse ops. candy/plugin-fleet's preresolveBuilderContexts BUILDS it plugin-side (over
// exec.InvokeProvider) and the pure BuildDeployPlan compiler READS it — it never crosses the
// plugin wire (HostContext.BuilderContext is populated in-process AFTER the HostContextJSON
// decode), so it is a plain spec Go value, NOT a CUE wire type (its map[string]any Context is
// not a clean wire shape either). sdk/deploykit keeps a `type BuilderPreresolved =
// spec.BuilderPreresolved` forwarder so its callers + candy/plugin-fleet compile unchanged.

// BuilderPreresolved is one candy×builder's pre-resolved payload: the builder-specific render
// Context (opaque key/value map from the builder plugin's OpCollectContext) plus its Reverse
// teardown ops (from OpReverse). Consumed by the deploy-plan compiler, which only reads it.
type BuilderPreresolved struct {
	Context map[string]any
	Reverse []ReverseOp
}
