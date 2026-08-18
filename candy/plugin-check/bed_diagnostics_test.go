package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// cachyosBuildExcerpt is a VERBATIM excerpt of a real `charly box build` log from a CachyOS
// bed run — the exact run whose summary.yml reported `image-build ok: true` while 51 install
// scriptlets never executed. It exercises, in one fixture, every property the gate must have:
// pacman's non-fatal `error:` refusals, the sandbox `warning:` residue, the two upstream
// local-newer-than-repo warnings the allowlist forgives, and the build-cache markers.
const cachyosBuildExcerpt = `STEP 1/18: FROM docker.io/cachyos/cachyos-v3:latest
--> Using cache 734340858c2e4c7178ac1fed60ef29952211d9914988a4741a2c3a1072110f04
STEP 2/18: RUN pacman -Syu --noconfirm
warning: pacman-contrib: local (1.13.1-2.1) is newer than cachyos-extra-v3 (1.13.1-1.2)
warning: zstd: local (1.5.7-3) is newer than cachyos-v3 (1.5.7-2)
warning: iproute2-7.1.0-1.1 is up to date -- reinstalling
installing dnsmasq...
warning: could not isolate the network for "/usr/bin/ldconfig" (Operation not permitted)
:: Running post-transaction hooks...
( 1/18) Creating system user accounts...
could not isolate the network (Operation not permitted)
refusing to run "/usr/share/libalpm/scripts/systemd-hook" with network access; set DisableSandboxNetwork in pacman.conf to override
error: command failed to execute correctly
( 2/18) Creating temporary files...
error: command failed to execute correctly
STEP 3/18: USER 1000
`

// TestScanCatchesTheSwallowedFailure is the gate's reason to exist, asserted directly on the
// captured log: the build exited 0, and the scan must still report the refusals as errors.
func TestScanCatchesTheSwallowedFailure(t *testing.T) {
	d := scanStepDiagnostics(cachyosBuildExcerpt)

	if d.Errors != 2 {
		t.Errorf("Errors = %d, want 2 (the pacman scriptlet refusals a zero exit code hid)", d.Errors)
	}
	if !d.fails(defaultDiagnosticPolicy()) {
		t.Fatal("a log carrying pacman `error:` refusals must FAIL the default policy; " +
			"a gate that cannot go red on this exact input is worse than no gate")
	}
	msg := d.failure(defaultDiagnosticPolicy(), "image-build", "/tmp/image-build.log")
	for _, want := range []string{"image-build", "exited 0", "2 error line(s)", "command failed to execute correctly"} {
		if !strings.Contains(msg, want) {
			t.Errorf("failure message %q missing %q — the operator must see the finding, not just a verdict", msg, want)
		}
	}
}

// TestAllowlistForgivesOnlyTheReviewedUpstreamSkew proves the exemptions are load-bearing AND
// narrow: the two CachyOS local-newer-than-repo warnings are claimed by name, and every other
// warning in the SAME log stays counted.
func TestAllowlistForgivesOnlyTheReviewedUpstreamSkew(t *testing.T) {
	d := scanStepDiagnostics(cachyosBuildExcerpt)

	if d.Allowlisted != 2 {
		t.Errorf("Allowlisted = %d, want 2 (zstd + pacman-contrib)", d.Allowlisted)
	}
	if d.Warnings != 2 {
		t.Errorf("Warnings = %d, want 2 (`is up to date -- reinstalling` and the ldconfig "+
			"sandbox residue) — the allowlist must not absorb warnings it does not name", d.Warnings)
	}

	claimed := map[string]string{}
	for _, f := range d.Findings {
		if f.AllowID != "" {
			claimed[f.AllowID] = f.Text
		}
	}
	if len(claimed) != 2 {
		t.Fatalf("allowlist claimed %d distinct entries, want exactly 2: %v", len(claimed), claimed)
	}
	for _, id := range []string{"cachyos-zstd-local-newer-than-repo", "cachyos-pacman-contrib-local-newer-than-repo"} {
		if _, ok := claimed[id]; !ok {
			t.Errorf("allowlist entry %s never fired on the log it was written for", id)
		}
	}
}

