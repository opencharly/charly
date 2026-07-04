package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	adb "github.com/zach-klippenstein/goadb"

	"github.com/opencharly/sdk/spec"
)

// methods.go is the adb method dispatcher: the 12-method surface moved from
// charly/adb.go, refactored from CLI Run() methods that PRINTED to stdout into
// functions that RETURN the captured output string (so provider.go can feed it
// through the shared sdk matcher pipeline — a host-side matcher step
// does not run for an out-of-process verb). The wire protocol, output tokens, and
// timeouts are unchanged, so a bed authored against the in-tree verb passes
// unchanged.

// requiredModifiers mirrors the in-tree adbMethods required-field specs. The
// host's validate-time + runtime required-modifier check keyed off the former
// in-proc live-verb seam, which an external verb is not — so the check moves HERE,
// at dispatch, preserving the "missing required modifier(s): X" failure.
var requiredModifiers = map[string][]string{
	"shell":       {"arg"},
	"install":     {"apk"},
	"install-app": {"app_id"},
	"uninstall":   {"arg"},
	"getprop":     {"property"},
	"screencap":   {"artifact"},
	"keyevent":    {"key"},
}

func modifierZero(op *spec.Op, name string) bool {
	switch name {
	case "arg":
		return len(op.Args) == 0
	case "apk":
		return op.Apk == ""
	case "app_id":
		return op.AppId == ""
	case "property":
		return op.Property == ""
	case "artifact":
		return op.Artifact == ""
	case "key":
		return op.KeyName == ""
	}
	return false
}

