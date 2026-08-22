// Scalar collapses for the CUE-single-source cutover (WF-B THE REPOINT).
//
// CUE validates these named scalars (CalVer/EntityRef/CandyRef pattern strings,
// VmSize/Size/PortPin/Duration patterns, the 11 live-verb method enums, the
// build-format/-type enums) at load — so the Go side gains nothing from a
// DISTINCT named string type, and package-main code uses plain string/[]string
// throughout. The matching CUE defs are `@go(-)` (no generated named type); the
// referencing fields keep the def name, which these Go ALIASES resolve to the
// builtin. An alias (not a defined type) is load-bearing: `&op.Cdp` is a
// `*string`, `[]PortPin` IS `[]string`, and an untyped/string assignment to a
// CalVer/EntityRef field just works — exactly the charly drop-in semantics.
//
// MECHANISM (RDD finding, mirrors spec/charly_names.go): `cue exp gengotypes`
// v0.16.1 does NOT propagate a def-level rename/suppression to the FIELDS that
// reference the def, so a bare `@go(-)` leaves the field dangling on the deleted
// name. Exposing the name as a Go alias here makes every reference compile while
// collapsing the Go type to the builtin.
//
// HAND-WRITTEN — not emitted by `task cue:gen`.
package spec

import "encoding/json"

// --- named-pattern scalars (string) ---
type (
	Context   = string
	CalVer    = string
	EntityRef = string
	CandyRef  = string
	PortPin   = string
	VmSize    = string
	Size      = string
	EnvVar    = string
	Duration  = string
)

// --- open passthrough map (alias so []RepoBlock IS []map[string]any) ---
type RepoBlock = map[string]any

// RawBody is an opaque JSON body the kernel stores and transports WITHOUT
// typing — a same-package alias for json.RawMessage so a CUE
// `@go(type=map[string]RawBody)` / `@go(type=RawBody)` override renders an
// opaque field/map in the generated types with no cross-package import
// (gengotypes would mis-import a qualified `json.RawMessage`). Moved here
// from the former hand-written deploy_wire.go (SDD conversion) — it is a
// scalar alias, not a wire struct, so it belongs beside the other scalar
// collapses in this file.
type RawBody = json.RawMessage

// --- build vocabulary enums (string) ---
type (
	BuildFormat = string
	BuildType   = string
)
