package adb

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/opencharly/sdk"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

// deploy.go — the `deploy:android` SUBSTRATE provider (F1). candy/plugin-adb serves
// BOTH the `adb:` check verb AND the `target: android` deploy substrate, so ALL
// Android device interaction — the goadb wire, the apkeep+adb install path — lives in
// this ONE plugin (R3, no duplicate installer). The host's android deploy preresolver
// (charly/android_deploy_preresolve.go) resolves the device endpoint + collects the
// apk install specs (committed-APK paths rewritten to ABSOLUTE host paths) and ships
// them in DeployVenue.Substrate; this provider drives the device:
//
//   - gate on sys.boot_completed (a real synchronization condition, not a sleep);
//   - install each app, RETRYING past PackageManager post-boot init (the install
//     SUCCEEDING is the readiness condition) — reusing the SAME install/install-app
//     method handlers the `adb:` verb dispatches (dispatch(), methods.go);
//   - return the uninstall teardown ops the host records in the ledger and replays at
//     `charly bundle del` (record-and-replay, the external deploy lifecycle).
//
// The plugin runs as a HOST subprocess (LocalTransport), so it reads the committed-APK
// host paths directly and reaches the adb endpoint (network / `engine exec`) directly —
// it never needs the executor reverse channel for android.

// deployAndroidVersion is the candy version stamped onto the ledger record (kept in
// lockstep with charly.yml + the Describe capability version).
const deployAndroidVersion = "2026.180.0001"

// invokeDeployAndroid handles an OpExecute Invoke for the deploy:android substrate.
// It decodes the preresolved venue, gates on boot, installs every app with retry, and
// returns the teardown ops. Any install failure is a hard deploy error.
func invokeDeployAndroid(req *pb.InvokeRequest) (*pb.InvokeReply, error) {
	venue, err := sdk.DecodeDeployVenue(req.GetEnvJson())
	if err != nil {
		return nil, fmt.Errorf("deploy:android: decode venue: %w", err)
	}
	if len(venue.Substrate) == 0 {
		return nil, fmt.Errorf("deploy:android: empty substrate payload (the host preresolver produced no AndroidDeployVenue)")
	}
	var av spec.AndroidDeployVenue
	if err := json.Unmarshal(venue.Substrate, &av); err != nil {
		return nil, fmt.Errorf("deploy:android: decode android venue: %w", err)
	}

	env := &adbEnv{
		AdbAddr:     av.AdbAddr,
		Engine:      av.Engine,
		Container:   av.Container,
		Serial:      av.Serial,
		GoogleEmail: av.GoogleEmail,
		GoogleToken: av.GoogleToken,
	}

	// Readiness gate — sys.boot_completed (the goadb poll IS the condition).
	dev, err := env.device()
	if err != nil {
		return nil, fmt.Errorf("deploy:android: %w", err)
	}
	if _, err := runWaitForDevice(dev, parseTimeout(av.BootTimeout, 5*time.Minute)); err != nil {
		return nil, fmt.Errorf("deploy:android: %w", err)
	}

	// Install each app, retrying past PackageManager post-boot init.
	deadline := parseTimeout(av.InstallDeadline, 180*time.Second)
	interval := parseTimeout(av.InstallInterval, 5*time.Second)
	var reverseOps []spec.ReverseOp
	for _, ap := range av.Installs {
		op := installOp(ap)
		if _, err := installWithRetry(deadline, interval, func() (string, error) { return dispatch(env, op) }); err != nil {
			return nil, fmt.Errorf("deploy:android: install %s: %w", installLabel(ap), err)
		}
		// A package id is uninstallable by id; a committed-APK entry has no id to
		// reverse by (its app is removed with the pod, or stays — same best-effort
		// as the pre-externalization android Del).
		if ap.Package != "" {
			reverseOps = append(reverseOps, androidUninstallReverseOp(env, ap.Package))
		}
	}
	return sdk.BuildDeployReply(reverseOps, "plugin-adb", deployAndroidVersion)
}

// installOp builds the synthetic adb #Op for one apk install spec, dispatched through
// the SAME method handlers the `adb:` verb uses (methods.go): a committed-APK entry →
// `install` (push the host path); a package id → `install-app` (apkeep download). The
// per-verb fields ride the plugin input map (params.AdbInput's wire keys) since the
// schema-compaction cutover.
func installOp(ap spec.ApkPackageSpec) *spec.Op {
	if ap.Apk != "" {
		return &spec.Op{Plugin: "adb", PluginInput: map[string]any{
			"method": "install", "apk": ap.Apk,
		}}
	}
	return &spec.Op{Plugin: "adb", PluginInput: map[string]any{
		"method":      "install-app",
		"app_id":      ap.Package,
		"source":      ap.Source,
		"arch":        ap.Arch,
		"app_version": ap.AppVersion,
	}}
}

// installLabel names an install spec for an error message.
func installLabel(ap spec.ApkPackageSpec) string {
	if ap.Apk != "" {
		return ap.Apk
	}
	return ap.Package
}

// installWithRetry runs an install op, retrying on failure until the deadline. The
// install SUCCEEDING is the readiness condition for PackageManager post-boot init (a
// freshly-pushed APK throws "Failed to parse APK file" while PM is still initializing
// even though the file is intact) — a real synchronization primitive, NOT a fixed
// sleep. deadline/interval are parameters (host-shipped) so tests drive it without real
// sleeps. Moved here from charly's core with the android deploy ORCHESTRATION (F1).
func installWithRetry(deadline, interval time.Duration, op func() (string, error)) (string, error) {
	end := time.Now().Add(deadline)
	for {
		out, err := op()
		if err == nil || time.Now().After(end) {
			return out, err
		}
		time.Sleep(interval)
	}
}

// androidUninstallReverseOp builds the best-effort teardown op for one installed
// package: a host shell script that `pm uninstall`s it via the venue's adb (the baked
// in-pod adb under `engine exec`, or host `adb -H -P` for a remote endpoint). `|| true`
// keeps it idempotent — at `charly bundle del` the emulator pod may already be gone
// (its lifecycle belongs to the pod deploy). System scope (uninstall mutates
// device-global package state); the host records + replays it (record-and-replay).
func androidUninstallReverseOp(env *adbEnv, pkg string) spec.ReverseOp {
	var script string
	if c := env.inPodContainer(); c != "" {
		script = fmt.Sprintf("%s exec %s /opt/android-sdk/platform-tools/adb -s %s uninstall %s || true",
			env.engine(), c, env.serial(), shellSingleQuote(pkg))
	} else if host, port, err := splitAdbAddr(env.AdbAddr); err == nil {
		script = fmt.Sprintf("adb -H %s -P %d -s %s uninstall %s || true",
			host, port, env.serial(), shellSingleQuote(pkg))
	} else {
		script = "true" // malformed addr → no-op teardown (best-effort)
	}
	return sdk.PluginScriptReverseOp(spec.ScopeSystem, script)
}
