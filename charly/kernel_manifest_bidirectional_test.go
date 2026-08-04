package main

// TestKernelManifestBidirectional is a W5-terminus CI tells-test (task #16): the
// kernel/plugin boundary law's receipt ledger, KERNEL_MANIFEST.md, must stay
// BIDIRECTIONALLY accurate — every charly/*.go production file has a documented row
// (clause ∈ {E, M, B, D, K1-IOU, MIXED-*, EXCEPTION-GPU, R-RESIDUE (exit: ...)} — see
// "Clause key" at the bottom of KERNEL_MANIFEST.md), and every row's named file
// genuinely exists (a row citing a since-deleted file is stale documentation, not a
// receipt).
//
// KERNEL_MANIFEST.md is presently a DRAFT (its own header says so): assembled
// incrementally, wave by wave, as each unit's teammate independently re-verified a
// STAY/MOVE verdict with their own defines-vs-calls grep. At first authoring it
// documented only 39 of charly/'s 133 production files; the program lead
// pre-authorized drafting the remaining 94-file census directly from
// /tmp/audit-prod-report.md (per-file E/M/B/D/R verdicts) + /tmp/w3-adjudication.md
// (binding corrections where the two conflict — adjudication wins) + a corroborating
// defines-vs-calls spot-grep for anything neither source settles. A file the audit
// classifies R (residue — its behaviour genuinely belongs in a plugin) but that is
// STILL PRESENT because the wave/IOU that dissolves it hasn't landed gets an
// R-RESIDUE(exit: <wave/unit/IOU>) row — documented residue, never pretend-kernel.
// kernelManifestPendingFiles below is now the CONTESTED-only remainder (a source
// conflict this teammate could not resolve unilaterally, routed to the program lead
// for a ruling) — see .w5-report.md in this worktree for the full per-file resolution
// trail. MAINTENANCE RULE — same shrink-only shape as reverseChannelHostBuilderWhitelist
// (gate 2): whoever authors a new manifest row removes that file from
// kernelManifestPendingFiles in the SAME commit (the gate fails otherwise, forcing the
// trim); a file may be added to the pending list only alongside a genuinely NEW
// production file with no row yet (a reviewable diff), never as a silent way to keep
// an old backlog entry around after it's been documented.
//
// A brand-new prod file appearing with NEITHER a manifest row NOR a pending-list entry
// is treated as a program-level FINDING (reported to the orchestrator), same as gate 2.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// kernelManifestPendingFiles is now EMPTY: the W5-gates teammate drafted the full
// 94-file backlog census directly into KERNEL_MANIFEST.md's new "## W5 census — the
// remaining 94 files (task #16)" section (program lead pre-authorization, msg
// 2026-08-04) using /tmp/audit-prod-report.md + /tmp/w3-adjudication.md + corroborating
// defines-vs-calls spot-grep per the resolution order the program lead specified. No
// file needed CONTESTED escalation — every audit-vs-adjudication disagreement resolved
// cleanly via the stated priority order (adjudication wins). Kept as an empty map
// (rather than deleted) so a future genuinely-new undocumented file has a place to land
// pending its own row — the shrink-only MAINTENANCE RULE above still applies to any
// future entry.
var kernelManifestPendingFiles = map[string]bool{}

// manifestFileRow is one parsed KERNEL_MANIFEST.md file-listing table row.
type manifestFileRow struct {
	File   string // basename, e.g. "checkrun.go"
	Clause string
	Line   int
}

// manifestTableHeaderCells names the first-column header text of a genuine
// per-FILE table (as opposed to a per-SYMBOL breakdown table like the
// `unified_targets.go` / `update_deploy_dispatch.go` sections' "| Symbol | ... |"
// tables, which document one file's internal function-level split and must NOT be
// read as separate top-level files).
var manifestTableHeaderCells = map[string]bool{
	"File":                     true,
	"File (slimmed remainder)": true,
}

var manifestGoFileRE = regexp.MustCompile("`([A-Za-z0-9_./-]+\\.go)`")