// TestAllowlistDoesNotSwallowARealFinding is the anti-regression the brief asks for by name.
// Each case is a line an over-broad exemption would have absorbed — a different package, a
// different message from the SAME package, and an ERROR wearing the allowlisted wording.
func TestAllowlistDoesNotSwallowARealFinding(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{
			"different package, same shape",
			`warning: openssl: local (3.6.1-1) is newer than cachyos-v3 (3.6.0-2)`,
		},
		{
			"allowlisted package, different message",
			`warning: zstd: signature from "CachyOS Builder" is unknown trust`,
		},
		{
			"allowlisted package, truncated to a prefix",
			`warning: zstd: local (1.5.7-3) is newer`,
		},
		{
			"error tier wearing the allowlisted wording — the error allowlist is empty",
			`error: zstd: local (1.5.7-3) is newer than cachyos-v3 (1.5.7-2)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := scanStepDiagnostics(tc.line)
			if len(d.Findings) != 1 {
				t.Fatalf("scan produced %d findings, want 1: %+v", len(d.Findings), d.Findings)
			}
			if id := d.Findings[0].AllowID; id != "" {
				t.Fatalf("allowlist entry %q swallowed a real finding: %q", id, tc.line)
			}
			if d.Warnings+d.Errors != 1 {
				t.Fatalf("finding was not counted (warnings=%d errors=%d) for %q", d.Warnings, d.Errors, tc.line)
			}
		})
	}
}

// TestScanIgnoresProseThatMerelyMENTIONSAnError is the false-positive gate. Every line here
// appears verbatim in real bed logs, and a substring scan would fire on all of them — which
// is how a diagnostics gate gets switched off within a day of landing.
func TestScanIgnoresProseThatMerelyMENTIONSAnError(t *testing.T) {
	benign := []string{
		`  PASS  check invoking relay-wrapper with no port argument prints a usage error and exits non-zero`,
		`[11/11] STEP 79/91: LABEL ai.opencharly.description='{"check":"…prints a usage error…","stderr":[{"op":"contains","value":"Usage"}]}'`,
		`Installation finished. No error reported.`,
		`   Compiling thiserror v2.0.12`,
		`            if "ValueError" in str(e):`,
		`>>> Curl error (2): Timeout was reached for https://example.invalid`,
		`+ CFLAGS='-O2 -Werror=format-security'`,
		`2026-07-06T22:22:02.695+0200 [DEBUG] plugin.stdio: received EOF, stopping recv loop`,
	}
	for _, line := range benign {
		if severity, pattern, ok := classifyDiagnosticLine(line); ok {
			t.Errorf("false positive: recognizer %q classified %q as %s", pattern, line, severity)
		}
	}
}

// TestScanRecognizesEveryEmitterShapeSeenInTheCorpus pins the recognizers to the shapes real
// tools actually emit, so a future narrowing of the anchors cannot silently blind the gate.
func TestScanRecognizesEveryEmitterShapeSeenInTheCorpus(t *testing.T) {
	cases := []struct {
		line string
		want diagnosticSeverity
	}{
		{`error: command failed to execute correctly`, severityError},
		{`fatal: No names found, cannot describe anything.`, severityError},
		{`>>> error: can't create transaction lock on /usr/lib/sysimage/rpm/.rpm.lock`, severityError},
		{`==> ERROR: failed to detect root filesystem`, severityError},
		{`charly: error: starting VM charly-k3s-vm: domain is already running`, severityError},
		{`/usr/sbin/grub-probe: error: failed to get canonical path of ` + "`overlay`", severityError},
		{`time="2026-07-02T23:03:55+02:00" level=error msg="did not get container create message"`, severityError},
		{`2026-07-04T23:45:34.336+0200 [ERROR] plugin: something broke`, severityError},
		{`warning: could not isolate the network for "/usr/bin/ldconfig" (Operation not permitted)`, severityWarning},
		{`==> WARNING: sd-vconsole: "/etc/vconsole.conf" not found, will use default values`, severityWarning},
		{`xorriso : WARNING : -volid text does not comply to ISO 9660 rules`, severityWarning},
		{`update-alternatives: warning: skip creation of /usr/share/man/man1/x.1.gz`, severityWarning},
		{`Warning: ResolveBoxEngineForDeploy: charly.yml unavailable for read`, severityWarning},
		{`gpg: WARNING: "--no-use-agent" is an obsolete option - it has no effect`, severityWarning},
		{`time="2026-07-02T23:03:55+02:00" level=warning msg="The storage 'driver' option should be set"`, severityWarning},
		{`2026-07-04T23:45:34.336+0200 [WARN]  plugin: plugin failed to exit gracefully`, severityWarning},
	}
	for _, tc := range cases {
		severity, _, ok := classifyDiagnosticLine(tc.line)
		if !ok {
			t.Errorf("recognizers missed a real diagnostic: %q", tc.line)
			continue
		}
		if severity != tc.want {
			t.Errorf("%q classified %s, want %s", tc.line, severity, tc.want)
		}
	}
}

