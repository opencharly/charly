package dbus

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/opencharly/charly/candy/plugin-dbus/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// methods.go is the dbus method dispatcher + the venue-driving layer. The 4-method surface
// (list/call/introspect/notify) drives the venue's session bus with gdbus over the host
// executor reverse channel (sdk.Executor.RunCapture) and RETURNS the captured output string —
// so provider.go can feed the output through the shared sdk matcher pipeline (a host-side
// matcher step does not run for an out-of-process verb). The plugin speaks gdbus
// to the venue's bus directly — no godbus, no in-container self-delegation; gdbus is the
// session-bus client every desktop carries. The per-verb fields arrive in the step's
// desugared plugin_input, decoded into the CUE-generated params.DbusInput (#DbusInput);
// only the genuinely shared step modifiers (matchers, description) still ride the Op.

// sessionBusExport prefixes every gdbus invocation so the venue's session bus is reachable.
// The dbus layer exports DBUS_SESSION_BUS_ADDRESS=unix:path=/tmp/dbus-session; the `:-` default
// is the in-tree gdbus-fallback safety net for a target whose env did not propagate it.
const sessionBusExport = `export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/tmp/dbus-session}" && `

// requiredModifiers mirrors the in-tree dbusMethods required-field specs (the host's
// validate-time + runtime required-modifier check keyed off the former in-proc live-verb seam,
// which an external verb is not — so the check moves HERE, at dispatch). The names are
// plugin INPUT keys (sdk.OpModifierZero is map-first over op.PluginInput): call needs a
// dest/path/member (the D-Bus member — the input's `method` key is the VERB method);
// introspect needs a dest/path; notify needs the text (the title).
var requiredModifiers = map[string][]string{
	"call":       {"dest", "path", "member"},
	"introspect": {"dest", "path"},
	"notify":     {"text"},
}

// dispatch runs one dbus method against the venue's session bus (over the host executor
// reverse channel) and returns its captured output. A returned error is the verb FAILING
// (the in-tree CLI Run() returning an error → exit 1); provider.go maps it through the
// exit_status / stderr matchers. op carries the shared step modifiers (the required-modifier
// check reads the input map through it; notify reads the shared description); the dbus-
// exclusive fields ride the decoded params.DbusInput.
func dispatch(ctx context.Context, ex *sdk.Executor, op *spec.Op, in *params.DbusInput) (string, error) {
	method := in.Method
	if err := sdk.RequireModifiers(method, op, requiredModifiers); err != nil {
		return "", err
	}
	if err := ensureGdbus(ctx, ex); err != nil {
		return "", err
	}
	switch method {
	case "list":
		return dbusList(ctx, ex)
	case "call":
		return dbusCall(ctx, ex, in)
	case "introspect":
		return dbusIntrospect(ctx, ex, in)
	case "notify":
		return dbusNotify(ctx, ex, op, in)
	}
	return "", fmt.Errorf("unknown dbus method %q", method)
}

// ---------------------------------------------------------------------------
// Methods (ported from charly/dbus.go, retargeted at gdbus over the reverse channel)
// ---------------------------------------------------------------------------

// dbusList lists the registered session-bus services (org.freedesktop.DBus.ListNames). The
// gdbus tuple output `(['org.freedesktop.DBus', ...],)` carries every service name as a
// substring, so a `contains:` matcher on a name still matches (the in-tree godbus path
// printed one name per line).
func dbusList(ctx context.Context, ex *sdk.Executor) (string, error) {
	return ex.VenueCapture(ctx, sessionBusExport+
		"gdbus call --session "+
		"--dest org.freedesktop.DBus "+
		"--object-path /org/freedesktop/DBus "+
		"--method org.freedesktop.DBus.ListNames")
}

// dbusCall makes a generic D-Bus method call (dest/path/member + typed args). The in-tree
// `type:value` arg vocabulary (string/uint32/int32/int64/uint64/boolean/double) is preserved,
// converted to explicit GVariant text for gdbus. `member` is the fully-qualified D-Bus
// interface.Method (gdbus --method) — the input's `method` key is the verb method.
func dbusCall(ctx context.Context, ex *sdk.Executor, in *params.DbusInput) (string, error) {
	args, err := gvariantArgs(in.Args)
	if err != nil {
		return "", err
	}
	cmd := sessionBusExport + "gdbus call --session " +
		"--dest " + kit.ShellQuote(in.Dest) + " " +
		"--object-path " + kit.ShellQuote(in.Path) + " " +
		"--method " + kit.ShellQuote(in.Member)
	for _, a := range args {
		cmd += " " + a
	}
	return ex.VenueCapture(ctx, cmd)
}

