// Package report hosts the check-run result reporters — the FormatStepResults* family +
// ReportStepResultsCount + ClassifyStepFailures — relocated to a spec fabric slice (#55
// CHECK-ENGINE cone Option A) so the deploy-verify path (candy/plugin-fleet's verify_local.go,
// #55 W3 B3 — a plugin now, not core) reaches them importing zero kit. The reporters operate on
// []spec.StepResult (the CUE-sourced spec wire type, checkresult.cue) + spec.Status; the INFRA-failure
// discriminator IsContainerInfraResult lives in spec/exec (a host primitive over podman exit
// strings), so this slice is its own package to import spec/exec WITHOUT forcing spec/spec to
// import spec/exec (which would cycle: spec/exec already imports spec/spec). sdk/kit re-exports
// each symbol (sdk/kit/report.go) so every existing kit.ReportStepResultsCount /
// kit.FormatStepResults* / kit.ClassifyStepFailures call site (charly core + the candies + sdk)
// is untouched. New consumers reference spec/report directly.
package report

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/opencharly/spec/exec"
	"github.com/opencharly/spec/spec"
)

// Plural returns "" for n==1 and "s" otherwise — the trivial English pluralization used by
// the result reporters and the eventually-retry summaries.
func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// FormatStepResultsText emits a human-readable per-step report to w.
func FormatStepResultsText(w io.Writer, results []spec.StepResult) {
	var passed, failed, skipped int
	for i := range results {
		step := results[i]
		switch step.Result.Status {
		case spec.StatusFail:
			failed++
		case spec.StatusSkip:
			skipped++
		default:
			passed++
		}
		renderStep(w, &step)
	}
	fmt.Fprintf(w, "\n%d step%s: %d passed, %d failed, %d skipped\n",
		len(results), Plural(len(results)), passed, failed, skipped)
}

func renderStep(w io.Writer, step *spec.StepResult) {
	status := strings.ToUpper(step.Result.Status.String())
	// A podman container-SETUP infra failure (R44) is NOT a checks-failed verdict — label it
	// INFRA so a recurring infra storm is loud and distinguishable from a real FAIL at a glance.
	if step.Result.Status == spec.StatusFail && exec.IsContainerInfraResult(step.Result.Message) {
		status = "INFRA"
	}
	retryInfo := ""
	if step.Result.Attempts > 1 {
		retryInfo = fmt.Sprintf(" (attempts=%d, elapsed=%s)",
			step.Result.Attempts, step.Result.TotalElapsed.Round(time.Millisecond))
	}
	msg := step.Result.Message
	if msg != "" {
		msg = "  " + msg
	}
	fmt.Fprintf(w, "  %-5s %s %s  [%s]%s%s\n",
		status, step.Keyword, step.Text, step.StepID, retryInfo, truncate(msg, 200))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// FormatStepResultsJSON emits a structured JSON document.
func FormatStepResultsJSON(w io.Writer, results []spec.StepResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(results)
}

// FormatStepResultsTAP emits TAP v13. Each step is one TAP test point.
func FormatStepResultsTAP(w io.Writer, results []spec.StepResult) {
	fmt.Fprintln(w, "TAP version 13")
	fmt.Fprintf(w, "1..%d\n", len(results))
	for i, step := range results {
		directive := "ok"
		if step.Result.Status == spec.StatusFail {
			directive = "not ok"
		}
		fmt.Fprintf(w, "%s %d - %s %s\n", directive, i+1, step.Keyword, step.Text)
		if step.Result.Status == spec.StatusFail {
			fmt.Fprintln(w, "  ---")
			fmt.Fprintf(w, "  origin: %q\n", step.Origin)
			fmt.Fprintf(w, "  step_id: %q\n", step.StepID)
			fmt.Fprintf(w, "  verb: %q\n", step.Result.Verb)
			fmt.Fprintf(w, "  message: %q\n", step.Result.Message)
			fmt.Fprintln(w, "  ...")
		}
	}
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}
type junitSkipped struct {
	Message string `xml:"message,attr"`
}
type junitTestCase struct {
	XMLName   xml.Name      `xml:"testcase"`
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      float64       `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}
type junitTestSuite struct {
	XMLName  xml.Name        `xml:"testsuite"`
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     float64         `xml:"time,attr"`
	Cases    []junitTestCase `xml:"testcase"`
}
type junitTestSuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestSuite `xml:"testsuite"`
}

// FormatStepResultsJUnit emits a JUnit XML report grouped by step origin.
func FormatStepResultsJUnit(w io.Writer, results []spec.StepResult) error {
	// Group steps by origin (preserving first-seen order).
	var order []string
	byOrigin := map[string]*junitTestSuite{}
	for i := range results {
		step := results[i]
		suite := byOrigin[step.Origin]
		if suite == nil {
			suite = &junitTestSuite{Name: step.Origin}
			byOrigin[step.Origin] = suite
			order = append(order, step.Origin)
		}
		elapsed := step.Result.Elapsed.Seconds()
		if step.Result.TotalElapsed > 0 {
			elapsed = step.Result.TotalElapsed.Seconds()
		}
		tc := junitTestCase{
			Name:      step.Keyword + " " + step.Text,
			Classname: step.Origin,
			Time:      elapsed,
		}
		switch step.Result.Status {
		case spec.StatusFail:
			tc.Failure = &junitFailure{
				Message: step.Result.Message,
				Body:    "Verb: " + step.Result.Verb + "\nStep ID: " + step.StepID,
			}
			suite.Failures++
		case spec.StatusSkip:
			tc.Skipped = &junitSkipped{Message: step.Result.Message}
			suite.Skipped++
		}
		suite.Cases = append(suite.Cases, tc)
		suite.Time += elapsed
	}

	var suites junitTestSuites
	for _, o := range order {
		s := byOrigin[o]
		s.Tests = len(s.Cases)
		suites.Suites = append(suites.Suites, *s)
	}

	fmt.Fprintln(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(suites); err != nil {
		return err
	}
	fmt.Fprintln(w)
	return nil
}

// ReportStepResults writes results in the requested format ("json"/"tap"/"junit", default
// text), dispatching to the FormatStepResults* family above.
func ReportStepResults(w io.Writer, results []spec.StepResult, format string) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		_ = FormatStepResultsJSON(w, results)
	case "tap":
		FormatStepResultsTAP(w, results)
	case "junit":
		_ = FormatStepResultsJUnit(w, results)
	default:
		FormatStepResultsText(w, results)
	}
}

// ReportStepResultsCount renders results per format via ReportStepResults and returns how many
// results ended in a FAIL verdict (no infra/check split — a caller needing that split uses
// ClassifyStepFailures directly).
func ReportStepResultsCount(w io.Writer, results []spec.StepResult, format string) int {
	ReportStepResults(w, results, format)
	fails := 0
	for i := range results {
		if results[i].Result.Status == spec.StatusFail {
			fails++
		}
	}
	return fails
}

// ClassifyStepFailures splits FAIL results into genuine check failures vs. container-setup
// INFRA failures (IsContainerInfraResult) — the shared discriminator every check-run caller
// (box/live/feature, in-core and externalized) uses to map its own exit-code error type. An
// infra failure means the check command never ran (a probe container's mount/passwd-gen raced
// concurrent build churn); it must never read as a checks-failed verdict.
func ClassifyStepFailures(results []spec.StepResult) (checkFails, infraFails int) {
	for i := range results {
		if results[i].Result.Status != spec.StatusFail {
			continue
		}
		if exec.IsContainerInfraResult(results[i].Result.Message) {
			infraFails++
		} else {
			checkFails++
		}
	}
	return checkFails, infraFails
}
