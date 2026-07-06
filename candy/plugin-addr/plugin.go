// Package addr is the importable, COMPILED-IN host-coupled `addr` check verb: an
// outside-in TCP reachability probe of a host:port — in-container `nc` under
// charly check box, a host-side dial under charly check live. It implements
// kit.CheckVerbProvider — RunVerb runs the probe via the live kit.CheckContext.
// Relocated out of charly's module (formerly charly/plugin/builtins/addr +
// charly/plugin_addr.go) onto the sdk/kit contract; COMPILED-IN-ONLY.
package addr

import (
	"context"
	"embed"
	"fmt"
	"net"

	"github.com/opencharly/charly/candy/plugin-addr/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewCheckVerb returns the addr verb as a kit.CheckVerbProvider for compiled-in registration.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

// NewMeta advertises verb:addr (plugin_input #AddrInput) + the embedded CUE schema, via
// sdk.NewMeta — the ONE meta both placements use (compiled-in registerCompiledCheckVerb reads
// it via Describe; cmd/serve serves it out-of-process), so a kit candy has the SAME
// NewCheckVerb()+NewMeta() shape as every pb-provider plugin (R3).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.1800",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "addr", InputDef: "#AddrInput", Primary: "addr"}},
		schemaFS)
}

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
