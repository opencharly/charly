package secrets

import (
	"context"
	"fmt"
	"os"
	"time"

	dbus "github.com/godbus/dbus/v5"
)

// keyring_unlock_wait.go is the externalized keyring-unlock WAITER — the godbus
// PropertiesChanged subscription the core's encrypted-volume mount path (enc.go) used to
// run in charly's core. The Secret Service is owned out-of-process by THIS plugin (the
// ssClient + godbus live here), so the wait lives here too: charly's core RPCs
// verb:credential `await-unlock` (charly/credential_plugin.go pluginCredentialStore.awaitUnlock)
// and BLOCKS the gRPC Invoke until the credential becomes resolvable or the host cancels the
// Invoke ctx (SIGINT/SIGTERM on `systemctl stop`). This is what sheds github.com/godbus/dbus/v5
// from charly/go.mod — godbus now lives ONLY in candy/plugin-secrets.

// awaitSignalBackstop is the safety-net re-probe interval for the event-driven keyring wait.
// Between DBus PropertiesChanged signals, the loop re-probes at this cadence to handle missed
// signals, subscribe races, or Secret Service providers that do not reliably emit the signal
// (KeePassXC's FdoSecrets plugin does NOT emit it; GNOME Keyring and KDE Wallet do).
var awaitSignalBackstop = 30 * time.Second

// awaitProgressLogInterval throttles the periodic "still waiting" log during the wait.
var awaitProgressLogInterval = 1 * time.Hour

// awaitUnlock blocks until the credential at service/key becomes resolvable
// (resolveStoreChain returns a source other than "locked") or ctx is cancelled.
// Event-driven via DBus PropertiesChanged signals on the Secret Service collections
// (zero CPU between events) with a periodic backstop re-probe as a safety net.
//
// ctx is the host's Invoke ctx: the core cancels it on SIGINT/SIGTERM, gRPC propagates the
// cancellation here, and the select loop returns — so `systemctl stop` ends the wait cleanly.
// Returns the resolved value and its source; on ctx cancellation it returns source "locked"
// (the gRPC layer surfaces the cancellation to the host as the authoritative signal).
func awaitUnlock(ctx context.Context, service, key string) (value, source string) {
	resolver := func() (string, string) { return resolveStoreChain(service, key) }
	reset := resetDefaultCredentialStore

	// Fast path: if the credential is ALREADY resolvable — the keyring is already
	// unlocked, or it is a config-backend credential that needs no unlock — return
	// immediately without touching DBus. Covers the common service-restart case and
	// makes resolution deterministic when no session bus is present.
	reset()
	if v, src := resolver(); src != "locked" {
		return v, src
	}

	conn, err := dbus.SessionBusPrivate()
	if err != nil {
		return awaitUnlockBackstopOnly(ctx, key, resolver, reset)
	}
	if err := conn.Auth(nil); err != nil {
		_ = conn.Close()
		return awaitUnlockBackstopOnly(ctx, key, resolver, reset)
	}
	if err := conn.Hello(); err != nil {
		_ = conn.Close()
		return awaitUnlockBackstopOnly(ctx, key, resolver, reset)
	}
	defer conn.Close() //nolint:errcheck

	matchRule := "type='signal',interface='org.freedesktop.DBus.Properties',member='PropertiesChanged',path_namespace='/org/freedesktop/secrets/collection'"
	call := conn.BusObject().Call("org.freedesktop.DBus.AddMatch", 0, matchRule)
	if call.Err != nil {
		return awaitUnlockBackstopOnly(ctx, key, resolver, reset)
	}

	sigCh := make(chan *dbus.Signal, 16)
	conn.Signal(sigCh)
	defer conn.RemoveSignal(sigCh)

	// Re-probe after subscribing to close the subscribe-unlock race.
	reset()
	if v, src := resolver(); src != "locked" {
		return v, src
	}

	fmt.Fprintf(os.Stderr,
		"charly: waiting for keyring unlock (charly/%s, event-driven via DBus PropertiesChanged)\n", key)
	return awaitUnlockLoop(ctx, key, sigCh, resolver, reset)
}

// awaitUnlockBackstopOnly is the fallback when DBus signal subscription fails (no session bus
// yet — e.g. a linger-based start before the graphical session). Polls at awaitSignalBackstop.
func awaitUnlockBackstopOnly(ctx context.Context, key string, resolver func() (string, string), reset func()) (string, string) {
	fmt.Fprintf(os.Stderr,
		"charly: waiting for keyring unlock (charly/%s, DBus signals unavailable — %v backstop only)\n",
		key, awaitSignalBackstop)
	return awaitUnlockLoop(ctx, key, nil, resolver, reset)
}

// awaitUnlockLoop is the core select loop shared by both the signal and backstop-only modes.
// When sigCh is nil, only the backstop fires.
func awaitUnlockLoop(ctx context.Context, key string, sigCh <-chan *dbus.Signal, resolver func() (string, string), reset func()) (string, string) {
	backstop := time.NewTicker(awaitSignalBackstop)
	defer backstop.Stop()
	nextLog := time.Now().Add(awaitProgressLogInterval)

	for {
		select {
		case <-ctx.Done():
			return "", "locked"
		case sig, ok := <-sigCh:
			if !ok {
				return awaitUnlockBackstopOnly(ctx, key, resolver, reset)
			}
			if !isCollectionUnlockedSignal(sig) {
				continue
			}
			reset()
			if v, src := resolver(); src != "locked" {
				return v, src
			}
		case <-backstop.C:
			reset()
			if v, src := resolver(); src != "locked" {
				return v, src
			}
			if time.Now().After(nextLog) {
				fmt.Fprintf(os.Stderr,
					"charly: still waiting for keyring unlock (charly/%s)\n", key)
				nextLog = time.Now().Add(awaitProgressLogInterval)
			}
		}
	}
}

// isCollectionUnlockedSignal returns true when sig is a DBus PropertiesChanged signal
// indicating a Secret Service collection's Locked property transitioned to false (unlocked).
// The await loop wakes only on relevant unlock events. ssCollectionInterface is the
// Secret Service Collection interface const defined alongside the ssClient (secret_service.go).
func isCollectionUnlockedSignal(sig *dbus.Signal) bool {
	if sig == nil || sig.Name != "org.freedesktop.DBus.Properties.PropertiesChanged" {
		return false
	}
	if len(sig.Body) < 2 {
		return false
	}
	iface, _ := sig.Body[0].(string)
	if iface != ssCollectionInterface {
		return false
	}
	changed, _ := sig.Body[1].(map[string]dbus.Variant)
	v, ok := changed["Locked"]
	if !ok {
		return false
	}
	locked, ok := v.Value().(bool)
	return ok && !locked
}
