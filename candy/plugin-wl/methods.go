package wl

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opencharly/charly/candy/plugin-wl/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// methods.go is the wl method dispatcher + the venue-driving layer, ported from
// charly/wl.go + charly/wl_overlay.go (the deleted host-side WlCmd/WlOverlayCmd). The
// ~40-method surface (status/toplevel/windows/geometry/xprop/atspi/screenshot/clipboard +
// click/type/key/mouse/scroll/drag/focus/close/fullscreen/minimize/exec/resolution +
// overlay-{list,status,show,hide} + sway-{tree,workspaces,outputs,msg,focus,move,resize,
// layout,workspace,kill,floating,reload}) was refactored from CLI Run() methods that
// PRINTED output into functions that RETURN the captured output string — so provider.go can
// feed the output through the shared sdk matcher pipeline + the artifact validators
// (screenshot). Every in-venue action (wlrctl/grim/wtype/wl-clipboard/swaymsg/kdotool/
// python3-pyatspi/charly-overlay) runs over the host executor reverse channel
// (sdk.Executor.RunCapture; screenshot pulls the PNG via GetFile) instead of the in-proc
// DeployExecutor the host-side WlCmd used. The per-verb fields arrive in the step's
// desugared plugin_input, decoded into the CUE-generated params.WlInput (#WlInput); only
// the genuinely shared step matchers still ride the Op. The CLI-only
// `--from-cdp`/`--from-sway`/`--from-x11` coordinate translation is DROPPED (the
// declarative click method uses X/Y directly), exactly as cdp/vnc dropped their From*
// flags — so this module carries NO CDP client and NO X11 geometry helper.

const screenshotVenuePath = "/tmp/charly-wl-screenshot.png"

// requiredModifiers mirrors the in-tree wlMethods required-field specs (the host's
// validate-time + runtime required-modifier check keyed off the former in-proc live-verb seam,
// which an external verb is not — so the check moves HERE, at dispatch). The names are
// plugin INPUT keys (sdk.OpModifierZero is map-first over op.PluginInput), keeping the
// zero-value required-field semantics — an int coordinate field is "missing" when zero,
// exactly as the host checked it.
var requiredModifiers = map[string][]string{
	"geometry":       {"target"},
	"atspi":          {"action"},
	"screenshot":     {"artifact"},
	"clipboard":      {"action"},
	"click":          {"x", "y"},
	"double-click":   {"x", "y"},
	"mouse":          {"x", "y"},
	"scroll":         {"x", "y", "direction"},
	"drag":           {"x", "y", "x2", "y2"},
	"type":           {"text"},
	"key":            {"key"},
	"key-combo":      {"combo"},
	"focus":          {"target"},
	"close":          {"target"},
	"fullscreen":     {"target"},
	"minimize":       {"target"},
	"exec":           {"command"},
	"resolution":     {"target"},
	"overlay-show":   {"text"},
	"overlay-hide":   {"target"},
	"sway-msg":       {"command"},
	"sway-focus":     {"target"},
	"sway-move":      {"target"},
	"sway-resize":    {"target"},
	"sway-layout":    {"target"},
	"sway-workspace": {"target"},
}

// dispatch runs one wl method against the venue (over the host executor reverse channel)
// and returns its captured output. A returned error is the verb FAILING (the in-tree CLI
// Run() returning an error → exit 1); provider.go maps it through the exit_status / stderr
// matchers + the artifact validators (screenshot). op carries the shared step modifiers
// (the required-modifier check reads the input map through it); the wl-exclusive fields
// ride the decoded params.WlInput.
func dispatch(ctx context.Context, ex *sdk.Executor, op *spec.Op, in *params.WlInput) (string, error) {
	method := in.Method
	if err := sdk.RequireModifiers(method, op, requiredModifiers); err != nil {
		return "", err
	}
	switch method {
	// queries
	case "status":
		return wlStatus(ctx, ex)
	case "toplevel":
		return wlToplevel(ctx, ex)
	case "windows":
		return wlWindows(ctx, ex)
	case "geometry":
		return wlGeometry(ctx, ex, in)
	case "xprop":
		return wlXprop(ctx, ex, in)
	case "atspi":
		return wlAtspi(ctx, ex, in)
	case "screenshot":
		return wlScreenshot(ctx, ex, in)
	case "clipboard":
		return wlClipboard(ctx, ex, in)
	// side-effect actions
	case "click":
		return wlClick(ctx, ex, in)
	case "double-click":
		return wlDoubleClick(ctx, ex, in)
	case "mouse":
		return wlMouse(ctx, ex, in)
	case "scroll":
		return wlScroll(ctx, ex, in)
	case "drag":
		return wlDrag(ctx, ex, in)
	case "type":
		return wlType(ctx, ex, in)
	case "key":
		return wlKey(ctx, ex, in)
	case "key-combo":
		return wlKeyCombo(ctx, ex, in)
	case "focus":
		return wlFocus(ctx, ex, in)
	case "close":
		return wlClose(ctx, ex, in)
	case "fullscreen":
		return wlFullscreen(ctx, ex, in)
	case "minimize":
		return wlMinimize(ctx, ex, in)
	case "exec":
		return wlExec(ctx, ex, in)
	case "resolution":
		return wlResolution(ctx, ex, in)
	// overlay nested
	case "overlay-list":
		return wlCapture(ctx, ex, "charly-overlay list")
	case "overlay-status":
		return wlCapture(ctx, ex, "charly-overlay status")
	case "overlay-show":
		return wlOverlayShow(ctx, ex, in)
	case "overlay-hide":
		return wlOverlayHide(ctx, ex, in)
	// sway nested
	case "sway-tree":
		return swaymsgCapture(ctx, ex, "-t", "get_tree")
	case "sway-workspaces":
		return swaymsgCapture(ctx, ex, "-t", "get_workspaces")
	case "sway-outputs":
		return swaymsgCapture(ctx, ex, "-t", "get_outputs")
	case "sway-msg":
		return swaymsgCapture(ctx, ex, in.Command)
	case "sway-focus":
		return swayFocus(ctx, ex, in)
	case "sway-move":
		return swayMove(ctx, ex, in)
	case "sway-resize":
		return swaymsgCapture(ctx, ex, append([]string{"resize"}, strings.Fields(in.Target)...)...)
	case "sway-layout":
		return swaymsgCapture(ctx, ex, "layout", in.Target)
	case "sway-workspace":
		return swaymsgCapture(ctx, ex, "workspace", "number", in.Target)
	case "sway-kill":
		return swaymsgCapture(ctx, ex, "kill")
	case "sway-floating":
		return swaymsgCapture(ctx, ex, "floating", "toggle")
	case "sway-reload":
		return swaymsgCapture(ctx, ex, "reload")
	}
	return "", fmt.Errorf("unknown wl method %q", method)
}

