package cmd

// notify.go — the venue desktop-notification helper (relocated from charly/notify.go in #118).
// A plugin drives the venue's session bus with gdbus itself (the boundary law's "host-boundary
// object is never a permanence reason"): it runs the probe + the gdbus call directly on the
// DeployExecutor the plugin built (deploykit.ContainerChain), so nothing crosses into charly core.

import (
	"context"
	"fmt"
	"os"

	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/spec/spec"
)

// sendVenueNotification sends a desktop notification on the venue (container / VM / host).
// Best-effort: silently ignores all errors (no daemon, no dbus, headless target). It drives the
// venue's session bus directly with gdbus (glib2) — desktops carry gdbus, and being an automatic
// side-effect (cmd completion) it deliberately stays a single lightweight gdbus call rather than
// transferring the charly binary into a container just for a best-effort popup.
func sendVenueNotification(ex deploykit.DeployExecutor, title, body string) {
	if _, _, exit, err := ex.RunCapture(context.Background(), "command -v gdbus >/dev/null 2>&1"); err == nil && exit == 0 {
		gdbusCmd := fmt.Sprintf(
			`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/tmp/dbus-session}" && `+
				`gdbus call --session `+
				`--dest=org.freedesktop.Notifications `+
				`--object-path=/org/freedesktop/Notifications `+
				`--method=org.freedesktop.Notifications.Notify `+
				`"charly" 0 "" %s %s "[]" "{}" -- -1`,
			spec.ShellQuote(title), spec.ShellQuote(body))
		_, _, _, _ = ex.RunCapture(context.Background(), gdbusCmd) // best-effort
		return
	}

	fmt.Fprintf(os.Stderr, "Warning: cannot send notification — 'gdbus' not found on target\n")
}
