// Package user is the importable, COMPILED-IN host-coupled `user` verb: a MULTI-ROLE
// state-provision verb. CHECK (kit.CheckVerbProvider): `getent passwd` via the live
// kit.CheckContext and compare uid/gid/home/shell. ACT (kit.ProvisionActor): render an
// idempotent `useradd`. Relocated out of charly's module (formerly
// charly/plugin/builtins/user + charly/plugin_user.go) onto the charly/plugin/kit
// contract; COMPILED-IN-ONLY. No matchers — direct field comparison.
package user

import (
	"context"
	"embed"
	"fmt"
	"strconv"
	"strings"

	"github.com/opencharly/charly/candy/plugin-user/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:user": "#UserInput"}

// NewCheckVerb returns the user verb as a kit.CheckVerbProvider for compiled-in
// registration. Because verb also implements kit.ProvisionActor, charly registers the
// multi-role (check + act) adapter.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "user" }

// RunVerb (do:assert) runs the getent-passwd probe via the live CheckContext and compares
// uid/gid/home/shell. Mirrors the former r.runUser.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.UserInput
	kit.DecodeInput(op.PluginInput, &in)
	probe := fmt.Sprintf(`getent passwd %s`, kit.ShellQuote(in.User))
	out, _, exit, err := cc.Exec().RunCapture(ctx, probe)
	if err != nil {
		return kit.Failf("probe: %v", err)
	}
	if exit != 0 {
		return kit.Fail("user not found")
	}
	// Fields: user:x:uid:gid:gecos:home:shell
	parts := strings.SplitN(strings.TrimSpace(out), ":", 7)
	if len(parts) < 7 {
		return kit.Failf("unexpected passwd line: %q", out)
	}
	uid, _ := strconv.Atoi(parts[2])
	gid, _ := strconv.Atoi(parts[3])
	home, shell := parts[5], parts[6]
	if in.UID != nil && uid != *in.UID {
		return kit.Failf("uid=%d, want %d", uid, *in.UID)
	}
	if in.GID != nil && gid != *in.GID {
		return kit.Failf("gid=%d, want %d", gid, *in.GID)
	}
	if in.Home != "" && home != in.Home {
		return kit.Failf("home=%s, want %s", home, in.Home)
	}
	if in.Shell != "" && shell != in.Shell {
		return kit.Failf("shell=%s, want %s", shell, in.Shell)
	}
	return kit.Passf("uid=%d gid=%d", uid, gid)
}

// RenderProvisionScript (do:act) renders an idempotent useradd. ok is always true — a
// user act always has a create form. distros are unused (account fields are
// distro-agnostic). Mirrors the former userVerb.RenderProvisionScript.
func (verb) RenderProvisionScript(op *spec.Op, _ []string) (string, bool) {
	var in params.UserInput
	kit.DecodeInput(op.PluginInput, &in)
	flags := ""
	if in.UID != nil {
		flags += fmt.Sprintf(" -u %d", *in.UID)
	}
	if in.Home != "" {
		flags += " -m -d " + kit.ShellQuote(in.Home)
	}
	if in.Shell != "" {
		flags += " -s " + kit.ShellQuote(in.Shell)
	}
	name := kit.ShellQuote(in.User)
	return fmt.Sprintf("id %[1]s >/dev/null 2>&1 || useradd%[2]s %[1]s", name, flags), true
}
