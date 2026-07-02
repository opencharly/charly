// Package addr is the importable, COMPILED-IN host-coupled `addr` check verb: an
// outside-in TCP reachability probe of a host:port — in-container `nc` under
// charly check box, a host-side dial under charly check live. It implements
// kit.CheckVerbProvider — RunVerb runs the probe via the live kit.CheckContext.
// Relocated out of charly's module (formerly charly/plugin/builtins/addr +
// charly/plugin_addr.go) onto the charly/plugin/kit contract; COMPILED-IN-ONLY.
package addr

import (
	"context"
	"embed"
	"fmt"
	"net"

	"github.com/opencharly/charly/candy/plugin-addr/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:addr": "#AddrInput"}

// NewCheckVerb returns the addr verb as a kit.CheckVerbProvider for compiled-in registration.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "addr" }

// RunVerb runs the reachability probe via the live CheckContext. Mirrors the former
// r.runAddr exactly (in-container nc under ModeBox, host-side dial under ModeLive).
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.AddrInput
	kit.DecodeInput(op.PluginInput, &in)

	wantReachable := true
	if in.Reachable != nil {
		wantReachable = *in.Reachable
	}
	if cc.Mode() == kit.ModeBox {
		host, port := splitHostPort(in.Addr)
		probe := fmt.Sprintf(`nc -z -w %d %s %s 2>/dev/null`, 3, kit.ShellQuote(host), kit.ShellQuote(port))
		_, _, exit, err := cc.Exec().RunCapture(ctx, probe)
		if err != nil {
			return kit.Failf("probe: %v", err)
		}
		reachable := exit == 0
		if reachable != wantReachable {
			return kit.Failf("reachable=%v, want %v", reachable, wantReachable)
		}
		return kit.Passf("reachable=%v", reachable)
	}
	conn, err := net.DialTimeout("tcp", in.Addr, cc.DialTimeout())
	reachable := err == nil
	if reachable {
		_ = conn.Close()
	}
	if reachable != wantReachable {
		return kit.Failf("reachable=%v (err: %v), want %v", reachable, err, wantReachable)
	}
	return kit.Passf("reachable=%v", reachable)
}

func splitHostPort(s string) (string, string) {
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, p
	}
	return s, ""
}