// TestErrorTierIsFatalAndWarningTierIsNot pins the FIRST-iteration disposition, so a change to
// either tier is a deliberate, reviewed edit rather than a drift nobody notices.
func TestErrorTierIsFatalAndWarningTierIsNot(t *testing.T) {
	p := defaultDiagnosticPolicy()
	if !p.ErrorsFatal {
		t.Error("ErrorsFatal must be true: an `error:` line under a zero exit code is the " +
			"swallowed failure this gate exists to catch")
	}
	if p.WarningsFatal {
		t.Error("WarningsFatal is true — promoting the warning tier reds beds the error tier " +
			"leaves green; promote it only with the file header's measured justification updated")
	}

	warningsOnly := scanStepDiagnostics(`warning: iproute2-7.1.0-1.1 is up to date -- reinstalling`)
	if warningsOnly.fails(p) {
		t.Error("a warnings-only log must not fail the first-iteration policy")
	}
}

// TestWarningTierIsReportedEvenWhenNotFatal proves the staged tier is COUNTED, not dropped:
// the numbers reach summary.yml on a passing step, so a reader can see the debt the gate is
// choosing not to fail on yet.
func TestWarningTierIsReportedEvenWhenNotFatal(t *testing.T) {
	d := scanStepDiagnostics(cachyosBuildExcerpt)
	if d.Warnings == 0 {
		t.Fatal("warnings were not counted at all")
	}

	var buf bytes.Buffer
	writeStepDiagnostics(&buf, "    ", d)
	out := buf.String()
	for _, want := range []string{"warnings: 2", "errors: 2", "allowlisted: 2", "cache_hits: 1", "cache_steps: 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("step diagnostics block missing %q:\n%s", want, out)
		}
	}
	if notice := diagNotice(d); !strings.Contains(notice, "warnings=2") || !strings.Contains(notice, "cache=1/3") {
		t.Errorf("console notice = %q, want the warning count and the cache ratio", notice)
	}
}

// TestPromotedWarningTierGoesRed is the proof the stage is a flag, not a hole: the SAME
// captured log that passes the warning tier today fails the moment the tier is promoted, and
// the allowlisted upstream skew still does not fail it.
func TestPromotedWarningTierGoesRed(t *testing.T) {
	promoted := diagnosticPolicy{ErrorsFatal: true, WarningsFatal: true}

	d := scanStepDiagnostics(cachyosBuildExcerpt)
	if !d.fails(promoted) {
		t.Fatal("the promoted policy must fail a log carrying real warnings")
	}
	msg := d.failure(promoted, "image-build", "/tmp/image-build.log")
	if !strings.Contains(msg, "warning line(s)") {
		t.Errorf("promoted failure message %q does not name the warning tier", msg)
	}

	// The allowlist must still hold under promotion — otherwise the exemptions are decorative
	// and the promotion would red every CachyOS bed on residue charly cannot fix.
	allowlistedOnly := scanStepDiagnostics(
		"warning: zstd: local (1.5.7-3) is newer than cachyos-v3 (1.5.7-2)\n" +
			"warning: pacman-contrib: local (1.13.1-2.1) is newer than cachyos-extra-v3 (1.13.1-1.2)\n")
	if allowlistedOnly.fails(promoted) {
		t.Error("allowlisted-only warnings must not fail even the promoted policy")
	}
	if allowlistedOnly.Allowlisted != 2 {
		t.Errorf("Allowlisted = %d, want 2", allowlistedOnly.Allowlisted)
	}
}

