package spec

// build_resolved_box.go — SPIKE: the render-time embedding wrapper relocated from
// sdk/buildkit/resolved_box.go (#55 value-type relocation spike). Every field type
// here already resolves to a spec.* type (DistroConfig/BuilderConfig/ResolvedDistro/
// ResolvedInit/AggregatedCandyCaps/BakedLabelSet are all CUE-generated in this same
// package) so the wrapper carries ZERO buildkit-only content — it exists purely
// because gengotypes has no json:"-" construct (the kit.CheckResult/DeadlineExceeded
// pattern — see /charly-internals:go "Generation coverage"). buildkit.ResolvedBox
// becomes a type alias onto this type so every existing buildkit/deploykit/candy
// call site compiles unchanged.
type BuildResolvedBox struct {
	ResolvedBox

	// Build config (resolved per-image via charly.yml import: + the binary-embedded build vocabulary)
	DistroConfig  *DistroConfig   `json:"-"`
	DistroDef     *ResolvedDistro `json:"-"`
	BuilderConfig *BuilderConfig  `json:"-"`
	InitSystem    string          `json:"-"`
	InitDef       *ResolvedInit   `json:"-"`

	CandyCaps *AggregatedCandyCaps `json:"-"`

	BakedMetadata    *BakedLabelSet           `json:"-"`
	RenderCandyOrder []string                 `json:"-"`
	ActiveInits      map[string]*ResolvedInit `json:"-"`
}
