// Command ledger-validate is the mechanical acceptance gate of the K-wave 2
// residue-ledger program (the plan at /home/atrawog/.claude/plans/kwave2-residue-ledger-plan.md).
//
// WHY THIS TOOL EXISTS: K-wave 1's target was an AGGREGATE ESTIMATE, so the outcome could never be
// validated against the original number — mid-course re-classifications lived in prose. K-wave 2
// fixes the process: the contract is the per-file CSV ledger charly/KWAVE2_LEDGER.csv, and
// acceptance is this tool exiting 0 against the tree. Zero prose interpretation at the end; a
// disposition can change ONLY by a ledger-amendment commit, so silent renumbering is mechanically
// impossible.
//
// The ledger columns are file,baseline_loc,disposition,target_loc,cone. The four dispositions:
//
//	DELETE    the file must NOT exist (target_loc is 0)
//	THIN      the file exists and its line count is <= target_loc
//	STAY      the file exists; no line-count constraint
//	THIN-TBD  a mixed row whose thin target the cone's own pre-cone spike has not measured yet.
//	          Never satisfied — it is PENDING until a ledger amendment turns it into a THIN row.
//
// Modes:
//
//	(default)   the acceptance gate. Exit 0 only if every non-TBD row is satisfied AND no
//	            THIN-TBD row remains.
//	-progress   the mid-program scoreboard: satisfied/pending/total per cone and overall.
//	            ALWAYS exits 0 — it reports, it does not gate.
//	-frozen     the freeze check: exit non-zero while any THIN-TBD row remains.
//
// Line counts match wc -l exactly (newline bytes), so a ledger figure can always be reproduced
// from the shell.
//
// This is its own module, deliberately outside the repo-root go.work (the tools/gomod-canonical
// and tools/golden-compile precedent), so run it from its own directory and point -root at the
// repo:
//
//	cd tools/ledger-validate && go run . -root ../.. -progress
//
// -ledger defaults to <root>/charly/KWAVE2_LEDGER.csv; pass it only to validate a ledger that
// lives somewhere else.
package main

import (
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	dispDelete  = "DELETE"
	dispThin    = "THIN"
	dispStay    = "STAY"
	dispThinTBD = "THIN-TBD"
)

// Row is one ledger line: a file and the disposition the program committed to for it.
type Row struct {
	File        string
	BaselineLOC int
	Disposition string
	TargetLOC   int
	HasTarget   bool
	Cone        string
	Line        int // 1-based CSV line, for error messages
}

// Result is a Row evaluated against the tree.
type Result struct {
	Row       Row
	Satisfied bool
	TBD       bool
	ActualLOC int
	Reason    string
}

func main() {
	var (
		ledger   = flag.String("ledger", "", "path to the ledger CSV (default <root>/charly/KWAVE2_LEDGER.csv)")
		root     = flag.String("root", ".", "repo root the ledger's file paths resolve against")
		progress = flag.Bool("progress", false, "print the per-cone scoreboard and exit 0")
		frozen   = flag.Bool("frozen", false, "exit non-zero if any THIN-TBD row remains")
	)
	flag.Parse()

	if *progress && *frozen {
		fmt.Fprintln(os.Stderr, "ledger-validate: -progress and -frozen are mutually exclusive")
		os.Exit(2)
	}

	if *ledger == "" {
		*ledger = filepath.Join(*root, "charly", "KWAVE2_LEDGER.csv")
	}
	rows, err := loadLedger(*ledger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger-validate: %v\n", err)
		os.Exit(2)
	}
	results, err := evaluate(*root, rows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger-validate: %v\n", err)
		os.Exit(2)
	}

	switch {
	case *progress:
		reportProgress(os.Stdout, *ledger, *root, results)
		os.Exit(0)
	case *frozen:
		os.Exit(reportFrozen(os.Stdout, results))
	default:
		os.Exit(reportAcceptance(os.Stdout, results))
	}
}

