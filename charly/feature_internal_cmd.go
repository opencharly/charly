package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// feature_internal_cmd.go implements the three HIDDEN core commands that expose the in-core
// plan-description machinery (LoadConfig / ScanCandy / the Step plan model / validatePlanSteps)
// to the externalized `charly feature …` COMMAND plugin (candy/plugin-feature). The plugin
// re-expresses each operator-facing `charly feature` leaf (list / pending / validate) as a
// shell-back through these sanctioned hidden verbs — the SAME `charly __cli-model` /
// `charly __plugin-providers` / `charly __preempt-status` internal-command pattern — so the
// `charly feature list`/`pending`/`validate` CLI is unchanged while the command implementation
// moved OUT of the core binary.
//
// What STAYS core (invoked ONLY here): the unified loader (LoadConfig / ScanCandy — the deepest
// core), the Step plan model (StepKind / Kind / IsAgent / KeywordText), and validatePlanSteps —
// which is SHARED with `charly box validate` (validate.go), so it cannot move or be duplicated
// (R3). The hidden commands render via an io.Writer so a unit test can drive the real loader +
// plan model against a fixture project and assert the output.

// FeatureListInternalCmd: `charly __feature-list [kind]` (hidden machinery). Prints every
// kind: entity and its plan summary exactly as the former `charly feature list` did.
type FeatureListInternalCmd struct {
	Kind string `arg:"" optional:"" help:"Restrict to one kind (candy|box). Default: all."`
}

func (c *FeatureListInternalCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	return renderFeatureList(cwd, c.Kind, os.Stdout)
}

// renderFeatureList loads the project config + candies and prints each entity's description
// summary + plan-step counts to out. Split from the command Run so a unit test can drive it
// against a fixture project dir (the loader read is the only side effect).
func renderFeatureList(dir, kindFilter string, out io.Writer) error {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanCandy(dir)
	if err != nil {
		return fmt.Errorf("scanning candies: %w", err)
	}

	filter := strings.ToLower(strings.TrimSpace(kindFilter))

	if filter == "" || filter == "candy" {
		for name, layer := range layers {
			if layer == nil {
				continue
			}
			summarizeDesc(out, "candy", name, layer.Description, layer.plan)
		}
	}
	if filter == "" || filter == "box" {
		for name, img := range cfg.Box {
			if img.Description != "" || len(img.Plan) > 0 {
				summarizeDesc(out, "box", name, img.Description, img.Plan)
			}
		}
	}
	return nil
}

// summarizeDesc prints one entity's description summary + plan-step/check counts to out.
func summarizeDesc(out io.Writer, kind, name string, desc string, plan []Step) {
	if desc == "" && len(plan) == 0 {
		fmt.Fprintf(out, "%s %s: (no description)\n", kind, name)
		return
	}
	summary := "(empty)"
	if s := descriptionInfo(desc); s != "" {
		summary = s
	}
	nChecks := 0
	for _, st := range plan {
		if st.Check != "" || st.AgentCheck != "" {
			nChecks++
		}
	}
	fmt.Fprintf(out, "%s %s: %q (%d step%s, %d check%s)\n",
		kind, name, summary, len(plan), plural(len(plan)), nChecks, plural(nChecks))
}

// FeaturePendingInternalCmd: `charly __feature-pending [entity]` (hidden machinery). Lists the
// agent-graded plan steps (agent-run:/agent-check:) exactly as the former `charly feature
// pending` did.
type FeaturePendingInternalCmd struct {
	Entity string `arg:"" optional:"" help:"Entity identifier (e.g. candy:redis); default: all"`
}

func (c *FeaturePendingInternalCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	return renderFeaturePending(cwd, c.Entity, os.Stdout)
}

