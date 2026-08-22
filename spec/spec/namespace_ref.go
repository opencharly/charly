package spec

import "strings"

// namespace_ref.go — the generic Go-inspired hierarchical-namespace-ref VOCAB
// (the `import:` statement's qualified-ref grammar: `ns.member`, `a.b.c`). Pure
// string predicates with a wide, non-loader-cone consumer set (config.go's
// resolveBoxRef/pullNamespacedBox family, the plugin-side substrate preresolvers) —
// legal for any charly package to import, same rationale as ref_parse.go.

// SplitNamespaceRef splits a qualified ref on its FIRST `.` into (namespace,
// remainder). A bare ref (no dot, or a leading/trailing dot) returns ok=false.
// The remainder may itself be qualified (`a.b.c` → "a", "b.c").
func SplitNamespaceRef(ref string) (ns, rest string, ok bool) {
	i := strings.IndexByte(ref, '.')
	if i <= 0 || i >= len(ref)-1 {
		return "", "", false
	}
	return ref[:i], ref[i+1:], true
}

// LeafName strips every namespace prefix from a (possibly qualified) ref,
// returning the final member name — e.g. "charly.arch-builder" -> "arch-builder",
// "a.b.c" -> "c", bare "fedora" -> "fedora".
func LeafName(ref string) string {
	for {
		_, rest, ok := SplitNamespaceRef(ref)
		if !ok {
			return ref
		}
		ref = rest
	}
}
