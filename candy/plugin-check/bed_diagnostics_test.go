package check

import (
	"strings"
	"testing"
)

// The bed diagnostics gate exists because a bed once reported ok:true over 56 error lines: the
// runner graded exit codes and nothing else. These tests pin the three properties that make the
// gate worth having — errors are never exempt, an allowlisted line is counted separately rather
// than vanishing, and every allowance is narrow enough that it cannot swallow a neighbouring
// finding.

// TestErrorAllowlistIsEmpty is the load-bearing assertion of the whole table. An `error:` line
// emitted under a zero exit code is a swallowed failure by construction; the moment an error-tier
// exemption exists, the gate can be talked out of the exact finding it was built to catch. If a
// future entry genuinely needs to be error-tier, deleting this test must be a deliberate, visible
// act rather than a side effect.
func TestErrorAllowlistIsEmpty(t *testing.T) {
	for _, a := range diagnosticAllowlist {
		if a.Severity == severityError {
			t.Errorf("allowlist entry %q is error-tier; the error allowlist must stay empty", a.ID)
		}
	}
}

// TestAllowlistEntriesAreWellFormed keeps the audit trail honest: the Why is printed verbatim
// into summary.yml on every run, so an empty or throwaway one silently converts a reviewed
// exemption into an unexplained one.
func TestAllowlistEntriesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range diagnosticAllowlist {
		if a.ID == "" || a.Match == nil {
			t.Errorf("allowlist entry %+v: ID and Match are both required", a)
			continue
		}
		if seen[a.ID] {
			t.Errorf("duplicate allowlist ID %q — IDs key the per-run usage report", a.ID)
		}
		seen[a.ID] = true
		if len(a.Why) < 80 {
			t.Errorf("allowlist entry %q: Why is %d chars; it is printed into summary.yml as the "+
				"justification a reader audits, so it must actually explain the exemption",
				a.ID, len(a.Why))
		}
	}
}

// TestPacmanNeededAllowanceIsScoped covers the entry added for pacman's `--needed` acknowledgement
// and, more importantly, its BOUNDARY. `--needed` is what makes a repeated install idempotent, so
// the message is unavoidable on any base that already ships a package a candy declares — but the
// pattern must claim ONLY that sentence. The negative cases are real pacman warnings that share
// its opening words.
func TestPacmanNeededAllowanceIsScoped(t *testing.T) {
	claimed := []string{
		"warning: dbus-1.16.2-1.1 is up to date -- skipping",
		"warning: podman-6.1.0-1.1 is up to date -- skipping",
		"warning: shadow-4.20.0.arch1-1.1 is up to date -- skipping",
	}
	for _, line := range claimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			t.Fatalf("%q was not recognised as a diagnostic at all", line)
		}
		a := allowanceFor(sev, line)
		if a == nil || a.ID != "pacman-needed-package-already-current" {
			t.Errorf("%q: want the pacman-needed allowance, got %v", line, a)
		}
	}

	notClaimed := []string{
		"warning: zstd: local (1.5.7-3) is newer than cachyos-v3 (1.5.7-2)",
		"warning: could not fully load metadata for package foo-1.0-1",
		"warning: database file for 'extra' does not exist (use '-Sy' to download)",
		"warning: dbus-1.16.2-1.1 is up to date -- skipping this and everything after it",
	}
	for _, line := range notClaimed {
		sev, _, ok := classifyDiagnosticLine(line)
		if !ok {
			continue // not recognised as a diagnostic; nothing to exempt
		}
		if a := allowanceFor(sev, line); a != nil && a.ID == "pacman-needed-package-already-current" {
			t.Errorf("%q must NOT be claimed by the pacman-needed allowance", line)
		}
	}
}

// TestScanCountsAllowlistedSeparately proves an exempted line is REPORTED, not erased. A gate that
// deleted its exemptions would read "0 warnings" while suppressing eight, which is the failure
// mode the summary's separate Allowlisted count exists to prevent.
func TestScanCountsAllowlistedSeparately(t *testing.T) {
	log := "STEP 1/2: RUN pacman -Syu --noconfirm --needed dbus\n" +
		"warning: dbus-1.16.2-1.1 is up to date -- skipping\n" +
		"STEP 2/2: RUN something-else\n" +
		"warning: this one is not exempt at all\n"

	d := scanStepDiagnostics(log)
	if d.Allowlisted != 1 {
		t.Errorf("Allowlisted = %d, want 1", d.Allowlisted)
	}
	if d.Warnings != 1 {
		t.Errorf("Warnings = %d, want 1 (the non-exempt line only)", d.Warnings)
	}
	if d.Errors != 0 {
		t.Errorf("Errors = %d, want 0", d.Errors)
	}
	var exempted int
	for _, f := range d.Findings {
		if f.AllowID != "" {
			exempted++
		}
	}
	if exempted != 1 {
		t.Errorf("findings carrying an AllowID = %d, want 1 — an exemption must stay auditable", exempted)
	}
}

