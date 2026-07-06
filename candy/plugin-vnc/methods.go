package vnc

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"strings"
	"time"

	"github.com/opencharly/charly/candy/plugin-vnc/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/spec"
)

// methods.go is the vnc method dispatcher: the 7-method surface moved from charly/vnc.go,
// refactored from CLI Run() methods that PRINTED to stdout/stderr into functions that
// RETURN the captured output string (status) or WRITE a PNG artifact + return a one-line
// confirmation (screenshot) — so provider.go can feed the output through the shared sdk
// matcher pipeline + sdk.RunArtifactValidators (a host-side matcher step does
// not run for an out-of-process verb). The RFB wire behaviour, the X11 keysym tables, and
// the status tokens are unchanged, so a bed authored against the in-tree verb passes
// unchanged. The RFB client (vnc_client.go) lives in THIS module now; the host pre-resolves
// the dialable endpoint (charly/vnc_preresolve.go) — the plugin needs no venue resolution.
//
// Two in-tree extras did NOT move: `vnc passwd` (wayvnc auth is provisioned at DEPLOY time
// by the wayvnc / sway-desktop-vnc candy, not the check verb) and the `vnc click`
// --from-cdp/--from-sway/--from-x11 CLI-only coordinate-translation flags (the declarative
// `vnc: click` uses x/y desktop-absolute coordinates directly).

// requiredModifiers mirrors the in-tree vncMethods required-field specs (the host's
// validate-time + runtime required-modifier check keyed off the former in-proc live-verb seam,
// which an external verb is not — so the check moves HERE, at dispatch). The strings name
// INPUT keys (the desugared plugin_input map): click/mouse need x+y, type needs text, key
// needs the key name, screenshot needs the artifact output path, rfb needs the RFB
// sub-method — `http_method`, the renamed former shared `method` modifier (the input's
// `method` key is the VERB method).
var requiredModifiers = map[string][]string{
	"screenshot": {"artifact"},
	"click":      {"x", "y"},
	"mouse":      {"x", "y"},
	"type":       {"text"},
	"key":        {"key"},
	"rfb":        {"http_method"},
}

// dispatch checks required modifiers, dials the host-pre-resolved RFB endpoint, and runs
// one vnc method, returning its captured output. The per-verb fields ride the typed plugin
// input (params.VncInput, decoded once by provider.go); op stays for the SHARED #Op fields
// (the required-modifier check off op.PluginInput). A returned error is the verb FAILING
// (the in-tree CLI Run() returning an error → exit 1); provider.go maps it through the
// exit_status / stderr matchers.
func dispatch(ep *vncEndpoint, op *spec.Op, in *params.VncInput) (string, error) {
	method := in.Method
	if err := sdk.RequireModifiers(method, op, requiredModifiers); err != nil {
		return "", err
	}
	c, err := NewVNCClient(ep.Addr, ep.Password)
	if err != nil {
		return "", err
	}
	defer c.Close() //nolint:errcheck

	switch method {
	case "status":
		return runStatus(c)
	case "screenshot":
		return runScreenshot(c, in.Artifact)
	case "click":
		return runClick(c, in)
	case "mouse":
		return runMouse(c, in)
	case "type":
		return runType(c, in.Text)
	case "key":
		return runKey(c, in.KeyName)
	case "rfb":
		return runRfb(c, in)
	}
	return "", fmt.Errorf("unknown vnc method %q", method)
}

// runStatus reports the RFB handshake result + display info. The first line is
// "VNC:        ok" so a `stdout: contains: ok` matcher passes — mirroring the
// cdp/wl/spice status convention.
func runStatus(c *VNCClient) (string, error) {
	var b strings.Builder
	fmt.Fprintln(&b, "VNC:        ok")
	fmt.Fprintf(&b, "desktop:    %s\n", c.DesktopName())
	fmt.Fprintf(&b, "resolution: %dx%d\n", c.Width(), c.Height())
	return b.String(), nil
}

// runScreenshot captures the framebuffer, encodes it as PNG, and writes it to the host
// artifact path. Returns a one-line confirmation.
func runScreenshot(c *VNCClient, artifact string) (string, error) {
	img, err := c.Screenshot()
	if err != nil {
		return "", fmt.Errorf("capturing framebuffer: %w", err)
	}
	f, err := os.Create(artifact)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", artifact, err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("encoding PNG: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	bnd := img.Bounds()
	return fmt.Sprintf("Screenshot saved to %s (%dx%d)", artifact, bnd.Dx(), bnd.Dy()), nil
}

