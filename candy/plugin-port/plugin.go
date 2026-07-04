// Package port is the importable host-coupled `port` check verb: the in-container
// listening probe (ss/netstat) or a host-side TCP dial for reachability. It implements
// kit.CheckVerbProvider — RunVerb runs against the live kit.CheckContext (the engine's
// executor, run mode, and dial timeout) and runs in EITHER placement (compiled-in OR
// out-of-process via the CheckContextService reverse channel, F2 + cmd/serve) with ZERO
// authoring change. Relocated out of charly's module (formerly charly/plugin/builtins/port
// + charly/plugin_port.go) onto the sdk/kit contract.
package port

import (
	"context"
	"embed"
	"fmt"
	"net"

	"github.com/opencharly/charly/candy/plugin-port/params"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir
// (it cannot be done here — internal/schemaconcat is not importable from a candy).
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:port": "#PortInput"}

// NewCheckVerb returns the port verb as a kit.CheckVerbProvider for compiled-in
// registration (charly's registerCompiledCheckVerb wraps it + registers the schema).
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "port" }

// RunVerb runs the listening/reachability probe via the live CheckContext. The port
// number + modifiers come from plugin_input (params.PortInput, generated from
// schema/port.cue). Mirrors the former r.runPort exactly.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.PortInput
	kit.DecodeInput(op.PluginInput, &in)

	wantListening := true
	if in.Listening != nil {
		wantListening = *in.Listening
	}

	// Outside-in reachability: dial from host when reachable is set, or when the
	// caller explicitly asked for listening:false.
	if in.Reachable != nil || (in.Listening != nil && !*in.Listening) {
		if cc.Mode() == kit.ModeBox {
			return kit.Skip("host-side port check not meaningful under charly check box")
		}
		return dialPort(cc, in.Port, in.IP, in.Reachable)
	}

	// In-container listening probe: ss first, netstat fallback.
	probe := fmt.Sprintf(
		`(ss -tlnH 2>/dev/null || netstat -tln 2>/dev/null) | awk '{print $4}' | grep -E ':%d$' >/dev/null`,
		in.Port)
	_, stderr, exit, err := cc.Exec().RunCapture(ctx, probe)
	if err != nil {
		return kit.Failf("probe failed: %v (%s)", err, stderr)
	}
	isListening := exit == 0
	if isListening != wantListening {
		return kit.Failf("listening=%v, want %v (on port %d)", isListening, wantListening, in.Port)
	}
	return kit.Passf("port %d listening=%v", in.Port, isListening)
}

func dialPort(cc kit.CheckContext, port int, ip string, reachable *bool) kit.Result {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	if ip != "" {
		addr = fmt.Sprintf("%s:%d", ip, port)
	}
	conn, err := net.DialTimeout("tcp", addr, cc.DialTimeout())
	wantReachable := true
	if reachable != nil {
		wantReachable = *reachable
	}
	if err != nil {
		if !wantReachable {
			return kit.Passf("%s unreachable (as expected)", addr)
		}
		return kit.Failf("dial %s: %v", addr, err)
	}
	_ = conn.Close()
	if !wantReachable {
		return kit.Failf("%s reachable but wanted unreachable", addr)
	}
	return kit.Passf("%s reachable", addr)
}
