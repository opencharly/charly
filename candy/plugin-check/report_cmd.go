package check

// report_cmd.go — the past-run inspection leaves (`charly check list` /
// `charly check report`) + the `charly check box --format yaml` reporter (P12).
//
// The yaml payload is the AI benchmark scorer's INPUT format (ParseCharlyTestOutput
// consumes it), so its shape is a contract: box/mode + a per-scored-step list + a
// pass/fail/skip summary. It uses the SAME spec.CheckRunResults / spec.StepScore /
// spec.ScoreSummary CUE-sourced wire types the plugin scorer (score.go) consumes —
// ONE definition, no local dup (R5).

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
	"gopkg.in/yaml.v3"
)

// CheckListRunsCmd lists past runs across all scores.
type CheckListRunsCmd struct{}

func (c *CheckListRunsCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	runs, err := ListRuns(context.TODO(), cwd)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("No runs found under .check/.")
		return nil
	}
	fmt.Printf("%-20s  %-25s  %-10s  %s\n", "SCORE", "RUN_ID", "STATUS", "STARTED")
	for _, r := range runs {
		started := r.StartedUTC.Format("2006-01-02 15:04:05Z")
		fmt.Printf("%-20s  %-25s  %-10s  %s\n", r.Score, r.RunID, r.Status, started)
	}
	return nil
}

// CheckReportCmd prints a past result-<calver>.yml. Result files live under
// .check/<score>/results/ (where the harness writes them), so this reads .check
// (the former in-core leaf read a stale .harness path that never held results — a
// pre-existing divergence corrected in the relocation).
type CheckReportCmd struct {
	Score  string `arg:"" optional:"" help:"Score name (default: latest)"`
	Calver string `arg:"" optional:"" help:"Calver of the result to display (default: latest)"`
}

func (c *CheckReportCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	resultsRoot := fmt.Sprintf("%s/.check", cwd)
	if c.Score == "" {
		entries, err := os.ReadDir(resultsRoot)
		if err != nil {
			return fmt.Errorf("no .check directory in %s", cwd)
		}
		var newest os.DirEntry
		var newestT int64
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if info, err := e.Info(); err == nil && info.ModTime().Unix() > newestT {
				newestT = info.ModTime().Unix()
				newest = e
			}
		}
		if newest == nil {
			return fmt.Errorf("no scores under %s", resultsRoot)
		}
		c.Score = newest.Name()
	}
	resultsDir := fmt.Sprintf("%s/%s/results", resultsRoot, c.Score)
	if c.Calver == "" {
		entries, err := os.ReadDir(resultsDir)
		if err != nil {
			return fmt.Errorf("no results directory: %s", resultsDir)
		}
		var latest string
		for _, e := range entries {
			n := e.Name()
			if len(n) > 7 && n[:7] == "result-" && n[len(n)-4:] == ".yml" {
				if n > latest {
					latest = n
				}
			}
		}
		if latest == "" {
			return fmt.Errorf("no result files under %s", resultsDir)
		}
		c.Calver = latest[7 : len(latest)-4]
	}
	path := fmt.Sprintf("%s/result-%s.yml", resultsDir, c.Calver)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// emitImageTestYAML writes the `charly check box --format yaml` payload that the
// benchmark scorer (ParseCharlyTestOutput) consumes:
//
//	box: <ref>
//	mode: box | run
//	step:
//	  - id, origin, text, tag, keyword, verb, status
//	summary: { total, pass, fail, skip }
//
// liveContainer non-empty ⇒ mode "run"; empty ⇒ mode "box". Only check:/agent-check:
// steps are emitted (the scored success criteria). It builds the SAME spec.*
// scoring wire types score.go's ParseCharlyTestOutput parses back (R5, one shape).
func emitImageTestYAML(w io.Writer, imageRef, liveContainer string, steps []kit.StepResult) error {
	mode := "box"
	if liveContainer != "" {
		mode = "run"
	}
	out := spec.CheckRunResults{Box: imageRef, Mode: mode}
	for _, sp := range steps {
		if sp.Keyword != string(kit.KwCheck) && sp.Keyword != string(kit.KwAgentCheck) {
			continue // only scored steps land in the --format yaml payload
		}
		ss := spec.StepScore{
			ID:      sp.StepID,
			Origin:  sp.Origin,
			Text:    sp.Text,
			Keyword: sp.Keyword,
			Verb:    sp.Result.Verb,
			Status:  sp.Result.Status.String(),
		}
		out.Step = append(out.Step, ss)
		out.Summary.Total++
		switch ss.Status {
		case "pass":
			out.Summary.Pass++
		case "fail":
			out.Summary.Fail++
		case "skip":
			out.Summary.Skip++
		}
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
