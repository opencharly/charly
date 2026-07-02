// Package dns is the importable, COMPILED-IN host-coupled `dns` check verb: a name
// resolution probe — in-container `getent hosts` under charly check box, a host-side
// net.LookupIP under charly check live (with optional required-address matching). It
// implements kit.CheckVerbProvider — RunVerb runs the probe via the live
// kit.CheckContext. Relocated out of charly's module (formerly
// charly/plugin/builtins/dns + charly/plugin_dns.go) onto the charly/plugin/kit
// contract; COMPILED-IN-ONLY.
package dns

import (
	"context"
	"embed"
	"fmt"
	"net"

	"github.com/opencharly/charly/candy/plugin-dns/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:dns": "#DnsInput"}

// NewCheckVerb returns the dns verb as a kit.CheckVerbProvider for compiled-in registration.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "dns" }

// RunVerb runs the resolution probe via the live CheckContext. Mirrors the former
// r.runDNS exactly (in-container getent under ModeBox, host-side net.LookupIP under
// ModeLive with optional required-address matching).
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.DnsInput
	kit.DecodeInput(op.PluginInput, &in)

	wantResolvable := true
	if in.Resolvable != nil {
		wantResolvable = *in.Resolvable
	}
	if cc.Mode() == kit.ModeBox {
		probe := fmt.Sprintf(`getent hosts %s >/dev/null 2>&1`, kit.ShellQuote(in.DNS))
		_, _, exit, err := cc.Exec().RunCapture(ctx, probe)
		if err != nil {
			return kit.Failf("probe: %v", err)
		}
		isResolvable := exit == 0
		if isResolvable != wantResolvable {
			return kit.Failf("resolvable=%v, want %v", isResolvable, wantResolvable)
		}
		return kit.Passf("resolvable=%v", isResolvable)
	}
	// Host-side resolve.
	ips, err := net.LookupIP(in.DNS)
	isResolvable := err == nil && len(ips) > 0
	if isResolvable != wantResolvable {
		return kit.Failf("resolvable=%v (err: %v), want %v", isResolvable, err, wantResolvable)
	}
	if len(in.Addrs) > 0 && isResolvable {
		want := map[string]bool{}
		for _, a := range in.Addrs {
			want[a] = true
		}
		for _, ip := range ips {
			if want[ip.String()] {
				return kit.Passf("resolved to %s (match)", ip)
			}
		}
		return kit.Failf("no resolved address matched required list %v (got %v)", in.Addrs, ips)
	}
	return kit.Passf("resolvable=%v", isResolvable)
}
