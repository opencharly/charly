package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// cdp_spa.go is the SPA-aware remote-desktop input group, moved from charly/cdp_spa.go.
// Each method was refactored from a CLI Run() into a function that RETURNS its captured
// confirmation string. These methods target a Selkies-style SPA that renders a remote
// desktop on a <canvas>: CDP Input events bypass the local compositor + Chrome shortcuts,
// so Super+e / Ctrl+T pass through to the remote desktop. The CLI-only `--scale` flag is
// NOT a declarative modifier, so SPA coordinate scaling uses the SPA's detected scale
// (0 → no correction) only.

// spaState holds the detected SPA state from a CDP eval query.
type spaState struct {
	CanvasWidth  int     `json:"canvasWidth"`
	CanvasHeight int     `json:"canvasHeight"`
	HasOverlay   bool    `json:"hasOverlay"`
	OverlayID    string  `json:"overlayId"`
	Focused      bool    `json:"focused"`
	ScaleX       float64 `json:"scaleX"`
	ScaleY       float64 `json:"scaleY"`
	Secure       bool    `json:"secure"`
	HasDecoder   bool    `json:"hasDecoder"`
	Error        string  `json:"error"`
}

// spaDetect queries the SPA state via a CDP eval.
func spaDetect(client *CDPClient) (spaState, error) {
	js := `(function() {
		var c = document.getElementById("videoCanvas");
		if (!c) return JSON.stringify({error: "no #videoCanvas element — not a Selkies SPA"});
		var oi = document.getElementById("overlayInput");
		var state = {
			canvasWidth: c.width,
			canvasHeight: c.height,
			hasOverlay: !!oi,
			overlayId: oi ? oi.id : "",
			focused: document.activeElement === oi,
			secure: window.isSecureContext,
			hasDecoder: typeof VideoDecoder !== "undefined",
			scaleX: 0,
			scaleY: 0,
			error: ""
		};
		return JSON.stringify(state);
	})()`

	result, err := cdpEvaluate(client, js)
	if err != nil {
		return spaState{}, fmt.Errorf("querying SPA state: %w", err)
	}
	var s spaState
	if err := json.Unmarshal([]byte(result), &s); err != nil {
		return spaState{}, fmt.Errorf("parsing SPA state: %w", err)
	}
	if s.Error != "" {
		return s, fmt.Errorf("%s", s.Error)
	}
	return s, nil
}

