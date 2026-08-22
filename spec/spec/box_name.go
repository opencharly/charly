package spec

import "strings"

// box_name.go — box-name / URL-scheme ref helpers (RELOCATED from sdk/kit remote_ref.go,
// #55 value extraction; the ref-parsing VOCAB itself already lived here in ref_parse.go, so
// these two thin helpers rejoin it — collapsing the sdk/kit duplicate, R3). Pure string
// transforms over the ref E-envelope; sdk/kit re-exports them so existing kit.StripURLScheme /
// kit.ResolveBoxName call sites are untouched.

// StripURLScheme removes http:// or https:// from a remote ref if present.
func StripURLScheme(ref string) string {
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimPrefix(ref, "http://")
	return ref
}

// ResolveBoxName extracts the short box name from a ref that may be
// a local box name or a remote ref (github.com/org/repo/box[@version]).
func ResolveBoxName(box string) string {
	ref := StripURLScheme(box)
	if IsRemoteImageRef(ref) {
		return ParseRemoteRef(ref).Name
	}
	return box
}
