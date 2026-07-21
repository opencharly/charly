package main

// `charly --host <alias|user@host[:port]> <verb>` — re-exec charly on a
// remote machine over SSH.
//
// S6 (mechanical relocation, no design risk): ReexecOverSSH's body was 100%
// stdlib+sdk/kit — zero core-only calls — so it moved wholesale to
// sdk/kit/reexec_ssh.go (kit.ReexecOverSSH), mirroring the already-accepted
// install_build_act.go DI-shell pattern (deploykit.CompileActOp = compileActOp).
// The ONE thing that stays here is shouldReexecForHost's ~15-line decision (it
// must run before Kong dispatches to ANYTHING, mirroring the already-accepted
// precedent of plugin_command_prescan.go's pre-parse hooks — the same
// decide-before-dispatch shape core already owns) plus main()'s dispatch glue,
// which resolves the charly-core-only inputs (the active controller binary,
// this binary's CalVer identity, and whether stdin is a terminal — the latter
// kept OUT of sdk/kit deliberately: sdk/kit is imported by nearly every
// out-of-tree plugin candy, so a new kit dependency on golang.org/x/term would
// have rippled a go.sum update into ~38 candy modules for one ssh -tt flag)
// and threads them into kit.ReexecOverSSH.

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
