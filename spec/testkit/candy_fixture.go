package testkit

import (
	"os"
	"path/filepath"
	"strings"
)

// CopyCandyFixReplace copies a candy module tree to dst, rewriting go.mod's RELATIVE
// `replace github.com/opencharly/{sdk,spec} => ../../{sdk,spec}` directives to the
// ABSOLUTE repo-submodule dirs (derived from charlyDir's parent — the repo root) so an
// out-of-process plugin-candy build resolves them from a staged temp project location.
// A candy go.mod carries BOTH replaces (the sdk contract + the spec contract module it
// depends on transitively); a relative `=> ../../spec` staged into a temp project
// resolves to `<temp>/spec`, which does not exist, so the out-of-process `go build`
// fails with "reading ../../spec/go.mod: no such file" unless the spec replace is
// rewritten to the absolute spec dir exactly as the sdk replace is.
//
// This is test-support infrastructure shared by charly's out-of-process plugin
// end-to-end tests (candy staging for a temp project's LoadUnified/build path). It
// lives here — outside both the charly module and the sdk contract module — because it
// spells the sdk/spec module import paths as STRING LITERALS (rewriting go.mod text),
// which would otherwise defeat a decoupling gate scoped to "no charly/*.go source
// spells the sdk import path as a literal" even though the file makes no actual Go
// import of either module.
func CopyCandyFixReplace(src, dst, charlyDir string) error {
	repoRoot := filepath.Dir(charlyDir)
	sdkDir := filepath.Join(repoRoot, "sdk")
	specDir := filepath.Join(repoRoot, "spec")
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if d.Name() == "go.mod" {
			var fixed []string
			for _, line := range strings.Split(string(b), "\n") {
				switch {
				case strings.HasPrefix(strings.TrimSpace(line), "replace github.com/opencharly/sdk"):
					fixed = append(fixed, "replace github.com/opencharly/sdk => "+sdkDir)
				case strings.HasPrefix(strings.TrimSpace(line), "replace github.com/opencharly/spec"):
					fixed = append(fixed, "replace github.com/opencharly/spec => "+specDir)
				default:
					fixed = append(fixed, line)
				}
			}
			b = []byte(strings.Join(fixed, "\n"))
		}
		return os.WriteFile(target, b, 0o644)
	})
}
