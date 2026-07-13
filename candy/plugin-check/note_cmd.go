package check

// note_cmd.go — the persistent NOTES.md memory leaves (P12: relocated from
// charly/check_runner_cmd.go). The durable NOTES.md is the harness's
// Syncthing-replicated memory across runs; the note I/O lives in note.go
// (plugin-local file I/O under the project dir).

import (
	"fmt"
	"os"
)

// CheckNoteCmd groups the note read/append verbs (a branch — Kong dispatches to a child).
type CheckNoteCmd struct {
	Read   CheckNoteReadCmd   `cmd:"" name:"read" help:"Print the persistent NOTES.md for a score"`
	Append CheckNoteAppendCmd `cmd:"" name:"append" help:"Atomically append a note to a score's NOTES.md"`
}

// CheckNoteReadCmd prints a score's persistent NOTES.md.
type CheckNoteReadCmd struct {
	Score string `arg:"" optional:"" help:"Score name (default: $CHARLY_EVAL_SCORE)"`
}

func (c *CheckNoteReadCmd) Run() error {
	score := c.Score
	if score == "" {
		score = os.Getenv("CHARLY_EVAL_SCORE")
	}
	if score == "" {
		return fmt.Errorf("charly check note read: score name required (pass as arg or set CHARLY_EVAL_SCORE)")
	}
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	body, err := ReadNote(dir, score)
	if err != nil {
		return err
	}
	if body == "" {
		fmt.Fprintln(os.Stdout, "(empty)")
		return nil
	}
	fmt.Fprint(os.Stdout, body)
	return nil
}

// CheckNoteAppendCmd atomically appends a note to a score's NOTES.md.
type CheckNoteAppendCmd struct {
	Score string `arg:"" help:"Score name (or skip and pass --score)"`
	Text  string `arg:"" help:"Note text (one paragraph)"`
}

func (c *CheckNoteAppendCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	runID := os.Getenv("CHARLY_EVAL_RUN_ID")
	iter := os.Getenv("CHARLY_EVAL_ITERATION")
	ai := os.Getenv("CHARLY_EVAL_AGENT")
	if iter == "" {
		iter = "0"
	}
	return AppendNote(dir, c.Score, runID, iter, ai, c.Text)
}
