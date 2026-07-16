package main

import (
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
)

// sendVenueNotification sends a desktop notification on the venue (container /
// VM / host). Best-effort: silently ignores all errors (no daemon, no dbus,
// headless target). It drives the venue's session bus directly with gdbus
// (glib2) — desktops carry gdbus, and being an automatic side-effect
// (deploy / cmd) it deliberately stays a single lightweight gdbus call
// rather than transferring the 27 MB charly binary into a container just for a
// best-effort popup. (The interactive `dbus:` check verb was externalized to
// candy/plugin-dbus, which also drives the bus via gdbus — never godbus. charly's core
// links no godbus at all; the Secret Service keyring lives out-of-process in
// candy/plugin-secrets.)
func sendVenueNotification(ex deploykit.DeployExecutor, title, body string) {
	if venueHasTool(ex, "gdbus") {
		gdbusCmd := fmt.Sprintf(
			`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/tmp/dbus-session}" && `+
				`gdbus call --session `+
				`--dest=org.freedesktop.Notifications `+
				`--object-path=/org/freedesktop/Notifications `+
				`--method=org.freedesktop.Notifications.Notify `+
				`"charly" 0 "" %s %s "[]" "{}" -- -1`,
			kit.ShellQuote(title), kit.ShellQuote(body))
		_ = venueRunSilent(ex, gdbusCmd) // best-effort
		return
	}

	fmt.Fprintf(os.Stderr, "Warning: cannot send notification — 'gdbus' not found on target\n")
}
