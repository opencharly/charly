package main

// harness_note.go — per-run notes memory subsystem.
//
// Each benchmark run starts with EMPTY notes — the AI generates fresh
// notes from scratch each run. When the run completes, the file is
// preserved on disk for browsing. So:
//
//   .harness/<score>/note/<run-id>.md   one file per run
//
// During a run, CHARLY_EVAL_NOTES_FILE is set to the per-run path so
// the AI's `charly check note append` (invoked from inside the per-run
// clone, with cwd != the host project) writes to the OUTER per-run
// file rather than a fresh per-clone copy that would die with the
// transient clone.
//
// Outside a run, `charly check note read` defaults to the most recent
// run's notes file. `charly check note append` outside a run errors —
// notes are run-scoped, ad-hoc seeding from the CLI is unsupported
// (use `charly check note append <score> <text>` only inside an
// iteration's runner.log via the env injection path).

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// NotePath returns the absolute path of the notes file the current
// caller should read/write.
//
//   - Inside an iteration, CHARLY_EVAL_NOTES_FILE env is set to the
//     per-run notes file; honor the override.
//   - Outside an iteration, return the most recent per-run notes
//     file under <harness-root>/note/, or — if none yet — a stable
//     <harness-root>/note/scratchpad.md (used by the read path; the
//     append path errors instead).
//
// Use NotePathForRun(layout) to compute the per-run path explicitly
// inside the harness loop (where we know the run-id without env).
func NotePath(projectDir, score string) string {
	if override := os.Getenv("CHARLY_EVAL_NOTES_FILE"); override != "" {
		return override
	}
	noteDir := filepath.Join(HarnessDataRoot(projectDir, score), "note")
	if latest := mostRecentNoteFile(noteDir); latest != "" {
		return latest
	}
	// Empty score history — return the conventional location even
	// though it doesn't exist yet. Callers handle ENOENT.
	return filepath.Join(noteDir, "scratchpad.md")
}

// NotePathForRun returns the canonical per-run notes path under the
// harness data root. Used by the harness loop (which knows the
// run-id directly without env-var indirection) for both ${NOTES}
// substitution snapshots and the CHARLY_EVAL_NOTES_FILE export.
func NotePathForRun(harnessRoot, runID string) string {
	return filepath.Join(harnessRoot, "note", runID+".md")
}

// mostRecentNoteFile returns the most recently modified <run-id>.md
// file under noteDir, or "" if none exist. Used by the read path
// when invoked outside an iteration.
func mostRecentNoteFile(noteDir string) string {
	entries, err := os.ReadDir(noteDir)
	if err != nil {
		return ""
	}
	var candidates []os.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		ai, _ := candidates[i].Info()
		aj, _ := candidates[j].Info()
		if ai == nil || aj == nil {
			return candidates[i].Name() > candidates[j].Name()
		}
		return ai.ModTime().After(aj.ModTime())
	})
	return filepath.Join(noteDir, candidates[0].Name())
}

// ReadNote returns the current contents of the per-run notes file
// (when invoked inside an iteration) or the most recent run's notes
// (outside an iteration). Empty content is NOT an error — fresh
// runs see "".
func ReadNote(projectDir, score string) (string, error) {
	path := NotePath(projectDir, score)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

// AppendNote writes one note to the per-run notes file. Requires
// CHARLY_EVAL_NOTES_FILE to be set (i.e., the caller is inside a
// harness iteration). Notes are run-scoped — ad-hoc seeding from
// outside an iteration is intentionally unsupported.
func AppendNote(_, score, runID, iter, ai, text string) error {
	if score == "" {
		return fmt.Errorf("note append: score name required")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("note append: text required (got empty/whitespace)")
	}
	path := os.Getenv("CHARLY_EVAL_NOTES_FILE")
	if path == "" {
		return fmt.Errorf("note append: notes are run-scoped — only supported inside a harness iteration (no CHARLY_EVAL_NOTES_FILE in env)")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("note append: mkdir %s: %w", filepath.Dir(path), err)
	}
	calver := ComputeCalVer()
	header := fmt.Sprintf("## %s run=%s iter=%s ai=%s\n\n",
		calver, orQuestion(runID), orQuestion(iter), orQuestion(ai))
	body := strings.TrimRight(text, "\n") + "\n\n"

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("note append: open %s: %w", path, err)
	}
	defer f.Close() //nolint:errcheck
	if _, err := f.WriteString(header + body); err != nil {
		return fmt.Errorf("note append: write %s: %w", path, err)
	}
	return nil
}

func orQuestion(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