// TestCacheRatioMakesAZeroReadable covers the cache-awareness requirement: a warm-cache run
// suppresses the very output this gate reads, so "0 warnings" must arrive with the ratio that
// lets a reader tell a real zero from a replayed one.
func TestCacheRatioMakesAZeroReadable(t *testing.T) {
	warm := scanStepDiagnostics(
		"STEP 1/3: FROM base\n--> Using cache abc\nSTEP 2/3: RUN true\n--> Using cache def\nSTEP 3/3: USER 1000\n")
	if warm.CacheHits != 2 || warm.CacheSteps != 3 {
		t.Errorf("cache = %d/%d, want 2/3", warm.CacheHits, warm.CacheSteps)
	}
	if warm.Warnings != 0 || warm.Errors != 0 {
		t.Errorf("a clean warm build must scan clean, got warnings=%d errors=%d", warm.Warnings, warm.Errors)
	}

	// A multi-stage build prefixes its markers; docker writes three dashes. Both must count,
	// or the ratio silently reads 0/0 and a reader mistakes a warm build for a cold one.
	multi := scanStepDiagnostics("[2/11] STEP 7/91: RUN true\n---> Using cache abc\n")
	if multi.CacheHits != 1 || multi.CacheSteps != 1 {
		t.Errorf("multi-stage cache = %d/%d, want 1/1", multi.CacheHits, multi.CacheSteps)
	}

	// No build markers at all: the rollup must SAY so rather than let a missing ratio read as
	// a cold-cache zero.
	var buf bytes.Buffer
	writeRunDiagnostics(&buf, stepDiagnostics{})
	if !strings.Contains(buf.String(), "cache_ratio: not-applicable-no-build-steps") {
		t.Errorf("run rollup must state when no cache ratio applies:\n%s", buf.String())
	}
}

// TestRunRollupPrintsEveryUsedExemptionWithItsJustification covers the "report, do not merely
// fail" requirement: the verdict is auditable from summary.yml alone, including WHY each
// suppression was accepted, re-read on every run instead of reviewed once and inherited.
func TestRunRollupPrintsEveryUsedExemptionWithItsJustification(t *testing.T) {
	d := scanStepDiagnostics(cachyosBuildExcerpt)

	var buf bytes.Buffer
	writeRunDiagnostics(&buf, d)
	out := buf.String()

	for _, want := range []string{
		"errors: 2", "warnings: 2", "allowlisted: 2",
		"errors_fatal: true", "warnings_fatal: false",
		"allowlist_used:",
		"id: cachyos-zstd-local-newer-than-repo",
		"id: cachyos-pacman-contrib-local-newer-than-repo",
		"suppressed: 1",
		"freshly published image",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("run rollup missing %q:\n%s", want, out)
		}
	}

	// An unused exemption must NOT be printed — the report describes THIS run, not the table.
	clean := scanStepDiagnostics("STEP 1/1: FROM base\n")
	buf.Reset()
	writeRunDiagnostics(&buf, clean)
	if strings.Contains(buf.String(), "allowlist_used:") {
		t.Errorf("clean run printed an allowlist section:\n%s", buf.String())
	}
}

// TestFindingsAreReportedAsShapesNotRawLines proves the report stays readable at the scale the
// RCA hit: 51 identical refusals are ONE shape seen 51 times, and the count is the signal.
func TestFindingsAreReportedAsShapesNotRawLines(t *testing.T) {
	log := strings.Repeat("error: command failed to execute correctly\n", 51) +
		"warning: iproute2-7.1.0-1.1 is up to date -- reinstalling\n"

	d := scanStepDiagnostics(log)
	if d.Errors != 51 {
		t.Fatalf("Errors = %d, want 51", d.Errors)
	}
	shapes := d.shapes()
	if len(shapes) != 2 {
		t.Fatalf("shapes = %d, want 2 distinct shapes: %+v", len(shapes), shapes)
	}
	// Errors lead, so the summary opens with the thing most worth reading.
	if shapes[0].Severity != severityError || shapes[0].Count != 51 {
		t.Errorf("first shape = %+v, want the error shape with count 51", shapes[0])
	}
	if shapes[0].Line != 1 {
		t.Errorf("first-occurrence line = %d, want 1", shapes[0].Line)
	}
}

