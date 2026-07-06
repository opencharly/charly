package appium

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tebeka/selenium"

	"github.com/opencharly/charly/candy/plugin-appium/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// dispatch.go is the appium method dispatcher: the 48-method W3C surface moved from
// charly/appium.go, refactored from CLI Run() methods that PRINTED to stdout into
// functions that RETURN the captured output string (so provider.go can feed it through
// the shared sdk matcher pipeline — a host-side matcher step does not run
// for an out-of-process verb). The W3C semantics, mobile: command names, and output
// tokens are unchanged, so a bed authored against the in-tree verb passes unchanged.

// appiumBasePath is the Appium server base path (the server's --base-path). The in-tree
// verb exposed it as a --base-path flag defaulting to /wd/hub; no #Op modifier carries
// it, so it is a constant here (every charly Appium deploy uses /wd/hub).
const appiumBasePath = "/wd/hub"

// requiredModifiers mirrors the in-tree appiumMethods required-field specs. The host's
// former validate-time + runtime required-modifier check keyed off the in-proc live-verb
// seam, which an external verb is not — so the check moves HERE, at dispatch, preserving
// the "missing required modifier(s): X" failure. The names are plugin-INPUT keys
// (#AppiumInput wire names) since the schema-compaction cutover — raw's W3C HTTP verb is
// `http_method` (the input's `method` key is the VERB method).
var requiredModifiers = map[string][]string{
	"session-create":         {"caps"},
	"install-app":            {"apk"},
	"find":                   {"selector"},
	"click":                  {"selector"},
	"send-keys":              {"selector", "text"},
	"screenshot":             {"artifact"},
	"get-text":               {"selector"},
	"get-attribute":          {"selector", "attribute"},
	"clear":                  {"selector"},
	"find-all":               {"selector"},
	"gesture-swipe":          {"direction"},
	"gesture-scroll":         {"direction"},
	"gesture-fling":          {"direction"},
	"app-start-activity":     {"activity"},
	"app-activate":           {"app_id"},
	"app-terminate":          {"app_id"},
	"app-remove":             {"app_id"},
	"app-clear":              {"app_id"},
	"app-is-installed":       {"app_id"},
	"app-state":              {"app_id"},
	"key-press":              {"keycode"},
	"device-set-orientation": {"params"},
	"device-set-clipboard":   {"params"},
	"execute":                {"expression"},
	"raw":                    {"http_method", "path"},
}

