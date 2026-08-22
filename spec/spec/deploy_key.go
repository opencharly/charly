package spec

import "strings"

// deploy_key.go — the pure deploy CONFIG/KEY value-vocabulary HELPERS, promoted from
// sdk/deploykit (#55 import-purity, Cone V). These carry NO mechanism dependency (stdlib
// `strings` only), so they are spec-hosted contract helpers an import-clean charly file can
// reach without an sdk mechanism-kit import — the FUNCTION analogue of the value TYPES the
// same cutover moved. deploykit keeps thin re-export aliases (var DeployKey = spec.DeployKey,
// …) so its own callers + tests + the deploy candies compile unchanged.

// DeployKey builds the charly.yml deployment map key from a box name and an optional instance:
// "selkies-desktop" (no instance) or "selkies-desktop/foo" (instance "foo"). The inverse is
// ParseDeployKey.
func DeployKey(boxName, instance string) string {
	if instance == "" {
		return boxName
	}
	return boxName + "/" + instance
}

// ParseDeployKey splits a charly.yml map key back into image name and instance.
// "selkies-desktop" → ("selkies-desktop", "")
// "selkies-desktop/foo" → ("selkies-desktop", "foo")
func ParseDeployKey(key string) (boxName, instance string) {
	if before, after, ok := strings.Cut(key, "/"); ok {
		return before, after
	}
	return key, ""
}

// FleetDelArgv returns the argv (everything after the charly binary) for a non-interactive
// `charly fleet del <name>`: the verb, the name, and the ONE valid skip-confirmation flag. Every
// programmatic teardown builds its command through this single helper — in-process
// (proc.RunCharlySubcommand), out-of-process (exec.Command), and the systemd-run TTL
// timer — so the flag can never drift across call sites again (R3 hoist: this was byte-identically
// duplicated in charly core, candy/plugin-fleet, and candy/plugin-substrate).
//
// The flag is `--assume-yes`, NOT `--yes`/`--force`: the command:fleet plugin's `charly fleet del`
// Kong grammar (candy/plugin-fleet) declares its AssumeYes field `name:"assume-yes"`, with `-y` as
// the short form. That tag used to read `long:"yes"` — go-flags syntax Kong ignores, so the flag was
// only ever --assume-yes by accident of Kong's field-name derivation; the whole tree was converted to
// `name:` so the declaration states what ships. A `--yes`/`--force` drift — neither of which Kong accepts —
// once aborted teardown at arg-parse and silently leaked the resource (see CHANGELOG/); the
// deploy-del-flag regression test guards this.
func FleetDelArgv(name string) []string {
	return []string{"fleet", "del", name, "--assume-yes"}
}

// DeriveDeploymentName turns "quay.io/myorg/openclaw:v1" → "openclaw" and
// "registry.example.com/path/foo" → "foo" — the shared default-name derivation for a
// source-less `charly fleet from-box` deploy (both the pod path, candy/plugin-fleet/from_box_pod.go,
// and the kubernetes path, candy/plugin-fleet/deploy_from_box.go — R3, one function, two callers).
func DeriveDeploymentName(imageRef string) string {
	// Strip tag.
	ref := imageRef
	if idx := lastIndexByteInRef(ref, ':'); idx >= 0 {
		ref = ref[:idx]
	}
	// Return last path component.
	if idx := lastIndexByteInRef(ref, '/'); idx >= 0 {
		return ref[idx+1:]
	}
	return ref
}

// lastIndexByteInRef returns the last index of c in s, ignoring any '/' that
// appears after a port number in a registry host (e.g., "localhost:5000/foo:v1"
// should not treat the ":5000" colon as a tag boundary). Simple heuristic:
// return last ':' only if it appears after the last '/'.
func lastIndexByteInRef(s string, c byte) int {
	lastSlash := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			lastSlash = i
		}
	}
	last := -1
	start := 0
	if c == ':' {
		start = lastSlash + 1 // only look after final path segment for tag
	}
	for i := start; i < len(s); i++ {
		if s[i] == c {
			last = i
		}
	}
	return last
}