// ---------------------------------------------------------------------------
// Query methods (ported from charly/wl.go)
// ---------------------------------------------------------------------------

// wlStatus reports the running compositor + per-tool availability + resolution + XWayland
// state, assembled as a report string (the in-tree WlStatusCmd printed it). The host-side
// EngineClient quick-probe summary line is dropped — the plugin has no podman engine handle,
// so it builds the report purely from venue RunCapture probes.
func wlStatus(ctx context.Context, ex *sdk.Executor) (string, error) {
	var b strings.Builder
	comp := detectCompositor(ctx, ex)
	fmt.Fprintf(&b, "%-12s %s\n", "compositor:", comp)

	for _, tool := range []string{"grim", "wtype", "wlrctl", "kdotool", "pixelflux-screenshot", "wl-copy", "wl-paste", "wlr-randr", "xdotool", "import", "xprop"} {
		if ex.VenueHasTool(ctx, tool) {
			fmt.Fprintf(&b, "%-12s available\n", tool+":")
		} else {
			fmt.Fprintf(&b, "%-12s not found\n", tool+":")
		}
	}

	atspiCheck := `/usr/bin/python3 -c "import gi; gi.require_version('Atspi','2.0')" 2>/dev/null`
	if ex.VenueRunSilent(ctx, atspiCheck) == nil {
		fmt.Fprintf(&b, "%-12s available\n", "atspi:")
	} else {
		fmt.Fprintf(&b, "%-12s not found\n", "atspi:")
	}

	gotResolution := false
	if data, err := swaymsgCaptureBytes(ctx, ex, "-t", "get_outputs"); err == nil {
		var outputs []struct {
			Name        string `json:"name"`
			CurrentMode struct {
				Width  int `json:"width"`
				Height int `json:"height"`
			} `json:"current_mode"`
		}
		if err := json.Unmarshal(data, &outputs); err == nil && len(outputs) > 0 {
			o := outputs[0]
			fmt.Fprintf(&b, "%-12s %s %dx%d (sway)\n", "output:", o.Name, o.CurrentMode.Width, o.CurrentMode.Height)
			gotResolution = true
		}
	}
	// wlr-randr needs the wlr-output-management protocol, which KWin does NOT
	// implement — on KWin it HANGS forever (no reply), wedging check-live for the
	// full deadline. Only probe it off KWin; on KWin report resolution unavailable.
	if !gotResolution && detectCompositor(ctx, ex) != "kwin" {
		if out, err := wlCapture(ctx, ex, "wlr-randr 2>/dev/null | head -3"); err == nil {
			if line := strings.TrimSpace(out); line != "" {
				fmt.Fprintf(&b, "%-12s %s\n", "output:", strings.Split(line, "\n")[0])
				gotResolution = true
			}
		}
	}
	if !gotResolution {
		fmt.Fprintf(&b, "%-12s unavailable (no sway/wlr-output-management)\n", "output:")
	}

	if ex.VenueRunSilent(ctx, `pgrep -f Xwayland >/dev/null 2>&1`) == nil {
		count := ""
		if out, err := wlCapture(ctx, ex, `DISPLAY=:0 xdotool search --name "." 2>/dev/null | wc -l`); err == nil {
			count = strings.TrimSpace(out)
		}
		if count == "" || count == "0" {
			fmt.Fprintf(&b, "%-12s running (no X11 clients)\n", "xwayland:")
		} else {
			fmt.Fprintf(&b, "%-12s running (%s X11 windows)\n", "xwayland:", count)
		}
	} else {
		fmt.Fprintf(&b, "%-12s not running (starts on demand)\n", "xwayland:")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// wlToplevel lists Wayland toplevel windows via wlrctl (KWin: kdotool window IDs).
func wlToplevel(ctx context.Context, ex *sdk.Executor) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		return wlCapture(ctx, ex, "kdotool search ''")
	}
	return wlCapture(ctx, ex, "wlrctl toplevel list")
}

// wlWindows lists windows: wlrctl toplevel (compositor-agnostic) then xdotool (XWayland);
// KWin uses kdotool window IDs.
func wlWindows(ctx context.Context, ex *sdk.Executor) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		return wlCapture(ctx, ex, "kdotool search ''")
	}
	if ex.VenueRunSilent(ctx, "command -v wlrctl >/dev/null 2>&1") == nil {
		if out, err := wlCapture(ctx, ex, "wlrctl toplevel list"); err == nil {
			return out, nil
		}
	}
	shellCmd := `export DISPLAY=:0 && xdotool search --name "." 2>/dev/null | while read wid; do
		name=$(xdotool getwindowname "$wid" 2>/dev/null)
		[ -n "$name" ] && printf "%s\t%s\n" "$wid" "$name"
	done`
	return wlCapture(ctx, ex, shellCmd)
}