func checkRequiredModifiers(method string, op *spec.Op) error {
	var missing []string
	for _, f := range requiredModifiers[method] {
		if modifierZero(op, f) {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required modifier(s): %s", strings.Join(missing, ", "))
}

// parseTimeout parses op.Timeout (a Go duration string), falling back to def.
func parseTimeout(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d
	}
	return def
}

// dispatch runs one adb method and returns its captured output. A returned error
// is the verb FAILING (the in-tree CLI Run() returning an error → exit 1);
// provider.go maps it through the exit_status / stderr matchers.
//
//nolint:gocyclo // a flat method switch over the 12-method allowlist; splitting would scatter the contract.
func dispatch(env *adbEnv, op *spec.Op) (string, error) {
	method := string(op.Adb)
	if err := checkRequiredModifiers(method, op); err != nil {
		return "", err
	}
	switch method {
	case "devices":
		return runDevices(env)
	case "install":
		// op.Apk is a HOST path, already resolved to an absolute candy-anchored
		// path by the host (invokeVerbProvider) before marshaling.
		return installFromHostApk(env, op.Apk)
	case "install-app":
		return installByPackage(env, spec.ApkPackageSpec{
			Package:    op.AppId,
			Source:     op.Source,
			Arch:       op.Arch,
			AppVersion: op.AppVersion,
		})
	case "uninstall":
		return uninstall(env, op.Args[0])
	}

	// Every remaining method operates against a single goadb device handle.
	dev, err := env.device()
	if err != nil {
		return "", err
	}
	switch method {
	case "shell":
		out, err := dev.RunCommand(op.Args[0], op.Args[1:]...)
		if err != nil {
			return "", fmt.Errorf("adb shell %v: %w", op.Args, err)
		}
		return out, nil
	case "getprop":
		out, err := dev.RunCommand("getprop", op.Property)
		if err != nil {
			return "", fmt.Errorf("getprop %s: %w", op.Property, err)
		}
		return strings.TrimSpace(out), nil
	case "screencap":
		return runScreencap(dev, op.Artifact)
	case "logcat-tail":
		return runLogcatTail(dev, op)
	case "wait-for-device":
		return runWaitForDevice(dev, parseTimeout(op.Timeout, 60*time.Second))
	case "wait-ui-settled":
		return runWaitUiSettled(dev, parseTimeout(op.Timeout, 300*time.Second))
	case "current-focus":
		return adbCurrentFocus(dev)
	case "keyevent":
		if _, err := dev.RunCommand("input", "keyevent", op.KeyName); err != nil {
			return "", fmt.Errorf("input keyevent %s: %w", op.KeyName, err)
		}
		return fmt.Sprintf("sent keyevent %s", op.KeyName), nil
	}
	return "", fmt.Errorf("unknown adb method %q", method)
}

// runDevices lists every device serial + its state as `<serial>\t<state>` lines
// (matches the `adb devices` CLI without the header).
func runDevices(env *adbEnv) (string, error) {
	client, err := env.client()
	if err != nil {
		return "", err
	}
	serials, err := client.ListDeviceSerials()
	if err != nil {
		return "", fmt.Errorf("adb host:devices: %w", err)
	}
	var b strings.Builder
	for _, s := range serials {
		d := client.Device(adb.DeviceWithSerial(s))
		state, err := d.State()
		if err != nil {
			fmt.Fprintf(&b, "%s\tunknown\n", s)
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\n", s, adbStateString(state))
	}
	return b.String(), nil
}

// runScreencap captures a PNG via `screencap -p | base64` and writes it to the
// host filesystem (base64 round-trips binary stdout safely through goadb).
func runScreencap(dev *adb.Device, artifact string) (string, error) {
	out, err := dev.RunCommand("sh", "-c", "screencap -p | base64")
	if err != nil {
		return "", fmt.Errorf("screencap: %w", err)
	}
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, out)
	pngBytes, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return "", fmt.Errorf("decode base64 PNG: %w", err)
	}
	if err := os.WriteFile(artifact, pngBytes, 0644); err != nil {
		return "", fmt.Errorf("write %s: %w", artifact, err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(pngBytes), artifact), nil
}

// runLogcatTail dumps recent logcat lines (`logcat -d`), trimmed to the last
// op.Amount lines and filtered by op.Query.
func runLogcatTail(dev *adb.Device, op *spec.Op) (string, error) {
	args := []string{"-d"}
	if op.Query != "" {
		args = append(args, strings.Fields(op.Query)...)
	}
	out, err := dev.RunCommand("logcat", args...)
	if err != nil {
		return "", fmt.Errorf("logcat: %w", err)
	}
	if op.Amount > 0 {
		lines := strings.Split(out, "\n")
		if len(lines) > op.Amount {
			lines = lines[len(lines)-op.Amount:]
		}
		out = strings.Join(lines, "\n")
	}
	return out, nil
}

// runWaitForDevice polls `getprop sys.boot_completed` until it returns "1" or the
// timeout expires. Prints "ready" on success.
func runWaitForDevice(dev *adb.Device, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := dev.RunCommand("getprop", "sys.boot_completed")
		if err == nil && strings.TrimSpace(out) == "1" {
			return "ready", nil
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("wait-for-device: sys.boot_completed != 1 after %s", timeout)
}

// runWaitUiSettled polls the focused window; while a system "Application Not
// Responding" dialog holds focus it dismisses it with KEYCODE_HOME and keeps
// polling, up to the timeout. Prints "settled" on success.
func runWaitUiSettled(dev *adb.Device, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	lastFocus := ""
	for time.Now().Before(deadline) {
		focus, ferr := adbCurrentFocus(dev)
		if ferr == nil {
			lastFocus = focus
			if strings.Contains(focus, "Application Not Responding") {
				_, _ = dev.RunCommand("input", "keyevent", "KEYCODE_HOME")
			} else {
				return "settled", nil
			}
		}
		time.Sleep(4 * time.Second)
	}
	return "", fmt.Errorf("wait-ui-settled: UI not settled after %s (last focus: %q)", timeout, lastFocus)
}

// adbCurrentFocus returns the `mCurrentFocus` window line from `dumpsys window`.
func adbCurrentFocus(dev *adb.Device) (string, error) {
	out, err := dev.RunCommand("dumpsys", "window")
	if err != nil {
		return "", fmt.Errorf("dumpsys window: %w", err)
	}
	return parseCurrentFocus(out), nil
}

// parseCurrentFocus extracts the first mCurrentFocus line from `dumpsys window`
// output (trimmed), or "" if absent. Pure — split out for unit testing.
func parseCurrentFocus(dumpsysWindow string) string {
	for line := range strings.SplitSeq(dumpsysWindow, "\n") {
		if strings.Contains(line, "mCurrentFocus") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
