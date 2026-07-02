package spec

// Reproducibility gate: the committed cue_types_gen.go and vocab_gen.go MUST
// equal a fresh `task cue:gen`. This re-runs the SAME tools the task runs (the
// charly/internal/schemagen concat + the pinned cue exp gengotypes + the
// schemagen vocab emitter) into a temp dir and diffs the result against the
// committed files. It skips gracefully when the pinned cue CLI is unavailable
// (a dev box without ./bin/cue), but in CI — where `task cue:gen` has run — it
// catches any drift between charly/schema/*.cue and the committed generated Go.

import (
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const cueVersion = "v0.16.1"

// findCue resolves the pinned cue CLI: ../../bin/cue (repo bin), the RDD scratch
// binary, or a PATH `cue` — whichever reports cueVersion. Returns "" if none.
func findCue(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "..", "bin", "cue"), // repoRoot/bin/cue (cwd = charly/spec)
		"/tmp/cue-rdd/cue",
	}
	if p, err := exec.LookPath("cue"); err == nil {
		candidates = append(candidates, p)
	}
	for _, c := range candidates {
		out, err := exec.Command(c, "version").CombinedOutput()
		if err == nil && strings.Contains(string(out), cueVersion) {
			abs, _ := filepath.Abs(c)
			return abs
		}
	}
	return ""
}

// freshTypesGen reproduces charly/spec/cue_types_gen.go: schemagen -mode=concat
// → cue exp gengotypes, returning the gofmt-normalized bytes.
func freshTypesGen(t *testing.T, cue string) []byte {
	t.Helper()
	gendir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gendir, "cue.mod"), 0o755); err != nil {
		t.Fatalf("mkdir cue.mod: %v", err)
	}
	module := "module: \"github.com/opencharly/charly/charly/schema/spec-codegen\"\nlanguage: version: \"" + cueVersion + "\"\n"
	if err := os.WriteFile(filepath.Join(gendir, "cue.mod", "module.cue"), []byte(module), 0o644); err != nil {
		t.Fatalf("write module.cue: %v", err)
	}
	concat := filepath.Join(gendir, "schema_spec.cue")
	runIn(t, "..", "go", "run", "./internal/schemagen", "-mode=concat", "-schema=schema", "-out="+concat)
	runIn(t, gendir, cue, "exp", "gengotypes", "./schema_spec.cue")

	matches, _ := filepath.Glob(filepath.Join(gendir, "cue_types*_gen.go"))
	if len(matches) == 0 {
		t.Fatalf("gengotypes produced no cue_types*_gen.go in %s", gendir)
	}
	// Apply the SAME yaml-tag doubling the real cue:gen pipeline runs — invoke
	// schemagen -mode=retag on the gengotypes output (R3: ONE retag implementation,
	// no parallel copy of the transform in the test).
	runIn(t, "..", "go", "run", "./internal/schemagen", "-mode=retag", "-out="+matches[0])
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read fresh types: %v", err)
	}
	return gofmt(t, raw)
}

// freshVocabGen reproduces charly/spec/vocab_gen.go via schemagen -mode=vocab.
func freshVocabGen(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "vocab_gen.go")
	runIn(t, "..", "go", "run", "./internal/schemagen", "-mode=vocab", "-schema=schema", "-out="+out)
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read fresh vocab: %v", err)
	}
	return gofmt(t, raw)
}

// freshVersionGen reproduces charly/spec/version_gen.go via schemagen -mode=version
// — the CUE-owned SchemaVersion/SchemaFloor consts read from schema/version.cue.
func freshVersionGen(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "version_gen.go")
	runIn(t, "..", "go", "run", "./internal/schemagen", "-mode=version", "-schema=schema", "-out="+out)
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read fresh version: %v", err)
	}
	return gofmt(t, raw)
}

func runIn(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func gofmt(t *testing.T, b []byte) []byte {
	t.Helper()
	f, err := format.Source(b)
	if err != nil {
		t.Fatalf("gofmt: %v", err)
	}
	return f
}

func committed(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read committed %s: %v", name, err)
	}
	return gofmt(t, raw)
}

func TestGenReproducible(t *testing.T) {
	cue := findCue(t)
	if cue == "" {
		t.Skipf("pinned cue %s not available (run `task cue:gen` to bootstrap ./bin/cue) — skipping reproducibility gate", cueVersion)
	}

	if got, want := freshTypesGen(t, cue), committed(t, "cue_types_gen.go"); !equalBytes(got, want) {
		t.Errorf("cue_types_gen.go is STALE: a fresh `task cue:gen` differs from the committed file.\n"+
			"Run `task cue:gen` and commit the result. (fresh=%d bytes, committed=%d bytes)", len(got), len(want))
	}
	if got, want := freshVocabGen(t), committed(t, "vocab_gen.go"); !equalBytes(got, want) {
		t.Errorf("vocab_gen.go is STALE: a fresh `task cue:gen` differs from the committed file.\n"+
			"Run `task cue:gen` and commit the result. (fresh=%d bytes, committed=%d bytes)", len(got), len(want))
	}
	if got, want := freshVersionGen(t), committed(t, "version_gen.go"); !equalBytes(got, want) {
		t.Errorf("version_gen.go is STALE: a fresh `task cue:gen` differs from the committed file "+
			"(did you bump #SchemaVersion in schema/version.cue without running `task cue:gen`?).\n"+
			"Run `task cue:gen` and commit the result. (fresh=%d bytes, committed=%d bytes)", len(got), len(want))
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
