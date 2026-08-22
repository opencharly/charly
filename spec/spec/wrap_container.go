package spec

// wrap_container.go — WrapContainerCommand, RELOCATED to the spec contract module (#55
// CHECK-ENGINE cone Option A — a pure stdlib string guard charly core's check-op dispatch
// (checkrun_act.go) reaches importing zero kit). sdk/kit re-exports it (sdk/kit/kit.go) so
// every existing kit.WrapContainerCommand call site (charly core + the candies + sdk) is
// untouched. New consumers reference spec.WrapContainerCommand directly.

// WrapContainerCommand guards an in-container command-check script against stdin-consuming
// subcommands. The runner delivers in-container scripts to the pod shell over a stdin heredoc
// ("stdin-attached exec"); without this guard the FIRST subcommand that reads stdin — adb shell,
// ssh, read, cat — consumes the REST of the heredoc (the not-yet-executed script lines), silently
// truncating the check to its first command. Wrapping the whole script in a brace group with stdin
// redirected from /dev/null fixes it generically: the shell reads the entire group before executing
// it (so the heredoc is fully drained by parse time), then runs every subcommand with stdin tied to
// /dev/null. The host path (a plain `sh -c` argv) is unaffected.
func WrapContainerCommand(script string) string {
	return "{ " + script + "\n} </dev/null"
}
