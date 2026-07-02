package vmshared

// Libvirt URI parsing — bridges our CLI's `--uri` flag / vm.yml
// fields to the go-libvirt transport.
//
// The library itself does NOT speak libvirt-URI transports like
// `qemu+ssh://`; it takes a raw net.Conn and does the Connect RPC.
// For remote hypervisors we build the net.Conn ourselves by:
//
//   1. Opening an SSH client to the remote host.
//   2. Using sshClient.Dial("unix", "/run/user/<uid>/libvirt/virtqemud-sock")
//      to forward the libvirt session socket over that SSH connection.
//   3. Passing the resulting net.Conn to libvirt.New, then calling
//      ConnectToURI(libvirt.QEMUSession).
//
// The URI shape we accept:
//   - ""                                    → local qemu:///session (default)
//   - "qemu:///session"                     → local qemu:///session
//   - "qemu+ssh://[user@]host[:port]/session"
//     (the `/system` path is not supported in this codebase —
//     everything charly manages lives in the session daemon)
//
// No other libvirt URI schemes (qemu+tcp, qemu+tls, qemu+unix) are
// supported. Add here if a concrete need arises.

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// LibvirtURI is the parsed form of an `charly --uri …` value.
type LibvirtURI struct {
	// Mode is "session" (currently the only supported value).
	Mode string

	// Remote is the SSH target when scheme is qemu+ssh://. Zero
	// value means local (qemu:///session).
	Remote SSHTarget

	// Raw is the original URI string for error messages.
	Raw string
}

// IsLocal reports whether the URI targets the local libvirt session.
// Equivalent to len(u.Remote.Host) == 0.
func (u LibvirtURI) IsLocal() bool {
	return u.Remote.Host == ""
}

// ParseLibvirtURI handles the four recognized shapes. Empty / missing
// URI returns a local-session value; invalid URIs return an error.
func ParseLibvirtURI(s string) (LibvirtURI, error) {
	if s == "" || s == "qemu:///session" {
		return LibvirtURI{Mode: "session", Raw: s}, nil
	}
	if !strings.HasPrefix(s, "qemu+ssh://") {
		return LibvirtURI{}, fmt.Errorf("unsupported libvirt URI %q (want qemu:///session or qemu+ssh://…/session)", s)
	}
	u, err := url.Parse(s)
	if err != nil {
		return LibvirtURI{}, fmt.Errorf("parse libvirt URI %q: %w", s, err)
	}
	mode := strings.TrimPrefix(u.Path, "/")
	if mode == "" {
		mode = "session"
	}
	if mode != "session" {
		return LibvirtURI{}, fmt.Errorf("libvirt URI %q: only /session is supported (got /%s)", s, mode)
	}
	target := SSHTarget{
		User: u.User.Username(),
		Host: u.Hostname(),
		Port: 22,
	}
	if target.User == "" {
		target.User = currentUsername()
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return LibvirtURI{}, fmt.Errorf("libvirt URI %q: invalid port %q", s, p)
		}
		target.Port = n
	}
	if target.Host == "" {
		return LibvirtURI{}, fmt.Errorf("libvirt URI %q: missing host", s)
	}
	return LibvirtURI{Mode: mode, Remote: target, Raw: s}, nil
}
