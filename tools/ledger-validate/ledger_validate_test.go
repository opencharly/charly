package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLines writes rel under dir with exactly n newline-terminated lines, so countLines(rel) == n.
func writeLines(t *testing.T, dir, rel string, n int) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x\n", n)), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func fixtureRows(t *testing.T) []Row {
	t.Helper()
	rows, err := loadLedger(filepath.Join("testdata", "ledger_fixture.csv"))
	if err != nil {
		t.Fatalf("loadLedger: %v", err)
	}
	return rows
}

// satisfiedTree builds the tree in which every non-TBD fixture row is satisfied: the DELETE file
// absent, the THIN file at its target, the STAY file present.
func satisfiedTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeLines(t, dir, "pkg/thin.go", 20)
	writeLines(t, dir, "pkg/stay.go", 50)
	writeLines(t, dir, "pkg/mixed.go", 70)
	return dir
}

func resultFor(t *testing.T, results []Result, file string) Result {
	t.Helper()
	for _, res := range results {
		if res.Row.File == file {
			return res
		}
	}
	t.Fatalf("no result for %s", file)
	return Result{}
}

func TestParseLedger_Fixture(t *testing.T) {
	rows := fixtureRows(t)
	if len(rows) != 4 {
		t.Fatalf("parsed %d rows, want 4", len(rows))
	}
	want := []Row{
		{File: "pkg/gone.go", BaselineLOC: 100, Disposition: dispDelete, TargetLOC: 0, HasTarget: true, Cone: "C1", Line: 2},
		{File: "pkg/thin.go", BaselineLOC: 80, Disposition: dispThin, TargetLOC: 20, HasTarget: true, Cone: "C1", Line: 3},
		{File: "pkg/stay.go", BaselineLOC: 50, Disposition: dispStay, Cone: "C2", Line: 4},
		{File: "pkg/mixed.go", BaselineLOC: 70, Disposition: dispThinTBD, Cone: "C2", Line: 5},
	}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], w)
		}
	}
}

// TestEvaluate_FourDispositions is the GREEN arm: each disposition is satisfied by the tree it
// describes, except THIN-TBD which is pending by construction.
func TestEvaluate_FourDispositions(t *testing.T) {
	results, err := evaluate(satisfiedTree(t), fixtureRows(t))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}

	for _, file := range []string{"pkg/gone.go", "pkg/thin.go", "pkg/stay.go"} {
		if res := resultFor(t, results, file); !res.Satisfied {
			t.Errorf("%s unsatisfied in the satisfied tree: %s", file, res.Reason)
		}
	}
	tbd := resultFor(t, results, "pkg/mixed.go")
	if tbd.Satisfied || !tbd.TBD {
		t.Errorf("THIN-TBD row: satisfied=%v tbd=%v, want satisfied=false tbd=true", tbd.Satisfied, tbd.TBD)
	}
}

// TestEvaluate_DeleteRowPresent_IsRed is the RED arm the brief names explicitly: a DELETE row whose
// file is still present must fail. Without this the whole gate would be vacuous.
func TestEvaluate_DeleteRowPresent_IsRed(t *testing.T) {
	dir := satisfiedTree(t)
	writeLines(t, dir, "pkg/gone.go", 100)

	results, err := evaluate(dir, fixtureRows(t))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	res := resultFor(t, results, "pkg/gone.go")
	if res.Satisfied {
		t.Fatal("DELETE row satisfied while the file is still present")
	}
	if !strings.Contains(res.Reason, "still present") {
		t.Errorf("reason = %q, want it to name the surviving file", res.Reason)
	}
	if res.ActualLOC != 100 {
		t.Errorf("ActualLOC = %d, want 100", res.ActualLOC)
	}
}