// wlGeometry gets window geometry compositor-agnostically: kdotool (KWin), the sway tree,
// xdotool (XWayland), then wlr-randr (Wayland-native maximized fallback).
func wlGeometry(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		return kdotoolSearchAction(ctx, ex, in.Target, "getwindowgeometry")
	}

	if rect, err := FindWindowRect(ctx, ex, in.Target); err == nil {
		out, _ := json.Marshal(map[string]int{"x": rect.X, "y": rect.Y, "width": rect.Width, "height": rect.Height})
		return string(out), nil
	}

	shellCmd := fmt.Sprintf(
		`export DISPLAY=:0 && WID=$(xdotool search --class %s 2>/dev/null | head -1 || xdotool search --name %s 2>/dev/null | head -1) && [ -n "$WID" ] && xdotool getwindowgeometry "$WID" 2>/dev/null`,
		kit.ShellQuote(in.Target), kit.ShellQuote(in.Target),
	)
	if data, err := wlCapture(ctx, ex, shellCmd); err == nil {
		var x, y, w, h int
		for line := range strings.SplitSeq(data, "\n") {
			line = strings.TrimSpace(line)
			if after, ok := strings.CutPrefix(line, "Position:"); ok {
				pos := strings.Split(strings.TrimSpace(after), " ")[0]
				if coords := strings.SplitN(pos, ",", 2); len(coords) == 2 {
					x, _ = strconv.Atoi(coords[0])
					y, _ = strconv.Atoi(coords[1])
				}
			}
			if after, ok := strings.CutPrefix(line, "Geometry:"); ok {
				if dims := strings.SplitN(strings.TrimSpace(after), "x", 2); len(dims) == 2 {
					w, _ = strconv.Atoi(dims[0])
					h, _ = strconv.Atoi(dims[1])
				}
			}
		}
		out, _ := json.Marshal(map[string]int{"x": x, "y": y, "width": w, "height": h})
		return string(out), nil
	}

	if randrOut, err := wlCapture(ctx, ex, "wlr-randr 2>/dev/null"); err == nil {
		for line := range strings.SplitSeq(randrOut, "\n") {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "current") && strings.Contains(line, "px") {
				res := strings.Fields(line)[0]
				if dims := strings.SplitN(res, "x", 2); len(dims) == 2 {
					w, _ := strconv.Atoi(dims[0])
					h, _ := strconv.Atoi(dims[1])
					out, _ := json.Marshal(map[string]int{"x": 0, "y": 0, "width": w, "height": h})
					return string(out), nil
				}
			}
		}
	}
	return "", fmt.Errorf("querying geometry for %q: not found via sway, xdotool, or wlr-randr", in.Target)
}

// wlXprop queries X11 window properties via xprop (XWayland windows only).
func wlXprop(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if ex.VenueRunSilent(ctx, `pgrep -f Xwayland >/dev/null 2>&1`) != nil {
		return "XWayland is not running (no X11 clients have been launched)", nil
	}
	var shellCmd string
	if in.Target == "" {
		shellCmd = `export DISPLAY=:0 && WID=$(xdotool getactivewindow 2>/dev/null) && [ -n "$WID" ] && xprop -id "$WID" WM_CLASS _NET_WM_NAME _NET_WM_WINDOW_TYPE _NET_WM_PID 2>/dev/null && xdotool getwindowgeometry "$WID" 2>/dev/null || echo "No active X11 window"`
	} else {
		shellCmd = fmt.Sprintf(
			`export DISPLAY=:0 && WID=$(xdotool search --class %s 2>/dev/null | head -1 || xdotool search --name %s 2>/dev/null | head -1) && [ -n "$WID" ] && xprop -id "$WID" WM_CLASS _NET_WM_NAME _NET_WM_WINDOW_TYPE _NET_WM_PID 2>/dev/null && xdotool getwindowgeometry "$WID" 2>/dev/null || echo "No X11 window matching %s"`,
			kit.ShellQuote(in.Target), kit.ShellQuote(in.Target), in.Target,
		)
	}
	return wlCapture(ctx, ex, shellCmd)
}

// wlAtspi queries the accessibility tree via AT-SPI2 (python3-pyatspi). Uses /usr/bin/python3
// explicitly so the system RPM packages (python3-pyatspi, python3-gobject) resolve, not a
// pixi python first on PATH.
func wlAtspi(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	switch in.Action {
	case "tree", "find", "click":
	default:
		return "", fmt.Errorf("unknown atspi action %q (valid: tree, find, click)", in.Action)
	}
	var shellCmd string
	if in.Query != "" {
		shellCmd = fmt.Sprintf("/usr/bin/python3 -c %s %s %s", kit.ShellQuote(atspiScript), kit.ShellQuote(in.Action), kit.ShellQuote(in.Query))
	} else {
		shellCmd = fmt.Sprintf("/usr/bin/python3 -c %s %s", kit.ShellQuote(atspiScript), kit.ShellQuote(in.Action))
	}
	wrapped := fmt.Sprintf(`export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=/tmp/dbus-session}" && %s`, shellCmd)
	return wlCapture(ctx, ex, wrapped)
}

