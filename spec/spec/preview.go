package spec

import "strings"

// TrimPreview truncates s to a 200-char preview (trailing "…") for compact
// check-output display. Homed in the fabric slice github.com/opencharly/spec/spec
// (the same fabric-primitive class as ShellQuote) so both the executor slice
// (spec/exec) and kit reach the ONE copy — kit re-exports it (kit.TrimPreview)
// so its existing callers compile unchanged (R3, single source).
func TrimPreview(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