// dbusIntrospect introspects a service object, returning the introspection XML
// (org.freedesktop.DBus.Introspectable.Introspect, surfaced by `gdbus introspect --xml`).
func dbusIntrospect(ctx context.Context, ex *sdk.Executor, in *params.DbusInput) (string, error) {
	return ex.VenueCapture(ctx, sessionBusExport+
		"gdbus introspect --session "+
		"--dest "+kit.ShellQuote(in.Dest)+" "+
		"--object-path "+kit.ShellQuote(in.Path)+" --xml")
}

// dbusNotify sends a desktop notification via org.freedesktop.Notifications.Notify. text is
// the title (required, from the plugin input); the SHARED step description (still an #Op
// modifier) is the body — the in-tree gdbus-fallback argv, promoted to the sole path.
func dbusNotify(ctx context.Context, ex *sdk.Executor, op *spec.Op, in *params.DbusInput) (string, error) {
	return ex.VenueCapture(ctx, sessionBusExport+
		"gdbus call --session "+
		"--dest=org.freedesktop.Notifications "+
		"--object-path=/org/freedesktop/Notifications "+
		"--method=org.freedesktop.Notifications.Notify "+
		`"charly" 0 "" `+kit.ShellQuote(in.Text)+" "+kit.ShellQuote(op.Description)+` "[]" "{}" -- -1`)
}

// ---------------------------------------------------------------------------
// Typed-arg → GVariant conversion (ported from charly/dbus.go parseDbusTypedValue)
// ---------------------------------------------------------------------------

// gvariantArgs converts the in-tree `type:value` arg vocabulary into shell-quoted GVariant
// text tokens for gdbus (one shell argument per call argument).
func gvariantArgs(args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		gv, err := gvariantArg(arg)
		if err != nil {
			return nil, err
		}
		out = append(out, gv)
	}
	return out, nil
}

// gvariantArg parses one `type:value` argument and returns a shell-quoted GVariant text
// token (e.g. `string:hello` → `'"hello"'`, `uint32:0` → `'@u 0'`).
func gvariantArg(arg string) (string, error) {
	typ, val, ok := strings.Cut(arg, ":")
	if !ok {
		return "", fmt.Errorf("invalid argument %q: expected type:value (e.g. string:hello, uint32:0)", arg)
	}
	var gv string
	switch typ {
	case "string":
		gv = gvariantString(val)
	case "uint32":
		if _, err := strconv.ParseUint(val, 10, 32); err != nil {
			return "", fmt.Errorf("argument %q: invalid uint32: %s", arg, val)
		}
		gv = "@u " + val
	case "int32":
		if _, err := strconv.ParseInt(val, 10, 32); err != nil {
			return "", fmt.Errorf("argument %q: invalid int32: %s", arg, val)
		}
		gv = "@i " + val
	case "int64":
		if _, err := strconv.ParseInt(val, 10, 64); err != nil {
			return "", fmt.Errorf("argument %q: invalid int64: %s", arg, val)
		}
		gv = "@x " + val
	case "uint64":
		if _, err := strconv.ParseUint(val, 10, 64); err != nil {
			return "", fmt.Errorf("argument %q: invalid uint64: %s", arg, val)
		}
		gv = "@t " + val
	case "boolean", "bool":
		b, err := strconv.ParseBool(val)
		if err != nil {
			return "", fmt.Errorf("argument %q: invalid boolean: %s", arg, val)
		}
		gv = strconv.FormatBool(b)
	case "double":
		if _, err := strconv.ParseFloat(val, 64); err != nil {
			return "", fmt.Errorf("argument %q: invalid double: %s", arg, val)
		}
		gv = "@d " + val
	default:
		return "", fmt.Errorf("argument %q: unsupported type %q (supported: string, uint32, int32, int64, uint64, boolean, double)", arg, typ)
	}
	return kit.ShellQuote(gv), nil
}

// gvariantString renders a GVariant double-quoted string literal with backslash escaping.
func gvariantString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// ---------------------------------------------------------------------------
// venue command helpers (over the executor reverse channel)
// ---------------------------------------------------------------------------

// ensureGdbus fails the verb with a clear message when gdbus (glib2) is absent on the venue —
// dbus drives the bus through gdbus, with no in-container charly self-delegation to fall back on.
func ensureGdbus(ctx context.Context, ex *sdk.Executor) error {
	if !ex.VenueHasTool(ctx, "gdbus") {
		return fmt.Errorf("'gdbus' is not installed on the target — ensure glib2 is present (the 'dbus' layer's desktops carry it)")
	}
	return nil
}
