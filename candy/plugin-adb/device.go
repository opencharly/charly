package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	adb "github.com/zach-klippenstein/goadb"
)

// device.go resolves a goadb device/client handle from the invocation env, and
// holds the goadb-backed device-state helpers. This is where the heavy
// github.com/zach-klippenstein/goadb dependency lives — entirely out of charly's
// core go.mod.

// adbEnv is the plugin-side decode of the invocation context the host ships as
// Operation.Env. It is the UNION of two producers:
//   - the CHECK verb: charly's CheckEnv (provider_checkenv.go) → Box/Instance/Mode/
//     ContainerName; the plugin resolves the device's adb-server port from
//     ContainerName via engine inspect.
//   - the deploy:android SUBSTRATE (deploy.go): built from the host-preresolved
//     spec.AndroidDeployVenue → an already-resolved AdbAddr (host:port) plus the in-pod
//     Engine/Container + the google-play creds the by-package installer needs.
//
// JSON is structural, so one struct decodes both producers — each sets only the
// fields it has, the rest stay zero.
type adbEnv struct {
	Box           string `json:"box"`
	Instance      string `json:"instance"`
	Mode          string `json:"mode"` // "live" | "box"
	ContainerName string `json:"container_name"`

	// deploy/status extras (absent for the check verb).
	AdbAddr     string `json:"adb_addr"`
	Engine      string `json:"engine"`
	Container   string `json:"container"`
	Serial      string `json:"serial"`
	GoogleEmail string `json:"google_email"`
	GoogleToken string `json:"google_token"`
}

func (e *adbEnv) serial() string {
	if e.Serial != "" {
		return e.Serial
	}
	return "emulator-5554"
}

func (e *adbEnv) engine() string {
	if e.Engine != "" {
		return e.Engine
	}
	return engineBinary()
}

// inPodContainer is the emulator pod container name when the device is in-pod
// (apkeep + the baked adb run inside it): the deploy's Container, else the check
// verb's ContainerName. "" for a remote/physical adb endpoint.
func (e *adbEnv) inPodContainer() string {
	if e.Container != "" {
		return e.Container
	}
	return e.ContainerName
}

// resolveAdbAddr returns the "host:port" of the device's adb server: the explicit
// AdbAddr (deploy/status — already resolved core-side) or, for the check verb, the
// container's host-published 5037 resolved via engine inspect.
func (e *adbEnv) resolveAdbAddr() (string, error) {
	if e.AdbAddr != "" {
		return e.AdbAddr, nil
	}
	if e.ContainerName == "" {
		return "", fmt.Errorf("adb: no adb_addr and no container name in env (box=%q) — the verb needs a running pod", e.Box)
	}
	insp, err := inspectContainer(e.engine(), e.ContainerName)
	if err != nil {
		return "", err
	}
	port, err := insp.findHostPort(adbServerPort)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("127.0.0.1:%d", port), nil
}

// device returns a goadb device handle for the resolved adb-server addr + serial.
func (e *adbEnv) device() (*adb.Device, error) {
	addr, err := e.resolveAdbAddr()
	if err != nil {
		return nil, err
	}
	return adbDeviceForAddr(addr, e.serial())
}

// client returns a goadb client (used by `devices` to list every serial).
func (e *adbEnv) client() (*adb.Adb, error) {
	addr, err := e.resolveAdbAddr()
	if err != nil {
		return nil, err
	}
	return adbClientForAddr(addr)
}

// splitAdbAddr parses a "host:port" adb-server address into its parts.
func splitAdbAddr(addr string) (string, int, error) {
	if addr == "" {
		return "", 0, fmt.Errorf("empty adb address")
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid adb address %q (want host:port): %w", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return "", 0, fmt.Errorf("invalid adb port in %q", addr)
	}
	return host, port, nil
}

// adbClientForAddr returns a goadb client for an adb server reachable at
// "host:port" (the in-pod published 5037, or a remote adb endpoint).
func adbClientForAddr(addr string) (*adb.Adb, error) {
	host, port, err := splitAdbAddr(addr)
	if err != nil {
		return nil, err
	}
	client, err := adb.NewWithConfig(adb.ServerConfig{
		// PathToAdb only matters if goadb has to spawn a local server; the
		// server is already running at host:port, so any existing binary
		// suffices. /bin/true is the smallest portable stand-in.
		PathToAdb: "/bin/true",
		Host:      host,
		Port:      port,
	})
	if err != nil {
		return nil, fmt.Errorf("adb client init (%s): %w", addr, err)
	}
	return client, nil
}

// adbDeviceForAddr returns a goadb Device handle for an adb server at "host:port".
func adbDeviceForAddr(addr, serial string) (*adb.Device, error) {
	client, err := adbClientForAddr(addr)
	if err != nil {
		return nil, err
	}
	if serial == "" {
		serial = "emulator-5554"
	}
	return client.Device(adb.DeviceWithSerial(serial)), nil
}

// adbStateString renders goadb's DeviceState enum into the canonical
// `adb devices` output strings (device / offline / unauthorized).
func adbStateString(s adb.DeviceState) string {
	switch s {
	case adb.StateOnline:
		return "device"
	case adb.StateOffline:
		return "offline"
	case adb.StateUnauthorized:
		return "unauthorized"
	case adb.StateDisconnected:
		return "disconnected"
	}
	return strings.ToLower(s.String())
}
