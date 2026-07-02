// THE single schema-concatenation contract (R3).
//
// Both the runtime (charly/cue_schema.go `sharedCueSchema`) and the dev-time
// code generator (charly/internal/schemagen) need to concatenate every
// package-less schema/*.cue file into ONE compilation unit. They used to carry
// two byte-identical-by-comment copies of the same loop; this is the ONE copy
// they both call, so the compiled schema (what the runtime validates against)
// and the generated Go types (`cue exp gengotypes`) can never drift.
//
// Leaf package (no deps beyond stdlib `io/fs`) so the dev-time generator can
// call it WITHOUT importing the spec package it regenerates.
//
// STANDALONE: depends only on the stdlib `io/fs` abstraction, so the runtime
// passes its `//go:embed` FS and the generator passes `os.DirFS(schemaDir)`.
package schemaconcat

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// ConcatSchema reads every `*.cue` file in fsys under dir (skipping any for
// which exclude reports true — pass nil to include all), sorts the names for a
// deterministic result, and joins the file bodies with a trailing newline. It
// returns the joined body (WITHOUT any package clause) and the sorted list of
// files included.
func ConcatSchema(fsys fs.FS, dir string, exclude func(name string) bool) (string, []string, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return "", nil, fmt.Errorf("read schema dir %q: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") {
			continue
		}
		if exclude != nil && exclude(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // deterministic concatenation
	var b strings.Builder
	for _, n := range names {
		data, err := fs.ReadFile(fsys, path(dir, n))
		if err != nil {
			return "", nil, fmt.Errorf("read schema %s: %w", n, err)
		}
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String(), names, nil
}

// path joins a dir and a file name with "/" (fs.FS always uses forward slashes,
// never the OS separator). A "." dir yields the bare name.
func path(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}
