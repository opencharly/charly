package main

// android_spec.go — the `kind: android` schema (AndroidSpec) and the
// `apk:` candy-package schema (ApkPackageSpec).
//
// kind:android is a first-class deploy SUBSTRATE, modeled on kind:k8s: it
// describes an Android DEVICE (an in-pod emulator OR a remote/physical adb
// endpoint) onto which `apk` packages are installed by a `target: android`
// deploy. The apps themselves are NOT a kind — `apk` is a package format
// declared in candies like pac/aur/rpm/deb (see ApkPackageSpec + the
// candy manifest's `apk:` field), and an android deploy applies candies (their
// `apk:` packages) onto the device via a `target: android` deploy — an EXTERNAL
// deploy substrate (F1) served out-of-process by candy/plugin-adb (deploy:android).
//
// Build-vs-runtime split: the Android system image + API level are baked
// into the referenced kind:box at BUILD time (sdkmanager in the
// android-sdk candy). kind:android REFERENCES that box — it never drives
// a build. The api_level/device fields are informational documentation of
// the baked profile, not assertions or build drivers.