// TestPromotedWarningTierGoesRed and TestWarningTierIsReportedEvenWhenNotFatal are the two
// assertions the file header cites as what keeps the warning stage from being a permanent
// weakening. They were named there before they were written — review caught the citation pointing
// at nothing, which is worse than an uncovered stage, because it asserts the stage is safe on
// evidence that does not exist.

// TestPromotedWarningTierGoesRed proves the promotion is a FLAG FLIP and not a new feature: the
// same scan result that passes today fails the moment WarningsFatal is true, and the failure
// message names the warning rather than reporting a generic red. The log is a real captured shape
// — a pacman skew line the allowlist does NOT claim — so this cannot pass on a synthetic string
// the matcher was written around.
func TestPromotedWarningTierGoesRed(t *testing.T) {
	const log = "STEP 1/2: RUN pacman -Syu --noconfirm --needed some-package\n" +
		"warning: could not fully load metadata for package some-package-1.0-1\n" +
		"STEP 2/2: RUN true\n"

	d := scanStepDiagnostics(log)
	if d.Warnings != 1 || d.Errors != 0 {
		t.Fatalf("fixture must produce exactly one non-allowlisted warning and no error; got %+v", d)
	}

	staged := diagnosticPolicy{ErrorsFatal: true, WarningsFatal: false}
	if d.fails(staged) {
		t.Errorf("the STAGED policy must not fail on a warning — that is what makes it staged")
	}
	if got := defaultDiagnosticPolicy(); got != staged {
		t.Errorf("defaultDiagnosticPolicy() = %+v, want %+v — the header claims the stage is one field", got, staged)
	}

	promoted := diagnosticPolicy{ErrorsFatal: true, WarningsFatal: true}
	if !d.fails(promoted) {
		t.Errorf("flipping WarningsFatal must turn this log red; it did not")
	}
	msg := d.failure(promoted, "image-build", ".check/x/y/image-build.log")
	if msg == "" || !strings.Contains(msg, "warning") {
		t.Errorf("the promoted failure must name the warning tier; got %q", msg)
	}

	// The allowlist must survive promotion: an EXEMPTED warning stays exempt when the tier is
	// fatal, or promotion would silently invalidate every reviewed exemption at once.
	exempt := scanStepDiagnostics("STEP 1/1: RUN pacman -Syu --needed dbus\n" +
		"warning: dbus-1.16.2-1.1 is up to date -- skipping\n")
	if exempt.Allowlisted != 1 || exempt.Warnings != 0 {
		t.Fatalf("fixture must be fully allowlisted; got %+v", exempt)
	}
	if exempt.fails(promoted) {
		t.Errorf("an allowlisted warning must not fail even under the promoted policy")
	}
}

// TestWarningTierIsReportedEvenWhenNotFatal proves the count is never silently dropped while the
// tier is staged off. A gate that stopped REPORTING what it stopped FAILING on would be a
// weakening dressed as a stage — the operator would have no way to see the debt accumulating, and
// the promotion condition in the header could never be evaluated.
func TestWarningTierIsReportedEvenWhenNotFatal(t *testing.T) {
	const log = "STEP 1/2: RUN pacman -Syu --noconfirm --needed dbus other\n" +
		"warning: dbus-1.16.2-1.1 is up to date -- skipping\n" +
		"warning: could not fully load metadata for package other-1.0-1\n" +
		"STEP 2/2: RUN true\n"

	d := scanStepDiagnostics(log)
	staged := defaultDiagnosticPolicy()
	if d.fails(staged) {
		t.Fatalf("the staged policy must not fail here; got %+v", d)
	}

	// Counted, not erased — both tiers, separately.
	if d.Warnings != 1 {
		t.Errorf("Warnings = %d, want 1 (the non-exempt line)", d.Warnings)
	}
	if d.Allowlisted != 1 {
		t.Errorf("Allowlisted = %d, want 1 (the exempt line)", d.Allowlisted)
	}

	// And REPORTED: the per-run notice a reader actually sees must mention the warning even
	// though nothing failed. This is the half that makes the stage auditable.
	notice := diagNotice(d)
	if notice == "" || !strings.Contains(notice, "warning") {
		t.Errorf("a non-fatal warning must still appear in the run notice; got %q", notice)
	}

	// The shape report must carry it too, so the promotion condition can be evaluated from a
	// real run rather than from a count alone.
	var shapes []string
	for _, sh := range d.shapes() {
		shapes = append(shapes, sh.Text)
	}
	joined := strings.Join(shapes, "\n")
	if !strings.Contains(joined, "could not fully load metadata") {
		t.Errorf("the non-fatal warning is missing from the shape report:\n%s", joined)
	}
}
