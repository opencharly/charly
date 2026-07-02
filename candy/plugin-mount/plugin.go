// Package mount is the importable, COMPILED-IN host-coupled `mount` verb: a MULTI-ROLE
// state-provision verb. CHECK (kit.CheckVerbProvider): `findmnt` the mountpoint via the
// live kit.CheckContext and match source/filesystem/opts. ACT (kit.ProvisionActor):
// render an idempotent `findmnt || mount`. Relocated out of charly's module (formerly
// charly/plugin/builtins/mount + charly/plugin_mount.go) onto the charly/plugin/kit
// contract; COMPILED-IN-ONLY. The matcher evaluation reuses sdk.MatchAll + spec.MatcherList.
package mount

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/charly/candy/plugin-mount/params"
	"github.com/opencharly/charly/charly/plugin/kit"
	"github.com/opencharly/charly/charly/plugin/sdk"
	"github.com/opencharly/charly/charly/spec"
)

//go:embed schema/*.cue
var SchemaFS embed.FS

// SchemaDir is the embedded schema directory; charly concatenates SchemaFS/SchemaDir.
const SchemaDir = "schema"

// InputDefs maps the provided capability to its CUE def for plugin_input validation.
var InputDefs = map[string]string{"verb:mount": "#MountInput"}

// NewCheckVerb returns the mount verb as a kit.CheckVerbProvider for compiled-in
// registration. Because verb also implements kit.ProvisionActor, charly registers the
// multi-role (check + act) adapter.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

type verb struct{}

func (verb) Reserved() string { return "mount" }

// RunVerb (do:assert) runs the findmnt probe via the live CheckContext and matches
// source/filesystem/opts. Mirrors the former r.runMount.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.MountInput
	kit.DecodeInput(op.PluginInput, &in)
	mp := kit.ShellQuote(in.Mount)
	probe := fmt.Sprintf(`findmnt -n -o SOURCE,FSTYPE,OPTIONS %s 2>/dev/null`, mp)
	out, _, exit, err := cc.Exec().RunCapture(ctx, probe)
	if err != nil {
		return kit.Failf("probe: %v", err)
	}
	if exit != 0 {
		return kit.Fail("mount not found")
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 3 {
		return kit.Failf("unexpected findmnt output: %q", out)
	}
	source, fstype, opts := fields[0], fields[1], fields[2]
	if in.MountSource != "" && source != in.MountSource {
		return kit.Failf("source=%s, want %s", source, in.MountSource)
	}
	if in.Filesystem != "" && fstype != in.Filesystem {
		return kit.Failf("filesystem=%s, want %s", fstype, in.Filesystem)
	}
	wantOpts := decodeMatcherList(in.Opts)
	if len(wantOpts) > 0 {
		if err := sdk.MatchAll(opts, wantOpts); err != nil {
			return kit.Failf("opts %q: %v", opts, err)
		}
	}
	return kit.Passf("source=%s fstype=%s", source, fstype)
}

// RenderProvisionScript (do:act) renders an idempotent mount. ok=false when no
// mount_source is given (nothing to mount). Mirrors the former mountVerb.RenderProvisionScript.
func (verb) RenderProvisionScript(op *spec.Op, _ []string) (string, bool) {
	var in params.MountInput
	kit.DecodeInput(op.PluginInput, &in)
	var args []string
	if in.Filesystem != "" {
		args = append(args, "-t "+kit.ShellQuote(in.Filesystem))
	}
	if v, ok := firstMatcherScalar(decodeMatcherList(in.Opts)); ok && v != "" {
		args = append(args, "-o "+kit.ShellQuote(v))
	}
	if in.MountSource == "" {
		return "", false
	}
	return fmt.Sprintf("findmnt %[1]s >/dev/null 2>&1 || mount %[2]s %[3]s %[1]s",
		kit.ShellQuote(in.Mount), strings.Join(args, " "), kit.ShellQuote(in.MountSource)), true
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