// parseManifestFileRows reads a KERNEL_MANIFEST.md-shaped file and returns every
// per-file table row (skipping per-symbol tables — header AND data rows alike — plus
// header rows and separator rows of the file tables themselves). A row citing
// multiple files (e.g. "`oci_step_emit.go` + `step_emit_hostbuild.go`") yields one
// manifestFileRow per file, sharing that row's clause text.
//
// Markdown tables are line-scoped, not delimited by any closing marker — a table
// "ends" at the first line that doesn't start with "|". This function therefore
// tracks CURRENT TABLE STATE across lines: a header row sets it (per-file vs
// per-symbol vs unrecognized), a non-"|" line resets it, and only rows seen while
// state == per-file are collected. Skipping just the per-symbol table's OWN header
// line is not enough — its DATA rows must be skipped too (a per-symbol row's first
// cell, e.g. "`hostEnvJSON`", can coincidentally look like a bare identifier, but a
// row like "`epsilon.go`" inside a Symbol table would otherwise be misread as a
// file row; the teeth proof below exercises exactly this case).
func parseManifestFileRows(path string) ([]manifestFileRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	type tableKind int
	const (
		tableNone tableKind = iota
		tableFile
		tableOther
	)

	var rows []manifestFileRow
	current := tableNone
	for i, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "|") {
			current = tableNone
			continue
		}
		trimmed := strings.Trim(strings.TrimSpace(line), "|")
		cells := strings.Split(trimmed, "|")
		if len(cells) < 3 {
			current = tableNone
			continue
		}
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		cell1, clause := cells[0], cells[2]

		if manifestTableHeaderCells[cell1] {
			current = tableFile
			continue
		}
		if cell1 == "Symbol" {
			current = tableOther
			continue
		}
		if isMarkdownSeparatorRow(cell1) {
			continue // separator row: keep the table state a header row just set
		}
		if current != tableFile {
			continue // inside a per-symbol (or otherwise unrecognized) table
		}
		for _, m := range manifestGoFileRE.FindAllStringSubmatch(cell1, -1) {
			base := filepath.Base(m[1])
			rows = append(rows, manifestFileRow{File: base, Clause: clause, Line: i + 1})
		}
	}
	return rows, nil
}

// isMarkdownSeparatorRow reports whether a table cell is a markdown header-separator
// (only '-', ':', and whitespace — e.g. "---" or ":---:").
func isMarkdownSeparatorRow(cell string) bool {
	if cell == "" {
		return false
	}
	for _, r := range cell {
		if r != '-' && r != ':' && r != ' ' {
			return false
		}
	}
	return true
}

// manifestClauseKeys are the recognized clause-code PREFIXES per KERNEL_MANIFEST.md's
// own "## Clause key" section (M, E, B, D, K1-IOU) plus the two forms the W5 brief
// additionally names for the end-state manifest (MIXED-*, EXCEPTION-GPU) and the
// joint "M/B" shorthand already used for check_graphics_endpoint.go. A clause is
// valid if, after stripping a leading "MIXED:" prefix, EVERY "/"-separated or
// " (" -prefixed leading token matches one of these single-letter/named codes.
var manifestClauseKeys = map[string]bool{
	"E": true, "M": true, "B": true, "D": true,
}

// clauseIsRecognized reports whether clause (the raw table-cell text) starts with a
// recognized clause code per manifestClauseKeys, or the literal "K1-IOU", "MIXED",
// "EXCEPTION-GPU", or "R-RESIDUE" forms. R-RESIDUE is the W5-census addition (task
// #16): a file the boundary law's own per-file test still classifies R (residue —
// its behaviour genuinely belongs in a plugin) but that is STILL PRESENT in charly/
// today because the wave/IOU that dissolves it hasn't landed yet. Per the program
// lead's ruling: "R-classified files still in tree are EXPECTED (K1-IOU/fenced/
// consumer-gated residue) — their rows say R-RESIDUE (exit: <wave/IOU>), which the
// gate accepts as a clause; the end-state manifest distinguishes documented-residue
// from kernel, it doesn't pretend residue is kernel." Every R-RESIDUE row MUST carry
// an "(exit: ...)" naming the wave/unit/IOU that dissolves it — enforced separately
// below (a bare "R-RESIDUE" with no exit clause fails validation), so residue is
// never a place to silently stop looking.
func clauseIsRecognized(clause string) bool {
	clause = strings.TrimSpace(clause)
	if strings.HasPrefix(clause, "K1-IOU") || strings.HasPrefix(clause, "MIXED") || strings.HasPrefix(clause, "EXCEPTION-GPU") {
		return true
	}
	if strings.HasPrefix(clause, "R-RESIDUE") {
		return strings.Contains(clause, "(exit:")
	}
	// Take the leading token up to the first space, "(", or ":" — then split any
	// "/"-joined compound ("M/B") and require every part be a recognized code.
	end := strings.IndexAny(clause, " (:")
	lead := clause
	if end >= 0 {
		lead = clause[:end]
	}
	for _, part := range strings.Split(lead, "/") {
		if !manifestClauseKeys[part] {
			return false
		}
	}
	return lead != ""
}

