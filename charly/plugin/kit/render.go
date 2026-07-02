package kit

// render.go — the PURE op→shell render helpers, moved here (Part 2) so the ONE copy
// serves BOTH charly's in-proc deploy targets (package main re-exports via kit_aliases.go)
// AND an OUT-OF-PROCESS deploy/step plugin's WalkPlans. They operate on spec.Op + plain
// values only (no executor, no os.*, no provider registry) — the act-`plugin:` verb case
// (which needs the in-proc ProvisionActor registry) stays in package main / routes through
// RunHostStep, so these functions are fully self-contained and importable by a leaf module.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/opencharly/charly/charly/spec"
)

// ShQuoteArg single-quotes an argument for POSIX shell embedding.
func ShQuoteArg(v string) string {
	if v == "" {
		return `''`
	}
	if !strings.ContainsAny(v, " \t\n\"'$*?[](){}<>|&;`\\!") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// ShDoubleQuote wraps a string in double quotes for a shell context where variable
// expansion MUST still happen (e.g. download URLs that template ${BUILD_ARCH}). Escapes
// the metachars that break out of a double-quoted string, but deliberately does NOT escape
// `$` so authored ${FOO} / $FOO still expand.
func ShDoubleQuote(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "`", "\\`")
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

// ShQuoteEnv single-quotes a value for POSIX sh (env.d export values). Inside single
// quotes nothing needs escaping except the single quote itself.
func ShQuoteEnv(v string) string {
	if v == "" {
		return `''`
	}
	if !strings.ContainsAny(v, " \t\n\"'$*?[](){}<>|&;`\\!") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// ShDoubleQuotePath escapes a PATH-list value for use INSIDE double quotes, leaving `$`
// unescaped so `$PATH` expands at sourcing time.
func ShDoubleQuotePath(v string) string {
	r := strings.NewReplacer(`\`, `\\`, "`", "\\`", `"`, `\"`)
	return r.Replace(v)
}

// BuildArchExports emits the BUILD_ARCH=$(uname -m) + ARCH=<buildkit-triplet> shell
// preamble so a cmd:/download: body can template ${ARCH}/${BUILD_ARCH} at deploy-time the
// same way the container build gets them from BuildKit's TARGETARCH.
func BuildArchExports() string {
	return "BUILD_ARCH=$(uname -m)\n" +
		"case \"$BUILD_ARCH\" in\n" +
		"  x86_64) ARCH=amd64 ;;\n" +
		"  aarch64) ARCH=arm64 ;;\n" +
		"  armv7l|armv7|armhf) ARCH=arm ;;\n" +
		"  *) ARCH=$BUILD_ARCH ;;\n" +
		"esac\n" +
		"export BUILD_ARCH ARCH\n"
}

// ParseTaskMode parses a candy task mode string ("0644","0o755") into a uint32 file mode,
// falling back to def when empty/unparseable.
func ParseTaskMode(mode string, def uint32) uint32 {
	if mode == "" {
		return def
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(mode, "0o"), 8, 32)
	if err != nil {
		return def
	}
	return uint32(v)
}

// TaskShellPreamble returns the BUILD_ARCH/ARCH exports plus any candy vars + op env
// (sorted for deterministic output) so cmd: bodies can reference ${ARCH} / ${MY_CANDY_VAR}
// at deploy-time the same way they do at build-time.
func TaskShellPreamble(candyVars, opEnv map[string]string) string {
	var b strings.Builder
	b.WriteString(BuildArchExports())
	writeSortedExports(&b, candyVars)
	writeSortedExports(&b, opEnv)
	return b.String()
}

func writeSortedExports(b *strings.Builder, m map[string]string) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "export %s=%s\n", k, ShQuoteArg(m[k]))
	}
}

// renderTaskCommandBody renders a command body: prepend the BUILD_ARCH/ARCH + candy.vars
// exports (so ${ARCH}/${MY_CANDY_VAR} resolve at deploy time) and rewrite /ctx/ to the
// staged candy dir.
func renderTaskCommandBody(command, ctxPath string, candyVars, opEnv map[string]string) string {
	body := command
	if ctxPath != "" {
		body = strings.ReplaceAll(body, "/ctx/", ctxPath+"/")
	}
	if preamble := TaskShellPreamble(candyVars, opEnv); preamble != "" {
		body = preamble + body
	}
	return body
}

