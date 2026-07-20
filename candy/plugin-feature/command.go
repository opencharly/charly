package feature

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/opencharly/sdk"
	"github.com/opencharly/sdk/deploykit"
	"github.com/opencharly/sdk/kit"
	"github.com/opencharly/sdk/spec"
)

// command.go — the externalized `charly feature` command (list / pending / validate — inspect
// plan-shaped descriptions). The plugin OWNS the subcommand grammar + the output formatting AND the
// plan-to-summary transform (keyword/text/agent/check flattening + validatePlanSteps — kit.KeywordOf /
// kit.ValidatePlanSteps / deploykit.DescriptionInfo are sdk-portable, K3); the genuine core subsystem
// it can't hold — the unified LOADER (LoadConfig / ScanCandy — the kernel) — stays core and is reached
// via the generic "feature" HostBuild seam, which enumerates every entity's RAW description + plan
// into plain DATA (charly/host_build_feature.go, which needs no kit/deploykit import as a result). No
// hidden `__feature-*` forward.
//
// (The Feature RUN verbs — `charly box feature run` / `charly check feature run` — stay children of
// box/check in the core binary, NOT part of this plugin.)
//
// feature is COMPILED-IN (charly.yml compiled_plugins): its Invoke(OpRun) runs in charly's process and
// gets the in-proc reverse channel (dispatchInProcCommand threads it), so HostBuild("feature") reaches
// the host loader. The out-of-process CliMain path has no reverse channel, so it errors.

const featureUsage = `usage: charly feature <list [kind] | pending [entity] | validate [entity]>`

// runFeatureCLI dispatches the feature subcommand and formats the enumerated plan data the "feature"
// HostBuild seam returns (the plugin owns list/pending/validate output; the loader stays core).
func runFeatureCLI(ctx context.Context, exec *sdk.Executor, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", featureUsage)
	}
	sub, rest := args[0], args[1:]
	filter := ""
	if len(rest) > 0 {
		filter = rest[0]
	}
	switch sub {
	case "-h", "--help", "help":
		fmt.Println(featureUsage)
		return nil
	case "list", "pending", "validate":
		reply, err := hostFeature(ctx, exec, spec.FeatureRequest{Filter: filter})
		if err != nil {
			return err
		}
		switch sub {
		case "list":
			for _, e := range reply.Entities {
				steps := planSteps(e.Plan)
				if e.Description == "" && len(steps) == 0 {
					fmt.Printf("%s %s: (no description)\n", e.Kind, e.Name)
					continue
				}
				nChecks := 0
				for _, s := range steps {
					if s.IsCheck {
						nChecks++
					}
				}
				fmt.Printf("%s %s: %q (%d step%s, %d check%s)\n",
					e.Kind, e.Name, summary(e.Description), len(steps), plural(len(steps)), nChecks, plural(nChecks))
			}
		case "pending":
			for _, e := range reply.Entities {
				for _, s := range planSteps(e.Plan) {
					if s.IsAgent {
						fmt.Printf("%s:%s — step %d: %s %q (agent-graded)\n", e.Kind, e.Name, s.Index, s.Keyword, s.Text)
					}
				}
			}
		case "validate":
			var errs []string
			for _, e := range reply.Entities {
				if e.Description == "" && len(e.Plan) == 0 {
					continue
				}
				errs = append(errs, kit.ValidatePlanSteps(e.Description, e.Plan, e.Kind+":"+e.Name)...)
			}
			if len(errs) > 0 {
				for _, er := range errs {
					fmt.Fprintln(os.Stderr, er)
				}
				return fmt.Errorf("%d validation error(s)", len(errs))
			}
			fmt.Println("All plan blocks validated successfully.")
		}
	default:
		return fmt.Errorf("unknown feature subcommand %q\n%s", sub, featureUsage)
	}
	return nil
}

// plural returns the plural suffix for a count (matches the former in-core summarizeDesc).
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// stepSummary is one plan step flattened for list/pending output — the plugin's OWN transform of the
// raw spec.Step the "feature" HostBuild seam ships (formerly computed host-side as spec.FeatureStep;
// K3 moved the transform here since kit.KeywordOf/Step.KeywordText/Step.IsAgent are sdk-portable).
type stepSummary struct {
	Index   int
	Keyword string
	Text    string
	IsAgent bool
	IsCheck bool
}

// planSteps flattens a raw plan into stepSummary (the former host-side FeatureStep loop, moved here).
func planSteps(plan []spec.Step) []stepSummary {
	out := make([]stepSummary, len(plan))
	for i := range plan {
		step := plan[i]
		out[i] = stepSummary{
			Index:   i,
			Keyword: string(kit.KeywordOf(&step)),
			Text:    step.KeywordText(),
			IsAgent: step.IsAgent(),
			IsCheck: step.Check != "" || step.AgentCheck != "",
		}
	}
	return out
}

// summary renders a description's info line, or "(empty)" for a description-less entity with a plan
// (the former host-side summarizeDesc/DescriptionInfo call, moved here).
func summary(desc string) string {
	if s := deploykit.DescriptionInfo(desc); s != "" {
		return s
	}
	return "(empty)"
}

// hostFeature enumerates the project's plans over the generic "feature" HostBuild kind. exec is nil on
// the out-of-process cliMain path (no reverse channel) → a clear error.
func hostFeature(ctx context.Context, exec *sdk.Executor, req spec.FeatureRequest) (spec.FeatureReply, error) {
	if exec == nil {
		return spec.FeatureReply{}, fmt.Errorf("charly feature requires compiled-in placement (the feature host seam is unavailable out-of-process)")
	}
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return spec.FeatureReply{}, err
	}
	resJSON, err := exec.HostBuild(ctx, "feature", reqJSON)
	if err != nil {
		return spec.FeatureReply{}, err
	}
	var reply spec.FeatureReply
	if uerr := json.Unmarshal(resJSON, &reply); uerr != nil {
		return spec.FeatureReply{}, uerr
	}
	if reply.Error != "" {
		return spec.FeatureReply{}, fmt.Errorf("%s", reply.Error)
	}
	return reply, nil
}