func loadLedger(path string) ([]Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseLedger(f)
}

func parseLedger(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = 5
	records, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("reading ledger: %w", err)
	}
	if len(records) == 0 {
		return nil, errors.New("empty ledger")
	}
	want := []string{"file", "baseline_loc", "disposition", "target_loc", "cone"}
	for i, h := range want {
		if strings.TrimSpace(records[0][i]) != h {
			return nil, fmt.Errorf("ledger header column %d is %q, want %q", i+1, records[0][i], h)
		}
	}

	rows := make([]Row, 0, len(records)-1)
	seen := make(map[string]int, len(records)-1)
	for i, rec := range records[1:] {
		line := i + 2
		row := Row{
			File:        strings.TrimSpace(rec[0]),
			Disposition: strings.TrimSpace(rec[2]),
			Cone:        strings.TrimSpace(rec[4]),
			Line:        line,
		}
		if row.File == "" {
			return nil, fmt.Errorf("line %d: empty file path", line)
		}
		if prev, dup := seen[row.File]; dup {
			return nil, fmt.Errorf("line %d: %s duplicates line %d", line, row.File, prev)
		}
		seen[row.File] = line
		if row.Cone == "" {
			return nil, fmt.Errorf("line %d: %s has no cone", line, row.File)
		}
		if row.BaselineLOC, err = strconv.Atoi(strings.TrimSpace(rec[1])); err != nil {
			return nil, fmt.Errorf("line %d: %s baseline_loc %q is not a number", line, row.File, rec[1])
		}

		target := strings.TrimSpace(rec[3])
		if target != "" {
			row.HasTarget = true
			if row.TargetLOC, err = strconv.Atoi(target); err != nil {
				return nil, fmt.Errorf("line %d: %s target_loc %q is not a number", line, row.File, rec[3])
			}
			if row.TargetLOC < 0 {
				return nil, fmt.Errorf("line %d: %s target_loc is negative", line, row.File)
			}
		}

		switch row.Disposition {
		case dispDelete:
			if row.HasTarget && row.TargetLOC != 0 {
				return nil, fmt.Errorf("line %d: %s is DELETE but target_loc is %d, want 0 or empty", line, row.File, row.TargetLOC)
			}
		case dispThin:
			if !row.HasTarget {
				return nil, fmt.Errorf("line %d: %s is THIN but target_loc is empty", line, row.File)
			}
			if row.TargetLOC >= row.BaselineLOC {
				return nil, fmt.Errorf("line %d: %s is THIN to %d but its baseline is %d — a thin target must be below the baseline", line, row.File, row.TargetLOC, row.BaselineLOC)
			}
		case dispStay, dispThinTBD:
			if row.HasTarget {
				return nil, fmt.Errorf("line %d: %s is %s but carries target_loc %d — leave it empty", line, row.File, row.Disposition, row.TargetLOC)
			}
		default:
			return nil, fmt.Errorf("line %d: %s has unknown disposition %q", line, row.File, row.Disposition)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func evaluate(root string, rows []Row) ([]Result, error) {
	results := make([]Result, 0, len(rows))
	for _, row := range rows {
		path := filepath.Join(root, filepath.FromSlash(row.File))
		res := Result{Row: row, ActualLOC: -1}

		present, err := isRegularFile(path)
		if err != nil {
			return nil, err
		}
		if present {
			if res.ActualLOC, err = countLines(path); err != nil {
				return nil, err
			}
		}

		switch row.Disposition {
		case dispDelete:
			if present {
				res.Reason = fmt.Sprintf("still present at %d LOC (baseline %d)", res.ActualLOC, row.BaselineLOC)
			} else {
				res.Satisfied = true
			}
		case dispThin:
			switch {
			case !present:
				res.Reason = fmt.Sprintf("missing — a THIN row must survive at <= %d LOC, not be deleted", row.TargetLOC)
			case res.ActualLOC > row.TargetLOC:
				res.Reason = fmt.Sprintf("%d LOC, over the %d target by %d (baseline %d)", res.ActualLOC, row.TargetLOC, res.ActualLOC-row.TargetLOC, row.BaselineLOC)
			default:
				res.Satisfied = true
			}
		case dispStay:
			if present {
				res.Satisfied = true
			} else {
				res.Reason = "missing — a STAY row must survive"
			}
		case dispThinTBD:
			res.TBD = true
			res.Reason = "thin target not yet measured by the cone's pre-cone spike"
		}
		results = append(results, res)
	}
	return results, nil
}

func isRegularFile(path string) (bool, error) {
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("%s exists but is not a regular file", path)
	}
	return true, nil
}

// countLines reproduces wc -l: the number of newline bytes in the file.
func countLines(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n, nil
}

type tally struct {
	satisfied int
	pending   int
	tbd       int
	total     int
}

func tallyByCone(results []Result) (map[string]*tally, tally, []string) {
	byCone := make(map[string]*tally)
	var overall tally
	for _, res := range results {
		t := byCone[res.Row.Cone]
		if t == nil {
			t = &tally{}
			byCone[res.Row.Cone] = t
		}
		t.total++
		overall.total++
		switch {
		case res.Satisfied:
			t.satisfied++
			overall.satisfied++
		default:
			t.pending++
			overall.pending++
			if res.TBD {
				t.tbd++
				overall.tbd++
			}
		}
	}
	cones := make([]string, 0, len(byCone))
	for cone := range byCone {
		cones = append(cones, cone)
	}
	sort.Strings(cones)
	return byCone, overall, cones
}

func reportProgress(w io.Writer, ledger, root string, results []Result) {
	byCone, overall, cones := tallyByCone(results)
	fmt.Fprintf(w, "K-wave 2 residue ledger — %s (root %s)\n\n", ledger, root)
	fmt.Fprintf(w, "%-12s %10s %8s %6s %6s\n", "CONE", "SATISFIED", "PENDING", "TBD", "TOTAL")
	for _, cone := range cones {
		t := byCone[cone]
		fmt.Fprintf(w, "%-12s %10d %8d %6d %6d\n", cone, t.satisfied, t.pending, t.tbd, t.total)
	}
	fmt.Fprintf(w, "%-12s %10d %8d %6d %6d\n", "OVERALL", overall.satisfied, overall.pending, overall.tbd, overall.total)
}

func reportAcceptance(w io.Writer, results []Result) int {
	_, overall, _ := tallyByCone(results)
	if overall.pending == 0 {
		fmt.Fprintf(w, "ledger-validate: PASS — all %d rows satisfied, no THIN-TBD remaining\n", overall.total)
		return 0
	}
	fmt.Fprintf(w, "ledger-validate: FAIL — %d of %d rows unsatisfied (%d still THIN-TBD)\n\n", overall.pending, overall.total, overall.tbd)
	for _, res := range results {
		if !res.Satisfied {
			fmt.Fprintf(w, "  %-8s %-10s %s: %s\n", res.Row.Cone, res.Row.Disposition, res.Row.File, res.Reason)
		}
	}
	return 1
}

func reportFrozen(w io.Writer, results []Result) int {
	_, overall, _ := tallyByCone(results)
	if overall.tbd == 0 {
		fmt.Fprintf(w, "ledger-validate: FROZEN — every row carries a measured disposition\n")
		return 0
	}
	fmt.Fprintf(w, "ledger-validate: NOT FROZEN — %d THIN-TBD row(s) still await a measured thin target\n\n", overall.tbd)
	for _, res := range results {
		if res.TBD {
			fmt.Fprintf(w, "  %-8s %s (baseline %d)\n", res.Row.Cone, res.Row.File, res.Row.BaselineLOC)
		}
	}
	return 1
}