// RenderOpCommand turns an op into a shell command suitable for sudo/user execution. It
// handles every plugin-renderable verb EXCEPT copy (staged via the executor's PutFile) and
// the act-`plugin:` verb (whose ProvisionActor shell needs the in-proc registry — package
// main / RunHostStep render those). Returns (cmd, handled): handled=false means the op is a
// copy or an act-`plugin:` verb the caller must route elsewhere; handled=true with an empty
// cmd never occurs (every handled verb yields a body).
func RenderOpCommand(op *spec.Op, ctxPath string, candyVars map[string]string) (string, bool) {
	if op == nil {
		return "", false
	}
	var opEnv map[string]string
	if op.Env != nil {
		opEnv = op.Env
	}
	switch {
	case op.Command != "":
		return renderTaskCommandBody(op.Command, ctxPath, candyVars, opEnv), true
	case op.Plugin == "command":
		// `plugin: command` — the command string rides plugin_input.command.
		cmdStr, _ := op.PluginInput["command"].(string)
		return renderTaskCommandBody(cmdStr, ctxPath, candyVars, opEnv), true
	case op.Mkdir != "":
		mode := op.Mode
		if mode == "" {
			mode = "0755"
		}
		return fmt.Sprintf("install -d -m%s %s", mode, ShDoubleQuote(op.Mkdir)), true
	case op.Link != "":
		target := op.Target
		if target == "" {
			target = op.To
		}
		return fmt.Sprintf("ln -sfn %s %s", ShDoubleQuote(target), ShDoubleQuote(op.Link)), true
	case op.Setcap != "":
		return fmt.Sprintf("setcap %s %s", ShDoubleQuote(op.Caps), ShDoubleQuote(op.Setcap)), true
	case op.Copy != "":
		// Staged via the executor's PutFile (portable across local + SSH). Not rendered.
		return "", false
	case op.Write != "":
		mode := op.Mode
		if mode == "" {
			mode = "0644"
		}
		return fmt.Sprintf("install -m%s /dev/stdin %s <<'CHARLY_WRITE'\n%s\nCHARLY_WRITE",
			mode, ShDoubleQuote(op.Write), op.Content), true
	case op.Download != "":
		return RenderDownloadScript(op, candyVars), true
	case op.Plugin != "":
		// An act-`plugin:` verb (a builtin ProvisionActor) — renders via the in-proc
		// registry (package main) or RunHostStep, not here.
		return "", false
	}
	return "", false
}

// RenderDownloadScript emits a shell snippet that fetches op.Download to a temp file,
// optionally extracts it into op.To, then cleans up — honoring the same flags the container
// build path respects (extract format, strip_components, include, mode, env). candyVars are
// exported alongside op.Env so a vars: key referenced in the URL resolves at deploy time.
func RenderDownloadScript(op *spec.Op, candyVars map[string]string) string {
	url := op.Download
	to := op.To
	extract := op.Extract
	if extract == "" {
		switch {
		case strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz"):
			extract = "tar.gz"
		case strings.HasSuffix(url, ".tar.xz"):
			extract = "tar.xz"
		case strings.HasSuffix(url, ".tar.zst"):
			extract = "tar.zst"
		case strings.HasSuffix(url, ".zip"):
			extract = "zip"
		case strings.HasSuffix(url, ".sh"):
			extract = "sh"
		default:
			extract = "none"
		}
	}

	var envPrefix strings.Builder
	writeSortedExports(&envPrefix, candyVars)
	writeSortedExports(&envPrefix, op.Env)

	var b strings.Builder
	b.WriteString("set -e\n")
	b.WriteString(BuildArchExports())
	b.WriteString(envPrefix.String())
	b.WriteString("ovtmp=\"$(mktemp -d)\"\n")
	b.WriteString("trap 'rm -rf \"$ovtmp\"' EXIT\n")

	quotedURL := ShDoubleQuote(url)

	if extract == "none" {
		mode := op.Mode
		if mode == "" {
			mode = "0755"
		}
		fmt.Fprintf(&b, "install -d -m0755 %s\n", ShQuoteArg(filepath.Dir(to)))
		fmt.Fprintf(&b, "curl -fL --retry 3 -o %s %s\n", ShQuoteArg(to), quotedURL)
		fmt.Fprintf(&b, "chmod %s %s\n", mode, ShQuoteArg(to))
		return b.String()
	}

	fmt.Fprintf(&b, "curl -fL --retry 3 -o \"$ovtmp/archive\" %s\n", quotedURL)
	fmt.Fprintf(&b, "install -d -m0755 %s\n", ShQuoteArg(to))

	strip := ""
	if op.StripComponents > 0 {
		strip = fmt.Sprintf(" --strip-components=%d", op.StripComponents)
	}
	includeFilter := ""
	if len(op.ExtractInclude) > 0 {
		quoted := make([]string, 0, len(op.ExtractInclude))
		for _, p := range op.ExtractInclude {
			quoted = append(quoted, ShQuoteArg(p))
		}
		includeFilter = " " + strings.Join(quoted, " ")
	}

	switch extract {
	case "tar.gz":
		fmt.Fprintf(&b, "tar -xzf \"$ovtmp/archive\" -C %s%s%s\n", ShQuoteArg(to), strip, includeFilter)
	case "tar.xz":
		fmt.Fprintf(&b, "tar -xJf \"$ovtmp/archive\" -C %s%s%s\n", ShQuoteArg(to), strip, includeFilter)
	case "tar.zst":
		fmt.Fprintf(&b, "tar --zstd -xf \"$ovtmp/archive\" -C %s%s%s\n", ShQuoteArg(to), strip, includeFilter)
	case "zip":
		if op.StripComponents > 0 {
			fmt.Fprintf(&b, "unzip -q \"$ovtmp/archive\" -d \"$ovtmp/unpack\"\n")
			fmt.Fprintf(&b, "(cd \"$ovtmp/unpack\" && ")
			for i := 0; i < op.StripComponents; i++ {
				b.WriteString("cd \"$(ls -1 | head -1)\" && ")
			}
			fmt.Fprintf(&b, "cp -a . %s)\n", ShQuoteArg(to))
		} else {
			fmt.Fprintf(&b, "unzip -q \"$ovtmp/archive\" -d %s\n", ShQuoteArg(to))
		}
	case "sh":
		fmt.Fprintf(&b, "chmod +x \"$ovtmp/archive\"\n")
		fmt.Fprintf(&b, "\"$ovtmp/archive\"\n")
	default:
		fmt.Fprintf(&b, "echo 'unsupported extract format: %s' >&2 && exit 1\n", extract)
	}

	return b.String()
}