// spaEnsureFocus ensures the overlayInput has focus for keyboard events.
func spaEnsureFocus(client *CDPClient) error {
	js := `(function() {
		var oi = document.getElementById("overlayInput");
		if (!oi) return "no overlayInput";
		oi.focus();
		return document.activeElement === oi ? "ok" : "focus failed";
	})()`
	result, err := cdpEvaluate(client, js)
	if err != nil {
		return fmt.Errorf("focusing overlayInput: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("focusing overlayInput: %s", result)
	}
	return nil
}

// spaApplyScale converts canvas coordinates to CDP Input coordinates. A click at canvas
// (x, y) is sent to (x/scaleX, y/scaleY); scale 0 means no correction.
func spaApplyScale(x, y int, sx, sy float64) (float64, float64) {
	if sx == 0 || sy == 0 {
		return float64(x), float64(y)
	}
	return float64(x) / sx, float64(y) / sy
}

// runSpaStatus shows the SPA state.
func runSpaStatus(client *CDPClient) (string, error) {
	state, err := spaDetect(client)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Canvas:     %dx%d (#videoCanvas)\n", state.CanvasWidth, state.CanvasHeight)
	if state.ScaleX > 0 && state.ScaleY > 0 {
		fmt.Fprintf(&b, "Scale:      %.3fx / %.3fy\n", state.ScaleX, state.ScaleY)
	} else {
		fmt.Fprintf(&b, "Scale:      not detected\n")
	}
	overlayStatus := "not found"
	if state.HasOverlay {
		if state.Focused {
			overlayStatus = state.OverlayID + " (focused)"
		} else {
			overlayStatus = state.OverlayID + " (not focused)"
		}
	}
	fmt.Fprintf(&b, "Input:      %s\n", overlayStatus)
	fmt.Fprintf(&b, "Secure:     %v\n", state.Secure)
	fmt.Fprintf(&b, "Decoder:    %v\n", state.HasDecoder)
	return b.String(), nil
}

// runSpaClick clicks at canvas coordinates with the SPA's detected scale correction.
func runSpaClick(client *CDPClient, x, y int, button string) (string, error) {
	state, err := spaDetect(client)
	if err != nil {
		return "", err
	}
	inputX, inputY := spaApplyScale(x, y, state.ScaleX, state.ScaleY)

	btn := button
	if btn == "" {
		btn = "left"
	}
	if btn != "left" && btn != "right" && btn != "middle" {
		return "", fmt.Errorf("unknown button %q (valid: left, right, middle)", btn)
	}

	_ = spaEnsureFocus(client)

	if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": inputX, "y": inputY}); err != nil {
		return "", fmt.Errorf("dispatching mouseMoved: %w", err)
	}
	if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mousePressed", "x": inputX, "y": inputY, "button": btn, "clickCount": 1}); err != nil {
		return "", fmt.Errorf("dispatching mousePressed: %w", err)
	}
	if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseReleased", "x": inputX, "y": inputY, "button": btn, "clickCount": 1}); err != nil {
		return "", fmt.Errorf("dispatching mouseReleased: %w", err)
	}
	return fmt.Sprintf("SPA click: (%d, %d) → input (%.0f, %.0f) button=%s", x, y, inputX, inputY, btn), nil
}

// runSpaMouse moves the pointer without clicking.
func runSpaMouse(client *CDPClient, x, y int) (string, error) {
	state, err := spaDetect(client)
	if err != nil {
		return "", err
	}
	inputX, inputY := spaApplyScale(x, y, state.ScaleX, state.ScaleY)
	if _, err := client.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": inputX, "y": inputY}); err != nil {
		return "", fmt.Errorf("dispatching mouseMoved: %w", err)
	}
	return fmt.Sprintf("SPA mouse: (%d, %d) → input (%.0f, %.0f)", x, y, inputX, inputY), nil
}

// runSpaType types text into the remote desktop via the SPA's overlayInput. Only keyDown
// + keyUp are sent (no "char") to prevent double input.
func runSpaType(client *CDPClient, text string) (string, error) {
	if _, err := spaDetect(client); err != nil {
		return "", err
	}
	if err := spaEnsureFocus(client); err != nil {
		return "", err
	}
	for _, ch := range text {
		key := string(ch)
		if err := spaDispatchCharKeyDown(client, key); err != nil {
			return "", fmt.Errorf("dispatching keyDown for %q: %w", key, err)
		}
		if err := spaDispatchCharKeyUp(client, key); err != nil {
			return "", fmt.Errorf("dispatching keyUp for %q: %w", key, err)
		}
	}
	return fmt.Sprintf("SPA typed %d characters", len([]rune(text))), nil
}

// runSpaKey sends a single named key press to the remote desktop.
func runSpaKey(client *CDPClient, keyName string) (string, error) {
	if _, err := spaDetect(client); err != nil {
		return "", err
	}
	if err := spaEnsureFocus(client); err != nil {
		return "", err
	}
	key, ok := spaKeyMap[strings.ToLower(keyName)]
	if !ok {
		return "", fmt.Errorf("unknown key %q (valid: %s)", keyName, spaKeyNames())
	}
	if err := spaDispatchKeyPress(client, key); err != nil {
		return "", fmt.Errorf("dispatching key %s: %w", keyName, err)
	}
	return fmt.Sprintf("SPA key: %s", keyName), nil
}

