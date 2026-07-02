package kit

// profile.go — the PURE shell-profile / env.d helpers, moved here (Part 2) so the ONE copy
// serves BOTH charly's in-proc deploy targets (package main re-exports via kit_aliases.go)
// AND an OUT-OF-PROCESS deploy plugin's WalkPlans env.d + managed-block writes. Pure string
// functions only — the I/O (read the rc file, write env.d) lives in the caller (package
// main's shell_profile.go via the executor; the plugin's WalkPlans via the wire Executor).

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ShellKind classifies the venue's login shell.
type ShellKind string

const (
	ShellBash ShellKind = "bash"
	ShellZsh  ShellKind = "zsh"
	ShellFish ShellKind = "fish"
)

// DetectShellFromPath maps a $SHELL path (or shell base name) to a ShellKind. Unknown /
// empty shells default to bash — the POSIX-safest choice.
func DetectShellFromPath(shellPath string) ShellKind {
	switch filepath.Base(shellPath) {
	case "zsh":
		return ShellZsh
	case "fish":
		return ShellFish
	default: // bash / sh / "" / unknown
		return ShellBash
	}
}

// EnvdDir returns the directory where per-candy env files live under a home.
func EnvdDir(home string) string {
	return filepath.Join(home, ".config", "opencharly", "env.d")
}

// EnvdFilePath returns the env file path for a given candy under a home.
func EnvdFilePath(home, candyName string) string {
	return filepath.Join(EnvdDir(home), candyName+".env")
}

// RenderEnvdBody produces the deterministic, shell-agnostic POSIX-sh fragment for a candy's
// env vars + PATH additions. Sorted keys guarantee stable output.
func RenderEnvdBody(candyName string, envVars map[string]string, pathAdd []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# opencharly env for layer %s — managed by charly; do not edit\n", candyName)
	keys := make([]string, 0, len(envVars))
	for k := range envVars {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, ShQuoteEnv(envVars[k]))
	}
	if len(pathAdd) > 0 {
		parts := append([]string(nil), pathAdd...)
		parts = append(parts, `$PATH`)
		fmt.Fprintf(&b, "export PATH=\"%s\"\n", ShDoubleQuotePath(strings.Join(parts, ":")))
	}
	return b.String()
}

const (
	managedBlockBegin = "# opencharly:begin (managed by charly; do not edit inside this block)"
	managedBlockEnd   = "# opencharly:end"
)

// ManagedBlockBody returns the shell-specific loop that sources the env.d directory under
// a home. POSIX-sh for bash/zsh, fish syntax for fish.
func ManagedBlockBody(shell ShellKind, home string) string {
	envdGlob := filepath.Join(EnvdDir(home), "*.env")
	switch shell {
	case ShellFish:
		return fmt.Sprintf(`for f in %s
  if test -r $f
    source $f
  end
end`, envdGlob)
	default: // bash/zsh POSIX
		return fmt.Sprintf(`for f in %s; do [ -r "$f" ] && . "$f"; done`, envdGlob)
	}
}

// ShellInitFilePath returns the init file the managed block lands in for each shell under
// a home.
func ShellInitFilePath(shell ShellKind, home string) string {
	switch shell {
	case ShellZsh:
		return filepath.Join(home, ".zshenv")
	case ShellFish:
		return filepath.Join(home, ".config", "fish", "conf.d", "opencharly.fish")
	case ShellBash:
		return filepath.Join(home, ".bashrc")
	default:
		return filepath.Join(home, ".profile")
	}
}

// MarkersForTag returns the begin/end fence pair for a marker tag. Empty tag → the
// global-block fence; non-empty → a per-candy fence so multiple candies coexist.
func MarkersForTag(marker string) (begin, end string) {
	if marker == "" {
		return managedBlockBegin, managedBlockEnd
	}
	return fmt.Sprintf("# opencharly:begin %s (managed by charly; do not edit inside this block)", marker),
		fmt.Sprintf("# opencharly:end %s", marker)
}

// StripLegacyOverthinkBlocks removes any pre-rebrand `# overthink:` managed block.
func StripLegacyOverthinkBlocks(existing string) string {
	const legacyBegin, legacyEnd = "# overthink:begin", "# overthink:end"
	if !strings.Contains(existing, legacyBegin) {
		return existing
	}
	var out strings.Builder
	inBlock := false
	for _, line := range strings.Split(existing, "\n") {
		if strings.Contains(line, legacyBegin) {
			inBlock = true
			continue
		}
		if inBlock && strings.Contains(line, legacyEnd) {
			inBlock = false
			continue
		}
		if !inBlock {
			out.WriteString(line + "\n")
		}
	}
	return strings.Trim(out.String(), "\n") + "\n"
}

// ReplaceOrAppendManagedBlock replaces the begin/end fence pair's body (tagged with marker,
// empty for the global block) in existing, appending a fresh block at EOF when absent. Any
// pre-rebrand `# overthink:` block is stripped first.
func ReplaceOrAppendManagedBlock(existing, body, marker string) string {
	existing = StripLegacyOverthinkBlocks(existing)
	begin, end := MarkersForTag(marker)
	if strings.Contains(existing, begin) {
		var out strings.Builder
		inBlock := false
		for _, line := range strings.Split(existing, "\n") {
			if strings.Contains(line, begin) {
				inBlock = true
				out.WriteString(begin + "\n")
				out.WriteString(body + "\n")
				continue
			}
			if inBlock && strings.Contains(line, end) {
				inBlock = false
				out.WriteString(end + "\n")
				continue
			}
			if !inBlock {
				out.WriteString(line + "\n")
			}
		}
		return strings.TrimRight(out.String(), "\n") + "\n"
	}
	prefix := existing
	if prefix != "" && !strings.HasSuffix(prefix, "\n") {
		prefix += "\n"
	}
	if prefix != "" {
		prefix += "\n"
	}
	return prefix + begin + "\n" + body + "\n" + end + "\n"
}

// StripManagedBlock removes the begin/end fence pair (tagged with marker) and its body.
func StripManagedBlock(existing, marker string) string {
	begin, end := MarkersForTag(marker)
	if !strings.Contains(existing, begin) {
		return existing
	}
	var out strings.Builder
	inBlock := false
	for _, line := range strings.Split(existing, "\n") {
		if strings.Contains(line, begin) {
			inBlock = true
			continue
		}
		if inBlock && strings.Contains(line, end) {
			inBlock = false
			continue
		}
		if !inBlock {
			out.WriteString(line + "\n")
		}
	}
	return strings.TrimRight(out.String(), "\n") + "\n"
}

func sortStrings(s []string) {
	// tiny insertion sort — kit avoids pulling sort twice; render.go uses sort.Strings.
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