func TestEvaluate_RedArms(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(t *testing.T, dir string)
		file       string
		wantReason string
	}{
		{
			name:       "thin over target",
			mutate:     func(t *testing.T, dir string) { writeLines(t, dir, "pkg/thin.go", 21) },
			file:       "pkg/thin.go",
			wantReason: "over the 20 target by 1",
		},
		{
			name: "thin deleted instead of thinned",
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "pkg", "thin.go")); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
			file:       "pkg/thin.go",
			wantReason: "must survive",
		},
		{
			name: "stay deleted",
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "pkg", "stay.go")); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
			file:       "pkg/stay.go",
			wantReason: "must survive",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := satisfiedTree(t)
			tc.mutate(t, dir)
			results, err := evaluate(dir, fixtureRows(t))
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			res := resultFor(t, results, tc.file)
			if res.Satisfied {
				t.Fatalf("%s satisfied, want unsatisfied", tc.file)
			}
			if !strings.Contains(res.Reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to contain %q", res.Reason, tc.wantReason)
			}
		})
	}
}

// TestEvaluate_ThinAtExactTarget pins the boundary: the target is inclusive.
func TestEvaluate_ThinAtExactTarget(t *testing.T) {
	dir := satisfiedTree(t)
	writeLines(t, dir, "pkg/thin.go", 20)
	results, err := evaluate(dir, fixtureRows(t))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if res := resultFor(t, results, "pkg/thin.go"); !res.Satisfied {
		t.Errorf("THIN at exactly the target is unsatisfied: %s", res.Reason)
	}
}

func TestReportAcceptance_FailsWhileAnyRowPending(t *testing.T) {
	results, err := evaluate(satisfiedTree(t), fixtureRows(t))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var out bytes.Buffer
	if code := reportAcceptance(&out, results); code == 0 {
		t.Fatal("acceptance passed with a THIN-TBD row outstanding")
	}
	if !strings.Contains(out.String(), "FAIL") || !strings.Contains(out.String(), "pkg/mixed.go") {
		t.Errorf("acceptance output does not name the pending row:\n%s", out.String())
	}
}

