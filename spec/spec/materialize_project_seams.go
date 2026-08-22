package spec

// MaterializeProjectSeams gathers the host-coupled leaf legs the kind-blind whole-project MATERIALIZE
// orchestration (loaderkit.MaterializeLoadedProject) calls out to. Each is registry-/host-/bootstrap-
// coupled and CANNOT run kind-blind inside loaderkit; the host wrapper (charly's
// hostMaterializeProjectSeams) is the sole constructor and always populates every field. A nil field
// panics on use. Relocated to the dedicated spec module (#55 2b C3) so the host supplies it as a
// spec-typed seam via LoaderExecutor.MaterializeProjectSeams() — loaderkit drives the orchestration
// internally, and charly core stops importing loaderkit to load its own config. All fields are
// callbacks over spec types (the same injected-seam shape as WalkSeams / MaterializeSeams).
type MaterializeProjectSeams struct {
	// MaterializeProject folds ONE document's parsed entity nodes into uf via the registered
	// Materializer (registry kind-decode + the per-node not-found policy). uf already carries the
	// document's decoded reserved directives; this adds the Box/Candy/Fleet/PluginKinds entities,
	// accumulating across the document's node list.
	MaterializeProject func(pp *ParsedProject, uf *UnifiedFile) error
	// FoldDiscoveredManifests folds every discovered manifest's parsed nodes into uf — a LAYER candy
	// registers a lazy `From:` reference (the bootstrap-critical candyIsImage box⊻layer routing stays
	// host-side), every other kind materializes via the registered Materializer. Shared host-side with
	// charly's ApplyDiscover (R3), so it stays one host leaf.
	FoldDiscoveredManifests func(dms []DiscoveredManifest, uf *UnifiedFile) error
	// ApplyEmbeddedDefaults merges the binary-embedded build vocabulary + sidecar templates UNDER uf's
	// own entries (project-wins). Host-resident: the embedded bytes and the host node-form parser are
	// charly's.
	ApplyEmbeddedDefaults func(uf *UnifiedFile) error
}