// runClick sends a pointer click at the (desktop-absolute) coordinates.
func runClick(c *VNCClient, in *params.VncInput) (string, error) {
	if err := c.PointerClick(uint16(in.X), uint16(in.Y), vncButton(in.Button)); err != nil {
		return "", fmt.Errorf("clicking at (%d, %d): %w", in.X, in.Y, err)
	}
	time.Sleep(50 * time.Millisecond)
	return fmt.Sprintf("Clicked %s at (%d, %d)", buttonName(in.Button), in.X, in.Y), nil
}

// runMouse moves the pointer without clicking.
func runMouse(c *VNCClient, in *params.VncInput) (string, error) {
	if err := c.PointerMove(uint16(in.X), uint16(in.Y)); err != nil {
		return "", fmt.Errorf("moving mouse to (%d, %d): %w", in.X, in.Y, err)
	}
	time.Sleep(50 * time.Millisecond)
	return fmt.Sprintf("Moved mouse to (%d, %d)", in.X, in.Y), nil
}

// runType types text as a sequence of key events.
func runType(c *VNCClient, text string) (string, error) {
	time.Sleep(100 * time.Millisecond)
	if err := c.TypeText(text); err != nil {
		return "", fmt.Errorf("typing text: %w", err)
	}
	time.Sleep(50 * time.Millisecond)
	return fmt.Sprintf("Typed %d characters", len(text)), nil
}

// runKey presses + releases one named key.
func runKey(c *VNCClient, keyName string) (string, error) {
	keysym, ok := vncKeyMap[keyName]
	if !ok {
		return "", fmt.Errorf("unknown key name %q (valid: %s)", keyName, vncKeyNames())
	}
	time.Sleep(100 * time.Millisecond)
	if err := c.KeyPress(keysym); err != nil {
		return "", fmt.Errorf("sending key %s: %w", keyName, err)
	}
	time.Sleep(50 * time.Millisecond)
	return fmt.Sprintf("Pressed key %s", keyName), nil
}

// runRfb sends a raw RFB message. The RFB sub-method rides on the input's
// http_method key (key/pointer/cut-text/fbupdate-request — the renamed former
// shared `method` modifier); its JSON params ride on the input's params key.
func runRfb(c *VNCClient, in *params.VncInput) (string, error) {
	switch in.HttpMethod {
	case "key":
		var args struct {
			Key  uint32 `json:"key"`
			Down bool   `json:"down"`
		}
		if err := json.Unmarshal([]byte(in.Params), &args); err != nil {
			return "", fmt.Errorf("invalid JSON params: %w (expected: {\"key\": 65293, \"down\": true})", err)
		}
		if err := c.KeyEvent(args.Key, args.Down); err != nil {
			return "", err
		}
		return fmt.Sprintf("Sent key event key=%d down=%v", args.Key, args.Down), nil

	case "pointer":
		var args struct {
			X      uint16 `json:"x"`
			Y      uint16 `json:"y"`
			Button uint8  `json:"button"`
		}
		if err := json.Unmarshal([]byte(in.Params), &args); err != nil {
			return "", fmt.Errorf("invalid JSON params: %w (expected: {\"x\": 100, \"y\": 200, \"button\": 1})", err)
		}
		if err := c.PointerEvent(args.Button, args.X, args.Y); err != nil {
			return "", err
		}
		return fmt.Sprintf("Sent pointer event button=%d at (%d, %d)", args.Button, args.X, args.Y), nil

	case "cut-text":
		var args struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(in.Params), &args); err != nil {
			return "", fmt.Errorf("invalid JSON params: %w (expected: {\"text\": \"clipboard content\"})", err)
		}
		if err := c.ClientCutText(args.Text); err != nil {
			return "", err
		}
		return fmt.Sprintf("Sent cut-text (%d bytes)", len(args.Text)), nil

	case "fbupdate-request":
		return fmt.Sprintf(`{"width":%d,"height":%d}`, c.Width(), c.Height()), nil

	default:
		return "", fmt.Errorf("unknown RFB method %q (valid: key, pointer, cut-text, fbupdate-request)", in.HttpMethod)
	}
}

// buttonName renders the click button label (default "left").
func buttonName(name string) string {
	if name == "" {
		return "left"
	}
	return name
}
