// Package file is the importable, COMPILED-IN host-coupled `file` verb: a MULTI-ROLE
// state-provision verb. CHECK (kit.CheckVerbProvider): stat the path via the live
// kit.CheckContext and assert exists/mode/owner/group/filetype/contains/sha256. ACT
// (kit.ProvisionActor): render an idempotent RUNTIME file-creation
// (mkdir/touch/cat-heredoc + chmod). Relocated out of charly's module (formerly
// charly/plugin/builtins/file + charly/plugin_verb_file.go) onto the sdk/kit
// contract; COMPILED-IN-ONLY. The matcher evaluation reuses sdk.MatchAll + spec.Matcher.
package file

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opencharly/charly/candy/plugin-file/params"
	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/kit"
	pb "github.com/opencharly/sdk/proto"
	"github.com/opencharly/sdk/spec"
)

//go:embed schema/*.cue
var schemaFS embed.FS

// NewCheckVerb returns the file verb as a kit.CheckVerbProvider for compiled-in
// registration. Because verb also implements kit.ProvisionActor, charly registers the
// multi-role (check + act) adapter.
func NewCheckVerb() kit.CheckVerbProvider { return verb{} }

// NewMeta advertises verb:file (plugin_input #FileInput) + the embedded CUE schema, via
// sdk.NewMeta — the ONE meta both placements use (compiled-in registerCompiledCheckVerb reads
// it via Describe; cmd/serve serves it out-of-process), so a kit candy has the SAME
// NewCheckVerb()+NewMeta() shape as every pb-provider plugin (R3).
func NewMeta() pb.PluginMetaServer {
	return sdk.NewMeta("2026.176.2700",
		[]sdk.ProvidedCapability{{Class: "verb", Word: "file", InputDef: "#FileInput", Primary: "file"}},
		schemaFS)
}

type verb struct{}

func (verb) Reserved() string { return "file" }

// fileCheck carries the decoded plugin_input.
type fileCheck struct {
	Path     string
	Exists   *bool
	Mode     string
	Owner    string
	GroupOf  string
	Filetype string
	Contains spec.MatcherList
	Sha256   string
}

// RunVerb (do:assert) runs the stat probe via the live CheckContext and asserts the
// file's attributes. Mirrors the former r.runFile.
func (verb) RunVerb(ctx context.Context, cc kit.CheckContext, op *spec.Op) kit.Result {
	var in params.FileInput
	kit.DecodeInput(op.PluginInput, &in)
	f := fileCheck{
		Path:     in.File,
		Exists:   in.Exists,
		Mode:     in.Mode,
		Owner:    in.Owner,
		GroupOf:  in.GroupOf,
		Filetype: in.Filetype,
		Contains: decodeContainsList(in.Contains),
		Sha256:   in.Sha256,
	}
	path := f.Path
	// Probe: exists=1|<type>|<mode>|<user>|<group>  OR  exists=0||||
	probe := fmt.Sprintf(
		`if [ -e %[1]s ] || [ -L %[1]s ]; then
  printf "exists=1|"
  stat -c "%%F|%%a|%%U|%%G" %[1]s
else
  printf "exists=0|||||\n"
fi`, kit.ShellQuote(path))
	stdout, stderr, exit, err := cc.Exec().RunCapture(ctx, probe)
	if err != nil {
		return kit.Failf("probe failed: %v (stderr: %s)", err, stderr)
	}
	if exit != 0 {
		return kit.Failf("probe exit %d (stderr: %s)", exit, stderr)
	}
	line := strings.TrimSpace(stdout)
	parts := strings.SplitN(line, "|", 5)
	if len(parts) < 5 {
		return kit.Failf("unexpected probe output: %q", line)
	}
	exists := strings.TrimPrefix(parts[0], "exists=") == "1"
	typeStr, mode, owner, group := parts[1], parts[2], parts[3], parts[4]

	wantExists := true
	if f.Exists != nil {
		wantExists = *f.Exists
	}
	if wantExists != exists {
		return kit.Failf("exists=%v, want %v", exists, wantExists)
	}
	if !exists {
		return kit.Pass("file absent (as expected)")
	}
	if f.Mode != "" && strings.TrimLeft(mode, "0") != strings.TrimLeft(f.Mode, "0") {
		return kit.Failf("mode=%s, want %s", mode, f.Mode)
	}
	if f.Owner != "" && owner != f.Owner {
		return kit.Failf("owner=%s, want %s", owner, f.Owner)
	}
	if f.GroupOf != "" && group != f.GroupOf {
		return kit.Failf("group=%s, want %s", group, f.GroupOf)
	}
	if f.Filetype != "" {
		if ft := normalizeFiletype(typeStr); ft != f.Filetype {
			return kit.Failf("filetype=%s, want %s", ft, f.Filetype)
		}
	}
	if len(f.Contains) > 0 {
		contents, err := readFile(ctx, cc, path)
		if err != nil {
			return kit.Failf("read for contains: %v", err)
		}
		if err := sdk.MatchAll(contents, f.Contains); err != nil {
			return kit.Failf("contains: %v", err)
		}
	}
	if f.Sha256 != "" {
		out, _, exit, err := cc.Exec().RunCapture(ctx, fmt.Sprintf("sha256sum %s", kit.ShellQuote(path)))
		if err != nil || exit != 0 {
			return kit.Failf("sha256 probe exit %d err %v", exit, err)
		}
		sum := strings.Fields(strings.TrimSpace(out))
		if len(sum) == 0 || sum[0] != f.Sha256 {
			return kit.Failf("sha256=%s, want %s", sum, f.Sha256)
		}
	}
	return kit.Pass("ok")
}

