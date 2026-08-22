// CUE schema for the unified-config LOADER's WALK result (K1). #LoadedProject is the generic,
// sdk-expressible output of WALKING a whole project — the orchestration half of LoadUnified
// (import queue + discover walk + namespace mount) relocated to sdk/loaderkit as a kind-blind
// driver. It carries the parse of every document (root file + flat imports + discovered
// manifests + namespace subtrees) in the exact traversal ORDER, WITHOUT materializing or
// merging: the host MATERIALIZES each #ParsedProject into the typed *UnifiedFile and MERGES
// (root-wins) itself, because the materialize is the registry-coupled kind-decode the kernel
// keeps (boundary law). So #LoadedProject is the PARSED, un-materialized stage — strictly
// UPSTREAM of the resolved-project envelope (post-materialize+resolve). It reuses #ParsedProject
// / #ParsedNode verbatim (see parsedproject.cue). Package-less; concatenated into the spec
// compilation unit. NOT an authoring kind (never in #Node/#Op) — a pure generated wire type
// (it becomes candy/plugin-loader's OpLoad reply at the K5 envelope unit), single-sourced here
// so `task cue:gen` produces the Go struct.

// #LoadedDoc — one parsed document of a namespace's flattened file tree (root file OR a flat
// import), in merge order. `directives` is the RAW reserved-directive mapping bytes
// (version/repo/defaults/provides/discover) the host yaml-decodes into a sub *UnifiedFile before
// merging (import is consumed by the walk); `project` is the document's decomposed entity nodes;
// `srcDir` anchors the document's discover paths + labels diagnostics.
#LoadedDoc: {
	// directives is the RAW reserved-directive mapping serialized as YAML bytes (NOT the RawBody
	// JSON the entity bodies use) — so the host replays the ORIGINAL mergeUnifiedDocs decode exactly
	// (`yaml.Unmarshal(directives, &sub)`), honoring the custom YAML unmarshalers on import/discover.
	directives?: bytes @go(Directives)
	project?: #ParsedProject @go(Project)
	srcDir?: string @go(SrcDir)
	srcLabel?: string @go(SrcLabel)
}

// #DiscoveredManifest — one discovered entity directory's manifest (a `discover:` walk hit),
// applied by the host AFTER the docs (explicit-entry-wins). `dir`/`rootDir`/`manifest` let the
// host register a lazy candy `From:` reference (a LAYER candy) or materialize the node (any
// other kind); `docs` are the manifest's parsed documents.
#DiscoveredManifest: {
	dir?: string @go(Dir)
	manifest?: string @go(Manifest)
	rootDir?: string @go(RootDir)
	docs?: [...#ParsedProject] @go(Docs)
}

// #NamespaceMount — one namespaced `import:` (alias → an isolated child project). Two shapes:
//   - a DEFINITION (ref=false): the mount carries the child project inline (project), whose own
//     `id` the host registers so a later reference to it shares the SAME materialized *UnifiedFile;
//   - a REFERENCE (ref=true): a repo-identity cycle-break or a diamond re-import — the child was
//     already walked, so the mount carries NO inline project, only refID (the target project's id).
// The host materialize resolves a reference to the pointer it registered for that id (register-
// before-recurse), preserving the original loader's pointer identity across the mutual-import cycle
// (main↔cachyos). Modeled as a LIST entry (not a self-recursive map) so `cue exp gengotypes`
// generates faithfully — the pointer slice `[]*NamespaceMount` on #LoadedProject breaks recursion.
#NamespaceMount: {
	alias?: string @go(Alias)
	ref?: bool @go(Ref)
	refID?: int @go(RefID)
	project?: #LoadedProject @go(Project)
}

// #LoadedProject — the whole WALK result of one project (root OR a mounted namespace): a stable
// per-walk id (so a namespaced cycle/diamond back-reference resolves to the SAME materialized
// *UnifiedFile), the ordered documents, the discovered manifests, and the mounted namespace
// subtrees. The host replays materialize+merge over this to reconstruct the typed *UnifiedFile
// (identical to the former in-line loadUnifiedInto), then applies the binary-embedded default vocab.
#LoadedProject: {
	id?: int @go(ID)
	docs?: [...#LoadedDoc] @go(Docs)
	discovered?: [...#DiscoveredManifest] @go(Discovered)
	namespaces?: [...#NamespaceMount] @go(Namespaces)
}