// wlScreenshot captures the desktop to a venue file (pixelflux-screenshot / grim), pulls it
// off the venue over the reverse channel (GetFile), and writes it to in.Artifact (the host
// path) BEFORE the provider's RunArtifactValidators reads it.
func wlScreenshot(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	var captureCmd string
	switch {
	case ex.VenueHasTool(ctx, "pixelflux-screenshot"):
		captureCmd = "pixelflux-screenshot > " + kit.ShellQuote(screenshotVenuePath)
	case ex.VenueHasTool(ctx, "grim"):
		captureCmd = "grim -o HEADLESS-1 " + kit.ShellQuote(screenshotVenuePath)
	default:
		return "", fmt.Errorf("no screenshot tool available (need pixelflux-screenshot or grim)")
	}
	if _, err := wlCapture(ctx, ex, captureCmd); err != nil {
		return "", fmt.Errorf("capturing screenshot: %w", err)
	}
	data, err := ex.GetFile(ctx, screenshotVenuePath, false)
	if err != nil {
		return "", fmt.Errorf("pulling screenshot: %w (file: %s)", err, screenshotVenuePath)
	}
	if err := os.WriteFile(in.Artifact, data, 0o644); err != nil {
		return "", fmt.Errorf("writing screenshot to %s: %w", in.Artifact, err)
	}
	_ = ex.VenueRunSilent(ctx, "rm -f "+kit.ShellQuote(screenshotVenuePath))
	return fmt.Sprintf("Screenshot saved to %s (%d bytes)", in.Artifact, len(data)), nil
}

// wlClipboard reads or writes the Wayland clipboard via wl-clipboard.
func wlClipboard(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	// wl-clipboard (wl-copy/wl-paste) needs the wlr-data-control protocol, which
	// KWin does NOT implement — on KWin these HANG forever (no reply), wedging
	// check-live for the full deadline. Fail fast with a clear error instead.
	if detectCompositor(ctx, ex) == "kwin" {
		return "", fmt.Errorf("clipboard unsupported on KWin (needs wlr-data-control, which KWin does not implement)")
	}
	switch in.Action {
	case "get":
		return wlCapture(ctx, ex, "wl-paste 2>/dev/null")
	case "set":
		if in.Text == "" {
			return "", fmt.Errorf("text argument required for 'set' action")
		}
		if _, err := wlCapture(ctx, ex, fmt.Sprintf("printf '%%s' %s | wl-copy", kit.ShellQuote(in.Text))); err != nil {
			return "", fmt.Errorf("setting clipboard: %w", err)
		}
		return fmt.Sprintf("Clipboard set (%d chars)", len(in.Text)), nil
	case "clear":
		if _, err := wlCapture(ctx, ex, "wl-copy --clear"); err != nil {
			return "", fmt.Errorf("clearing clipboard: %w", err)
		}
		return "Clipboard cleared", nil
	default:
		return "", fmt.Errorf("unknown action %q (valid: get, set, clear)", in.Action)
	}
}

// ---------------------------------------------------------------------------
// Action methods (ported from charly/wl.go) — declarative X/Y are used directly;
// the CLI-only --from-cdp/--from-sway/--from-x11 translation is dropped.
// ---------------------------------------------------------------------------

func wlClick(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		return "", errKWinPointerUnsupported("click")
	}
	btn := wlButton(in.Button)
	if btn == "" {
		return "", fmt.Errorf("unknown button %q (valid: left, right, middle)", in.Button)
	}
	cmd := fmt.Sprintf(
		"wlrctl pointer move -10000 -10000 && wlrctl pointer move %d %d && sleep 0.05 && wlrctl pointer click %s",
		in.X, in.Y, btn,
	)
	if _, err := wlCapture(ctx, ex, cmd); err != nil {
		return "", fmt.Errorf("clicking at (%d, %d): %w", in.X, in.Y, err)
	}
	return fmt.Sprintf("Clicked %s at (%d, %d)", btnName(in.Button), in.X, in.Y), nil
}

func wlDoubleClick(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		return "", errKWinPointerUnsupported("double-click")
	}
	btn := wlButton(in.Button)
	if btn == "" {
		return "", fmt.Errorf("unknown button %q (valid: left, right, middle)", in.Button)
	}
	cmd := fmt.Sprintf(
		"wlrctl pointer move -10000 -10000 && wlrctl pointer move %d %d && sleep 0.05 && wlrctl pointer click %s && sleep 0.050 && wlrctl pointer click %s",
		in.X, in.Y, btn, btn,
	)
	if _, err := wlCapture(ctx, ex, cmd); err != nil {
		return "", fmt.Errorf("double-clicking at (%d, %d): %w", in.X, in.Y, err)
	}
	return fmt.Sprintf("Double-clicked %s at (%d, %d)", btnName(in.Button), in.X, in.Y), nil
}

func wlMouse(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		return "", errKWinPointerUnsupported("mouse")
	}
	cmd := fmt.Sprintf("wlrctl pointer move -10000 -10000 && wlrctl pointer move %d %d", in.X, in.Y)
	if _, err := wlCapture(ctx, ex, cmd); err != nil {
		return "", fmt.Errorf("moving mouse to (%d, %d): %w", in.X, in.Y, err)
	}
	return fmt.Sprintf("Moved mouse to (%d, %d)", in.X, in.Y), nil
}