// dispatch runs one appium method and returns its captured stdout-equivalent output. It
// decodes the step's typed plugin_input (params.AppiumInput — the per-verb fields left
// core #Op in the schema-compaction cutover). A returned error is the verb FAILING (the
// in-tree CLI Run() returning an error → exit 1); provider.go maps it through the
// exit_status / stderr matchers.
//
//nolint:gocyclo // a flat method switch over the 48-method allowlist; splitting would scatter the contract.
func dispatch(env *checkEnv, op *spec.Op) (string, error) {
	var in params.AppiumInput
	kit.DecodeInput(op.PluginInput, &in)
	method := in.Method
	if err := sdk.RequireModifiers(method, op, requiredModifiers); err != nil {
		return "", err
	}
	switch method {
	case "status":
		return runStatus(env)
	case "session-create":
		return runSessionCreate(env, &in)
	case "session-delete":
		return runSessionDelete(env)
	case "install-app":
		return runInstallApp(env, &in)
	}

	// Every remaining method operates against the persisted session.
	s, err := resolveW3CSession(env.Box, env.Instance, in.Session)
	if err != nil {
		return "", err
	}
	switch method {
	case "find":
		id, err := s.findElement(in.Strategy, in.Selector)
		if err != nil {
			return "", err
		}
		return id, nil
	case "click":
		id, err := s.findElement(in.Strategy, in.Selector)
		if err != nil {
			return "", err
		}
		if err := s.click(id); err != nil {
			return "", fmt.Errorf("click %s=%q: %w", in.Strategy, in.Selector, err)
		}
		return "clicked", nil
	case "send-keys":
		id, err := s.findElement(in.Strategy, in.Selector)
		if err != nil {
			return "", err
		}
		if err := s.sendKeys(id, in.Text); err != nil {
			return "", fmt.Errorf("send-keys %s=%q: %w", in.Strategy, in.Selector, err)
		}
		return "sent", nil
	case "screenshot":
		pngBytes, err := s.screenshot()
		if err != nil {
			return "", fmt.Errorf("screenshot: %w", err)
		}
		if err := os.WriteFile(in.Artifact, pngBytes, 0644); err != nil {
			return "", fmt.Errorf("write %s: %w", in.Artifact, err)
		}
		return fmt.Sprintf("wrote %d bytes to %s", len(pngBytes), in.Artifact), nil
	case "get-text":
		id, err := s.findElement(in.Strategy, in.Selector)
		if err != nil {
			return "", err
		}
		text, err := s.elementText(id)
		if err != nil {
			return "", fmt.Errorf("get-text %s=%q: %w", in.Strategy, in.Selector, err)
		}
		return text, nil
	case "get-attribute":
		id, err := s.findElement(in.Strategy, in.Selector)
		if err != nil {
			return "", err
		}
		v, err := s.elementAttribute(id, in.Attribute)
		if err != nil {
			return "", fmt.Errorf("get-attribute %s on %s=%q: %w", in.Attribute, in.Strategy, in.Selector, err)
		}
		return v, nil
	case "clear":
		id, err := s.findElement(in.Strategy, in.Selector)
		if err != nil {
			return "", err
		}
		if err := s.clearElement(id); err != nil {
			return "", fmt.Errorf("clear %s=%q: %w", in.Strategy, in.Selector, err)
		}
		return "cleared", nil
	case "find-all":
		ids, err := s.findElements(in.Strategy, in.Selector)
		if err != nil {
			return "", err
		}
		out := strconv.Itoa(len(ids))
		for _, id := range ids {
			out += "\n" + id
		}
		return out, nil
	case "source":
		return s.source()
	case "back":
		if err := s.navigateBack(); err != nil {
			return "", fmt.Errorf("back: %w", err)
		}
		return "back", nil
	}

	// Tier 2 — gesture group.
	if past, ok := gesturePastTense[method]; ok {
		return runGesture(s, gestureMobileName[method], past, &in)
	}

	switch method {
	// Tier 2 — app lifecycle + activity.
	case "app-start-activity":
		return runStartActivity(s, &in)
	case "app-activate":
		return appIDMobile(s, "activateApp", "activated", in.AppId)
	case "app-terminate":
		return appIDMobile(s, "terminateApp", "terminated", in.AppId)
	case "app-remove":
		return appIDMobile(s, "removeApp", "removed", in.AppId)
	case "app-clear":
		return appIDMobile(s, "clearApp", "cleared", in.AppId)
	case "app-is-installed":
		return appIDMobile(s, "isAppInstalled", "", in.AppId)
	case "app-state":
		return appIDMobile(s, "queryAppState", "", in.AppId)
	case "app-current-activity":
		return deviceMobile(s, "getCurrentActivity")
	case "app-current-package":
		return deviceMobile(s, "getCurrentPackage")

	// Tier 2 — keys + keyboard.
	case "key-press":
		return runKeyPress(s, &in)
	case "key-hide":
		return deviceMobile(s, "hideKeyboard")
	case "key-shown":
		return deviceMobile(s, "isKeyboardShown")

	// Tier 2 — device / system + WebView context.
	case "device-info":
		return deviceMobile(s, "deviceInfo")
	case "device-battery":
		return deviceMobile(s, "batteryInfo")
	case "device-time":
		return deviceMobile(s, "getDeviceTime")
	case "device-notifications":
		if _, err := s.executeScript("mobile: openNotifications", []any{}); err != nil {
			return "", fmt.Errorf("mobile: openNotifications: %w", err)
		}
		return "opened", nil
	case "device-orientation":
		o, err := s.orientation()
		if err != nil {
			return "", fmt.Errorf("get orientation: %w", err)
		}
		return o, nil
	case "device-set-orientation":
		if err := s.setOrientation(strings.ToUpper(strings.TrimSpace(in.Params))); err != nil {
			return "", fmt.Errorf("set orientation %q: %w", in.Params, err)
		}
		return "oriented", nil
	case "device-get-clipboard":
		return runGetClipboard(s)
	case "device-set-clipboard":
		return runSetClipboard(s, &in)
	case "device-contexts":
		ctxs, err := s.contexts()
		if err != nil {
			return "", err
		}
		return strings.Join(ctxs, "\n"), nil
	case "device-context":
		return runContext(s, &in)

	// Tier 3 — generic escape hatch.
	case "execute":
		return runExecute(s, &in)
	case "raw":
		return runRaw(s, &in)
	}
	return "", fmt.Errorf("unknown appium method %q", method)
}

