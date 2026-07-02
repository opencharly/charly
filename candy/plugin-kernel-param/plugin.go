// Package kernelparam is the importable, COMPILED-IN host-coupled `kernel-param` verb: a
// MULTI-ROLE state-provision verb. CHECK (kit.CheckVerbProvider): read
// /proc/sys/<key-as-slashes> via the live kit.CheckContext and match the value.
// ACT (kit.ProvisionActor): render `sysctl -w key=value`. Relocated out of charly's
// module (formerly charly/plugin/builtins/kernel_param + charly/plugin_kernel_param.go)
// onto the charly/plugin/kit contract; COMPILED-IN-ONLY. The verb word stays
// `kernel-param`; the Go package is `kernelparam` (a hyphen is not a legal package name).
// The matcher evaluation reuses the importable sdk.MatchAll + spec.MatcherList (R3).
package kernelparam

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/charly/candy/plugin-kernel-param/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:kernel-param": "#KernelParamInput"}

// NewCheckVerb returns the kernel-param verb as a kit.CheckVerbProvider for compiled-in
// registration. Because verb also implements kit.ProvisionActor, charly registers the
// multi-role (check + act) adapter.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "kernel-param" }

// RunVerb (do:assert) reads /proc/sys/<key> via the live CheckContext and matches the
// value. The CHECK reads /proc/sys directly (equivalent to `sysctl -n` but needing no
// procps-ng, which minimal images omit). Mirrors the former r.runKernelParam.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.KernelParamInput
	kit.DecodeInput(op.PluginInput, &in)
	path := "/proc/sys/" + strings.ReplaceAll(in.KernelParam, ".", "/")
	probe := fmt.Sprintf(`cat %s 2>/dev/null`, kit.ShellQuote(path))
	out, _, exit, err := cc.Exec().RunCapture(ctx, probe)
	if err != nil {
		return kit.Failf("probe: %v", err)
	}
	if exit != 0 {
		return kit.Failf("kernel param not readable (exit %d)", exit)
	}
	value := strings.TrimSpace(out)
	want := decodeMatcherList(in.Value)
	if len(want) == 0 {
		return kit.Passf("value=%s", value)
	}
	if err := sdk.MatchAll(value, want); err != nil {
		return kit.Failf("value=%s: %v", value, err)
	}
	return kit.Passf("value=%s", value)
}

// RenderProvisionScript (do:act) renders `sysctl -w key=value` (the act runs where
// procps-ng is present). ok=false when no desired value is given (a sysctl write with no
// value is meaningless). Mirrors the former kernelParamVerb.RenderProvisionScript.
func (verb) RenderProvisionScript(op *spec.Op, _ []string) (string, bool) {
	var in params.KernelParamInput
	kit.DecodeInput(op.PluginInput, &in)
	if v, ok := firstMatcherScalar(decodeMatcherList(in.Value)); ok {
		return fmt.Sprintf("sysctl -w %s=%s", kit.ShellQuote(in.KernelParam), kit.ShellQuote(v)), true
	}
	return "", false
}

// decodeMatcherList re-decodes a gengotypes-degraded matcher value (`any`) through the
// shared spec.MatcherList JSON codec. A nil / unparseable value yields a nil list.
func decodeMatcherList(v any) spec.MatcherList {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var ml spec.MatcherList
	if err := json.Unmarshal(raw, &ml); err != nil {
		return nil
	}
	return ml
}

// firstMatcherScalar returns the first non-nil matcher's scalar value as a string.
func firstMatcherScalar(ml spec.MatcherList) (string, bool) {
	for _, m := range ml {
		if m.Value == nil {
			continue
		}
		return fmt.Sprintf("%v", m.Value), true
	}
	return "", false
}