// TestShapeNormalizationFoldsCountersNotIdentity keeps the shape count honest in BOTH
// directions. Version and progress digits fold, so one recurring finding does not shatter into
// dozens of singletons — but the package NAME and the message do not fold, because "which
// package" and "which failure" are the whole content of the finding.
func TestShapeNormalizationFoldsCountersNotIdentity(t *testing.T) {
	d := scanStepDiagnostics(
		// Same package, two versions — must fold to ONE shape seen twice.
		"warning: git-2.51.1-1.1 is up to date -- reinstalling\n" +
			"warning: git-2.52.0-1.1 is up to date -- reinstalling\n" +
			// Different package, same message — must stay its own shape.
			"warning: curl-8.21.0-1.1 is up to date -- reinstalling\n" +
			// Same package, different message — must stay its own shape.
			"warning: git-2.51.1-1.1 is out of date\n")

	shapes := d.shapes()
	if len(shapes) != 3 {
		t.Fatalf("shapes = %d, want 3 (the two git versions fold; the other package and the "+
			"other message do not): %+v", len(shapes), shapes)
	}
	byText := map[string]int{}
	for _, s := range shapes {
		byText[s.Text] = s.Count
		if strings.Contains(s.Text, "2.51.1") || strings.Contains(s.Text, "8.21.0") {
			t.Errorf("shape %q kept a version literal; counters must fold to N", s.Text)
		}
	}
	if got := byText["warning: git-N.N.N-N.N is up to date -- reinstalling"]; got != 2 {
		t.Errorf("version-only variants folded to count %d, want 2", got)
	}
	if _, ok := byText["warning: curl-N.N.N-N.N is up to date -- reinstalling"]; !ok {
		t.Error("a different package collapsed into another package's shape — which package " +
			"is affected is the content of the finding, not noise")
	}
}

// TestSummaryTextIsQuotedForYAML guards the report itself: diagnostic text is arbitrary tool
// output, and an unquoted colon or quote would make summary.yml unparseable — turning a
// finding into a corrupt artifact.
func TestSummaryTextIsQuotedForYAML(t *testing.T) {
	d := scanStepDiagnostics(`error: refusing to run "/usr/share/libalpm/scripts/systemd-hook": bad\path`)

	var buf bytes.Buffer
	writeStepDiagnostics(&buf, "    ", d)
	out := buf.String()
	if !strings.Contains(out, `text: "error: refusing to run \"/usr/share/libalpm/scripts/systemd-hook\": bad\\path"`) {
		t.Errorf("diagnostic text was not YAML-quoted:\n%s", out)
	}
}

// TestCleanStepAddsNothingToTheSummary keeps the common case unchanged: a step whose log is
// clean and carries no build markers keeps its existing three-line shape, so the gate does not
// make every summary.yml diff noisy.
func TestCleanStepAddsNothingToTheSummary(t *testing.T) {
	var buf bytes.Buffer
	writeStepDiagnostics(&buf, "    ", scanStepDiagnostics("deploy complete\nall good\n"))
	if buf.Len() != 0 {
		t.Errorf("a clean step emitted a diagnostics block:\n%s", buf.String())
	}
}

