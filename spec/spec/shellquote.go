package spec

import "strings"

// ShellQuote wraps s in single quotes for safe interpolation into a shell
// command line, escaping any embedded single quotes via the standard
// '\” idiom. It is a pure stdlib FABRIC primitive (POSIX single-quoting) with
// exactly one definition across the tree (#55 step1): the process-launch
// renderer (github.com/opencharly/spec/proc RemoteLaunchCommand), the sdk/kit
// deploy executors, the sdk/deploykit Containerfile emitter, and every plugin
// candy that quotes a shell token all call spec.ShellQuote — one source, no
// duplicate quoter (R3).
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