func wlScroll(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	btn, err := wlScrollButton(in.Direction)
	if err != nil {
		return "", err
	}
	if detectCompositor(ctx, ex) == "kwin" {
		return "", errKWinPointerUnsupported("scroll")
	}
	amount := in.Amount
	if amount == 0 {
		amount = 3
	}
	moveCmd := fmt.Sprintf("wlrctl pointer move -10000 -10000 && wlrctl pointer move %d %d", in.X, in.Y)
	if err := wlSilent(ctx, ex, moveCmd); err != nil {
		return "", fmt.Errorf("moving pointer to (%d, %d): %w", in.X, in.Y, err)
	}
	var clickCmds []string
	for range amount {
		clickCmds = append(clickCmds, fmt.Sprintf("DISPLAY=:0 xdotool click %d", btn))
	}
	if _, err := wlCapture(ctx, ex, strings.Join(clickCmds, " && sleep 0.02 && ")); err != nil {
		var keyName string
		switch in.Direction {
		case "up":
			keyName = "Page_Up"
		case "down":
			keyName = "Page_Down"
		default:
			return "", fmt.Errorf("scrolling %s at (%d, %d): xdotool failed and no wtype fallback for %s: %w", in.Direction, in.X, in.Y, in.Direction, err)
		}
		for range amount {
			if _, err := wlCapture(ctx, ex, "wtype -k "+keyName); err != nil {
				return "", fmt.Errorf("scroll fallback via wtype: %w", err)
			}
		}
		return fmt.Sprintf("Scrolled %s %d steps at (%d, %d) via wtype fallback", in.Direction, amount, in.X, in.Y), nil
	}
	return fmt.Sprintf("Scrolled %s %d steps at (%d, %d)", in.Direction, amount, in.X, in.Y), nil
}

func wlDrag(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		return "", errKWinPointerUnsupported("drag")
	}
	var btnNum int
	switch in.Button {
	case "", "left":
		btnNum = 1
	case "middle":
		btnNum = 2
	case "right":
		btnNum = 3
	default:
		return "", fmt.Errorf("unknown button %q (valid: left, right, middle)", in.Button)
	}
	cmd := fmt.Sprintf(
		"export DISPLAY=:0 && xdotool mousemove %d %d mousedown %d && sleep 0.200 && xdotool mousemove %d %d mouseup %d",
		in.X, in.Y, btnNum, in.X2, in.Y2, btnNum,
	)
	if _, err := wlCapture(ctx, ex, cmd); err != nil {
		return "", fmt.Errorf("dragging from (%d,%d) to (%d,%d): %w (requires XWayland)", in.X, in.Y, in.X2, in.Y2, err)
	}
	return fmt.Sprintf("Dragged from (%d, %d) to (%d, %d)", in.X, in.Y, in.X2, in.Y2), nil
}

func wlType(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	// wtype needs zwp_virtual_keyboard_manager_v1, which KWin does NOT implement —
	// on KWin it HANGS forever (no reply), wedging check-live. Fail fast instead.
	if detectCompositor(ctx, ex) == "kwin" {
		return "", fmt.Errorf("keyboard typing unsupported on KWin (needs zwp_virtual_keyboard_manager_v1, which KWin does not implement)")
	}
	if _, err := wlCapture(ctx, ex, "wtype -- "+kit.ShellQuote(in.Text)); err != nil {
		return "", fmt.Errorf("typing text: %w", err)
	}
	return fmt.Sprintf("Typed %d characters", len(in.Text)), nil
}

func wlKey(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if !wlValidKey(in.KeyName) {
		return "", fmt.Errorf("unknown key %q (valid: %s)", in.KeyName, wlKeyNames())
	}
	if _, err := wlCapture(ctx, ex, "wtype -k "+kit.ShellQuote(in.KeyName)); err != nil {
		return "", fmt.Errorf("pressing key %s: %w", in.KeyName, err)
	}
	return fmt.Sprintf("Pressed key %s", in.KeyName), nil
}

func wlKeyCombo(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	modifiers, key, err := parseKeyCombo(in.Combo)
	if err != nil {
		return "", err
	}
	var args []string
	for _, mod := range modifiers {
		args = append(args, "-M", mod)
	}
	if len(key) == 1 {
		args = append(args, key)
	} else {
		args = append(args, "-k", key)
	}
	if _, err := wlCapture(ctx, ex, "wtype "+strings.Join(args, " ")); err != nil {
		return "", fmt.Errorf("sending key combo %s: %w", in.Combo, err)
	}
	return fmt.Sprintf("Sent key combo %s", in.Combo), nil
}

func wlFocus(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		if _, err := kdotoolSearchAction(ctx, ex, in.Target, "windowactivate"); err != nil {
			return "", fmt.Errorf("focusing window %q via kdotool: %w", in.Target, err)
		}
		return fmt.Sprintf("Focused window matching %q via kdotool", in.Target), nil
	}
	if ex.VenueRunSilent(ctx, "command -v wlrctl >/dev/null 2>&1") == nil {
		if wlSilent(ctx, ex, "wlrctl toplevel focus "+kit.ShellQuote(in.Target)) == nil {
			return fmt.Sprintf("Focused window matching %q via wlrctl", in.Target), nil
		}
	}
	cmd := fmt.Sprintf(
		`export DISPLAY=:0 && xdotool search --name %s windowactivate 2>/dev/null || export DISPLAY=:0 && xdotool search --class %s windowactivate`,
		kit.ShellQuote(in.Target), kit.ShellQuote(in.Target),
	)
	if _, err := wlCapture(ctx, ex, cmd); err != nil {
		return "", fmt.Errorf("focusing window %q: %w", in.Target, err)
	}
	return fmt.Sprintf("Focused window matching %q via xdotool", in.Target), nil
}

func wlClose(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		if _, err := kdotoolSearchAction(ctx, ex, in.Target, "windowclose"); err != nil {
			return "", fmt.Errorf("closing window %q via kdotool: %w", in.Target, err)
		}
		return fmt.Sprintf("Closed window matching %q", in.Target), nil
	}
	if err := wlrctlToplevel(ctx, ex, "close", in.Target); err != nil {
		return "", fmt.Errorf("closing window %q: %w", in.Target, err)
	}
	return fmt.Sprintf("Closed window matching %q", in.Target), nil
}

