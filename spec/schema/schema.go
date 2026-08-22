// Package schema exports the CUE schema source as an embedded FS — the single
// runtime origin for charly core's sharedCueSchema, the loader's base-schema
// splice, and the dev-time spec generation. The schema and the Go types
// generated from it (package spec) live in ONE module so the reproducibility
// gate (TestGenReproducible) is a single-repo check.
package schema

import "embed"

// FS holds every *.cue schema file at the FS root (no directory prefix).
// Consumers concatenate it via schemaconcat.ConcatSchema(FS, ".", nil).
//
//go:embed *.cue
var FS embed.FS
