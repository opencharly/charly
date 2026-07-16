package main

// vnc_helpers.go holds the host-side VNC support the generic graphics reverse-leg
// (resolveVerbGraphics, check_endpoint_resolve.go) needs but the out-of-process
// candy/plugin-vnc cannot reach: the credential-store VNC ticket. The former host-side
// vnc endpoint preresolvers moved into resolveVerbGraphics (the ONE venue-aware
// vnc/spice resolution). The UNIX-socket→TCP bridge (unixToTcpBridge) is pure
// host-side networking with zero core state — it moved to sdk/kit.UnixToTCPBridge
// (P12a follow-up); ssh.go and check_endpoint_resolve.go call it there directly.

// resolveVNCPassword resolves a deployment's VNC ticket from the credential store (the
// VNC_PASSWORD env override first, then the instance-specific then image-level key). It stays
// HOST-side — the out-of-process plugin cannot reach the credential store; resolveVerbGraphics
// hands the resolved password to the plugin via the reverse-leg reply. wayvnc auth itself is
// provisioned at DEPLOY time (the wayvnc / sway-desktop-vnc candy), not by the check verb.
func resolveVNCPassword(boxName, instance string) string {
	if instance != "" {
		key := boxName + "-" + instance
		val, _ := ResolveCredential("VNC_PASSWORD", CredServiceVNC, key, "")
		if val != "" {
			return val
		}
	}
	val, _ := ResolveCredential("VNC_PASSWORD", CredServiceVNC, boxName, "")
	return val
}
