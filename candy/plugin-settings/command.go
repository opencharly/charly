package settings

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// command.go is the command:settings leg of this plugin — the externalized `charly settings` CLI.
// Unlike the secrets/mcp/preempt/feature plugins (which port their command grammar INTO the
// plugin), the SettingsCmd subtree (get/set/list/path/reset, charly/main.go) STAYS in charly's
// core: its Run handlers read and write the runtime config file ~/.config/charly/config.yml and
// resolve the credential-store backend + the runtime engine — none of which this out-of-process
// plugin can reach. Core re-homes SettingsCmd onto a hidden `charly __settings` command, and this
// plugin is a THIN FORWARDER: it raw-forwards the pass-through tokens to `charly __settings <args…>`.
// Keeping the subtree in ONE place (core) avoids duplicating the config-store logic in the plugin
// (R3).
//
// Dispatch contract (charly/provider_command_external.go dispatchExternalCommand): on
// `charly settings <args…>`, charly RESOLVES this plugin's binary (host-built from source, or baked
// into /usr/lib/charly/plugins via pkg/arch) and syscall.Exec's it with the pass-through tokens
// after the `settings` word, in CLI mode (the go-plugin handshake cookie is stripped, so sdk.Main
// runs cliMain instead of serving gRPC) with CHARLY_BIN stamped to charly's own executable. cliMain
// then syscall.Exec's `charly __settings <args…>`, so the in-core SettingsCmd runs in the re-entered
// charly process and inherits charly's stdin/stdout/stderr/TTY natively.

// cliMain is the CLI-mode entry point (sdk.Main calls it when charly syscall.Exec'd this plugin
// as a command passthrough). It RAW-FORWARDS every pass-through token to the hidden in-core
// `charly __settings <args…>` command via execCharly (no local kong parse — the core SettingsCmd
// grammar owns the contract, so the get/set/list/path/reset subcommand + its flags pass straight
// through). On success this never returns (syscall.Exec replaces the process image); only a PRE-exec
// failure returns a non-zero code.
func cliMain(args []string) int {
	if err := execCharly(append([]string{"__settings"}, args...)...); err != nil {
		fmt.Fprintf(os.Stderr, "charly settings: %v\n", err)
		return 1
	}
	return 0 // unreachable: execCharly's syscall.Exec replaced the process image
}

// charlyBin returns the host charly binary the dispatch seam stamped into CHARLY_BIN, falling
// back to `charly` on PATH (e.g. if the plugin binary is run directly, off the dispatch path).
func charlyBin() string {
	if b := os.Getenv("CHARLY_BIN"); b != "" {
		return b
	}
	return "charly"
}

// execCharly REPLACES this process with `charly <args…>` via syscall.Exec, so the re-entered
// charly runs the hidden in-core SettingsCmd (`__settings …`) and its stdout/stderr/exit-code/TTY
// flow back through this plugin (which IS the operator's `charly settings` process) natively. On
// success this never returns; only a PRE-exec failure (binary missing) returns an error.
func execCharly(args ...string) error {
	bin, err := exec.LookPath(charlyBin())
	if err != nil {
		return fmt.Errorf("resolving charly binary %q: %w", charlyBin(), err)
	}
	argv := append([]string{"charly"}, args...)
	if err := syscall.Exec(bin, argv, os.Environ()); err != nil { //nolint:gosec // bin is CHARLY_BIN, args are the __settings hidden command + operator settings tokens
		return fmt.Errorf("exec %s: %w", bin, err)
	}
	return nil // unreachable: syscall.Exec replaced the process image
}
