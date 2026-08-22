// CUE schema for the unified-config LOADER's parse result (P6). #ParsedProject is the
// generic, sdk-expressible output of parsing a project — the parse half of LoadUnified that
// candy/plugin-loader produces and the host MATERIALIZES into the typed *UnifiedFile. A
// compiled-in loader plugin is a separate module importing only sdk, so it cannot produce the
// core *UnifiedFile (which embeds the core Config mechanism + runtime Candy graph the plan
// keeps in core); this generic node tree is the wire between the parse (plugin) and the
// materialize (host). Package-less; concatenated into the spec compilation unit. NOT an
// authoring kind (never in #Node/#Op) — a pure generated wire type, single-sourced here so
// `task cue:gen` produces the Go struct (charly aliases it via vmshared/spec_aliases.go).

// #ParsedNode — one decomposed reserved-word node: its name, kind discriminator, the opaque
// entity body (the complete kind value as JSON, materialized per-kind by the host), and any
// sub-entity member children (deployable kinds only). The recursive Children mirror the
// deploy tree's member nesting the host folds into uf.Fleet.
#ParsedNode: {
	name!: string @go(Name)
	disc!: string @go(Disc)
	body?: bytes  @go(Body,type=RawBody)
	children?: [...#ParsedNode] @go(Children)
}

// #ParsedProject — the whole parse result: the schema version (for the post-parse gate), the
// decomposed reserved-word nodes, and the resolved import refs (already fetched + merged by
// the plugin's import walk). Discover results are decomposed into the same nodes list.
#ParsedProject: {
	version?: string @go(Version)
	nodes?: [...#ParsedNode] @go(Nodes)
	imports?: [...string] @go(Imports)
}
