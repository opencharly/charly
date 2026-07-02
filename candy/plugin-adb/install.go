package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/spec"
)

// install.go is the SINGLE Android app-install path (R3), living in this out-of-tree
// plugin (the adb → external-plugin dep-shed). Both the `adb` check verb (install /
// install-app / uninstall) AND the `deploy:android` substrate (the `apk:` package
// format, via deploy.go's installOp → dispatch) reach these helpers through the ONE
// provider — there is exactly ONE implementation of "download by package id via apkeep
// + install" and ONE of "push a committed APK + install".
//
// Venue:
//   - in-pod (inPodContainer set): apkeep + adb run inside the emulator pod via
//     `engine exec`; google-play creds come from the container env.
//   - host/endpoint (inPodContainer == ""): apkeep + adb run on the host; adb
//     targets the device's adb server over the network (`adb -H host -P port`).
//
// Committed-APK installs (installFromHostApk) always push via goadb against the
// resolved adb addr, so they are venue-agnostic.

// adbScriptPrefix returns the adb invocation the install script uses. In-pod it's
// the baked platform-tools adb against the local emulator; on the host it's the
// system adb pointed at the device's adb server over the network.
func adbScriptPrefix(env *adbEnv) (string, error) {
	if env.inPodContainer() != "" {
		return "/opt/android-sdk/platform-tools/adb -s " + env.serial(), nil
	}
	host, port, err := splitAdbAddr(env.AdbAddr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("adb -H %s -P %d -s %s", host, port, env.serial()), nil
}

// apkeepArgString renders the apkeep command (without the trailing output dir) for
// one package spec. google-play reads $GOOGLE_ACCOUNT_EMAIL/$GOOGLE_AAS_TOKEN from
// the env (container env in-pod; exec env on the host).
func apkeepArgString(s spec.ApkPackageSpec) string {
	appArg := s.Package
	if s.AppVersion != "" {
		appArg = s.Package + "@" + s.AppVersion
	}
	switch s.EffectiveSource() {
	case "google-play":
		return `if [ -z "${GOOGLE_ACCOUNT_EMAIL:-}" ] || [ -z "${GOOGLE_AAS_TOKEN:-}" ]; then ` +
			`echo "apk source google-play requires GOOGLE_ACCOUNT_EMAIL + GOOGLE_AAS_TOKEN (set via charly secrets / kind:android google_account)" >&2; exit 1; fi; ` +
			`apkeep -a ` + shellSingleQuote(appArg) + ` -d google-play -e "$GOOGLE_ACCOUNT_EMAIL" -t "$GOOGLE_AAS_TOKEN" -o split_apk=1 "$TMP"`
	case "f-droid", "huawei-app-gallery":
		return `apkeep -a ` + shellSingleQuote(appArg) + ` -d ` + shellSingleQuote(s.EffectiveSource()) + ` "$TMP"`
	default: // apk-pure
		out := `apkeep -a ` + shellSingleQuote(appArg) + ` -d apk-pure`
		if s.EffectiveArch() != "" {
			out += ` -o arch=` + shellSingleQuote(s.EffectiveArch())
		}
		return out + ` "$TMP"`
	}
}

// installScript builds the ONE bash script that downloads via apkeep and installs
// the result (single .apk / split set / .xapk) onto the device. adbPrefix selects
// the venue's adb (in-pod baked adb or host `adb -H -P`).
func installScript(s spec.ApkPackageSpec, adbPrefix string) string {
	return `set -euo pipefail
export PATH="/opt/android-sdk/platform-tools:$PATH"
ADB="` + adbPrefix + `"
TMP="$(mktemp -d /tmp/charly-apk-XXXXXX)"
trap 'rm -rf "$TMP"' EXIT
` + apkeepArgString(s) + `
shopt -s nullglob
xapks=("$TMP"/*.xapk)
apks=("$TMP"/*.apk)
if [ ${#xapks[@]} -gt 0 ]; then
  mkdir -p "$TMP/x"; unzip -o -q "${xapks[0]}" -d "$TMP/x"
  $ADB install-multiple -r "$TMP"/x/*.apk
elif [ ${#apks[@]} -gt 1 ]; then
  $ADB install-multiple -r "${apks[@]}"
elif [ ${#apks[@]} -eq 1 ]; then
  $ADB install -r "${apks[0]}"
else
  echo "apk install: apkeep produced no .apk/.xapk in $TMP" >&2; ls -la "$TMP" >&2 || true; exit 1
fi`
}

// shellSingleQuote is the shared kit helper (FU-11 — formerly duplicated in core + plugins).
var shellSingleQuote = kit.ShellQuote

// installByPackage downloads an app by package id via apkeep and installs it onto
// the device. In-pod runs the script via `engine exec`; host/endpoint runs it via
// local bash with host apkeep + `adb -H -P`. Asserts "Success" in the output.
func installByPackage(env *adbEnv, s spec.ApkPackageSpec) (string, error) {
	adbPrefix, err := adbScriptPrefix(env)
	if err != nil {
		return "", err
	}
	script := installScript(s, adbPrefix)

	var cmd *exec.Cmd
	if c := env.inPodContainer(); c != "" {
		cmd = exec.Command(env.engine(), "exec", c, "bash", "-c", script)
	} else {
		cmd = exec.Command("bash", "-c", script)
		cmd.Env = os.Environ()
		if env.GoogleEmail != "" {
			cmd.Env = append(cmd.Env, "GOOGLE_ACCOUNT_EMAIL="+env.GoogleEmail)
		}
		if env.GoogleToken != "" {
			cmd.Env = append(cmd.Env, "GOOGLE_AAS_TOKEN="+env.GoogleToken)
		}
	}
	out, runErr := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if runErr != nil {
		return trimmed, fmt.Errorf("install %s (source %s): %w: %s", s.Package, s.EffectiveSource(), runErr, trimmed)
	}
	if !strings.Contains(trimmed, "Success") {
		return trimmed, fmt.Errorf("install %s did not return Success: %s", s.Package, trimmed)
	}
	return trimmed, nil
}

// installFromHostApk pushes a committed local APK to the device via the goadb sync
// protocol and runs `pm install -r`. Venue-agnostic. Asserts "Success".
func installFromHostApk(env *adbEnv, path string) (string, error) {
	dev, err := env.device()
	if err != nil {
		return "", err
	}
	apkBytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read APK %s: %w", path, err)
	}
	if len(apkBytes) == 0 {
		return "", fmt.Errorf("APK %s is empty", path)
	}
	remote := fmt.Sprintf("/data/local/tmp/charly-apk-%d.apk", time.Now().UnixNano())
	writer, err := dev.OpenWrite(remote, 0644, time.Now())
	if err != nil {
		return "", fmt.Errorf("adb push %s → %s: %w", path, remote, err)
	}
	if _, err := writer.Write(apkBytes); err != nil {
		writer.Close() //nolint:errcheck
		return "", fmt.Errorf("write APK to device: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close push stream: %w", err)
	}
	out, err := dev.RunCommand("pm", "install", "-r", remote)
	if err != nil {
		return "", fmt.Errorf("pm install: %w", err)
	}
	trimmed := strings.TrimSpace(out)
	_, _ = dev.RunCommand("rm", "-f", remote)
	if !strings.Contains(trimmed, "Success") {
		return trimmed, fmt.Errorf("pm install did not return Success: %s", trimmed)
	}
	return trimmed, nil
}

// uninstall removes a package by id, returning the raw `pm uninstall` output (a
// "Success"/"Failure" line). It does NOT assert Success: the check verb's bed step
// asserts it via a stdout matcher, while the deploy Del path is best-effort
// idempotent (an already-absent package is not a hard error).
func uninstall(env *adbEnv, pkg string) (string, error) {
	dev, err := env.device()
	if err != nil {
		return "", err
	}
	out, err := dev.RunCommand("pm", "uninstall", pkg)
	if err != nil {
		return "", fmt.Errorf("pm uninstall %s: %w", pkg, err)
	}
	return strings.TrimSpace(out), nil
}
