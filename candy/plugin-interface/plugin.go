// Package iface is the importable, COMPILED-IN host-coupled `interface` check verb:
// it probes a network interface's presence, MTU, and addresses via `ip` on the live
// deployment. It implements kit.CheckVerbProvider — RunVerb runs the probes via the
// live kit.CheckContext. Relocated out of charly's module (formerly
// charly/plugin/builtins/interface + charly/plugin_interface.go) onto the
// sdk/kit contract; COMPILED-IN-ONLY. (Package named iface, not interface —
// the latter is a Go keyword; the reserved verb word is still "interface".)
package iface

import (
	"context"
	"embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/opencharly/charly/candy/plugin-interface/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewCheckVerb returns the interface verb as a kit.CheckVerbProvider for compiled-in registration.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

// NewMeta advertises verb:interface (plugin_input #InterfaceInput) + the embedded CUE schema,
// via sdk.NewMeta — the ONE meta both placements use (compiled-in registerCompiledCheckVerb reads
// it via Describe; cmd/serve serves it out-of-process), so a kit candy has the SAME
// NewCheckVerb()+NewMeta() shape as every pb-provider plugin (R3).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.1700",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "interface", InputDef: "#InterfaceInput"}},
		schemaFS)
}

type verb struct{}

func (verb) Reserved() string { return "interface" }

// RunVerb probes the interface via `ip` on the live CheckContext. Mirrors the former
// r.runInterface exactly (presence, optional MTU, optional required addresses).
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.InterfaceInput
	kit.DecodeInput(op.PluginInput, &in)

	probe := fmt.Sprintf(`ip -o addr show %s 2>/dev/null`, kit.ShellQuote(in.Interface))
	out, _, exit, err := cc.Exec().RunCapture(ctx, probe)
	if err != nil {
		return kit.Failf("probe: %v", err)
	}
	if exit != 0 || strings.TrimSpace(out) == "" {
		return kit.Fail("interface not found")
	}
	// MTU check via `ip link show`.
	if in.MTU != nil {
		mtuOut, _, exit, err := cc.Exec().RunCapture(ctx,
			fmt.Sprintf(`ip -o link show %s 2>/dev/null | awk '{for(i=1;i<=NF;i++)if($i=="mtu"){print $(i+1);exit}}'`,
				kit.ShellQuote(in.Interface)))
		if err != nil || exit != 0 {
			return kit.Failf("mtu probe exit %d err %v", exit, err)
		}
		got, _ := strconv.Atoi(strings.TrimSpace(mtuOut))
		if got != *in.MTU {
			return kit.Failf("mtu=%d, want %d", got, *in.MTU)
		}
	}
	for _, want := range in.Addrs {
		if !strings.Contains(out, want) {
			return kit.Failf("missing address %s", want)
		}
	}
	return kit.Pass("ok")
}