// RenderProvisionScript (do:act) renders the idempotent RUNTIME file-creation:
// `mkdir -p $(dirname) && cat > FILE <<EOF …` (with content) or `… && touch FILE`, then
// optionally `chmod MODE`. `content` is read off the step Op (a SHARED #Op modifier the
// write verb also reads). Mirrors the former fileVerb.RenderProvisionScript.
func (verb) RenderProvisionScript(op *spec.Op, _ []string) (string, bool) {
	var in params.FileInput
	kit.DecodeInput(op.PluginInput, &in)
	path := kit.ShellQuote(in.File)
	var b strings.Builder
	if op.Content != "" {
		fmt.Fprintf(&b, "mkdir -p \"$(dirname %s)\" && cat > %s <<'CHARLY_ACT_EOF'\n%s\nCHARLY_ACT_EOF", path, path, op.Content)
	} else {
		fmt.Fprintf(&b, "mkdir -p \"$(dirname %s)\" && touch %s", path, path)
	}
	if in.Mode != "" {
		fmt.Fprintf(&b, " && chmod %s %s", kit.ShellQuote(in.Mode), path)
	}
	return b.String(), true
}

// readFile cats a file's contents via the live CheckContext.
func readFile(ctx context.Context, cc kit.CheckContext, path string) (string, error) {
	out, stderr, exit, err := cc.Exec().RunCapture(ctx, "cat "+kit.ShellQuote(path))
	if err != nil {
		return "", err
	}
	if exit != 0 {
		return "", fmt.Errorf("cat exit %d: %s", exit, stderr)
	}
	return out, nil
}

// normalizeFiletype maps `stat -c %F` strings to the verb's filetype vocabulary.
func normalizeFiletype(s string) string {
	switch {
	case strings.Contains(s, "regular"):
		return "file"
	case strings.Contains(s, "directory"):
		return "directory"
	case strings.Contains(s, "symbolic link"), strings.Contains(s, "symlink"):
		return "symlink"
	case strings.Contains(s, "character"):
		return "character"
	case strings.Contains(s, "block"):
		return "block"
	case strings.Contains(s, "fifo"):
		return "fifo"
	case strings.Contains(s, "socket"):
		return "socket"
	}
	return s
}

// decodeContainsList re-decodes a gengotypes-degraded `contains` value (`any`) into a
// MatcherList with the goss contains-default: a BARE scalar element defaults to the
// `contains` operator (substring match), while a single-operator map ({equals: …},
// {matches: …}, …) keeps its explicit operator. A nil value yields nil.
func decodeContainsList(v any) spec.MatcherList {
	if v == nil {
		return nil
	}
	elems, ok := v.([]any)
	if !ok {
		elems = []any{v}
	}
	var ml spec.MatcherList
	for _, e := range elems {
		if m, isMap := e.(map[string]any); isMap && len(m) == 1 {
			raw, err := json.Marshal(m)
			if err != nil {
				continue
			}
			var matcher spec.Matcher
			if err := matcher.UnmarshalJSON(raw); err != nil {
				continue
			}
			ml = append(ml, matcher)
			continue
		}
		ml = append(ml, spec.Matcher{Op: "contains", Value: e})
	}
	return ml
}