func TestReportAcceptance_PassesWhenEveryRowSatisfied(t *testing.T) {
	// The amended ledger: the mixed row's spike has landed, so it is a measured THIN row.
	rows, err := parseLedger(strings.NewReader(
		"file,baseline_loc,disposition,target_loc,cone\n" +
			"pkg/gone.go,100,DELETE,0,C1\n" +
			"pkg/thin.go,80,THIN,20,C1\n" +
			"pkg/stay.go,50,STAY,,C2\n" +
			"pkg/mixed.go,70,THIN,60,C2\n"))
	if err != nil {
		t.Fatalf("parseLedger: %v", err)
	}
	dir := satisfiedTree(t)
	writeLines(t, dir, "pkg/mixed.go", 60)
	results, err := evaluate(dir, rows)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var out bytes.Buffer
	if code := reportAcceptance(&out, results); code != 0 {
		t.Fatalf("acceptance failed on a fully satisfied tree (exit %d):\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "PASS") {
		t.Errorf("acceptance output = %q, want a PASS line", out.String())
	}
}

func TestReportFrozen_ExitCodes(t *testing.T) {
	results, err := evaluate(satisfiedTree(t), fixtureRows(t))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var out bytes.Buffer
	if code := reportFrozen(&out, results); code == 0 {
		t.Fatal("freeze check passed with a THIN-TBD row outstanding")
	}
	if !strings.Contains(out.String(), "pkg/mixed.go") {
		t.Errorf("freeze output does not name the unmeasured row:\n%s", out.String())
	}

	// Drop the TBD row: the ledger is then frozen even though nothing has been removed yet.
	var measured []Result
	for _, res := range results {
		if !res.TBD {
			measured = append(measured, res)
		}
	}
	out.Reset()
	if code := reportFrozen(&out, measured); code != 0 {
		t.Fatalf("freeze check failed with no TBD rows (exit %d):\n%s", code, out.String())
	}
}

// TestReportProgress_CountsPerCone covers the mid-program scoreboard. It reports only — main exits
// 0 unconditionally in this mode — so the assertion is on the counts.
func TestReportProgress_CountsPerCone(t *testing.T) {
	results, err := evaluate(satisfiedTree(t), fixtureRows(t))
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	var out bytes.Buffer
	reportProgress(&out, "testdata/ledger_fixture.csv", ".", results)
	got := out.String()

	// C1: gone.go + thin.go both satisfied. C2: stay.go satisfied, mixed.go pending as TBD.
	for _, want := range []string{"C1", "C2", "OVERALL"} {
		if !strings.Contains(got, want) {
			t.Errorf("progress output missing %q:\n%s", want, got)
		}
	}
	byCone, overall, _ := tallyByCone(results)
	if byCone["C1"].satisfied != 2 || byCone["C1"].pending != 0 {
		t.Errorf("C1 tally = %+v, want 2 satisfied / 0 pending", *byCone["C1"])
	}
	if byCone["C2"].satisfied != 1 || byCone["C2"].pending != 1 || byCone["C2"].tbd != 1 {
		t.Errorf("C2 tally = %+v, want 1 satisfied / 1 pending / 1 tbd", *byCone["C2"])
	}
	if overall.total != 4 || overall.satisfied != 3 || overall.pending != 1 || overall.tbd != 1 {
		t.Errorf("overall tally = %+v, want 4 total / 3 satisfied / 1 pending / 1 tbd", overall)
	}
}

func TestParseLedger_RejectsMalformedRows(t *testing.T) {
	const header = "file,baseline_loc,disposition,target_loc,cone\n"
	tests := []struct {
		name string
		body string
		want string
	}{
		{"unknown disposition", "pkg/a.go,10,SHRINK,,C1\n", "unknown disposition"},
		{"thin without target", "pkg/a.go,10,THIN,,C1\n", "target_loc is empty"},
		{"thin target above baseline", "pkg/a.go,10,THIN,10,C1\n", "must be below the baseline"},
		{"delete with nonzero target", "pkg/a.go,10,DELETE,5,C1\n", "want 0 or empty"},
		{"stay with a target", "pkg/a.go,10,STAY,5,C1\n", "leave it empty"},
		{"tbd with a target", "pkg/a.go,10,THIN-TBD,5,C1\n", "leave it empty"},
		{"non-numeric baseline", "pkg/a.go,many,STAY,,C1\n", "not a number"},
		{"empty cone", "pkg/a.go,10,STAY,,\n", "no cone"},
		{"empty file", ",10,STAY,,C1\n", "empty file path"},
		{"duplicate file", "pkg/a.go,10,STAY,,C1\npkg/a.go,10,STAY,,C2\n", "duplicates line 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseLedger(strings.NewReader(header + tc.body))
			if err == nil {
				t.Fatal("parsed a malformed ledger without error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestParseLedger_RejectsBadHeader(t *testing.T) {
	_, err := parseLedger(strings.NewReader("file,loc,disposition,target_loc,cone\npkg/a.go,10,STAY,,C1\n"))
	if err == nil || !strings.Contains(err.Error(), "header column 2") {
		t.Fatalf("error = %v, want a header-column complaint", err)
	}
}

// TestCountLines_MatchesWc pins the wc -l semantics the ledger's numbers were measured with:
// newline bytes, so an unterminated final line does not count.
func TestCountLines_MatchesWc(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		body string
		want int
	}{
		{"empty", "", 0},
		{"one terminated line", "a\n", 1},
		{"unterminated final line", "a\nb", 1},
		{"three lines", "a\nb\nc\n", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "f.txt")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := countLines(path)
			if err != nil {
				t.Fatalf("countLines: %v", err)
			}
			if got != tc.want {
				t.Errorf("countLines(%q) = %d, want %d", tc.body, got, tc.want)
			}
		})
	}
}

// TestRealLedger_Parses guards the committed contract itself: a malformed KWAVE2_LEDGER.csv must
// never reach main, where it would only surface as a runtime exit 2.
func TestRealLedger_Parses(t *testing.T) {
	path := filepath.Join("..", "..", "charly", "KWAVE2_LEDGER.csv")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("ledger not reachable from this module copy: %v", err)
	}
	rows, err := loadLedger(path)
	if err != nil {
		t.Fatalf("the committed ledger does not parse: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the committed ledger has no rows")
	}
	for _, row := range rows {
		if !strings.HasPrefix(row.File, "charly/") {
			t.Errorf("line %d: %s is outside charly/ — the ledger scopes the kernel only", row.Line, row.File)
		}
	}
}