// TestBedSummaryStaysParseableYAMLWithDiagnostics is the artifact guard. summary.yml is
// hand-rolled (dependency-free, diff-friendly), so adding a nested findings list carrying raw
// tool output is exactly the change that could quietly produce an unparseable file — and a
// summary a reader's tooling cannot open is a finding lost, which is the failure this whole
// gate exists to prevent. Parses the REAL emitter output with a real YAML parser.
func TestBedSummaryStaysParseableYAMLWithDiagnostics(t *testing.T) {
	dir := t.TempDir()
	res := &bedRunResult{
		Bed:    "check-charly-selftest-pod",
		CalVer: "2026.230.1200",
		OK:     false,
		Step: []stepResult{
			{Name: "image-build", Duration: 110 * time.Second, OK: false, Diag: scanStepDiagnostics(cachyosBuildExcerpt)},
			{Name: "check-live", Duration: 4 * time.Second, OK: true},
		},
		FailExitCode: CheckFailExitCode,
	}
	writeBedSummary(dir, res)

	raw, err := os.ReadFile(filepath.Join(dir, "summary.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Bed   string `yaml:"bed"`
		Steps []struct {
			Name        string `yaml:"name"`
			OK          bool   `yaml:"ok"`
			Diagnostics struct {
				Errors      int `yaml:"errors"`
				Warnings    int `yaml:"warnings"`
				Allowlisted int `yaml:"allowlisted"`
				CacheHits   int `yaml:"cache_hits"`
				CacheSteps  int `yaml:"cache_steps"`
				Findings    []struct {
					Severity    string `yaml:"severity"`
					Count       int    `yaml:"count"`
					Text        string `yaml:"text"`
					Allowlisted string `yaml:"allowlisted"`
				} `yaml:"findings"`
			} `yaml:"diagnostics"`
		} `yaml:"steps"`
		Diagnostics struct {
			Errors        int  `yaml:"errors"`
			Warnings      int  `yaml:"warnings"`
			Allowlisted   int  `yaml:"allowlisted"`
			ErrorsFatal   bool `yaml:"errors_fatal"`
			WarningsFatal bool `yaml:"warnings_fatal"`
			AllowlistUsed []struct {
				ID         string `yaml:"id"`
				Suppressed int    `yaml:"suppressed"`
				Why        string `yaml:"why"`
			} `yaml:"allowlist_used"`
		} `yaml:"diagnostics"`
		OK bool `yaml:"ok"`
	}
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("summary.yml is not parseable YAML: %v\n%s", err, raw)
	}

	if len(parsed.Steps) != 2 {
		t.Fatalf("steps = %d, want 2\n%s", len(parsed.Steps), raw)
	}
	build := parsed.Steps[0]
	if build.Diagnostics.Errors != 2 || build.Diagnostics.Warnings != 2 || build.Diagnostics.Allowlisted != 2 {
		t.Errorf("per-step counts = %+v, want errors=2 warnings=2 allowlisted=2", build.Diagnostics)
	}
	if build.Diagnostics.CacheHits != 1 || build.Diagnostics.CacheSteps != 3 {
		t.Errorf("per-step cache = %d/%d, want 1/3", build.Diagnostics.CacheHits, build.Diagnostics.CacheSteps)
	}
	if len(build.Diagnostics.Findings) == 0 {
		t.Fatal("per-step findings list is empty")
	}
	if build.Diagnostics.Findings[0].Severity != "error" {
		t.Errorf("findings lead with %q, want the error tier first", build.Diagnostics.Findings[0].Severity)
	}
	// The clean step must NOT carry a diagnostics block at all.
	if parsed.Steps[1].Diagnostics.Errors != 0 || len(parsed.Steps[1].Diagnostics.Findings) != 0 {
		t.Errorf("clean step gained a diagnostics block: %+v", parsed.Steps[1].Diagnostics)
	}

	if !parsed.Diagnostics.ErrorsFatal || parsed.Diagnostics.WarningsFatal {
		t.Errorf("run rollup disposition = %+v, want errors_fatal=true warnings_fatal=false", parsed.Diagnostics)
	}
	if len(parsed.Diagnostics.AllowlistUsed) != 2 {
		t.Fatalf("allowlist_used = %d entries, want 2", len(parsed.Diagnostics.AllowlistUsed))
	}
	for _, u := range parsed.Diagnostics.AllowlistUsed {
		if u.Why == "" {
			t.Errorf("allowlist entry %s printed no justification — an exemption a reader "+
				"cannot audit is an exemption nobody re-reviews", u.ID)
		}
	}
}