func wlFullscreen(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		if _, err := kdotoolSearchAction(ctx, ex, in.Target, "windowstate", "--toggle", "FULLSCREEN"); err != nil {
			return "", fmt.Errorf("toggling fullscreen on %q via kdotool: %w", in.Target, err)
		}
		return fmt.Sprintf("Toggled fullscreen on window matching %q", in.Target), nil
	}
	if err := wlrctlToplevel(ctx, ex, "fullscreen", in.Target); err != nil {
		return "", fmt.Errorf("toggling fullscreen on %q: %w", in.Target, err)
	}
	return fmt.Sprintf("Toggled fullscreen on window matching %q", in.Target), nil
}

func wlMinimize(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		if _, err := kdotoolSearchAction(ctx, ex, in.Target, "windowminimize"); err != nil {
			return "", fmt.Errorf("toggling minimize on %q via kdotool: %w", in.Target, err)
		}
		return fmt.Sprintf("Toggled minimize on window matching %q", in.Target), nil
	}
	if err := wlrctlToplevel(ctx, ex, "minimize", in.Target); err != nil {
		return "", fmt.Errorf("toggling minimize on %q: %w", in.Target, err)
	}
	return fmt.Sprintf("Toggled minimize on window matching %q", in.Target), nil
}

func wlExec(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	// Background the process so it doesn't block. DISPLAY=:0 for XWayland apps.
	// Don't shellQuote — the command may contain args (e.g. "xterm -hold").
	cmd := fmt.Sprintf("export DISPLAY=:0; %s &", in.Command)
	if _, err := wlCapture(ctx, ex, cmd); err != nil {
		return "", fmt.Errorf("launching %q: %w", in.Command, err)
	}
	return fmt.Sprintf("Launched %q", in.Command), nil
}

func wlResolution(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if detectCompositor(ctx, ex) == "kwin" {
		return "", fmt.Errorf("wl resolution: not supported on KWin (kscreen-doctor has no working backend on the headless Plasma session — it hangs; tracked as its own cutover). The selkies stream resolution is set at session start, not at runtime")
	}
	// in.Target carries the WxH resolution (the in-tree resolution positional).
	res := in.Target
	parts := strings.SplitN(res, "x", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid resolution %q (expected WxH, e.g. 1920x1080)", res)
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return "", fmt.Errorf("invalid width in %q: %w", res, err)
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return "", fmt.Errorf("invalid height in %q: %w", res, err)
	}
	output := ""
	if data, err := wlCapture(ctx, ex, "wlr-randr 2>/dev/null | head -1"); err == nil {
		if fields := strings.Fields(strings.TrimSpace(data)); len(fields) > 0 {
			output = fields[0]
		}
	}
	if output == "" {
		output = "HEADLESS-1"
	}
	cmd := fmt.Sprintf("wlr-randr --output %s --custom-mode %s", kit.ShellQuote(output), kit.ShellQuote(res))
	if _, err := wlCapture(ctx, ex, cmd); err != nil {
		return "", fmt.Errorf("setting resolution: %w", err)
	}
	return fmt.Sprintf("Set %s to %s", output, res), nil
}

// ---------------------------------------------------------------------------
// Overlay methods (ported from charly/wl_overlay.go)
// ---------------------------------------------------------------------------

// overlayDaemonSession is the tmux session name for the overlay daemon.
const overlayDaemonSession = "charly-overlay-daemon"

func wlOverlayShow(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if err := checkOverlayAvailable(ctx, ex); err != nil {
		return "", err
	}
	if err := ensureOverlayDaemon(ctx, ex); err != nil {
		return "", err
	}
	// The declarative overlay-show is the text-overlay positional form: the text is
	// required; in.Target, when set, names the overlay.
	args := "charly-overlay show --type text --text " + kit.ShellQuote(in.Text)
	if in.Target != "" {
		args += " --name " + kit.ShellQuote(in.Target)
	}
	return wlCapture(ctx, ex, args)
}

func wlOverlayHide(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if in.Target == "all" {
		return wlCapture(ctx, ex, "charly-overlay hide --all")
	}
	return wlCapture(ctx, ex, "charly-overlay hide --name "+kit.ShellQuote(in.Target))
}

// checkOverlayAvailable verifies charly-overlay is installed on the venue.
func checkOverlayAvailable(ctx context.Context, ex *sdk.Executor) error {
	if ex.VenueRunSilent(ctx, "command -v charly-overlay >/dev/null 2>&1") != nil {
		return fmt.Errorf("charly-overlay not available on the target (add the wl-overlay candy to your box, or install it on the host/VM)")
	}
	return nil
}

// ensureOverlayDaemon starts the overlay daemon in a tmux session on the venue if not
// already running. The daemon hosts the overlay socket; the tmux session keeps it alive
// across the short-lived overlay invocations.
func ensureOverlayDaemon(ctx context.Context, ex *sdk.Executor) error {
	if ex.VenueRunSilent(ctx, "test -S /tmp/charly-overlay.sock") == nil {
		return nil
	}
	if ex.VenueRunSilent(ctx, "command -v tmux >/dev/null 2>&1") != nil {
		return fmt.Errorf("tmux not available on the target (needed to host the overlay daemon)")
	}
	_ = ex.VenueRunSilent(ctx, "rm -f /tmp/charly-overlay.sock")
	daemonCmd := wlShellCmd("charly-overlay daemon")
	startScript := fmt.Sprintf("tmux new-session -d -s %s sh -c %s", overlayDaemonSession, kit.ShellQuote(daemonCmd))
	if _, stderr, exit, err := ex.RunCapture(ctx, startScript); err != nil {
		return fmt.Errorf("starting overlay daemon: %w", err)
	} else if exit != 0 {
		return fmt.Errorf("starting overlay daemon: %s", strings.TrimSpace(stderr))
	}
	for range 20 {
		time.Sleep(250 * time.Millisecond)
		if ex.VenueRunSilent(ctx, "test -S /tmp/charly-overlay.sock") == nil {
			return nil
		}
	}
	return fmt.Errorf("overlay daemon started but socket not ready after 5s")
}