// --- lifecycle methods (no persisted session, or session creation) ---

func runStatus(env *checkEnv) (string, error) {
	base, err := appiumBaseURL(env, appiumBasePath)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(base + "/status")
	if err != nil {
		return "", fmt.Errorf("GET %s/status: %w", base, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	body := string(raw)
	if resp.StatusCode != 200 {
		return body, fmt.Errorf("appium status returned HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// runSessionCreate reads the W3C caps from the plugin input (in.Caps — appium's
// caps moved INTO #AppiumInput in the schema compaction; the core #Op caps field
// is setcap's modifier only now).
func runSessionCreate(env *checkEnv, in *params.AppiumInput) (string, error) {
	capsRaw := in.Caps
	if strings.HasPrefix(capsRaw, "@") {
		data, err := os.ReadFile(capsRaw[1:])
		if err != nil {
			return "", fmt.Errorf("reading caps file %s: %w", capsRaw[1:], err)
		}
		capsRaw = string(data)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(capsRaw), &parsed); err != nil {
		return "", fmt.Errorf("parsing caps JSON: %w", err)
	}
	// Unwrap alwaysMatch if present — the SDK adds its own wrapper, so a pre-wrapped
	// map would nest alwaysMatch twice and break caps matching server-side.
	if inner, ok := parsed["alwaysMatch"].(map[string]any); ok {
		parsed = inner
	}
	base, err := appiumBaseURL(env, appiumBasePath)
	if err != nil {
		return "", err
	}
	// Delete any previous session for this image+instance first (best effort).
	if prev, _ := loadAppiumSession(env.Box, env.Instance); prev != nil {
		_ = appiumDeleteSessionRemote(base, prev.SessionID)
		_ = deleteAppiumSession(env.Box, env.Instance)
	}
	caps := selenium.Capabilities(parsed)
	wd, err := selenium.NewRemote(caps, base)
	if err != nil {
		return "", fmt.Errorf("creating Appium session at %s: %w", base, err)
	}
	sid := wd.SessionID()
	if sid == "" {
		return "", fmt.Errorf("appium session created but SessionID was empty")
	}
	sess := &AppiumSession{
		SessionID: sid,
		BaseURL:   base,
		CreatedAt: time.Now().UTC(),
		Image:     env.Box,
		Instance:  env.Instance,
		Caps:      parsed,
	}
	if err := saveAppiumSession(sess); err != nil {
		_ = wd.Quit()
		return "", err
	}
	return sid, nil
}

func runSessionDelete(env *checkEnv) (string, error) {
	sess, err := loadAppiumSession(env.Box, env.Instance)
	if err != nil {
		return "", err
	}
	if sess == nil {
		return "no session to delete", nil
	}
	// A DELETE failure is a warning (the server may have GC'd the session); the file is
	// still removed.
	_ = appiumDeleteSessionRemote(sess.BaseURL, sess.SessionID)
	if err := deleteAppiumSession(env.Box, env.Instance); err != nil {
		return "", err
	}
	return "deleted", nil
}

func runInstallApp(env *checkEnv, in *params.AppiumInput) (string, error) {
	// in.Apk is a HOST path, already resolved to an absolute candy-anchored path by the
	// host (invokeVerbProvider) before marshaling — the plugin has no CandyDirs.
	if _, statErr := os.Stat(in.Apk); statErr != nil {
		return "", fmt.Errorf("appium install-app: APK not found on host: %w", statErr)
	}
	sess, err := loadActiveSession(env.Box, env.Instance)
	if err != nil {
		return "", err
	}
	if env.ContainerName == "" {
		return "", fmt.Errorf("appium install-app: no container name in check env (box=%q)", env.Box)
	}
	remote, cleanup, err := stageAPKIntoContainer(env.ContainerName, in.Apk)
	if err != nil {
		return "", err
	}
	defer cleanup()

	s := newW3CSession(sess.BaseURL, sess.SessionID)
	result, err := s.executeScript("mobile: installApp", []any{map[string]any{"appPath": remote}})
	if err != nil {
		return "", fmt.Errorf("mobile: installApp %s (host %s): %w", remote, in.Apk, err)
	}
	if len(result) > 0 && string(result) != "null" {
		return string(result), nil
	}
	return "installed", nil
}

// --- gesture / app / key / device helpers (mirror charly/appium.go) ---

var gestureMobileName = map[string]string{
	"gesture-tap":         "clickGesture",
	"gesture-double-tap":  "doubleClickGesture",
	"gesture-long-press":  "longClickGesture",
	"gesture-drag":        "dragGesture",
	"gesture-swipe":       "swipeGesture",
	"gesture-scroll":      "scrollGesture",
	"gesture-fling":       "flingGesture",
	"gesture-pinch-open":  "pinchOpenGesture",
	"gesture-pinch-close": "pinchCloseGesture",
}

var gesturePastTense = map[string]string{
	"gesture-tap":         "tapped",
	"gesture-double-tap":  "double-tapped",
	"gesture-long-press":  "long-pressed",
	"gesture-drag":        "dragged",
	"gesture-swipe":       "swiped",
	"gesture-scroll":      "scrolled",
	"gesture-fling":       "flung",
	"gesture-pinch-open":  "pinched-open",
	"gesture-pinch-close": "pinched-close",
}

// runGesture resolves the target (elementId from selector, else x/y unless params
// already carries the area form), merges direction/percent and extra params, and
// invokes the named mobile: gesture.
func runGesture(s *w3cSession, gesture, pastTense string, in *params.AppiumInput) (string, error) {
	args := map[string]any{}
	if in.Params != "" {
		if err := json.Unmarshal([]byte(in.Params), &args); err != nil {
			return "", fmt.Errorf("invalid params JSON: %w", err)
		}
	}
	if in.Selector != "" {
		id, err := s.findElement(in.Strategy, in.Selector)
		if err != nil {
			return "", err
		}
		args["elementId"] = id
	} else if _, hasArea := args["left"]; !hasArea {
		args["x"] = in.X
		args["y"] = in.Y
	}
	if in.Direction != "" {
		args["direction"] = in.Direction
	}
	if in.Percent != "" {
		p, perr := strconv.ParseFloat(in.Percent, 64)
		if perr != nil {
			return "", fmt.Errorf("invalid percent %q: %w", in.Percent, perr)
		}
		args["percent"] = p
	}
	if _, err := s.executeScript("mobile: "+gesture, []any{args}); err != nil {
		return "", fmt.Errorf("mobile: %s: %w", gesture, err)
	}
	return pastTense, nil
}

func runStartActivity(s *w3cSession, in *params.AppiumInput) (string, error) {
	args := map[string]any{"intent": in.Activity}
	if in.Params != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(in.Params), &extra); err != nil {
			return "", fmt.Errorf("invalid params JSON: %w", err)
		}
		maps.Copy(args, extra)
	}
	if _, err := s.executeScript("mobile: startActivity", []any{args}); err != nil {
		return "", fmt.Errorf("mobile: startActivity %s: %w", in.Activity, err)
	}
	return "started " + in.Activity, nil
}

// appIDMobile invokes mobile: <method> with {appId} and returns the W3C value.
func appIDMobile(s *w3cSession, method, fallback, appID string) (string, error) {
	result, err := s.executeScript("mobile: "+method, []any{map[string]any{"appId": appID}})
	if err != nil {
		return "", fmt.Errorf("mobile: %s %s: %w", method, appID, err)
	}
	return formatW3CValue(result, fallback), nil
}

// deviceMobile runs an argless mobile: <method> and returns the value.
func deviceMobile(s *w3cSession, method string) (string, error) {
	result, err := s.executeScript("mobile: "+method, []any{})
	if err != nil {
		return "", fmt.Errorf("mobile: %s: %w", method, err)
	}
	return formatW3CValue(result, ""), nil
}

func runKeyPress(s *w3cSession, in *params.AppiumInput) (string, error) {
	args := map[string]any{"keycode": in.Keycode}
	if in.Params != "" {
		var extra map[string]any
		if err := json.Unmarshal([]byte(in.Params), &extra); err != nil {
			return "", fmt.Errorf("invalid params JSON: %w", err)
		}
		maps.Copy(args, extra)
	}
	if _, err := s.executeScript("mobile: pressKey", []any{args}); err != nil {
		return "", fmt.Errorf("mobile: pressKey %d: %w", in.Keycode, err)
	}
	return fmt.Sprintf("pressed %d", in.Keycode), nil
}

func runGetClipboard(s *w3cSession) (string, error) {
	result, err := s.executeScript("mobile: getClipboard", []any{})
	if err != nil {
		return "", fmt.Errorf("mobile: getClipboard: %w", err)
	}
	// getClipboard returns base64-encoded content by default; decode for readability.
	var b64 string
	if json.Unmarshal(result, &b64) == nil && b64 != "" {
		if dec, derr := base64.StdEncoding.DecodeString(b64); derr == nil {
			return string(dec), nil
		}
	}
	return formatW3CValue(result, ""), nil
}

func runSetClipboard(s *w3cSession, in *params.AppiumInput) (string, error) {
	content := base64.StdEncoding.EncodeToString([]byte(in.Params))
	arg := map[string]any{"content": content, "contentType": "plaintext"}
	if _, err := s.executeScript("mobile: setClipboard", []any{arg}); err != nil {
		return "", fmt.Errorf("mobile: setClipboard: %w", err)
	}
	return "set", nil
}

func runContext(s *w3cSession, in *params.AppiumInput) (string, error) {
	if in.Params == "" {
		cur, err := s.currentContext()
		if err != nil {
			return "", err
		}
		return cur, nil
	}
	if err := s.setContext(in.Params); err != nil {
		return "", fmt.Errorf("set context %q: %w", in.Params, err)
	}
	return in.Params, nil
}

func runExecute(s *w3cSession, in *params.AppiumInput) (string, error) {
	body := in.RequestBody
	if in.Selector != "" {
		id, ferr := s.findElement(in.Strategy, in.Selector)
		if ferr != nil {
			return "", ferr
		}
		body = substituteElement(body, id)
	}
	args := []any{}
	if strings.TrimSpace(body) != "" {
		var v any
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			return "", fmt.Errorf("invalid request_body JSON: %w", err)
		}
		if arr, ok := v.([]any); ok {
			args = arr
		} else {
			args = []any{v}
		}
	}
	result, err := s.executeScript(in.Expression, args)
	if err != nil {
		return "", fmt.Errorf("execute %q: %w", in.Expression, err)
	}
	return formatW3CValue(result, ""), nil
}

// runRaw issues an arbitrary W3C call. The HTTP verb is the input's http_method
// (renamed from the former shared #Op `method` request modifier — the input's
// `method` key is the VERB method).
func runRaw(s *w3cSession, in *params.AppiumInput) (string, error) {
	path := in.Path
	body := in.RequestBody
	if in.Selector != "" {
		id, ferr := s.findElement(in.Strategy, in.Selector)
		if ferr != nil {
			return "", ferr
		}
		path = substituteElement(path, id)
		body = substituteElement(body, id)
	}
	var reqBody any
	if strings.TrimSpace(body) != "" {
		if err := json.Unmarshal([]byte(body), &reqBody); err != nil {
			return "", fmt.Errorf("invalid request_body JSON: %w", err)
		}
	}
	result, err := s.rawCall(strings.ToUpper(in.HTTPMethod), path, reqBody)
	if err != nil {
		return "", err
	}
	return formatW3CValue(result, ""), nil
}
