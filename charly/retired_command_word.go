package main

// retired_command_word.go — the CC-2 cutover's load-time hard error for the retired
// `fleet` CLI word (R5: no alias — the word is GONE from the Kong grammar, the
// command:deploy provider (candy/plugin-fleet v0.2026250.558+) registers only
// `deploy`; this intercept fires BEFORE kong.Parse so a stray `charly fleet …`
// invocation fails with a pointed message instead of kong's generic unknown-command
// error). Exit code 80 matches kong's usage-error exit (the bundle-era cutover's
// `charly bundle` also exited 80 — same convention, pointier message).

import (
	"fmt"
	"os"
	"strings"
)

// firstCommandWord returns the first non-flag argv token — the top-level CLI word.
// It understands charly's root VALUE flags (--host, --host-identity-file,
// --host-option, -C/--dir, --repo) both in "--flag value" and "--flag=value" form,
// so a flag's value is never mistaken for the command word. The first positional
// token after them is the command. Returns ok=false when no positional token exists
// (e.g. bare `charly` / `charly --help`).
func firstCommandWord(args []string) (string, bool) {
	valueFlags := map[string]bool{
		"--host": true, "--host-identity-file": true, "--host-option": true,
		"--dir": true, "--repo": true, "-C": true,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" { // explicit end-of-flags separator
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", false
		}
		if a != "-" && strings.HasPrefix(a, "-") {
			if valueFlags[a] && !strings.Contains(a, "=") {
				i++ // value flag: skip its value token
			}
			continue
		}
		return a, true
	}
	return "", false
}

// retiredCommandExit is the exit code for a retired-word invocation: kong's usage
// error code (80), the same code the bundle-era cutover's `charly bundle` exited.
const retiredCommandExit = 80

// retireCommandWord emits the CC-2 cutover's hard error for the retired `fleet`
// word and exits. It is the ONLY place the old word may still be typed — and it
// never parses (R5, no alias).
func retireCommandWord(word string) {
	fmt.Fprintf(os.Stderr,
		"charly: error: the %q command is retired in the CC-2 vocabulary cutover — use `charly deploy add` / `charly deploy del` instead (fleet → deploy).\n",
		word)
	os.Exit(retiredCommandExit)
}
