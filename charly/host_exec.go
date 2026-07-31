package main

// `charly --host <alias|user@host[:port]> <verb>` — re-exec charly on a
// remote machine over SSH.
//
// ReexecOverSSH's body is 100% stdlib+spec — zero core-only calls — so it lives
// in spec/hostenv/reexec.go (hostenv.ReexecOverSSH), a host primitive in a spec
// fabric slice per the kernel/plugin boundary law. It was relocated there from
// sdk/kit by #55 coneG (charly/ off sdk/kit); the prior S6 core→kit move is in
// CHANGELOG. The ONE thing that stays here is shouldReexecForHost's ~15-line
// decision (it must run before Kong dispatches to ANYTHING, mirroring the
// already-accepted precedent of plugin_command_prescan.go's pre-parse hooks —
// the same decide-before-dispatch shape core already owns) plus main()'s
// dispatch glue, which resolves the charly-core-only inputs (the active
// controller binary, this binary's CalVer identity, and whether stdin is a
// terminal — the latter kept OUT of spec/hostenv deliberately: spec/hostenv is
// a shared fabric slice, so a new dependency on golang.org/x/term would ripple
// a go.sum update into every consumer for one ssh -tt flag) and threads them
// into hostenv.ReexecOverSSH.

import "strings"

// shouldReexecForHost returns true if charly should forward the current
// invocation to a remote machine via SSH. False when:
//   - cli.Host is empty
//   - the top-level command path starts with one of the LocalOnly
//     commands (settings, version, ssh) — these manage the LOCAL charly
//     installation and must not be re-execed.
//
// `cmdPath` is the space-separated path reported by Kong (e.g.
// "settings get", "check libvirt status").
func shouldReexecForHost(cli *CLI, cmdPath string) bool {
	if cli.Host == "" {
		return false
	}
	head := cmdPath
	if before, _, ok := strings.Cut(cmdPath, " "); ok {
		head = before
	}
	switch head {
	case "settings", "version", "ssh":
		return false
	}
	return true
}