func TestKernelManifestBidirectional(t *testing.T) {
	rows, err := parseManifestFileRows("KERNEL_MANIFEST.md")
	if err != nil {
		t.Fatal(err)
	}

	manifestByFile := map[string]manifestFileRow{}
	var dupRows []string
	for _, r := range rows {
		if prior, dup := manifestByFile[r.File]; dup {
			dupRows = append(dupRows, fmt.Sprintf("%s: rows at line %d and line %d both name it", r.File, prior.Line, r.Line))
			continue
		}
		manifestByFile[r.File] = r
	}
	if len(dupRows) > 0 {
		sort.Strings(dupRows)
		t.Errorf("KERNEL_MANIFEST.md has %d duplicate file row(s) — one file, one row:\n  %s", len(dupRows), strings.Join(dupRows, "\n  "))
	}

	// Direction 1: every manifest row's file must exist as a real charly/ prod file.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	prodFiles := map[string]bool{}
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			prodFiles[f] = true
		}
	}
	var orphanRows []string
	for f, r := range manifestByFile {
		if !prodFiles[f] {
			orphanRows = append(orphanRows, fmt.Sprintf("%s (KERNEL_MANIFEST.md:%d) — no such production file in charly/", f, r.Line))
		}
	}
	if len(orphanRows) > 0 {
		sort.Strings(orphanRows)
		t.Errorf("KERNEL_MANIFEST.md cites %d file(s) that no longer exist — stale documentation, R5 finding (delete the row or the row's file was deleted without updating the manifest):\n  %s",
			len(orphanRows), strings.Join(orphanRows, "\n  "))
	}

	// Direction 2: every prod file must have a manifest row, OR be named in the
	// reviewed shrink-only pending backlog.
	var newUndocumented []string
	var stalePendingEntries []string
	for f := range prodFiles {
		_, documented := manifestByFile[f]
		_, pending := kernelManifestPendingFiles[f]
		if !documented && !pending {
			newUndocumented = append(newUndocumented, f)
		}
	}
	for f := range kernelManifestPendingFiles {
		_, documented := manifestByFile[f]
		_, stillProd := prodFiles[f]
		if documented {
			stalePendingEntries = append(stalePendingEntries, fmt.Sprintf("%s — now has a manifest row (KERNEL_MANIFEST.md:%d); remove it from kernelManifestPendingFiles", f, manifestByFile[f].Line))
		} else if !stillProd {
			stalePendingEntries = append(stalePendingEntries, fmt.Sprintf("%s — no longer a production file (moved/deleted); remove it from kernelManifestPendingFiles", f))
		}
	}
	if len(newUndocumented) > 0 {
		sort.Strings(newUndocumented)
		t.Errorf("KERNEL_MANIFEST.md gate: %d NEW production file(s) with neither a manifest row nor a pending-backlog entry — a program-level finding (report to the orchestrator; a genuinely new file needs EITHER a manifest row authored now OR an explicit, reviewed kernelManifestPendingFiles entry):\n  %s",
			len(newUndocumented), strings.Join(newUndocumented, "\n  "))
	}
	if len(stalePendingEntries) > 0 {
		sort.Strings(stalePendingEntries)
		t.Errorf("KERNEL_MANIFEST.md gate: %d stale kernelManifestPendingFiles entrie(s) — trim them in the commit that authored the row or deleted the file (the pending list only ever shrinks):\n  %s",
			len(stalePendingEntries), strings.Join(stalePendingEntries, "\n  "))
	}

	// Clause validity: every DOCUMENTED file's clause must be a recognized code.
	var badClauses []string
	for f, r := range manifestByFile {
		if !clauseIsRecognized(r.Clause) {
			badClauses = append(badClauses, fmt.Sprintf("%s (KERNEL_MANIFEST.md:%d): unrecognized clause %q", f, r.Line, r.Clause))
		}
	}
	if len(badClauses) > 0 {
		sort.Strings(badClauses)
		t.Errorf("KERNEL_MANIFEST.md gate: %d row(s) with an unrecognized clause (expected E/M/B/D/K1-IOU/MIXED/EXCEPTION-GPU, or a \"/\"-joined compound of those):\n  %s",
			len(badClauses), strings.Join(badClauses, "\n  "))
	}
}

