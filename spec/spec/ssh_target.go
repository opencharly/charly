package spec

// ssh_target.go — the PURE (x/crypto-free) SSH target vocabulary:
// SSHTarget + ParseSSHTarget + String + CurrentUsername. It is a FABRIC
// primitive (stdlib-only value vocabulary), so it lives in the spec contract
// module (#55 step1) rather than a mechanism kit: the x/crypto SSH client in
// github.com/opencharly/spec/sshx reuses spec.SSHTarget, and sdk/vmshared keeps
// a `type SSHTarget = spec.SSHTarget` alias for its libvirt_uri.go use. Keeping
// this vocabulary pure keeps every non-ssh consumer free of the x/crypto/ssh
// dependency — only spec/sshx (the in-process SSH forwarder) links x/crypto.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// SSHTarget is the parsed form of an ssh-style target string.
// Examples:
//   - "host"                     → {User: $USER, Host: "host", Port: 22}
//   - "user@host"                → {User: "user", Host: "host", Port: 22}
//   - "user@host:2222"           → {User: "user", Host: "host", Port: 2222}
//   - "qemu+ssh://user@host/…"   → parsed via ParseLibvirtSSHURI (not here)
type SSHTarget struct {
	User string
	Host string
	Port int
}

// ParseSSHTarget accepts "[user@]host[:port]" and fills defaults:
//   - User: $USER
//   - Port: 22
func ParseSSHTarget(s string) (SSHTarget, error) {
	if s == "" {
		return SSHTarget{}, fmt.Errorf("empty ssh target")
	}
	t := SSHTarget{Port: 22}
	rest := s
	if user, after, ok := strings.Cut(rest, "@"); ok {
		t.User = user
		rest = after
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		t.Host = rest[:i]
		p, err := strconv.Atoi(rest[i+1:])
		if err != nil {
			return SSHTarget{}, fmt.Errorf("invalid port in %q: %w", s, err)
		}
		t.Port = p
	} else {
		t.Host = rest
	}
	if t.Host == "" {
		return SSHTarget{}, fmt.Errorf("missing host in ssh target %q", s)
	}
	if t.User == "" {
		t.User = CurrentUsername()
	}
	return t, nil
}

// String renders the canonical "user@host:port" form.
func (t SSHTarget) String() string {
	if t.Port == 22 {
		return fmt.Sprintf("%s@%s", t.User, t.Host)
	}
	return fmt.Sprintf("%s@%s:%d", t.User, t.Host, t.Port)
}

// CurrentUsername returns $USER or falls back to "charly" if neither
// $USER nor $LOGNAME is set.
func CurrentUsername() string {
	for _, env := range []string{"USER", "LOGNAME"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return "charly"
}
