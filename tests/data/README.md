# tests/data — vendored test assets for `charly check adb` + `charly check appium`

## ApiDemos-debug.apk

The Android `ApiDemos` sample app used by the R10 acceptance round for
the `adb` + `appium` check verbs. Originally part of the Android SDK
Samples (Apache-2.0 licensed); vendored from the Appium project's
canonical sample-apps location.

| Field | Value |
|---|---|
| **Source** | `appium/appium` — `packages/appium/sample-code/apps/ApiDemos-debug.apk` |
| **Upstream blob SHA** | `e972bcb766d60674ce9a4f9e17350454471f6f54` |
| **Local sha256** | `354b56605e8f201ce5fdd5b796524d8fabae726ee57de2d76dcf878c4d7826f1` |
| **Size** | 4.6 MB |
| **License** | Apache-2.0 |
| **Package id** | `io.appium.android.apis` |
| **Main activity** | `.ApiDemos` |

Refresh procedure (if upstream updates the file):

```bash
curl -fsSL -o tests/data/ApiDemos-debug.apk \
  https://github.com/appium/appium/raw/refs/heads/master/packages/appium/sample-code/apps/ApiDemos-debug.apk
sha256sum tests/data/ApiDemos-debug.apk
# Update the sha256 + upstream blob SHA in this README.
```

## api-demos-caps.json

W3C WebDriver capabilities for `charly check appium session-create` against
the ApiDemos APK on the check-android-emulator-pod deploy. Uses the flat (non-
alwaysMatch-wrapped) form — the appium session-create verb wraps it
under W3C `alwaysMatch` automatically.

Used by R10 step 8 (manual CLI smoke):

```bash
charly check appium session-create check-android-emulator-pod \
  --caps @tests/data/api-demos-caps.json
```

The `appium:noReset: true` keeps the AVD state across session-create
cycles (faster re-runs during exploration). `appium:newCommandTimeout`
gives 120s of session-idle headroom for inter-command waits.