// ---------------------------------------------------------------------------
// Sway IPC methods (ported from charly/wl.go)
// ---------------------------------------------------------------------------

func swayFocus(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	if strings.Contains(in.Target, "=") || strings.HasPrefix(in.Target, "[") {
		criteria := in.Target
		if !strings.HasPrefix(criteria, "[") {
			criteria = "[" + criteria + "]"
		}
		return swaymsgCapture(ctx, ex, criteria+" focus")
	}
	return swaymsgCapture(ctx, ex, "focus", in.Target)
}

func swayMove(ctx context.Context, ex *sdk.Executor, in *params.WlInput) (string, error) {
	target := in.Target
	if strings.HasPrefix(target, "workspace") {
		ws := strings.TrimPrefix(target, "workspace ")
		return swaymsgCapture(ctx, ex, "move", "container", "to", "workspace", "number", ws)
	}
	return swaymsgCapture(ctx, ex, append([]string{"move"}, strings.Fields(target)...)...)
}

// ---------------------------------------------------------------------------
// Sway tree window-rect lookup (ported from charly/wl.go — used by geometry)
// ---------------------------------------------------------------------------

// SwayRect represents a window's position and size on the desktop.
type SwayRect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type swayWindowProperties struct {
	Class string `json:"class"`
}

type swayNode struct {
	Name             string                `json:"name"`
	AppID            string                `json:"app_id"`
	Rect             SwayRect              `json:"rect"`
	Focused          bool                  `json:"focused"`
	FullscreenMode   int                   `json:"fullscreen_mode"`
	WindowProperties *swayWindowProperties `json:"window_properties,omitempty"`
	Nodes            []swayNode            `json:"nodes"`
	FloatingNodes    []swayNode            `json:"floating_nodes"`
}

// FindWindowRect searches the sway tree for a window matching appID or X11 class.
func FindWindowRect(ctx context.Context, ex *sdk.Executor, appID string) (SwayRect, error) {
	data, err := swaymsgCaptureBytes(ctx, ex, "-t", "get_tree")
	if err != nil {
		return SwayRect{}, fmt.Errorf("querying sway tree: %w", err)
	}
	var root swayNode
	if err := json.Unmarshal(data, &root); err != nil {
		return SwayRect{}, fmt.Errorf("parsing sway tree: %w", err)
	}
	rect, found := searchSwayNode(&root, appID)
	if !found {
		return SwayRect{}, fmt.Errorf("window with app_id or class %q not found in sway tree", appID)
	}
	return rect, nil
}

func searchSwayNode(node *swayNode, appID string) (SwayRect, bool) {
	var matches []swayNode
	collectSwayMatches(node, appID, &matches)
	if len(matches) == 0 {
		return SwayRect{}, false
	}
	best := matches[0]
	for _, m := range matches[1:] {
		if m.Focused {
			best = m
			break
		}
		if m.FullscreenMode > best.FullscreenMode {
			best = m
		} else if m.FullscreenMode == best.FullscreenMode &&
			m.Rect.Width*m.Rect.Height > best.Rect.Width*best.Rect.Height {
			best = m
		}
	}
	return best.Rect, true
}

func collectSwayMatches(node *swayNode, appID string, matches *[]swayNode) {
	matched := (node.AppID == appID) ||
		(node.WindowProperties != nil && node.WindowProperties.Class == appID)
	if matched && node.Rect.Width > 0 {
		*matches = append(*matches, *node)
	}
	for i := range node.Nodes {
		collectSwayMatches(&node.Nodes[i], appID, matches)
	}
	for i := range node.FloatingNodes {
		collectSwayMatches(&node.FloatingNodes[i], appID, matches)
	}
}

// ---------------------------------------------------------------------------
// Venue command helpers (over the executor reverse channel)
// ---------------------------------------------------------------------------

// wlCompositorEnvPrelude sources the RUNNING compositor's real session environment from its
// process before applying safe fallbacks (load-bearing for KWin's random dbus-run-session
// bus + wayland-1; a strict improvement for sway/labwc). Ported verbatim from charly/wl.go.
const wlCompositorEnvPrelude = `for __c in kwin_wayland sway labwc; do __p=$(pgrep -x "$__c" 2>/dev/null | head -1); [ -n "$__p" ] && break; done; ` +
	`if [ -n "$__p" ] && [ -r /proc/$__p/environ ]; then eval "$(tr '\0' '\n' < /proc/$__p/environ | grep -E '^(XDG_RUNTIME_DIR|WAYLAND_DISPLAY|DBUS_SESSION_BUS_ADDRESS)=' | sed 's/^/export /')"; fi; ` +
	`export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp}" WAYLAND_DISPLAY="${WAYLAND_DISPLAY:-wayland-0}"`

// wlShellCmd wraps a command with the live compositor session environment.
func wlShellCmd(cmd string) string {
	return fmt.Sprintf("%s && %s", wlCompositorEnvPrelude, cmd)
}

// wlCapture runs a Wayland tool command on the venue (with the compositor env prelude) and
// returns its stdout, surfacing stderr on a non-zero exit.
func wlCapture(ctx context.Context, ex *sdk.Executor, cmd string) (string, error) {
	return ex.VenueCapture(ctx, wlShellCmd(cmd))
}

// wlSilent runs a Wayland tool command on the venue discarding output (probes / fire-and-
// forget input), returning an error on a non-zero exit.
func wlSilent(ctx context.Context, ex *sdk.Executor, cmd string) error {
	return ex.VenueRunSilent(ctx, wlShellCmd(cmd))
}

