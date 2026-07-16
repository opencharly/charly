package main

// android_deploy_preresolve.go — the HOST-SIDE deploy:android preresolver (F1).
//
// `target: android` is an EXTERNAL deploy substrate served out-of-process by
// candy/plugin-adb (the same plugin that serves the `adb:` check verb — one plugin
// owns ALL Android device interaction, so the goadb dep + the apk install path stay
// the single copy, R3). The plugin drives the device but cannot resolve WHICH device
// (engine inspect on the running pod, ${HOST_PORT:N}, the credential store) nor read
// the project's apk: candy specs — that needs host context. This preresolver does the
// host half and ships the result in DeployVenue.Substrate (a spec.AndroidDeployVenue):
//
//   - resolve the kind:android device → adb endpoint (resolveAndroidDevice, the SAME
//     helper the `charly status` AndroidCollector uses — R3);
//   - collect the apk install specs from the deploy's COMPILED plans (each apk: candy
//     compiled to an ApkInstallStep), rewriting committed-APK relative paths to
//     ABSOLUTE host paths the plugin reads;
//   - stamp the readiness + install-retry windows (no magic numbers in the plugin).
//
// The install/uninstall ORCHESTRATION (boot gate + retry loop) moved INTO the plugin
// (candy/plugin-adb/deploy.go), reusing its existing install/install-app/uninstall/
// wait-for-device method handlers. The host keeps ONLY device-endpoint resolution +
// apk-artifact collection (this file).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/opencharly/sdk/spec"
)

// androidBootDeadline bounds the readiness gate for a freshly-started emulator
// (cold boot of an API-36 Play-Store image is the worst case). Shipped to the
// plugin as AndroidDeployVenue.BootTimeout.
const androidBootDeadline = 5 * time.Minute

// androidInstallDeadline / androidInstallInterval bound the per-apk install retry:
// Android's PackageManager keeps initializing for SECONDS after sys.boot_completed
// flips to 1, and a freshly-pushed APK installed in that window throws "Failed to
// parse APK file" even though the file is intact. The install SUCCEEDING is the
// readiness condition (a real synchronization primitive, NOT a fixed sleep). Shipped
// to the plugin as AndroidDeployVenue.InstallDeadline / InstallInterval.
const (
	androidInstallDeadline = 180 * time.Second
	androidInstallInterval = 5 * time.Second
)

// register the android deploy preresolver at package-var init (before any init()),
// race-free with the rest of the F1 wiring.
var _ = func() bool {
	registerDeployPreresolver("android", androidDeployPreresolve)
	return true
}()

// androidDeployPreresolve is the deploy:android preresolver. It resolves the live
// device endpoint + the apk install specs and marshals a spec.AndroidDeployVenue.
// node may be nil (the Update path carries no DeployContext) — it then re-resolves
// the deploy node from the tree by name (the node-free re-resolution every node-free
// deploy lifecycle method uses).
func androidDeployPreresolve(name, dir string, node *spec.BundleNode, plans []*InstallPlan) (json.RawMessage, error) {
	if dir == "" {
		if cwd, err := os.Getwd(); err == nil {
			dir = cwd
		}
	}
	if node == nil {
		tree, err := resolveTreeRoot(dir)
		if err != nil {
			return nil, fmt.Errorf("deploy %q: resolve android deploy node: %w", name, err)
		}
		n, ok := tree[name]
		if !ok {
			return nil, fmt.Errorf("deploy %q: no android deploy entry", name)
		}
		node = &n
	}
	if node.From == "" {
		return nil, fmt.Errorf("deploy %q: target=android requires `android:` (kind:android device reference)", name)
	}
	spc := findAndroidSpec(dir, node.From)
	if spc == nil {
		return nil, fmt.Errorf("deploy %q: kind:android device %q not declared in the android: section", name, node.From)
	}
	dev, err := resolveAndroidDevice(spc, node, name)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: resolving android device %q: %w", name, node.From, err)
	}

	installs, err := collectAndroidInstalls(plans)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: %w", name, err)
	}

	venue := spec.AndroidDeployVenue{
		AdbAddr:         dev.AdbAddr,
		Engine:          dev.Engine,
		Container:       dev.Container,
		Serial:          dev.serial(),
		GoogleEmail:     dev.GoogleEmail,
		GoogleToken:     dev.GoogleToken,
		Installs:        installs,
		BootTimeout:     androidBootDeadline.String(),
		InstallDeadline: androidInstallDeadline.String(),
		InstallInterval: androidInstallInterval.String(),
	}
	payload, err := json.Marshal(venue)
	if err != nil {
		return nil, fmt.Errorf("deploy %q: marshal android venue: %w", name, err)
	}
	return payload, nil
}

// collectAndroidInstalls walks the deploy's compiled plans for ApkInstallStep
// entries (the apk: package format) and flattens them into the wire install list,
// rewriting committed-APK relative paths to ABSOLUTE host paths (resolved against the
// candy source tree) so the plugin — which reads the file on the host but has no
// candy context — can open it. package entries pass through unchanged.
func collectAndroidInstalls(plans []*InstallPlan) ([]spec.ApkPackageSpec, error) {
	var installs []spec.ApkPackageSpec
	for _, p := range plans {
		if p == nil {
			continue
		}
		for _, step := range p.Steps {
			apkStep, ok := step.(*ApkInstallStep)
			if !ok {
				continue
			}
			for _, ap := range apkStep.Packages {
				if ap.Apk != "" {
					abs, err := resolveApkPath(ap.Apk, apkStep.CandyDir)
					if err != nil {
						return nil, fmt.Errorf("candy %q: %w", apkStep.CandyName, err)
					}
					ap.Apk = abs
				} else if ap.Package == "" {
					return nil, fmt.Errorf("apk entry in candy %q has neither package: nor apk: declared", apkStep.CandyName)
				}
				installs = append(installs, ap)
			}
		}
	}
	return installs, nil
}

// resolveApkPath resolves a committed-APK reference against the candy's SOURCE tree.
// Absolute paths are used verbatim; a relative path anchors candy-dir-relative first,
// then each ancestor up to the candy's project / repo root (first existing match wins).
// This resolves a path like `tests/data/ApiDemos-debug.apk` identically whether the
// candy is LOCAL (candyDir under the consuming project root) or fetched via @github
// (candyDir under the cloned-repo cache, where a project-root-relative file lives at
// <repo-root>/tests/data/... several levels above candyDir).
//
// It FAILS HARD when a relative ref has no candy dir to anchor against, or when the
// file is not found anywhere up the tree — the caller surfaces that, never silently
// passing an unresolvable path downstream.
func resolveApkPath(ref, candyDir string) (string, error) {
	if filepath.IsAbs(ref) {
		return ref, nil
	}
	if candyDir == "" {
		return "", fmt.Errorf("cannot resolve relative committed APK %q: no candy source dir to anchor against", ref)
	}
	for dir := candyDir; ; {
		cand := filepath.Join(dir, ref)
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("committed APK %q not found under candy source tree %q (searched every ancestor up to the filesystem root)", ref, candyDir)
		}
		dir = parent
	}
}