// renderFeaturePending loads the project config + candies and prints every agent-graded plan
// step to out. Split from the command Run for the same testability reason as renderFeatureList.
func renderFeaturePending(dir, entityFilter string, out io.Writer) error {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanCandy(dir)
	if err != nil {
		return fmt.Errorf("scanning candies: %w", err)
	}

	filter := strings.ToLower(strings.TrimSpace(entityFilter))

	scan := func(kind, name string, desc string, plan []Step) {
		if desc == "" && len(plan) == 0 {
			return
		}
		eid := kind + ":" + name
		if filter != "" && filter != eid && filter != kind {
			return
		}
		for i := range plan {
			step := plan[i]
			if step.IsAgent() {
				fmt.Fprintf(out, "%s — step %d: %s %q (agent-graded)\n", eid, i, keywordOf(&step), step.KeywordText())
			}
		}
	}

	for name, layer := range layers {
		if layer != nil {
			scan("candy", name, layer.Description, layer.plan)
		}
	}
	for name, img := range cfg.Box {
		scan("box", name, img.Description, img.Plan)
	}
	return nil
}

// FeatureValidateInternalCmd: `charly __feature-validate [entity]` (hidden machinery). Parses
// every plan: block and reports issues exactly as the former `charly feature validate` did
// (the verb `charly box validate` also invokes — via validatePlanSteps, which STAYS core, R3).
type FeatureValidateInternalCmd struct {
	Entity string `arg:"" optional:"" help:"Entity identifier (e.g. candy:redis); default: all"`
}

func (c *FeatureValidateInternalCmd) Run() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	return renderFeatureValidate(cwd, c.Entity, os.Stdout, os.Stderr)
}

// renderFeatureValidate loads the project config + candies, validates every plan: block via the
// shared validatePlanSteps, writes the success line to out (errors to errOut), and returns a
// non-nil error (so the exit code reflects failure) when any plan block is invalid. Split from
// the command Run so a unit test can drive the real loader + plan model against a fixture.
func renderFeatureValidate(dir, entityFilter string, out, errOut io.Writer) error {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	layers, err := ScanCandy(dir)
	if err != nil {
		return fmt.Errorf("scanning candies: %w", err)
	}

	filter := strings.ToLower(strings.TrimSpace(entityFilter))
	var errs []string

	validate := func(kind, name string, desc string, plan []Step) {
		if desc == "" && len(plan) == 0 {
			return
		}
		eid := kind + ":" + name
		if filter != "" && filter != eid && filter != kind {
			return
		}
		errs = append(errs, validatePlanSteps(desc, plan, eid)...)
	}

	for name, layer := range layers {
		if layer != nil {
			validate("candy", name, layer.Description, layer.plan)
		}
	}
	for name, img := range cfg.Box {
		validate("box", name, img.Description, img.Plan)
	}

	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(errOut, e)
		}
		return fmt.Errorf("%d validation error(s)", len(errs))
	}
	fmt.Fprintln(out, "All plan blocks validated successfully.")
	return nil
}

// validatePlanSteps runs static checks against a description + its plan steps (complementary to
// ValidatePlan in step_validate.go, which validates list structure / depends_on). It STAYS core
// — it is SHARED by `charly box validate` (validate.go) AND the hidden `charly __feature-validate`
// command above, so it cannot move into the externalized plugin (R3):
//
//   - description non-empty
//   - every step has exactly one keyword (StepKind())
//   - run/check steps carry exactly one Op verb; agent-* steps carry none
//
// Returns a list of human-readable error strings (empty if OK).
func validatePlanSteps(desc string, plan []Step, eid string) []string {
	var errs []string
	if strings.TrimSpace(desc) == "" {
		errs = append(errs, fmt.Sprintf("%s: description is empty", eid))
	}
	for i := range plan {
		step := plan[i]
		kw, err := step.StepKind()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: step %d: %v", eid, i, err))
			continue
		}
		switch kw {
		case KwRun, KwCheck:
			if _, verbErr := step.Kind(); verbErr != nil {
				errs = append(errs, fmt.Sprintf("%s: step %d (%s): %v", eid, i, kw, verbErr))
			}
		case KwAgentRun, KwAgentCheck:
			if _, verbErr := step.Kind(); verbErr == nil {
				errs = append(errs, fmt.Sprintf("%s: step %d (%s): agent steps must not carry an Op verb", eid, i, kw))
			}
		}
	}
	return errs
}