// detectCompositor reports "kwin" when KWin (KDE Plasma) is PID-present, else "wlroots"
// (sway / labwc). The probe runs raw (no env prelude — it needs no Wayland env itself).
func detectCompositor(ctx context.Context, ex *sdk.Executor) string {
	if ex.VenueRunSilent(ctx, "pgrep -x kwin_wayland >/dev/null 2>&1") == nil {
		return "kwin"
	}
	return "wlroots"
}

// errKWinPointerUnsupported is returned for pointer methods on KWin (no host-safe injectable
// virtual-pointer protocol; tracked as its own cutover).
func errKWinPointerUnsupported(method string) error {
	return fmt.Errorf("wl %s: pointer injection is not supported on KWin (no host-safe backend; KWin 6 removed org_kde_kwin_fake_input and the RemoteDesktop portal is approval-gated). Window management, keyboard, clipboard, and screenshots ARE supported on KWin", method)
}

// kdotoolSearchAction chains a kdotool window query with an action verb (KWin focus/close/
// minimize/fullscreen/geometry), operating on the first match, and returns its stdout.
func kdotoolSearchAction(ctx context.Context, ex *sdk.Executor, title, verb string, extra ...string) (string, error) {
	cmd := fmt.Sprintf("kdotool search --name %s %s", kit.ShellQuote(title), verb)
	if len(extra) > 0 {
		cmd += " " + strings.Join(extra, " ")
	}
	return wlCapture(ctx, ex, cmd)
}

// wlrctlToplevel runs a wlrctl toplevel action matching by app_id (sway/labwc).
func wlrctlToplevel(ctx context.Context, ex *sdk.Executor, action, target string) error {
	return wlSilent(ctx, ex, fmt.Sprintf("wlrctl toplevel %s %s", action, kit.ShellQuote(target)))
}

// ---------------------------------------------------------------------------
// Sway IPC helpers (ported from charly/wl.go)
// ---------------------------------------------------------------------------

// swaymsgShellCmd builds a shell command that discovers SWAYSOCK and runs swaymsg.
func swaymsgShellCmd(args ...string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = kit.ShellQuote(a)
	}
	return fmt.Sprintf(
		`export SWAYSOCK=$(ls -t /tmp/sway-ipc.*.sock 2>/dev/null | head -1) && [ -n "$SWAYSOCK" ] && swaymsg %s`,
		strings.Join(quoted, " "),
	)
}

// swaymsgCapture runs swaymsg on the venue and returns stdout (error on non-zero exit).
func swaymsgCapture(ctx context.Context, ex *sdk.Executor, args ...string) (string, error) {
	return ex.VenueCapture(ctx, swaymsgShellCmd(args...))
}

// swaymsgCaptureBytes is the []byte form for JSON tree/output parsing.
func swaymsgCaptureBytes(ctx context.Context, ex *sdk.Executor, args ...string) ([]byte, error) {
	out, err := swaymsgCapture(ctx, ex, args...)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// ---------------------------------------------------------------------------
// Key combo + button mappings (ported from charly/wl.go)
// ---------------------------------------------------------------------------

// wlModifierMap maps human-friendly modifier names to wtype -M arguments.
var wlModifierMap = map[string]string{
	"ctrl":    "ctrl",
	"control": "ctrl",
	"alt":     "alt",
	"shift":   "shift",
	"super":   "logo",
	"win":     "logo",
	"logo":    "logo",
	"meta":    "alt",
}

// parseKeyCombo splits a key combo string into wtype -M flags and the final key.
func parseKeyCombo(combo string) (modifiers []string, key string, err error) {
	parts := strings.Split(strings.ToLower(combo), "+")
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("empty key combination")
	}
	key = parts[len(parts)-1]
	for _, p := range parts[:len(parts)-1] {
		mod, ok := wlModifierMap[p]
		if !ok {
			return nil, "", fmt.Errorf("unknown modifier %q (valid: ctrl, alt, shift, super, win, logo, meta)", p)
		}
		modifiers = append(modifiers, mod)
	}
	return modifiers, key, nil
}

// wlScrollButton maps scroll direction to X11 button number.
func wlScrollButton(dir string) (int, error) {
	switch strings.ToLower(dir) {
	case "up":
		return 4, nil
	case "down":
		return 5, nil
	case "left":
		return 6, nil
	case "right":
		return 7, nil
	default:
		return 0, fmt.Errorf("unknown scroll direction %q (valid: up, down, left, right)", dir)
	}
}

// wlKeySet contains valid XKB key names accepted by wtype -k.
var wlKeySet = map[string]bool{
	"Return": true, "Escape": true, "Tab": true, "BackSpace": true,
	"Delete": true, "Insert": true, "Home": true, "End": true,
	"Page_Up": true, "Page_Down": true, "space": true,
	"Up": true, "Down": true, "Left": true, "Right": true,
	"F1": true, "F2": true, "F3": true, "F4": true,
	"F5": true, "F6": true, "F7": true, "F8": true,
	"F9": true, "F10": true, "F11": true, "F12": true,
	"Shift_L": true, "Shift_R": true,
	"Control_L": true, "Control_R": true,
	"Alt_L": true, "Alt_R": true,
	"Super_L": true, "Super_R": true,
	"Meta_L": true, "Meta_R": true,
	"Caps_Lock": true,
}

func wlValidKey(name string) bool { return wlKeySet[name] }

func wlKeyNames() string {
	names := make([]string, 0, len(wlKeySet))
	for k := range wlKeySet {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// wlButton maps button names to wlrctl button arguments (empty default → left).
func wlButton(name string) string {
	switch name {
	case "", "left":
		return "left"
	case "right":
		return "right"
	case "middle":
		return "middle"
	default:
		return ""
	}
}

// btnName renders a button name for messages (empty → "left").
func btnName(name string) string {
	if name == "" {
		return "left"
	}
	return name
}