// runSpaKeyCombo sends a modifier key combination (super+e, ctrl+t, alt+f4) to the remote
// desktop — bypassing the local compositor and Chrome.
func runSpaKeyCombo(client *CDPClient, combo string) (string, error) {
	if _, err := spaDetect(client); err != nil {
		return "", err
	}
	if err := spaEnsureFocus(client); err != nil {
		return "", err
	}
	parts := strings.Split(strings.ToLower(combo), "+")
	if len(parts) < 2 {
		return "", fmt.Errorf("key combo must have at least one modifier + key (e.g., super+e, ctrl+t)")
	}
	mainKeyName := parts[len(parts)-1]
	modNames := parts[:len(parts)-1]

	mainKey, ok := spaKeyMap[mainKeyName]
	if !ok {
		if len(mainKeyName) == 1 {
			ch := strings.ToLower(mainKeyName)
			mainKey = spaKeyDef{Key: ch, Code: "Key" + strings.ToUpper(ch), WindowsVirtualCode: int(strings.ToUpper(ch)[0])}
		} else {
			return "", fmt.Errorf("unknown key %q in combo (valid: %s, or single character)", mainKeyName, spaKeyNames())
		}
	}

	var mods []spaKeyDef
	for _, modName := range modNames {
		mod, ok := spaModifierMap[modName]
		if !ok {
			return "", fmt.Errorf("unknown modifier %q (valid: ctrl, alt, shift, super/meta)", modName)
		}
		mods = append(mods, mod)
	}

	for _, mod := range mods {
		if err := spaDispatchKeyDown(client, mod); err != nil {
			return "", fmt.Errorf("modifier %s keyDown: %w", mod.Key, err)
		}
	}
	if err := spaDispatchKeyPress(client, mainKey); err != nil {
		return "", fmt.Errorf("key %s press: %w", mainKey.Key, err)
	}
	for _, mod := range slices.Backward(mods) {
		if err := spaDispatchKeyUp(client, mod); err != nil {
			return "", fmt.Errorf("modifier %s keyUp: %w", mod.Key, err)
		}
	}
	return fmt.Sprintf("SPA key-combo: %s", combo), nil
}

// --- SPA Key Definitions ---

// spaKeyDef maps a key name to CDP Input.dispatchKeyEvent properties.
type spaKeyDef struct {
	Key                string
	Code               string
	WindowsVirtualCode int
}

func spaDispatchCharKeyDown(client *CDPClient, ch string) error {
	upper := strings.ToUpper(ch)
	code := "Key" + upper
	switch {
	case ch == " ":
		code = "Space"
	case ch == "-":
		code = "Minus"
	case ch >= "0" && ch <= "9":
		code = "Digit" + ch
	}
	params := map[string]any{"type": "keyDown", "key": ch, "code": code, "text": ch}
	if len(upper) == 1 && upper[0] >= 'A' && upper[0] <= 'Z' {
		params["windowsVirtualKeyCode"] = int(upper[0])
	}
	_, err := client.Call("Input.dispatchKeyEvent", params)
	return err
}

func spaDispatchCharKeyUp(client *CDPClient, ch string) error {
	upper := strings.ToUpper(ch)
	code := "Key" + upper
	switch {
	case ch == " ":
		code = "Space"
	case ch == "-":
		code = "Minus"
	case ch >= "0" && ch <= "9":
		code = "Digit" + ch
	}
	params := map[string]any{"type": "keyUp", "key": ch, "code": code}
	_, err := client.Call("Input.dispatchKeyEvent", params)
	return err
}

func spaDispatchKeyDown(client *CDPClient, key spaKeyDef) error {
	_, err := client.Call("Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown", "key": key.Key, "code": key.Code, "windowsVirtualKeyCode": key.WindowsVirtualCode,
	})
	return err
}

func spaDispatchKeyUp(client *CDPClient, key spaKeyDef) error {
	_, err := client.Call("Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "key": key.Key, "code": key.Code, "windowsVirtualKeyCode": key.WindowsVirtualCode,
	})
	return err
}

func spaDispatchKeyPress(client *CDPClient, key spaKeyDef) error {
	if err := spaDispatchKeyDown(client, key); err != nil {
		return err
	}
	return spaDispatchKeyUp(client, key)
}