// TestKernelManifestBidirectional_TeethProof proves the parser and clause validator
// fire correctly against an in-memory synthetic manifest (never touching the real
// KERNEL_MANIFEST.md) — a per-symbol table is correctly IGNORED, a per-file table row
// is correctly picked up (including a multi-file "+" row sharing one clause), a
// recognized clause passes, and an unrecognized clause fails.
func TestKernelManifestBidirectional_TeethProof(t *testing.T) {
	const synthetic = `# Synthetic manifest

| File | LOC | Clause | Evidence |
|---|---:|---|---|
| ` + "`alpha.go`" + ` | 10 | M — a mechanism | some evidence |
| ` + "`beta.go`" + ` + ` + "`gamma.go`" + ` | 20 | MIXED: M (a) / B (b) | shared row, two files |
| ` + "`delta.go`" + ` | 5 | NOT-A-REAL-CLAUSE | bogus |

## A per-symbol table (must be IGNORED — not a per-file row)

| Symbol | LOC | Clause | Evidence |
|---|---:|---|---|
| ` + "`epsilon.go`" + ` | 99 | M — must NOT be collected, this is a symbol table | fake |
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "synthetic_manifest.md")
	if err := os.WriteFile(path, []byte(synthetic), 0o644); err != nil {
		t.Fatal(err)
	}

	rows, err := parseManifestFileRows(path)
	if err != nil {
		t.Fatal(err)
	}
	byFile := map[string]manifestFileRow{}
	for _, r := range rows {
		byFile[r.File] = r
	}

	if _, ok := byFile["epsilon.go"]; ok {
		t.Fatalf("teeth proof FAILED: the per-SYMBOL table's row was collected as a per-file row (epsilon.go) — the Symbol-table skip regressed")
	}
	if len(rows) != 4 {
		t.Fatalf("teeth proof FAILED: expected exactly 4 collected file rows (alpha, beta, gamma, delta), got %d: %+v", len(rows), rows)
	}
	for _, want := range []string{"alpha.go", "beta.go", "gamma.go", "delta.go"} {
		if _, ok := byFile[want]; !ok {
			t.Fatalf("teeth proof FAILED: expected %s to be collected; got %+v", want, rows)
		}
	}
	if byFile["beta.go"].Clause != byFile["gamma.go"].Clause {
		t.Fatalf("teeth proof FAILED: a multi-file '+' row must share ONE clause across both files; got beta=%q gamma=%q",
			byFile["beta.go"].Clause, byFile["gamma.go"].Clause)
	}
	t.Logf("teeth proof OK: per-file rows collected (incl. multi-file '+' row), per-symbol table ignored: %+v", rows)

	if !clauseIsRecognized(byFile["alpha.go"].Clause) {
		t.Fatalf("teeth proof FAILED: %q should be a recognized clause (M)", byFile["alpha.go"].Clause)
	}
	if !clauseIsRecognized(byFile["beta.go"].Clause) {
		t.Fatalf("teeth proof FAILED: %q should be a recognized clause (MIXED)", byFile["beta.go"].Clause)
	}
	if clauseIsRecognized(byFile["delta.go"].Clause) {
		t.Fatalf("teeth proof FAILED: %q must NOT be recognized as a valid clause", byFile["delta.go"].Clause)
	}
	t.Logf("teeth proof OK: clause validator accepts M/MIXED, rejects a bogus clause")

	// R-RESIDUE requires a named "(exit: ...)" — a bare R-RESIDUE with no exit is
	// exactly the "residue as a place to stop looking" failure mode this form exists
	// to prevent.
	if !clauseIsRecognized("R-RESIDUE (exit: W3 unit A9)") {
		t.Fatalf("teeth proof FAILED: an R-RESIDUE clause WITH a named exit must be recognized")
	}
	if clauseIsRecognized("R-RESIDUE") {
		t.Fatalf("teeth proof FAILED: a bare R-RESIDUE clause with NO named exit must NOT be recognized")
	}
	t.Logf("teeth proof OK: R-RESIDUE requires a named exit clause")

	// Bidirectional diff logic: a new-undocumented file (no row, no pending entry)
	// and a stale-pending entry (pending, but now documented) both fire.
	prodFiles := map[string]bool{"alpha.go": true, "beta.go": true, "gamma.go": true, "zeta.go": true}
	pending := map[string]bool{"beta.go": true, "eta.go": true} // beta now HAS a row -> stale; zeta has neither -> new finding
	var newUndocumented, stale []string
	for f := range prodFiles {
		_, documented := byFile[f]
		if !documented && !pending[f] {
			newUndocumented = append(newUndocumented, f)
		}
	}
	for f := range pending {
		if _, documented := byFile[f]; documented {
			stale = append(stale, f)
		} else if !prodFiles[f] {
			stale = append(stale, f)
		}
	}
	if len(newUndocumented) != 1 || newUndocumented[0] != "zeta.go" {
		t.Fatalf("teeth proof FAILED: expected exactly zeta.go flagged as new-undocumented; got %+v", newUndocumented)
	}
	sort.Strings(stale)
	if len(stale) != 2 { // beta.go (now documented) + eta.go (not even a prod file)
		t.Fatalf("teeth proof FAILED: expected exactly 2 stale pending entries (beta.go now-documented, eta.go not-a-prod-file); got %+v", stale)
	}
	t.Logf("teeth proof OK: new-undocumented + stale-pending diff logic both fire: new=%+v stale=%+v", newUndocumented, stale)
}
