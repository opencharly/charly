// Package unixgroup is the importable, COMPILED-IN host-coupled `unix_group` verb: a
// MULTI-ROLE state-provision verb. CHECK (kit.CheckVerbProvider): `getent group` via the
// live kit.CheckContext and compare gid. ACT (kit.ProvisionActor): render an idempotent
// `groupadd`. Relocated out of charly's module (formerly
// charly/plugin/builtins/unix_group + charly/plugin_unix_group.go) onto the
// charly/plugin/kit contract; COMPILED-IN-ONLY. The verb word stays `unix_group`.
package unixgroup

import (
	"context"
	"embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/opencharly/charly/candy/plugin-unix-group/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:unix_group": "#UnixGroupInput"}

// NewCheckVerb returns the unix_group verb as a kit.CheckVerbProvider for compiled-in
// registration. Because verb also implements kit.ProvisionActor, charly registers the
// multi-role (check + act) adapter.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "unix_group" }

// RunVerb (do:assert) runs the getent-group probe via the live CheckContext and compares
// gid. Mirrors the former r.runUnixGroup.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.UnixGroupInput
	kit.DecodeInput(op.PluginInput, &in)
	probe := fmt.Sprintf(`getent group %s`, kit.ShellQuote(in.UnixGroup))
	out, _, exit, err := cc.Exec().RunCapture(ctx, probe)
	if err != nil {
		return kit.Failf("probe: %v", err)
	}
	if exit != 0 {
		return kit.Fail("group not found")
	}
	// Fields: group:x:gid:members
	parts := strings.SplitN(strings.TrimSpace(out), ":", 4)
	if len(parts) < 4 {
		return kit.Failf("unexpected group line: %q", out)
	}
	gid, _ := strconv.Atoi(parts[2])
	if in.GID != nil && gid != *in.GID {
		return kit.Failf("gid=%d, want %d", gid, *in.GID)
	}
	return kit.Passf("gid=%d", gid)
}

// RenderProvisionScript (do:act) renders an idempotent groupadd. ok is always true — a
// unix_group act always has a create form. Mirrors the former unixGroupVerb.RenderProvisionScript.
func (verb) RenderProvisionScript(op *spec.Op, _ []string) (string, bool) {
	var in params.UnixGroupInput
	kit.DecodeInput(op.PluginInput, &in)
	flags := ""
	if in.GID != nil {
		flags += fmt.Sprintf(" -g %d", *in.GID)
	}
	name := kit.ShellQuote(in.UnixGroup)
	return fmt.Sprintf("getent group %[1]s >/dev/null 2>&1 || groupadd%[2]s %[1]s", name, flags), true
}