// spaKeyMap maps key names to CDP key properties.
var spaKeyMap = map[string]spaKeyDef{
	"return":    {Key: "Enter", Code: "Enter", WindowsVirtualCode: 13},
	"enter":     {Key: "Enter", Code: "Enter", WindowsVirtualCode: 13},
	"escape":    {Key: "Escape", Code: "Escape", WindowsVirtualCode: 27},
	"tab":       {Key: "Tab", Code: "Tab", WindowsVirtualCode: 9},
	"backspace": {Key: "Backspace", Code: "Backspace", WindowsVirtualCode: 8},
	"delete":    {Key: "Delete", Code: "Delete", WindowsVirtualCode: 46},
	"home":      {Key: "Home", Code: "Home", WindowsVirtualCode: 36},
	"end":       {Key: "End", Code: "End", WindowsVirtualCode: 35},
	"page_up":   {Key: "PageUp", Code: "PageUp", WindowsVirtualCode: 33},
	"page_down": {Key: "PageDown", Code: "PageDown", WindowsVirtualCode: 34},
	"up":        {Key: "ArrowUp", Code: "ArrowUp", WindowsVirtualCode: 38},
	"down":      {Key: "ArrowDown", Code: "ArrowDown", WindowsVirtualCode: 40},
	"left":      {Key: "ArrowLeft", Code: "ArrowLeft", WindowsVirtualCode: 37},
	"right":     {Key: "ArrowRight", Code: "ArrowRight", WindowsVirtualCode: 39},
	"insert":    {Key: "Insert", Code: "Insert", WindowsVirtualCode: 45},
	"space":     {Key: " ", Code: "Space", WindowsVirtualCode: 32},
	"f1":        {Key: "F1", Code: "F1", WindowsVirtualCode: 112},
	"f2":        {Key: "F2", Code: "F2", WindowsVirtualCode: 113},
	"f3":        {Key: "F3", Code: "F3", WindowsVirtualCode: 114},
	"f4":        {Key: "F4", Code: "F4", WindowsVirtualCode: 115},
	"f5":        {Key: "F5", Code: "F5", WindowsVirtualCode: 116},
	"f6":        {Key: "F6", Code: "F6", WindowsVirtualCode: 117},
	"f7":        {Key: "F7", Code: "F7", WindowsVirtualCode: 118},
	"f8":        {Key: "F8", Code: "F8", WindowsVirtualCode: 119},
	"f9":        {Key: "F9", Code: "F9", WindowsVirtualCode: 120},
	"f10":       {Key: "F10", Code: "F10", WindowsVirtualCode: 121},
	"f11":       {Key: "F11", Code: "F11", WindowsVirtualCode: 122},
	"f12":       {Key: "F12", Code: "F12", WindowsVirtualCode: 123},
}

// spaModifierMap maps modifier names to CDP key properties.
var spaModifierMap = map[string]spaKeyDef{
	"ctrl":    {Key: "Control", Code: "ControlLeft", WindowsVirtualCode: 17},
	"control": {Key: "Control", Code: "ControlLeft", WindowsVirtualCode: 17},
	"alt":     {Key: "Alt", Code: "AltLeft", WindowsVirtualCode: 18},
	"shift":   {Key: "Shift", Code: "ShiftLeft", WindowsVirtualCode: 16},
	"super":   {Key: "Meta", Code: "MetaLeft", WindowsVirtualCode: 91},
	"meta":    {Key: "Meta", Code: "MetaLeft", WindowsVirtualCode: 91},
	"win":     {Key: "Meta", Code: "MetaLeft", WindowsVirtualCode: 91},
	"logo":    {Key: "Meta", Code: "MetaLeft", WindowsVirtualCode: 91},
}

// spaKeyNames returns a sorted list of valid key names.
func spaKeyNames() string {
	names := make([]string, 0, len(spaKeyMap))
	for name := range spaKeyMap {
		names = append(names, name)
	}
	slices.Sort(names)
	return strings.Join(names, ", ")
}
