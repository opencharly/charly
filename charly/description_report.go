package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

// FormatStepResultsText emits a human-readable per-step report to w.
func FormatStepResultsText(w io.Writer, results []StepResult) {
	var passed, failed, skipped int
	for i := range results {
		step := results[i]
		switch step.Result.Status {
		case TestFail:
			failed++
		case TestSkip:
			skipped++
		default:
			passed++
		}
		renderStep(w, &step)
	}
	fmt.Fprintf(w, "\n%d step%s: %d passed, %d failed, %d skipped\n",
		len(results), plural(len(results)), passed, failed, skipped)
}

func renderStep(w io.Writer, step *StepResult) {
	status := strings.ToUpper(step.Result.Status.String())
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
func FormatStepResultsJSON(w io.Writer, results []StepResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(results)
}

// FormatStepResultsTAP emits TAP v13. Each step is one TAP test point.
func FormatStepResultsTAP(w io.Writer, results []StepResult) {
	fmt.Fprintln(w, "TAP version 13")
	fmt.Fprintf(w, "1..%d\n", len(results))
	for i, step := range results {
		directive := "ok"
		if step.Result.Status == TestFail {
			directive = "not ok"
		}
		fmt.Fprintf(w, "%s %d - %s %s\n", directive, i+1, step.Keyword, step.Text)
		if step.Result.Status == TestFail {
			fmt.Fprintln(w, "  ---")
			fmt.Fprintf(w, "  origin: %q\n", step.Origin)
			fmt.Fprintf(w, "  step_id: %q\n", step.StepID)
			fmt.Fprintf(w, "  verb: %q\n", step.Result.Verb)
			fmt.Fprintf(w, "  message: %q\n", step.Result.Message)
			fmt.Fprintln(w, "  ...")
		}
	}
}

// FormatStepResultsJUnit emits JUnit XML for CI dashboards. Steps surface as
// <testcase> grouped by origin into <testsuite>s.
func FormatStepResultsJUnit(w io.Writer, results []StepResult) error {
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
		case TestFail:
			tc.Failure = &junitFailure{
				Message: step.Result.Message,
				Body:    "Verb: " + step.Result.Verb + "\nStep ID: " + step.StepID,
			}
			suite.Failures++
		case TestSkip:
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
